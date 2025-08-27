package keg_test

import (
	"testing"
	"time"

	"github.com/jlrickert/tapper/pkg/keg"
)

func TestTagsIndex_Data(t *testing.T) {
	tests := []struct {
		name string
		idx  *keg.TagsIndex
		want string
	}{
		{
			name: "empty map",
			idx:  keg.NewTagsIndex(),
			want: "",
		},
		{
			name: "single tag sorted",
			idx: &keg.TagsIndex{
				Tags: map[string][]keg.NodeID{
					"zeke": {3, 10, 45},
				},
			},
			want: "zeke 3 10 45\n",
		},
		// {
		// 	name: "unsorted and duplicate ids normalized",
		// 	idx: &keg.TagsIndex{
		// 		Tags: map[string][]keg.NodeID{
		// 			"draft": {12, 10, 12, 10},
		// 		},
		// 	},
		// 	want: "draft 10 12\n",
		// },
		{
			name: "multiple tags lexicographic order",
			idx: &keg.TagsIndex{
				Tags: map[string][]keg.NodeID{
					"b": {2},
					"a": {1},
				},
			},
			want: "a 1\nb 2\n",
		},
		{
			name: "omit empty-list tag",
			idx: &keg.TagsIndex{
				Tags: map[string][]keg.NodeID{
					"keep":  {5},
					"empty": {},
				},
			},
			want: "keep 5\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotB, err := tc.idx.Data(t.Context())
			if err != nil {
				t.Fatalf("Data() returned error: %v", err)
			}
			got := string(gotB)
			if got != tc.want {
				t.Fatalf("unexpected output:\nwant:\n%q\ngot:\n%q", tc.want, got)
			}
		})
	}
}

func TestLinksIndex_Data(t *testing.T) {
	tests := []struct {
		name string
		idx  *keg.LinksIndex
		want string
	}{
		{
			name: "empty",
			idx:  keg.NewLinksIndex(),
			want: "",
		},
		{
			name: "simple links",
			idx: &keg.LinksIndex{
				Links: map[keg.NodeID][]keg.NodeID{
					1: {3, 5},
					2: {3},
				},
			},
			want: "1\t3 5\n2\t3\n",
		},
		{
			name: "src with no destinations emits empty second column",
			idx: &keg.LinksIndex{
				Links: map[keg.NodeID][]keg.NodeID{
					10: {},
					3:  {10, 42},
				},
			},
			want: "3\t10 42\n10\t\n",
		},
		{
			name: "dedupe and sort destinations",
			idx: &keg.LinksIndex{
				Links: map[keg.NodeID][]keg.NodeID{
					7: {5, 3, 5, 3},
				},
			},
			want: "7\t3 5\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotB, err := tc.idx.Data(nil)
			if err != nil {
				t.Fatalf("Data() error: %v", err)
			}
			got := string(gotB)
			if got != tc.want {
				t.Fatalf("unexpected output:\nwant:\n%q\ngot:\n%q", tc.want, got)
			}
		})
	}
}

func TestBacklinksIndex_Data(t *testing.T) {
	tests := []struct {
		name string
		idx  *keg.BacklinksIndex
		want string
	}{
		{
			name: "empty",
			idx:  keg.NewBacklinksIndex(),
			want: "",
		},
		{
			name: "simple backlinks",
			idx: &keg.BacklinksIndex{
				Backlinks: map[keg.NodeID][]keg.NodeID{
					3:  {1, 2},
					10: {3},
				},
			},
			want: "3\t1 2\n10\t3\n",
		},
		{
			name: "dedupe and sort sources",
			idx: &keg.BacklinksIndex{
				Backlinks: map[keg.NodeID][]keg.NodeID{
					42: {7, 3, 7, 3},
				},
			},
			want: "42\t3 7\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotB, err := tc.idx.Data(t.Context())
			if err != nil {
				t.Fatalf("Data() error: %v", err)
			}
			got := string(gotB)
			if got != tc.want {
				t.Fatalf("unexpected output:\nwant:\n%q\ngot:\n%q", tc.want, got)
			}
		})
	}
}

func TestNodesIndex_Data(t *testing.T) {
	// time layout matches nodes index serialization in pkg (2006-01-02 15:04:05Z)
	const layout = "2006-01-02 15:04:05Z"
	t1, _ := time.Parse(layout, "2025-08-09 17:44:04Z")
	t2, _ := time.Parse(layout, "2025-08-11 00:12:29Z")

	tests := []struct {
		name string
		idx  *keg.NodesIndex
		want string
	}{
		{
			name: "empty nodes",
			idx:  keg.NewNodesIndex(),
			want: "",
		},
		{
			name: "nodes with updated timestamps and titles",
			idx: &keg.NodesIndex{
				Nodes: []keg.NodeRef{
					{ID: 3, Updated: t1, Title: "Zeke AI utility (zeke)"},
					{ID: 12, Updated: t2, Title: "Idiomatic Go error handling"},
				},
			},
			want: "3\t2025-08-09 17:44:04Z\tZeke AI utility (zeke)\n12\t2025-08-11 00:12:29Z\tIdiomatic Go error handling\n",
		},
		{
			name: "title tab replaced with space",
			idx: &keg.NodesIndex{
				Nodes: []keg.NodeRef{
					{ID: 7, Title: "Title\tWith\tTabs"},
				},
			},
			want: "7\t\tTitle With Tabs\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotB, err := tc.idx.Data(t.Context())
			if err != nil {
				t.Fatalf("Data() error: %v", err)
			}
			got := string(gotB)
			if got != tc.want {
				t.Fatalf("unexpected output:\nwant:\n%q\ngot:\n%q", tc.want, got)
			}
		})
	}
}
