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
	"syscall"

	"github.com/jlrickert/tapper/pkg/keg"
	"gopkg.in/yaml.v3"
)

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

// NewServeHandler builds an http.Handler that dynamically renders KEG pages.
// Templates are parsed once at creation time. Each request reads fresh data
// from the keg.
func (t *Tap) NewServeHandler(ctx context.Context, opts ServeOptions) (http.Handler, error) {
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

	sh := &serveHandler{
		tap:       t,
		keg:       k,
		tmpl:      tmpl,
		siteTitle: siteTitle,
		baseURL:   baseURL,
	}

	mux := http.NewServeMux()
	// Use a single catch-all pattern and dispatch manually. Go's ServeMux
	// wildcard patterns conflict when /{id}/ overlaps with literal paths
	// like /tags/ and /changes/. Manual dispatch avoids this.
	mux.HandleFunc("GET /{path...}", sh.dispatch)

	return mux, nil
}

// serveHandler holds shared state for all HTTP handlers.
type serveHandler struct {
	tap       *Tap
	keg       *keg.Keg
	tmpl      *template.Template
	siteTitle string
	baseURL   string
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
func (sh *serveHandler) buildNodeByID(ctx context.Context) (map[string]siteNodeRef, []keg.NodeIndexEntry, error) {
	dex, err := sh.keg.Dex(ctx)
	if err != nil {
		return nil, nil, err
	}

	entries := dex.Nodes(ctx)
	nodeByID := map[string]siteNodeRef{}
	for _, e := range entries {
		nodeByID[e.ID] = siteNodeRef{
			ID:      e.ID,
			Title:   e.Title,
			Updated: e.Updated,
			Created: e.Created,
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

	// Build dex data for links/backlinks.
	dex, err := sh.keg.Dex(ctx)
	if err != nil {
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
	title := idStr
	var updated, created = stats.Updated(), stats.Created()
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

	dex, err := sh.keg.Dex(ctx)
	if err != nil {
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

	dex, err := sh.keg.Dex(ctx)
	if err != nil {
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
