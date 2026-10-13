package keg

import (
	"strings"
	"testing"
)

func TestRelationshipAliasRenderingAndParsing(t *testing.T) {
	links := []AliasEntry{{Alias: "docs", URL: "keg:@team/manual"}}
	ref, err := ParseNodeRef("keg:~docs/12")
	if err != nil || ref.Form != RefSettingsAlias || ref.String() != "keg:~docs/12" {
		t.Fatalf("parse: %+v %v", ref, err)
	}
	opts := RenderOptions{Aliases: links, KegResolver: func(ns, alias, id string) string { return "/@" + ns + "/" + alias + "/" + id }}
	got, ok := ResolveNodeLink("keg:~docs/12#heading", opts)
	if !ok || got != "/@team/manual/12#heading" {
		t.Fatalf("resolved: %q %t", got, ok)
	}
	opts.Aliases[0].URL = "keg:@team/renamed"
	got, ok = ResolveNodeLink("keg:~docs/12", opts)
	if !ok || got != "/@team/renamed/12" {
		t.Fatalf("remapped: %q %t", got, ok)
	}
	if _, ok = ResolveNodeLink("keg:~missing/12", opts); ok {
		t.Fatal("missing alias resolved")
	}
	if err := ValidateAliases(append(links, links[0])); err == nil {
		t.Fatal("duplicate aliases accepted")
	}
	if _, _, err := RelationshipTarget("keg:manual"); err == nil {
		t.Fatal("ambiguous target accepted")
	}
	if _, ok, err := RelationshipTarget("https://example.com/docs"); err != nil || ok {
		t.Fatal("external link grants traversal")
	}
}

func TestOrientationStateNeedsScopeButNotRevision(t *testing.T) {
	state := OrientationState{Root: "@team/+root", Active: "@team/+child"}
	encoded, err := EncodeOrientationState(state)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeOrientationState(encoded)
	if err != nil || decoded.Root != state.Root || decoded.Active != state.Active {
		t.Fatalf("round trip: %+v %v", decoded, err)
	}
}

// A keg: link outside the syntax renders as a dead link and links nothing,
// so it is reported, with the qualified form when it is a cross-KEG link
// missing its @. A ~alias the settings do not name is reported too; a link
// to a KEG's root node, keg:@ns/keg/0, is valid.
func TestKegLinkProblems(t *testing.T) {
	aliases := []AliasEntry{{Alias: "docs", URL: "keg:@team/manual"}}
	src := []byte("# N\n\n" +
		"[guide](keg:outsider/guide/1) [again](keg:outsider/guide/1) " +
		"[odd](keg:nothing) [root](keg:@team/manual/0) [local](keg:other/4) " +
		"[docs](keg:~docs/2) [missing](keg:~gone/3) [web](https://example.com)\n")
	problems := KegLinkProblems(src, aliases)
	if len(problems) != 3 {
		t.Fatalf("problems = %q, want three", problems)
	}
	if !strings.Contains(problems[0], "keg:@outsider/guide/1") {
		t.Fatalf("unqualified link = %q, want the qualified form suggested", problems[0])
	}
	if !strings.Contains(problems[1], `"keg:nothing"`) || !strings.Contains(problems[2], `"keg:~gone/3"`) {
		t.Fatalf("problems = %q", problems)
	}
}
