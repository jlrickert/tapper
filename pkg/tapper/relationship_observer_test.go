package tapper

import (
	"context"
	"fmt"
	"github.com/jlrickert/tapper/pkg/keg"
	"reflect"
	"testing"
)

type observedLinksKeg struct{ keg.Keg }

// Node 1 relates to 3 and node 2 relates to 4.
func (k *observedLinksKeg) RelatedNodes(_ context.Context, o keg.RelatedNodesOptions) (*keg.RelatedNodesResult, error) {
	out := &keg.RelatedNodesResult{}
	for _, id := range o.NodeIDs {
		to := map[int]string{1: "3", 2: "4"}[id.ID]
		out.Entries = append(out.Entries, keg.NodeIndexEntry{ID: to})
		out.Pairs = append(out.Pairs, keg.RelatedPair{From: id.Path(), To: to})
	}
	return out, nil
}
func TestRelationshipObserverOnlyActualPairsInResultPage(t *testing.T) {
	k := &observedLinksKeg{}
	tap := &Tap{KegResolver: func(context.Context, KegTargetOptions, FlightRole) (keg.Keg, error) { return k, nil }}
	for _, back := range []bool{false, true} {
		var got []Relationship
		ctx := WithRelationshipResultObserver(context.Background(), func(_ context.Context, owner keg.Keg, rows []Relationship) {
			if owner != k {
				t.Fatal("lost owner")
			}
			got = rows
		})
		var err error
		if back {
			_, err = tap.Backlinks(ctx, BacklinksOptions{NodeIDs: []string{"1", "2"}, IdOnly: true, Offset: 1, Limit: 1})
		} else {
			_, err = tap.Links(ctx, LinksOptions{NodeIDs: []string{"1", "2"}, IdOnly: true, Offset: 1, Limit: 1})
		}
		if err != nil {
			t.Fatal(err)
		}
		want := Relationship{Source: keg.NodeId{ID: 2}, Target: keg.NodeId{ID: 4}}
		if back {
			want.Source, want.Target = want.Target, want.Source
		}
		if !reflect.DeepEqual(got, []Relationship{want}) {
			t.Fatalf("pairs=%+v want=%+v", got, want)
		}
	}
}

type crossObservedKeg struct {
	keg.Keg
	fail      bool
	malformed bool
}

// Every input relates to the same two cross-keg nodes.
func (k *crossObservedKeg) RelatedNodes(_ context.Context, o keg.RelatedNodesOptions) (*keg.RelatedNodesResult, error) {
	if k.fail {
		return nil, fmt.Errorf("lookup failed")
	}
	targets := []string{"keg:other/4", "keg:@team/third/4"}
	if k.malformed {
		targets = []string{"keg:@broken"}
	}
	out := &keg.RelatedNodesResult{}
	for _, target := range targets {
		out.Entries = append(out.Entries, keg.NodeIndexEntry{ID: target})
	}
	for _, id := range o.NodeIDs {
		for _, target := range targets {
			out.Pairs = append(out.Pairs, keg.RelatedPair{From: id.Path(), To: target})
		}
	}
	return out, nil
}
func TestRelationshipObserverFullIdentitiesAndFailures(t *testing.T) {
	for _, back := range []bool{false, true} {
		for _, fail := range []string{"", "lookup", "malformed"} {
			k := &crossObservedKeg{fail: fail == "lookup", malformed: fail == "malformed"}
			tap := &Tap{KegResolver: func(context.Context, KegTargetOptions, FlightRole) (keg.Keg, error) { return k, nil }}
			var got []Relationship
			ctx := WithRelationshipResultObserver(context.Background(), func(_ context.Context, _ keg.Keg, p []Relationship) { got = p })
			var err error
			if back {
				_, err = tap.Backlinks(ctx, BacklinksOptions{NodeIDs: []string{"1", "2"}, IdOnly: true})
			} else {
				_, err = tap.Links(ctx, LinksOptions{NodeIDs: []string{"1", "2"}, IdOnly: true})
			}
			if fail != "" {
				if err == nil || len(got) != 0 {
					t.Fatalf("%s emitted pairs: %+v, %v", fail, got, err)
				}
				continue
			}
			if err != nil || len(got) != 4 {
				t.Fatalf("lost equal-ID cross-KEG pairs: %+v, %v", got, err)
			}
			for _, p := range got {
				a, b := p.Source, p.Target
				if back {
					a, b = b, a
				}
				if a.Alias != "" || b.Alias == "" {
					t.Fatalf("lost direction/identity: %+v", p)
				}
			}
		}
	}
}
