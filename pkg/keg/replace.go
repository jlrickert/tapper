package keg

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// MaxReplaceNodes caps how many nodes one ReplaceText may change, so a
// careless pattern cannot rewrite a whole large keg in one call.
const MaxReplaceNodes = 500

// A short pattern and a long replacement multiply: a pattern that matches at
// every position, or at every word boundary, turns a small node and a small
// request into gigabytes. Each limit is checked before the replacement text
// is built, so an oversized replace allocates nothing for it.
const (
	// MaxReplaceNodeBytes caps the text a replace builds for one node: its
	// body and metadata, where each matched.
	MaxReplaceNodeBytes = 8 << 20
	// MaxReplaceTotalBytes caps every changed node's text after a replace,
	// together.
	MaxReplaceTotalBytes = 32 << 20
	// MaxReplaceNodeMatches caps the matches in one node's body or metadata.
	MaxReplaceNodeMatches = 100_000
)

var errReplaceTooLarge = fmt.Errorf("the replacement would grow the notes past %d MiB in all; narrow it with a query or a more specific find: %w", MaxReplaceTotalBytes>>20, ErrInvalid)

// ReplaceTextOptions is a keg-wide find and replace.
type ReplaceTextOptions struct {
	// Find is literal text, or with Regex a Go regular expression.
	Find string `json:"find"`
	// Replace is the replacement; with Regex, $1 and ${name} expand groups.
	Replace string `json:"replace"`
	Regex   bool   `json:"regex,omitempty"`
	// Query narrows the nodes considered, in node_list's query syntax. Empty
	// considers every node.
	Query string `json:"query,omitempty"`
	// IncludeMeta replaces in each node's metadata YAML as well as its body.
	IncludeMeta bool `json:"include_meta,omitempty"`
	// DryRun reports what would change and writes nothing.
	DryRun bool `json:"dry_run,omitempty"`
}

// ReplacedNode is one node a replace changed, or would change.
type ReplacedNode struct {
	ID      NodeId `json:"id"`
	Matches int    `json:"matches"`
	// ContentBefore/After and MetaBefore/After are set for the parts that
	// change, on nodes that would be written.
	ContentBefore string `json:"content_before,omitempty"`
	ContentAfter  string `json:"content_after,omitempty"`
	MetaBefore    string `json:"meta_before,omitempty"`
	MetaAfter     string `json:"meta_after,omitempty"`
	// Error says why a node that matched is left alone: the write would be
	// refused by any rule a node edit is held to (unparseable metadata, a
	// body opening with frontmatter, the keg's schemas). A dry run reports
	// the same errors a real run would.
	Error string `json:"error,omitempty"`
	// The written node's tokens; empty on a dry run or a skipped node.
	Hash        string `json:"hash,omitempty"`
	ContentHash string `json:"content_hash,omitempty"`
	MetaHash    string `json:"meta_hash,omitempty"`
}

type ReplaceTextResult struct {
	Nodes []ReplacedNode `json:"nodes"`
}

// ReplaceText replaces text across the keg as one write: every node it
// changes is written, or none is. A node whose replacement would be refused
// is reported with an Error (and no bodies) and left as it was; the rest are
// written. MaxReplaceNodes counts every changed node, written or refused.
func (k *LocalKeg) ReplaceText(ctx context.Context, opts ReplaceTextOptions) (ReplaceTextResult, error) {
	if opts.DryRun {
		return withKegReadValue(ctx, k, func(ctx context.Context) (ReplaceTextResult, error) { return k.replaceText(ctx, opts) })
	}
	return withKegAtomicWriteValue(ctx, k, func(ctx context.Context) (ReplaceTextResult, error) { return k.replaceText(ctx, opts) })
}

func (k *LocalKeg) replaceText(ctx context.Context, opts ReplaceTextOptions) (ReplaceTextResult, error) {
	if opts.Find == "" {
		return ReplaceTextResult{}, fmt.Errorf("find is required: %w", ErrInvalid)
	}
	// measure counts the matches in text and how long the text would be
	// after the replace, without building it. ok is false past
	// MaxReplaceNodeMatches.
	measure := func(text string) (n, size int, ok bool) {
		n = strings.Count(text, opts.Find)
		return n, len(text) + n*(len(opts.Replace)-len(opts.Find)), n <= MaxReplaceNodeMatches
	}
	replace := func(text string) string { return strings.ReplaceAll(text, opts.Find, opts.Replace) }
	if opts.Regex {
		re, err := regexp.Compile(opts.Find)
		if err != nil {
			return ReplaceTextResult{}, fmt.Errorf("find is not a valid regular expression: %v: %w", err, ErrInvalid)
		}
		measure = func(text string) (n, size int, ok bool) {
			matches := re.FindAllStringSubmatchIndex(text, MaxReplaceNodeMatches+1)
			if len(matches) > MaxReplaceNodeMatches {
				return len(matches), 0, false
			}
			size = len(text)
			var expanded []byte
			for _, m := range matches {
				expanded = re.ExpandString(expanded[:0], opts.Replace, text, m)
				// Past the node cap the exact size no longer matters.
				if size += len(expanded) - (m[1] - m[0]); size > MaxReplaceNodeBytes {
					break
				}
			}
			return len(matches), size, true
		}
		replace = func(text string) string { return re.ReplaceAllString(text, opts.Replace) }
	}
	total := 0
	// replacePart replaces in one part of a node. The error is the node's,
	// for a part too large to replace; the whole replace fails only when the
	// total grows past MaxReplaceTotalBytes.
	replacePart := func(text string, nodeSize *int) (string, int, string, error) {
		n, size, ok := measure(text)
		if n == 0 {
			return text, 0, "", nil
		}
		if !ok {
			return text, n, fmt.Sprintf("this note has more than %d matches; narrow the find", MaxReplaceNodeMatches), nil
		}
		if *nodeSize += size; *nodeSize > MaxReplaceNodeBytes {
			return text, n, fmt.Sprintf("the replacement would grow this note past %d MiB", MaxReplaceNodeBytes>>20), nil
		}
		if total += size; total > MaxReplaceTotalBytes {
			return text, n, "", errReplaceTooLarge
		}
		return replace(text), n, "", nil
	}

	var ids []NodeId
	if q := strings.TrimSpace(opts.Query); q != "" {
		entries, err := k.query(ctx, QueryOptions{Expr: q})
		if err != nil {
			return ReplaceTextResult{}, err
		}
		for _, entry := range entries {
			if id, err := ParseNode(entry.ID); err == nil && id != nil {
				ids = append(ids, *id)
			}
		}
	} else {
		listed, err := k.Repo.ListNodes(ctx)
		if err != nil {
			return ReplaceTextResult{}, err
		}
		ids = listed
	}

	result := ReplaceTextResult{Nodes: []ReplacedNode{}}
	var updates []NodeUpdateOptions
	var written []int
	for _, id := range ids {
		view, err := k.ReadNode(ctx, id)
		if err != nil {
			continue
		}
		node := ReplacedNode{ID: id}
		nodeSize := 0
		content, n, tooLarge, err := replacePart(string(view.Content), &nodeSize)
		if err != nil {
			return ReplaceTextResult{}, err
		}
		node.Matches += n
		contentChanged := content != string(view.Content)
		if contentChanged {
			node.ContentBefore, node.ContentAfter = string(view.Content), content
		}
		metaChanged := false
		if opts.IncludeMeta && tooLarge == "" {
			meta, n, metaTooLarge, err := replacePart(string(view.Meta), &nodeSize)
			if err != nil {
				return ReplaceTextResult{}, err
			}
			node.Matches += n
			tooLarge = metaTooLarge
			if metaChanged = meta != string(view.Meta); metaChanged {
				node.MetaBefore, node.MetaAfter = string(view.Meta), meta
			}
		}
		if tooLarge != "" {
			// Left alone like any refused node, and counted against the cap.
			if len(result.Nodes) == MaxReplaceNodes {
				return ReplaceTextResult{}, fmt.Errorf("this matches more than %d nodes; narrow it with a query: %w", MaxReplaceNodes, ErrInvalid)
			}
			result.Nodes = append(result.Nodes, ReplacedNode{ID: id, Matches: node.Matches, Error: tooLarge})
			continue
		}
		if !contentChanged && !metaChanged {
			continue
		}
		update := NodeUpdateOptions{ID: id, ExpectedHash: view.Hash()}
		if contentChanged {
			update.Content, update.HasContent = []byte(node.ContentAfter), true
		}
		if metaChanged {
			update.Meta, update.HasMeta = []byte(node.MetaAfter), true
		}
		// Hold each node to every rule a write would, dry run or not, so a
		// node that would be refused is reported and left alone instead of
		// failing the whole replace.
		if len(result.Nodes) == MaxReplaceNodes {
			return ReplaceTextResult{}, fmt.Errorf("this matches more than %d nodes; narrow it with a query: %w", MaxReplaceNodes, ErrInvalid)
		}
		if _, err := k.prepareNodeUpdate(ctx, update); err != nil {
			// A refused node reports why, not its bodies: it is left alone.
			result.Nodes = append(result.Nodes, ReplacedNode{ID: id, Matches: node.Matches, Error: replaceNodeError(err)})
			continue
		}
		result.Nodes = append(result.Nodes, node)
		updates = append(updates, update)
		written = append(written, len(result.Nodes)-1)
	}
	if opts.DryRun || len(updates) == 0 {
		return result, nil
	}
	for start := 0; start < len(updates); start += MaxMutationBatchSize {
		end := min(start+MaxMutationBatchSize, len(updates))
		results, err := k.updateNodes(ctx, updates[start:end])
		if err != nil {
			// Report the failing node by its place in the result, not in
			// the chunk.
			var batchErr *BatchMutationError
			if errors.As(err, &batchErr) && batchErr.Index >= 0 && start+batchErr.Index < len(written) {
				batchErr.Index = written[start+batchErr.Index]
			}
			return ReplaceTextResult{}, err
		}
		for i, res := range results {
			node := &result.Nodes[written[start+i]]
			node.Hash, node.ContentHash, node.MetaHash = res.Hash, res.ContentHash, res.MetaHash
		}
	}
	return result, nil
}

// replaceNodeError is why a node a replace leaves alone would be refused,
// worded for the node's Error.
func replaceNodeError(err error) string {
	var batchErr *BatchMutationError
	if errors.As(err, &batchErr) && batchErr.Err != nil {
		err = batchErr.Err
	}
	return strings.TrimSuffix(err.Error(), ": "+ErrInvalid.Error())
}
