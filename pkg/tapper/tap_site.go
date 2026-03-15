package tapper

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jlrickert/tapper/pkg/keg"
	"gopkg.in/yaml.v3"
)

//go:embed site_templates/*.html
var siteTemplateFS embed.FS

// SiteOptions configures static site generation.
type SiteOptions struct {
	KegTargetOptions

	// Output is the directory where the site will be written.
	Output string

	// Title overrides the site title. If empty, the keg summary or URL is used.
	Title string

	// BaseURL is the base URL for absolute links. Defaults to "/".
	BaseURL string

	// NoSearch skips Pagefind search indexing.
	NoSearch bool
}

// SiteResult summarizes what the site generator produced.
type SiteResult struct {
	OutputDir string
	NodeCount int
	TagCount  int
	HasSearch bool
}

// siteNodeRef is a lightweight reference for templates.
type siteNodeRef struct {
	ID      string
	Title   string
	Lead    string
	Updated time.Time
	Created time.Time
}

type siteTagRef struct {
	Name  string
	Count int
}

type siteLinkRef struct {
	ID    string
	Title string
}

// layoutData is passed to the layout template.
type layoutData struct {
	Title     string
	SiteTitle string
	BaseURL   string
	HasSearch bool
	Content   template.HTML
}

// nodeData is passed to the node template.
type nodeData struct {
	ID              string
	Title           string
	Entity          string
	Updated         time.Time
	Created         time.Time
	Tags            []string
	RenderedContent template.HTML
	OutLinks        []siteLinkRef
	Backlinks       []siteLinkRef
	BaseURL         string
}

// indexData is passed to the index template.
type indexData struct {
	SiteTitle string
	Summary   string
	BaseURL   string
	Nodes     []siteNodeRef
}

// tagsData is passed to the tags template.
type tagsData struct {
	BaseURL string
	Tags    []siteTagRef
}

// tagData is passed to a single tag template.
type tagData struct {
	TagName string
	Count   int
	BaseURL string
	Nodes   []siteNodeRef
}

// changesData is passed to the changes template.
type changesData struct {
	BaseURL string
	Nodes   []siteNodeRef
}

// Site generates a static HTML website from the resolved keg.
func (t *Tap) Site(ctx context.Context, opts SiteOptions) (*SiteResult, error) {
	k, err := t.resolveKeg(ctx, opts.KegTargetOptions)
	if err != nil {
		return nil, fmt.Errorf("unable to open keg: %w", err)
	}

	dex, err := k.Dex(ctx)
	if err != nil {
		return nil, fmt.Errorf("unable to read dex: %w", err)
	}

	cfg, err := k.Config(ctx)
	if err != nil {
		return nil, fmt.Errorf("unable to read config: %w", err)
	}

	// Resolve options: CLI flags override keg config site section.
	siteCfg := cfg.Site
	if siteCfg == nil {
		siteCfg = &keg.SiteConfig{}
	}

	outputDir := opts.Output
	if outputDir == "" && siteCfg.Output != "" {
		outputDir = siteCfg.Output
	}
	if outputDir == "" {
		outputDir = "./site"
	}

	siteTitle := opts.Title
	if siteTitle == "" && siteCfg.Title != "" {
		siteTitle = siteCfg.Title
	}
	if siteTitle == "" {
		siteTitle = strings.TrimSpace(cfg.Title)
	}
	if siteTitle == "" {
		siteTitle = strings.TrimSpace(cfg.Summary)
		// Truncate long summaries.
		if len(siteTitle) > 80 {
			siteTitle = siteTitle[:80] + "..."
		}
	}
	if siteTitle == "" {
		siteTitle = "KEG"
	}

	baseURL := opts.BaseURL
	if baseURL == "" && siteCfg.BaseURL != "" {
		baseURL = siteCfg.BaseURL
	}
	if baseURL == "" {
		baseURL = "/"
	}
	if !strings.HasSuffix(baseURL, "/") {
		baseURL += "/"
	}

	// If the config explicitly disables search, respect it.
	if siteCfg.Search != nil && !*siteCfg.Search {
		opts.NoSearch = true
	}

	summary := strings.TrimSpace(cfg.Summary)

	// Parse templates.
	tmpl, err := parseSiteTemplates()
	if err != nil {
		return nil, fmt.Errorf("unable to parse site templates: %w", err)
	}

	rt := t.Runtime

	// Ensure output directory exists.
	absOutput, err := filepath.Abs(outputDir)
	if err != nil {
		return nil, fmt.Errorf("unable to resolve output path: %w", err)
	}
	if err := rt.Mkdir(absOutput, 0o755, true); err != nil {
		return nil, fmt.Errorf("unable to create output directory: %w", err)
	}

	// Build node index for lookups.
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

	// Populate lead text for each node.
	for id, ref := range nodeByID {
		nid, err := keg.ParseNode(id)
		if err != nil || nid == nil {
			continue
		}
		if stats, err := k.Repo.ReadStats(ctx, *nid); err == nil {
			ref.Lead = stats.Lead()
		}
		nodeByID[id] = ref
	}

	// Build tag data for generation.
	tagList := dex.TagList(ctx)
	sort.Strings(tagList)

	type tagGenData struct {
		name  string
		nodes []siteNodeRef
	}
	var tagGenList []tagGenData
	tagRefs := make([]siteTagRef, 0, len(tagList))
	for _, tag := range tagList {
		nodes, ok := dex.TagNodes(ctx, tag)
		if !ok || len(nodes) == 0 {
			continue
		}
		refs := make([]siteNodeRef, 0, len(nodes))
		for _, n := range nodes {
			if ref, ok := nodeByID[n.Path()]; ok {
				refs = append(refs, ref)
			}
		}
		sortNodeRefsByUpdated(refs)
		tagGenList = append(tagGenList, tagGenData{name: tag, nodes: refs})
		tagRefs = append(tagRefs, siteTagRef{Name: tag, Count: len(nodes)})
	}

	// Build changes data.
	changeNodes := make([]siteNodeRef, 0, len(entries))
	for _, e := range entries {
		if ref, ok := nodeByID[e.ID]; ok {
			changeNodes = append(changeNodes, ref)
		}
	}
	sortNodeRefsByUpdated(changeNodes)

	// generateAllPages writes all HTML pages with the given hasSearch flag.
	// On the first call, it also writes raw/converted data files and copies
	// assets (skipped on subsequent calls via writeAssets=false).
	generateAllPages := func(hasSearch bool, writeAssets bool) (int, int, error) {
		nodeCount := 0
		for _, entry := range entries {
			nid, err := keg.ParseNode(entry.ID)
			if err != nil || nid == nil {
				continue
			}
			if err := t.generateNodePage(ctx, k, dex, tmpl, *nid, entry, nodeByID, absOutput, siteTitle, baseURL, hasSearch, writeAssets); err != nil {
				rt.Logger().Warn(fmt.Sprintf("site: skip node %s: %s", entry.ID, err))
				continue
			}
			nodeCount++
		}

		if err := t.generateIndexPage(tmpl, entries, nodeByID, absOutput, siteTitle, summary, baseURL, hasSearch); err != nil {
			return 0, 0, fmt.Errorf("unable to generate index page: %w", err)
		}

		tagCount := 0
		for _, tg := range tagGenList {
			if err := t.generateTagPage(tmpl, tg.name, tg.nodes, absOutput, siteTitle, baseURL, hasSearch); err != nil {
				return 0, 0, fmt.Errorf("unable to generate tag page for %q: %w", tg.name, err)
			}
			tagCount++
		}

		if err := t.generateTagsIndexPage(tmpl, tagRefs, absOutput, siteTitle, baseURL, hasSearch); err != nil {
			return 0, 0, fmt.Errorf("unable to generate tags index: %w", err)
		}

		if err := t.generateChangesPage(tmpl, changeNodes, absOutput, siteTitle, baseURL, hasSearch); err != nil {
			return 0, 0, fmt.Errorf("unable to generate changes page: %w", err)
		}

		return nodeCount, tagCount, nil
	}

	// First pass: generate all pages without search.
	nodeCount, tagCount, err := generateAllPages(false, true)
	if err != nil {
		return nil, err
	}

	// Run Pagefind if available and not disabled.
	hasSearch := false
	if !opts.NoSearch {
		hasSearch = t.runPagefind(absOutput)
		if hasSearch {
			// Second pass: re-generate HTML pages with search UI enabled.
			// Assets (README.md, meta, stats, images) are already written.
			_, _, _ = generateAllPages(true, false)
		}
	}

	return &SiteResult{
		OutputDir: absOutput,
		NodeCount: nodeCount,
		TagCount:  tagCount,
		HasSearch: hasSearch,
	}, nil
}

func (t *Tap) generateNodePage(
	ctx context.Context,
	k *keg.Keg,
	dex *keg.Dex,
	tmpl *template.Template,
	nid keg.NodeId,
	entry keg.NodeIndexEntry,
	nodeByID map[string]siteNodeRef,
	outputDir, siteTitle, baseURL string,
	hasSearch bool,
	writeAssets bool,
) error {
	rt := t.Runtime

	// Read raw content.
	rawContent, err := k.Repo.ReadContent(ctx, nid)
	if err != nil {
		return fmt.Errorf("read content: %w", err)
	}

	// Read meta for tags and entity.
	rawMeta, _ := k.Repo.ReadMeta(ctx, nid)
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
	stats, _ := k.Repo.ReadStats(ctx, nid)

	// Render markdown to HTML.
	rendered, err := keg.RenderMarkdown(rawContent, keg.RenderOptions{BaseURL: baseURL})
	if err != nil {
		return fmt.Errorf("render markdown: %w", err)
	}

	// Build outgoing links.
	var outLinks []siteLinkRef
	if links, ok := dex.Links(ctx, nid); ok {
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
	if blinks, ok := dex.Backlinks(ctx, nid); ok {
		for _, b := range blinks {
			title := b.Path()
			if ref, ok := nodeByID[b.Path()]; ok && ref.Title != "" {
				title = ref.Title
			}
			backlinks = append(backlinks, siteLinkRef{ID: b.Path(), Title: title})
		}
	}

	// Execute node template.
	nd := nodeData{
		ID:              entry.ID,
		Title:           entry.Title,
		Entity:          entity,
		Updated:         entry.Updated,
		Created:         entry.Created,
		Tags:            tags,
		RenderedContent: template.HTML(rendered),
		OutLinks:        outLinks,
		Backlinks:       backlinks,
		BaseURL:         baseURL,
	}
	var nodeBuf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&nodeBuf, "node.html", nd); err != nil {
		return fmt.Errorf("execute node template: %w", err)
	}

	// Wrap in layout.
	ld := layoutData{
		Title:     entry.Title,
		SiteTitle: siteTitle,
		BaseURL:   baseURL,
		HasSearch: hasSearch,
		Content:   template.HTML(nodeBuf.String()),
	}
	var pageBuf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&pageBuf, "layout.html", ld); err != nil {
		return fmt.Errorf("execute layout template: %w", err)
	}

	// Write output files.
	nodeDir := filepath.Join(outputDir, entry.ID)
	if err := rt.Mkdir(nodeDir, 0o755, true); err != nil {
		return fmt.Errorf("create node dir: %w", err)
	}

	// Write rendered HTML.
	if err := rt.AtomicWriteFile(filepath.Join(nodeDir, "index.html"), pageBuf.Bytes(), 0o644); err != nil {
		return fmt.Errorf("write index.html: %w", err)
	}

	if writeAssets {
		// Write raw README.md.
		if err := rt.AtomicWriteFile(filepath.Join(nodeDir, "README.md"), rawContent, 0o644); err != nil {
			return fmt.Errorf("write README.md: %w", err)
		}

		// Write raw meta.yaml.
		if len(rawMeta) > 0 {
			if err := rt.AtomicWriteFile(filepath.Join(nodeDir, "meta.yaml"), rawMeta, 0o644); err != nil {
				return fmt.Errorf("write meta.yaml: %w", err)
			}
		}

		// Write meta.json (converted).
		if meta != nil {
			metaMap := map[string]any{"tags": meta.Tags()}
			if entity != "" {
				metaMap["entity"] = entity
			}
			if mj, err := json.MarshalIndent(metaMap, "", "  "); err == nil {
				_ = rt.AtomicWriteFile(filepath.Join(nodeDir, "meta.json"), mj, 0o644)
			}
		}

		// Write raw stats.json.
		if stats != nil {
			if sj, err := stats.ToJSON(); err == nil {
				_ = rt.AtomicWriteFile(filepath.Join(nodeDir, "stats.json"), sj, 0o644)
			}
		}

		// Write stats.yaml (converted).
		if stats != nil {
			if sj, err := stats.ToJSON(); err == nil {
				var statsMap map[string]any
				if json.Unmarshal(sj, &statsMap) == nil {
					if sy, err := yaml.Marshal(statsMap); err == nil {
						_ = rt.AtomicWriteFile(filepath.Join(nodeDir, "stats.yaml"), sy, 0o644)
					}
				}
			}
		}

		// Copy images.
		t.copyNodeAssets(ctx, k, nid, nodeDir, keg.AssetKindImage)

		// Copy file attachments.
		t.copyNodeAssets(ctx, k, nid, nodeDir, keg.AssetKindItem)
	}

	return nil
}

func (t *Tap) copyNodeAssets(ctx context.Context, k *keg.Keg, nid keg.NodeId, nodeDir string, kind keg.AssetKind) {
	rt := t.Runtime
	var names []string
	var readFn func(context.Context, keg.NodeId, string) ([]byte, error)

	switch kind {
	case keg.AssetKindImage:
		if ri, ok := k.Repo.(keg.RepositoryImages); ok {
			var err error
			names, err = ri.ListImages(ctx, nid)
			if err != nil {
				return
			}
			readFn = ri.ReadImage
		}
	case keg.AssetKindItem:
		if rf, ok := k.Repo.(keg.RepositoryFiles); ok {
			var err error
			names, err = rf.ListFiles(ctx, nid)
			if err != nil {
				return
			}
			readFn = rf.ReadFile
		}
	}

	for _, name := range names {
		data, err := readFn(ctx, nid, name)
		if err != nil {
			continue
		}
		_ = rt.AtomicWriteFile(filepath.Join(nodeDir, name), data, 0o644)
	}
}

func (t *Tap) generateIndexPage(
	tmpl *template.Template,
	entries []keg.NodeIndexEntry,
	nodeByID map[string]siteNodeRef,
	outputDir, siteTitle, summary, baseURL string,
	hasSearch bool,
) error {
	nodes := make([]siteNodeRef, 0, len(entries))
	for _, e := range entries {
		if ref, ok := nodeByID[e.ID]; ok {
			nodes = append(nodes, ref)
		}
	}
	sortNodeRefsByUpdated(nodes)

	id := indexData{
		SiteTitle: siteTitle,
		Summary:   summary,
		BaseURL:   baseURL,
		Nodes:     nodes,
	}

	var contentBuf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&contentBuf, "index.html", id); err != nil {
		return err
	}

	ld := layoutData{
		Title:     "Home",
		SiteTitle: siteTitle,
		BaseURL:   baseURL,
		HasSearch: hasSearch,
		Content:   template.HTML(contentBuf.String()),
	}

	var pageBuf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&pageBuf, "layout.html", ld); err != nil {
		return err
	}

	return t.Runtime.AtomicWriteFile(filepath.Join(outputDir, "index.html"), pageBuf.Bytes(), 0o644)
}

func (t *Tap) generateTagsIndexPage(
	tmpl *template.Template,
	tagRefs []siteTagRef,
	outputDir, siteTitle, baseURL string,
	hasSearch bool,
) error {
	td := tagsData{BaseURL: baseURL, Tags: tagRefs}

	var contentBuf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&contentBuf, "tags.html", td); err != nil {
		return err
	}

	ld := layoutData{
		Title:     "Tags",
		SiteTitle: siteTitle,
		BaseURL:   baseURL,
		HasSearch: hasSearch,
		Content:   template.HTML(contentBuf.String()),
	}

	var pageBuf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&pageBuf, "layout.html", ld); err != nil {
		return err
	}

	tagsDir := filepath.Join(outputDir, "tags")
	if err := t.Runtime.Mkdir(tagsDir, 0o755, true); err != nil {
		return err
	}

	return t.Runtime.AtomicWriteFile(filepath.Join(tagsDir, "index.html"), pageBuf.Bytes(), 0o644)
}

func (t *Tap) generateTagPage(
	tmpl *template.Template,
	tagName string,
	nodes []siteNodeRef,
	outputDir, siteTitle, baseURL string,
	hasSearch bool,
) error {
	td := tagData{
		TagName: tagName,
		Count:   len(nodes),
		BaseURL: baseURL,
		Nodes:   nodes,
	}

	var contentBuf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&contentBuf, "tag.html", td); err != nil {
		return err
	}

	ld := layoutData{
		Title:     "Tag: " + tagName,
		SiteTitle: siteTitle,
		BaseURL:   baseURL,
		HasSearch: hasSearch,
		Content:   template.HTML(contentBuf.String()),
	}

	var pageBuf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&pageBuf, "layout.html", ld); err != nil {
		return err
	}

	tagDir := filepath.Join(outputDir, "tags", tagName)
	if err := t.Runtime.Mkdir(tagDir, 0o755, true); err != nil {
		return err
	}

	return t.Runtime.AtomicWriteFile(filepath.Join(tagDir, "index.html"), pageBuf.Bytes(), 0o644)
}

func (t *Tap) generateChangesPage(
	tmpl *template.Template,
	nodes []siteNodeRef,
	outputDir, siteTitle, baseURL string,
	hasSearch bool,
) error {
	cd := changesData{BaseURL: baseURL, Nodes: nodes}

	var contentBuf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&contentBuf, "changes.html", cd); err != nil {
		return err
	}

	ld := layoutData{
		Title:     "Changes",
		SiteTitle: siteTitle,
		BaseURL:   baseURL,
		HasSearch: hasSearch,
		Content:   template.HTML(contentBuf.String()),
	}

	var pageBuf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&pageBuf, "layout.html", ld); err != nil {
		return err
	}

	changesDir := filepath.Join(outputDir, "changes")
	if err := t.Runtime.Mkdir(changesDir, 0o755, true); err != nil {
		return err
	}

	return t.Runtime.AtomicWriteFile(filepath.Join(changesDir, "index.html"), pageBuf.Bytes(), 0o644)
}

func parseSiteTemplates() (*template.Template, error) {
	return template.ParseFS(siteTemplateFS, "site_templates/*.html")
}

func sortNodeRefsByUpdated(refs []siteNodeRef) {
	sort.Slice(refs, func(i, j int) bool {
		return refs[i].Updated.After(refs[j].Updated)
	})
}

// runPagefind attempts to run pagefind on the output directory.
// Returns true if pagefind ran successfully.
func (t *Tap) runPagefind(outputDir string) bool {
	rt := t.Runtime

	// Try pagefind directly.
	if path, err := exec.LookPath("pagefind"); err == nil {
		cmd := exec.Command(path, "--site", outputDir)
		if err := cmd.Run(); err == nil {
			return true
		}
	}

	// Try npx pagefind.
	if path, err := exec.LookPath("npx"); err == nil {
		cmd := exec.Command(path, "pagefind", "--site", outputDir)
		if err := cmd.Run(); err == nil {
			return true
		}
	}

	rt.Logger().Warn("pagefind not found; site generated without search. Install pagefind: npm install -g pagefind")
	return false
}
