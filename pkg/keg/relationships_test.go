package keg

import "testing"

func TestRelationshipAliasRenderingAndParsing(t *testing.T) {
	links := []LinkEntry{{Alias: "docs", URL: "keg:@team/manual"}}
	ref, err := ParseNodeRef("keg:~docs/12")
	if err != nil || ref.Form != RefSettingsAlias || ref.String() != "keg:~docs/12" {
		t.Fatalf("parse: %+v %v", ref, err)
	}
	opts := RenderOptions{Links: links, KegResolver: func(ns, alias, id string) string { return "/@" + ns + "/" + alias + "/" + id }}
	got, ok := ResolveNodeLink("keg:~docs/12#heading", opts)
	if !ok || got != "/@team/manual/12#heading" {
		t.Fatalf("resolved: %q %t", got, ok)
	}
	opts.Links[0].URL = "keg:@team/renamed"
	got, ok = ResolveNodeLink("keg:~docs/12", opts)
	if !ok || got != "/@team/renamed/12" {
		t.Fatalf("remapped: %q %t", got, ok)
	}
	if _, ok = ResolveNodeLink("keg:~missing/12", opts); ok {
		t.Fatal("missing alias resolved")
	}
	if err := ValidateRelationships(append(links, links[0])); err == nil {
		t.Fatal("duplicate aliases accepted")
	}
	if _, _, err := RelationshipTarget("keg:manual"); err == nil {
		t.Fatal("ambiguous target accepted")
	}
	if _, ok, err := RelationshipTarget("https://example.com/docs"); err != nil || ok {
		t.Fatal("external link grants traversal")
	}
}
