package tapper

import (
	"context"
	"fmt"
	"github.com/jlrickert/tapper/pkg/keg"
	"reflect"
	"testing"
)

type observedLinksKeg struct{ keg.Keg }

func (k *observedLinksKeg) RelatedNodes(_ context.Context, o keg.RelatedNodesOptions) ([]keg.NodeIndexEntry, error) {
	if len(o.NodeIDs) > 1 {
		return []keg.NodeIndexEntry{{ID: "3"}, {ID: "4"}}, nil
	}
	if o.NodeIDs[0].ID == 1 {
		return []keg.NodeIndexEntry{{ID: "3"}}, nil
	}
	return []keg.NodeIndexEntry{{ID: "4"}}, nil
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

func (k *crossObservedKeg) RelatedNodes(_ context.Context, o keg.RelatedNodesOptions) ([]keg.NodeIndexEntry, error) {
	if k.fail && len(o.NodeIDs) == 1 {
		return nil, fmt.Errorf("lookup failed")
	}
	if k.malformed {
		return []keg.NodeIndexEntry{{ID: "keg:@broken"}}, nil
	}
	return []keg.NodeIndexEntry{{ID: "keg:other/4"}, {ID: "keg:@team/third/4"}, {ID: "keg:other/4"}}, nil
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
