package keg

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jlrickert/cli-toolkit/clock"
)

// Node provides operations and lifecycle management for a single KEG node.
// It holds the node identifier, repository reference, and lazily-loaded node data.
type Node struct {
	ID   NodeId
	Repo Repository

	data *NodeData
}

// Init loads and initializes the node data from the repository including content,
// metadata, items, and images. Returns an error if the repository is not set or
// if any repository operation fails.
func (n *Node) Init(ctx context.Context) error {
	if n.data != nil {
		return nil
	}
	if n.Repo == nil {
		return fmt.Errorf("repo required")
	}
	content, err := n.getContent(ctx, n.ID)
	if err != nil {
		return err
	}
	meta, stats, err := n.getMetaAndStats(ctx, n.ID)
	if err != nil {
		return err
	}

	items, err := n.Repo.ListItems(ctx, n.ID)
	if err != nil {
		return err
	}

	images, err := n.Repo.ListImages(ctx, n.ID)
	if err != nil {
		return err
	}

	n.data = &NodeData{
		ID:      n.ID,
		Content: content,
		Meta:    meta,
		Stats:   stats,
		Items:   items,
		Images:  images,
	}

	return nil
}

// getContent retrieves and parses raw markdown content for a node.
func (n *Node) getContent(ctx context.Context, id NodeId) (*NodeContent, error) {
	raw, err := n.Repo.ReadContent(ctx, id)
	if err != nil {
		return nil, err
	}
	return ParseContent(ctx, raw, FormatMarkdown)
}

// getMetaAndStats retrieves and parses YAML metadata plus programmatic stats
// for a node via repository APIs.
func (n *Node) getMetaAndStats(ctx context.Context, id NodeId) (*NodeMeta, *NodeStats, error) {
	raw, err := n.Repo.ReadMeta(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	meta, err := ParseMeta(ctx, raw)
	if err != nil {
		return nil, nil, err
	}
	stats, err := n.Repo.ReadStats(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	return meta, stats, nil
}

func (n *Node) String() string { return n.ID.String() }

func (n *Node) Ref(ctx context.Context) (NodeIndexEntry, error) {
	if err := n.Init(ctx); err != nil {
		return NodeIndexEntry{}, err
	}

	return n.data.Ref(), nil
}

func (n *Node) Accessed(ctx context.Context) (time.Time, error) {
	if err := n.Init(ctx); err != nil {
		return time.Time{}, err
	}

	return n.data.Accessed(), nil
}

func (n *Node) Updated(ctx context.Context) (time.Time, error) {
	if err := n.Init(ctx); err != nil {
		return time.Time{}, err
	}

	return n.data.Updated(), nil
}

func (n *Node) Created(ctx context.Context) (time.Time, error) {
	if err := n.Init(ctx); err != nil {
		return time.Time{}, err
	}

	return n.data.Created(), nil
}

func (n *Node) Stats(ctx context.Context) (*NodeStats, error) {
	if err := n.Init(ctx); err != nil {
		return nil, err
	}
	if n.data.Stats == nil {
		return &NodeStats{}, nil
	}
	copyStats := *n.data.Stats
	copyStats.links = n.data.Stats.Links()
	return &copyStats, nil
}

func (n *Node) Tags(ctx context.Context) ([]string, error) {
	if err := n.Init(ctx); err != nil {
		return nil, err
	}

	return n.data.Tags(), nil
}

func (n *Node) Lead(ctx context.Context) (string, error) {
	if err := n.Init(ctx); err != nil {
		return "", err
	}

	return n.data.Lead(), nil
}

func (n *Node) Links(ctx context.Context) ([]NodeId, error) {
	if err := n.Init(ctx); err != nil {
		return nil, err
	}
	return n.data.Links(), nil
}

func (n *Node) ListImages(ctx context.Context) ([]string, error) {
	if err := n.Init(ctx); err != nil {
		return nil, err
	}
	return n.data.Images, nil
}

func (n *Node) ListItems(ctx context.Context) ([]string, error) {
	if err := n.Init(ctx); err != nil {
		return nil, err
	}
	return n.data.Items, nil
}

func (n *Node) Update(ctx context.Context) error {
	if err := n.Init(ctx); err != nil {
		return err
	}

	clk := clock.ClockFromContext(ctx)
	now := clk.Now()

	err1 := n.data.UpdateMeta(ctx, &now)
	if n.data.Stats == nil {
		n.data.Stats = NewStats(now)
	}
	n.data.Stats.EnsureTimes(now)
	err2 := n.Save(ctx)
	return errors.Join(err1, err2)
}

func (n *Node) Touch(ctx context.Context) error {
	if err := n.Init(ctx); err != nil {
		return err
	}

	clk := clock.ClockFromContext(ctx)
	now := clk.Now()
	n.data.Touch(ctx, &now)
	return n.Save(ctx)
}

func (n *Node) Changed(ctx context.Context) (bool, error) {
	if err := n.Init(ctx); err != nil {
		return false, err
	}
	return n.data.ContentChanged(), nil
}

func (n *Node) ClearCache() {
	n.data = nil
}

func (n *Node) Save(ctx context.Context) error {
	if err := n.Init(ctx); err != nil {
		return err
	}
	if err := n.Repo.WriteMeta(ctx, n.ID, []byte(n.data.Meta.ToYAML())); err != nil {
		return err
	}
	return n.Repo.WriteStats(ctx, n.ID, n.data.Stats)
}
