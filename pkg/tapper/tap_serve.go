package tapper

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"mime"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/jlrickert/tapper/pkg/keg"
	"gopkg.in/yaml.v3"
)

// sseClient tracks a single SSE subscriber along with its registration time.
// The subscribedAt timestamp lets the broadcaster skip events that arrive
// within a grace period after connection, preventing reload cascades when
// the browser reconnects EventSource after a page reload.
type sseClient struct {
	ch           chan struct{}
	subscribedAt time.Time
}

// sseBroadcaster manages connected SSE clients and broadcasts reload events.
// It is safe for concurrent use.
type sseBroadcaster struct {
	mu      sync.Mutex
	clients map[*sseClient]struct{}

	// clientGrace is the minimum time a client must be connected before it
	// receives broadcast events. This prevents the reload loop where:
	//   event -> broadcast -> reload -> reconnect -> pending broadcast -> reload
	clientGrace time.Duration
}

func newSSEBroadcaster() *sseBroadcaster {
	return &sseBroadcaster{
		clients:     make(map[*sseClient]struct{}),
		clientGrace: 2 * time.Second,
	}
}

// subscribe registers a new SSE client and returns its event channel.
// The caller must call unsubscribe when done.
func (b *sseBroadcaster) subscribe() *sseClient {
	c := &sseClient{
		ch:           make(chan struct{}, 1),
		subscribedAt: time.Now(),
	}
	b.mu.Lock()
	b.clients[c] = struct{}{}
	b.mu.Unlock()
	return c
}

// unsubscribe removes a client from the broadcaster.
func (b *sseBroadcaster) unsubscribe(c *sseClient) {
	b.mu.Lock()
	delete(b.clients, c)
	b.mu.Unlock()
}

// broadcast sends a reload signal to all connected clients that have been
// subscribed longer than the grace period. Clients that connected recently
// (e.g., right after a reload) are skipped to break the reload cascade.
// Non-blocking: if a client's buffer is full, the event is skipped for
// that client (it will get the next one).
func (b *sseBroadcaster) broadcast() {
	now := time.Now()
	b.mu.Lock()
	defer b.mu.Unlock()
	for c := range b.clients {
		if now.Sub(c.subscribedAt) < b.clientGrace {
			continue // still in grace period, skip
		}
		select {
		case c.ch <- struct{}{}:
		default:
		}
	}
}

// count returns the number of connected SSE clients.
func (b *sseBroadcaster) count() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.clients)
}

// ServeOptions configures the embedded HTTP server.
type ServeOptions struct {
	KegTargetOptions

	// Host is the bind address (default: 127.0.0.1).
	Host string

	// Port is the port to listen on (default: 0 for random).
	Port int

	// Title overrides the site title. If empty, the keg summary or URL is used.
	Title string

	// BaseURL is the base URL for absolute links. Defaults to "/".
	BaseURL string

	// Watch enables the filesystem watcher and SSE endpoint for automatic
	// browser refresh when node files change. When nil, defaults to true.
	Watch *bool
}

// ServeResult is returned when the server shuts down.
type ServeResult struct {
	URL string
}

// Serve starts an HTTP server that dynamically renders KEG pages on each
// request. It blocks until ctx is cancelled or an OS interrupt signal is
// received. The URL is printed to stdout immediately after binding.
func (t *Tap) Serve(ctx context.Context, opts ServeOptions) (*ServeResult, error) {
	handler, err := t.NewServeHandler(ctx, opts)
	if err != nil {
		return nil, err
	}
	defer handler.Close()

	host := opts.Host
	if host == "" {
		host = "127.0.0.1"
	}

	addr := net.JoinHostPort(host, strconv.Itoa(opts.Port))
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("unable to bind %s: %w", addr, err)
	}

	actualAddr := listener.Addr().(*net.TCPAddr)
	url := fmt.Sprintf("http://%s:%d/", actualAddr.IP, actualAddr.Port)

	// Print the URL to stdout so callers can parse it.
	fmt.Fprintf(t.Runtime.Stream().Out, "Serving KEG at %s\n", url)

	srv := &http.Server{Handler: handler}

	// Set up signal handling for graceful shutdown.
	sigCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		<-sigCtx.Done()
		_ = srv.Shutdown(context.Background())
	}()

	if err := srv.Serve(listener); err != nil && err != http.ErrServerClosed {
		return nil, fmt.Errorf("server error: %w", err)
	}

	return &ServeResult{URL: url}, nil
}

// ServeHandler wraps the HTTP handler for serving KEG pages. It implements
// http.Handler and provides a Close method to release background resources
// such as the filesystem watcher used for proactive dex invalidation.
type ServeHandler struct {
	mux          *http.ServeMux
	sh           *serveHandler
	watcherClose func() // nil when no watcher is active
	sse          *sseBroadcaster // nil when watch mode is disabled
}

// ServeHTTP implements http.Handler.
func (h *ServeHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

// Close releases background resources. It is safe to call multiple times.
func (h *ServeHandler) Close() {
	if h.watcherClose != nil {
		h.watcherClose()
		h.watcherClose = nil
	}
}

// NewServeHandler builds a ServeHandler that dynamically renders KEG pages.
// Templates are parsed once at creation time. Each request reads fresh data
// from the keg. If the keg is backed by a filesystem repository, a background
// watcher proactively invalidates the dex cache when node files change.
// Callers should call Close when the handler is no longer needed.
func (t *Tap) NewServeHandler(ctx context.Context, opts ServeOptions) (*ServeHandler, error) {
	k, err := t.resolveKeg(ctx, opts.KegTargetOptions)
	if err != nil {
		return nil, fmt.Errorf("unable to open keg: %w", err)
	}

	cfg, err := k.Config(ctx)
	if err != nil {
		return nil, fmt.Errorf("unable to read config: %w", err)
	}

	// Resolve site title.
	siteTitle := opts.Title
	if siteTitle == "" {
		siteTitle = strings.TrimSpace(cfg.Title)
	}
	if siteTitle == "" {
		siteTitle = strings.TrimSpace(cfg.Summary)
		if len(siteTitle) > 80 {
			siteTitle = siteTitle[:80] + "..."
		}
	}
	if siteTitle == "" {
		siteTitle = "KEG"
	}

	// Resolve base URL.
	baseURL := opts.BaseURL
	if baseURL == "" {
		baseURL = "/"
	}
	if !strings.HasSuffix(baseURL, "/") {
		baseURL += "/"
	}

	// Parse templates once at startup.
	tmpl, err := parseSiteTemplates()
	if err != nil {
		return nil, fmt.Errorf("unable to parse site templates: %w", err)
	}

	// Determine whether watch mode is enabled. Default to true.
	watchEnabled := true
	if opts.Watch != nil {
		watchEnabled = *opts.Watch
	}

	sh := &serveHandler{
		tap:       t,
		keg:       k,
		tmpl:      tmpl,
		siteTitle: siteTitle,
		baseURL:   baseURL,
		loc:       cfg.Location(),
		watch:     watchEnabled,
	}

	mux := http.NewServeMux()
	// Use a single catch-all pattern and dispatch manually. Go's ServeMux
	// wildcard patterns conflict when /{id}/ overlaps with literal paths
	// like /tags/ and /changes/. Manual dispatch avoids this.
	mux.HandleFunc("GET /{path...}", sh.dispatch)

	handler := &ServeHandler{mux: mux, sh: sh}

	// Start a filesystem watcher for proactive dex invalidation. When node
	// files change on disk, the watcher invalidates the cached dex so the
	// next request gets fresh data without waiting for an mtime check.
	// When watch mode is enabled, also set up SSE broadcasting so
	// connected browsers reload automatically.
	if watchEnabled {
		if fsRepo, ok := k.Repo.(*keg.FsRepo); ok {
			sse := newSSEBroadcaster()
			handler.sse = sse

			// Register the SSE endpoint.
			mux.HandleFunc("GET /events", sh.handleSSE(sse))

			w, watchErr := fsRepo.WatchEvents()
			if watchErr == nil {
				ch, chErr := w.Watch(ctx)
				if chErr == nil {
					go func() {
						// Broadcast-level debounce: accumulate events and
						// broadcast once after 500ms of quiet. This prevents
						// the reload loop caused by editors emitting multiple
						// fs events per save (write-rename cycles produce
						// separate events for README.md, stats.json, etc.).
						const broadcastDebounce = 500 * time.Millisecond
						var debounceTimer *time.Timer
						for range ch {
							k.InvalidateDex()
							if debounceTimer != nil {
								debounceTimer.Stop()
							}
							debounceTimer = time.AfterFunc(broadcastDebounce, func() {
								sse.broadcast()
							})
						}
						// Flush any pending broadcast on channel close.
						if debounceTimer != nil {
							debounceTimer.Stop()
						}
					}()
					handler.watcherClose = func() { _ = w.Close() }
				} else {
					_ = w.Close()
				}
			}
		}
	} else {
		// Watch disabled: still start the watcher for dex invalidation
		// but without SSE broadcasting.
		if fsRepo, ok := k.Repo.(*keg.FsRepo); ok {
			w, watchErr := fsRepo.WatchEvents()
			if watchErr == nil {
				ch, chErr := w.Watch(ctx)
				if chErr == nil {
					go func() {
						for range ch {
							k.InvalidateDex()
						}
					}()
					handler.watcherClose = func() { _ = w.Close() }
				} else {
					_ = w.Close()
				}
			}
		}
	}

	return handler, nil
}

// serveHandler holds shared state for all HTTP handlers.
type serveHandler struct {
	tap       *Tap
	keg       *keg.Keg
	tmpl      *template.Template
	siteTitle string
	baseURL   string
	loc       *time.Location
	watch     bool // whether SSE auto-refresh is active
}

// handleSSE returns an HTTP handler that serves Server-Sent Events for
// browser auto-refresh. Each connected client receives "data: reload"
// events whenever the watcher detects node changes.
func (sh *serveHandler) handleSSE(sse *sseBroadcaster) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming not supported", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("Access-Control-Allow-Origin", "*")

		client := sse.subscribe()
		defer sse.unsubscribe(client)

		// Flush headers immediately so the browser's EventSource connects.
		flusher.Flush()

		ctx := r.Context()
		for {
			select {
			case <-ctx.Done():
				return
			case _, ok := <-client.ch:
				if !ok {
					return
				}
				fmt.Fprintf(w, "data: reload\n\n")
				flusher.Flush()
			}
		}
	}
}

// dispatch routes requests based on URL path structure.
// KEG node IDs are always numeric, so we can distinguish /{id}/ from /tags/ etc.
func (sh *serveHandler) dispatch(w http.ResponseWriter, r *http.Request) {
	path := strings.Trim(r.URL.Path, "/")

	// Root path.
	if path == "" {
		sh.handleIndex(w, r)
		return
	}

	parts := strings.SplitN(path, "/", 3)

	switch parts[0] {
	case "tags":
		if len(parts) == 1 {
			// /tags or /tags/
			sh.handleTagsIndex(w, r)
			return
		}
		if len(parts) >= 2 {
			// /tags/{tag}/ or /tags/{tag}
			r.SetPathValue("tag", parts[1])
			sh.handleTag(w, r)
			return
		}
	case "changes":
		sh.handleChanges(w, r)
		return
	default:
		// Try to parse as a numeric node ID.
		nodeID := parts[0]
		if _, err := strconv.Atoi(nodeID); err != nil {
			sh.handleNotFound(w, r)
			return
		}
		r.SetPathValue("id", nodeID)

		if len(parts) == 1 {
			// /{id} or /{id}/
			sh.handleNodePage(w, r)
			return
		}

		// /{id}/{something}
		subpath := parts[1]
		r.SetPathValue("asset", subpath)

		switch subpath {
		case "README.md":
			sh.handleNodeRaw("README.md")(w, r)
		case "meta.yaml":
			sh.handleNodeRaw("meta.yaml")(w, r)
		case "meta.json":
			sh.handleNodeMetaJSON(w, r)
		case "stats.json":
			sh.handleNodeRaw("stats.json")(w, r)
		case "stats.yaml":
			sh.handleNodeStatsYAML(w, r)
		default:
			sh.handleNodeAsset(w, r)
		}
		return
	}

	sh.handleNotFound(w, r)
}

// renderPage executes the content template, wraps it in layout, and writes the response.
func (sh *serveHandler) renderPage(w http.ResponseWriter, templateName string, data any, pageTitle string) {
	var contentBuf bytes.Buffer
	if err := sh.tmpl.ExecuteTemplate(&contentBuf, templateName, data); err != nil {
		http.Error(w, "template error", http.StatusInternalServerError)
		return
	}

	ld := layoutData{
		Title:     pageTitle,
		SiteTitle: sh.siteTitle,
		BaseURL:   sh.baseURL,
		HasSearch: false,
		HasWatch:  sh.watch,
		Content:   template.HTML(contentBuf.String()),
	}

	var pageBuf bytes.Buffer
	if err := sh.tmpl.ExecuteTemplate(&pageBuf, "layout.html", ld); err != nil {
		http.Error(w, "template error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(pageBuf.Bytes())
}

// buildNodeByID loads the dex and builds a map of node references.
// Timestamps are converted to the keg's configured timezone for display.
// Non-fatal dex errors (e.g. a malformed custom index) are tolerated as
// long as the dex itself is usable.
func (sh *serveHandler) buildNodeByID(ctx context.Context) (map[string]siteNodeRef, []keg.NodeIndexEntry, error) {
	dex, err := sh.keg.DexFresh(ctx)
	if err != nil && dex == nil {
		return nil, nil, err
	}

	entries := dex.Nodes(ctx)
	nodeByID := map[string]siteNodeRef{}
	for _, e := range entries {
		nodeByID[e.ID] = siteNodeRef{
			ID:      e.ID,
			Title:   e.Title,
			Updated: e.Updated.In(sh.loc),
			Created: e.Created.In(sh.loc),
		}
	}

	// Populate lead text.
	for id, ref := range nodeByID {
		nid, err := keg.ParseNode(id)
		if err != nil || nid == nil {
			continue
		}
		if stats, err := sh.keg.Repo.ReadStats(ctx, *nid); err == nil {
			ref.Lead = stats.Lead()
		}
		nodeByID[id] = ref
	}

	return nodeByID, entries, nil
}

// handleIndex serves the landing page.
func (sh *serveHandler) handleIndex(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	nodeByID, entries, err := sh.buildNodeByID(ctx)
	if err != nil {
		http.Error(w, "unable to read keg index", http.StatusInternalServerError)
		return
	}

	nodes := make([]siteNodeRef, 0, len(entries))
	for _, e := range entries {
		if ref, ok := nodeByID[e.ID]; ok {
			nodes = append(nodes, ref)
		}
	}
	sortNodeRefsByUpdated(nodes)

	cfg, _ := sh.keg.Config(ctx)
	summary := ""
	if cfg != nil {
		summary = strings.TrimSpace(cfg.Summary)
	}

	id := indexData{
		SiteTitle: sh.siteTitle,
		Summary:   summary,
		BaseURL:   sh.baseURL,
		Nodes:     nodes,
	}

	sh.renderPage(w, "index.html", id, "Home")
}

// handleNodePage serves a rendered node page.
func (sh *serveHandler) handleNodePage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	idStr := r.PathValue("id")

	nid, err := keg.ParseNode(idStr)
	if err != nil || nid == nil {
		sh.handleNotFound(w, r)
		return
	}

	// Read raw content.
	rawContent, err := sh.keg.Repo.ReadContent(ctx, *nid)
	if err != nil {
		sh.handleNotFound(w, r)
		return
	}

	// Read meta.
	rawMeta, _ := sh.keg.Repo.ReadMeta(ctx, *nid)
	meta, _ := keg.ParseMeta(ctx, rawMeta)
	var tags []string
	var entity string
	if meta != nil {
		tags = meta.Tags()
		if e, ok := meta.Get("entity"); ok {
			entity = e
		}
	}

	// Read stats.
	stats, _ := sh.keg.Repo.ReadStats(ctx, *nid)

	// Render markdown.
	rendered, err := keg.RenderMarkdown(rawContent, keg.RenderOptions{BaseURL: sh.baseURL})
	if err != nil {
		http.Error(w, "render error", http.StatusInternalServerError)
		return
	}

	// Build dex data for links/backlinks. Tolerate non-fatal dex errors
	// (e.g. a malformed custom index) as long as the dex is usable.
	dex, err := sh.keg.DexFresh(ctx)
	if err != nil && dex == nil {
		http.Error(w, "unable to read index", http.StatusInternalServerError)
		return
	}

	nodeByID, _, _ := sh.buildNodeByID(ctx)

	// Build outgoing links.
	var outLinks []siteLinkRef
	if links, ok := dex.Links(ctx, *nid); ok {
		for _, l := range links {
			title := l.Path()
			if ref, ok := nodeByID[l.Path()]; ok && ref.Title != "" {
				title = ref.Title
			}
			outLinks = append(outLinks, siteLinkRef{ID: l.Path(), Title: title})
		}
	}

	// Build backlinks.
	var backlinks []siteLinkRef
	if blinks, ok := dex.Backlinks(ctx, *nid); ok {
		for _, b := range blinks {
			title := b.Path()
			if ref, ok := nodeByID[b.Path()]; ok && ref.Title != "" {
				title = ref.Title
			}
			backlinks = append(backlinks, siteLinkRef{ID: b.Path(), Title: title})
		}
	}

	// Determine title and timestamps from the node index or stats.
	// Timestamps are converted to the keg's configured timezone.
	title := idStr
	var updated, created = stats.Updated().In(sh.loc), stats.Created().In(sh.loc)
	if entry, ok := nodeByID[idStr]; ok {
		title = entry.Title
		updated = entry.Updated
		created = entry.Created
	}

	nd := nodeData{
		ID:              idStr,
		Title:           title,
		Entity:          entity,
		Updated:         updated,
		Created:         created,
		Tags:            tags,
		RenderedContent: template.HTML(rendered),
		OutLinks:        outLinks,
		Backlinks:       backlinks,
		BaseURL:         sh.baseURL,
	}

	sh.renderPage(w, "node.html", nd, title)
}

// handleNodeRaw returns a handler that serves a raw node file.
func (sh *serveHandler) handleNodeRaw(filename string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		idStr := r.PathValue("id")

		nid, err := keg.ParseNode(idStr)
		if err != nil || nid == nil {
			sh.handleNotFound(w, r)
			return
		}

		var data []byte
		var contentType string

		switch filename {
		case "README.md":
			data, err = sh.keg.Repo.ReadContent(ctx, *nid)
			contentType = "text/markdown; charset=utf-8"
		case "meta.yaml":
			data, err = sh.keg.Repo.ReadMeta(ctx, *nid)
			contentType = "application/x-yaml; charset=utf-8"
		case "stats.json":
			stats, serr := sh.keg.Repo.ReadStats(ctx, *nid)
			if serr != nil {
				err = serr
			} else if stats != nil {
				data, err = stats.ToJSON()
				contentType = "application/json; charset=utf-8"
			} else {
				err = fmt.Errorf("no stats")
			}
		}

		if err != nil || len(data) == 0 {
			sh.handleNotFound(w, r)
			return
		}

		w.Header().Set("Content-Type", contentType)
		w.Write(data)
	}
}

// handleNodeMetaJSON serves meta converted to JSON.
func (sh *serveHandler) handleNodeMetaJSON(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	idStr := r.PathValue("id")

	nid, err := keg.ParseNode(idStr)
	if err != nil || nid == nil {
		sh.handleNotFound(w, r)
		return
	}

	rawMeta, err := sh.keg.Repo.ReadMeta(ctx, *nid)
	if err != nil || len(rawMeta) == 0 {
		sh.handleNotFound(w, r)
		return
	}

	meta, err := keg.ParseMeta(ctx, rawMeta)
	if err != nil {
		sh.handleNotFound(w, r)
		return
	}

	metaMap := map[string]any{"tags": meta.Tags()}
	if entity, ok := meta.Get("entity"); ok {
		metaMap["entity"] = entity
	}

	data, err := json.MarshalIndent(metaMap, "", "  ")
	if err != nil {
		http.Error(w, "json error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Write(data)
}

// handleNodeStatsYAML serves stats converted to YAML.
func (sh *serveHandler) handleNodeStatsYAML(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	idStr := r.PathValue("id")

	nid, err := keg.ParseNode(idStr)
	if err != nil || nid == nil {
		sh.handleNotFound(w, r)
		return
	}

	stats, err := sh.keg.Repo.ReadStats(ctx, *nid)
	if err != nil || stats == nil {
		sh.handleNotFound(w, r)
		return
	}

	sj, err := stats.ToJSON()
	if err != nil {
		http.Error(w, "stats error", http.StatusInternalServerError)
		return
	}

	var statsMap map[string]any
	if err := json.Unmarshal(sj, &statsMap); err != nil {
		http.Error(w, "stats error", http.StatusInternalServerError)
		return
	}

	data, err := yaml.Marshal(statsMap)
	if err != nil {
		http.Error(w, "yaml error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/x-yaml; charset=utf-8")
	w.Write(data)
}

// handleNodeAsset serves images and file attachments from a node directory.
func (sh *serveHandler) handleNodeAsset(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	idStr := r.PathValue("id")
	asset := r.PathValue("asset")

	nid, err := keg.ParseNode(idStr)
	if err != nil || nid == nil {
		sh.handleNotFound(w, r)
		return
	}

	// Try images first.
	if ri, ok := sh.keg.Repo.(keg.RepositoryImages); ok {
		data, err := ri.ReadImage(ctx, *nid, asset)
		if err == nil && len(data) > 0 {
			contentType := mime.TypeByExtension(filepath.Ext(asset))
			if contentType == "" {
				contentType = "application/octet-stream"
			}
			w.Header().Set("Content-Type", contentType)
			w.Write(data)
			return
		}
	}

	// Try file attachments.
	if rf, ok := sh.keg.Repo.(keg.RepositoryFiles); ok {
		data, err := rf.ReadFile(ctx, *nid, asset)
		if err == nil && len(data) > 0 {
			contentType := mime.TypeByExtension(filepath.Ext(asset))
			if contentType == "" {
				contentType = "application/octet-stream"
			}
			w.Header().Set("Content-Type", contentType)
			w.Write(data)
			return
		}
	}

	sh.handleNotFound(w, r)
}

// handleTagsIndex serves the tag index page.
func (sh *serveHandler) handleTagsIndex(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	dex, err := sh.keg.DexFresh(ctx)
	if err != nil && dex == nil {
		http.Error(w, "unable to read index", http.StatusInternalServerError)
		return
	}

	tagList := dex.TagList(ctx)
	sort.Strings(tagList)

	tagRefs := make([]siteTagRef, 0, len(tagList))
	for _, tag := range tagList {
		nodes, ok := dex.TagNodes(ctx, tag)
		if !ok || len(nodes) == 0 {
			continue
		}
		tagRefs = append(tagRefs, siteTagRef{Name: tag, Count: len(nodes)})
	}

	td := tagsData{BaseURL: sh.baseURL, Tags: tagRefs}
	sh.renderPage(w, "tags.html", td, "Tags")
}

// handleTag serves a single tag page.
func (sh *serveHandler) handleTag(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tagName := r.PathValue("tag")

	dex, err := sh.keg.DexFresh(ctx)
	if err != nil && dex == nil {
		http.Error(w, "unable to read index", http.StatusInternalServerError)
		return
	}

	tagNodes, ok := dex.TagNodes(ctx, tagName)
	if !ok || len(tagNodes) == 0 {
		sh.handleNotFound(w, r)
		return
	}

	nodeByID, _, err := sh.buildNodeByID(ctx)
	if err != nil {
		http.Error(w, "unable to read index", http.StatusInternalServerError)
		return
	}

	refs := make([]siteNodeRef, 0, len(tagNodes))
	for _, n := range tagNodes {
		if ref, ok := nodeByID[n.Path()]; ok {
			refs = append(refs, ref)
		}
	}
	sortNodeRefsByUpdated(refs)

	td := tagData{
		TagName: tagName,
		Count:   len(tagNodes),
		BaseURL: sh.baseURL,
		Nodes:   refs,
	}

	sh.renderPage(w, "tag.html", td, "Tag: "+tagName)
}

// handleChanges serves the changes page.
func (sh *serveHandler) handleChanges(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	nodeByID, entries, err := sh.buildNodeByID(ctx)
	if err != nil {
		http.Error(w, "unable to read index", http.StatusInternalServerError)
		return
	}

	nodes := make([]siteNodeRef, 0, len(entries))
	for _, e := range entries {
		if ref, ok := nodeByID[e.ID]; ok {
			nodes = append(nodes, ref)
		}
	}
	sortNodeRefsByUpdated(nodes)

	cd := changesData{BaseURL: sh.baseURL, Nodes: nodes}
	sh.renderPage(w, "changes.html", cd, "Changes")
}

// handleNotFound serves a 404 page.
func (sh *serveHandler) handleNotFound(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusNotFound)

	content := `<div class="not-found"><h2>Page Not Found</h2><p>The requested page could not be found.</p><p><a href="` + sh.baseURL + `">Return to home</a></p></div>`

	ld := layoutData{
		Title:     "Not Found",
		SiteTitle: sh.siteTitle,
		BaseURL:   sh.baseURL,
		HasSearch: false,
		HasWatch:  sh.watch,
		Content:   template.HTML(content),
	}

	var pageBuf bytes.Buffer
	if err := sh.tmpl.ExecuteTemplate(&pageBuf, "layout.html", ld); err != nil {
		// Fallback to plain text.
		w.Write([]byte("404 Not Found"))
		return
	}

	w.Write(pageBuf.Bytes())
}
