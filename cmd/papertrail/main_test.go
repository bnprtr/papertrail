package main

import (
	"strings"
	"testing"
)

func TestBumpSemver(t *testing.T) {
	t.Parallel()

	got, err := bumpSemver("v1.2.3", bumpPatch)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "v1.2.4" {
		t.Fatalf("got %q, want %q", got, "v1.2.4")
	}

	got, err = bumpSemver("v1.2.3", bumpMinor)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "v1.3.0" {
		t.Fatalf("got %q, want %q", got, "v1.3.0")
	}

	got, err = bumpSemver("v1.2.3", bumpMajor)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "v2.0.0" {
		t.Fatalf("got %q, want %q", got, "v2.0.0")
	}
}

func TestParsePRType(t *testing.T) {
	t.Parallel()

	cfg := prPolicy{
		TitleEnabled: true,
		AllowedTypes: []string{"feat", "fix", "docs"},
		TypeAliases:  map[string]string{"feature": "feat"},
	}

	typ, err := parsePRType(cfg, "feat(cli): add thing")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if typ != "feat" {
		t.Fatalf("got %q, want %q", typ, "feat")
	}

	typ, err = parsePRType(cfg, "feature: add thing")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if typ != "feat" {
		t.Fatalf("got %q, want %q", typ, "feat")
	}

	if _, err := parsePRType(cfg, "bad: no"); err == nil {
		t.Fatalf("expected error")
	}
}

func TestCanonicalizeType_Alias(t *testing.T) {
	t.Parallel()

	var m releaseManifest
	m.Types.Aliases = map[string]string{
		"CI": "PATCH",
	}
	got := canonicalizeFragmentType("ci", m)
	if got != "PATCH" {
		t.Fatalf("got %q, want %q", got, "PATCH")
	}
}

func TestRenderReleaseSection_DeterministicOrdering(t *testing.T) {
	t.Parallel()

	var m releaseManifest
	m.Types.Order = []string{"BREAKING CHANGE", "PATCH"}
	m.Changelog.Components = []string{"A", "B"}

	items := []item{
		{Path: "changelog.d/20250101_b.yml", Frag: fragment{Component: "B", Type: "PATCH", Summary: "b"}},
		{Path: "changelog.d/20250101_a.yml", Frag: fragment{Component: "A", Type: "PATCH", Summary: "a"}},
		{Path: "changelog.d/20250101_a_break.yml", Frag: fragment{Component: "A", Type: "BREAKING CHANGE", Summary: "z"}},
	}

	section, notes := renderReleaseSection("v0.1.0", "2025-12-23", items, m)
	s := string(section)
	n := string(notes)

	// Component A first (per config), BREAKING CHANGE before PATCH (per config), then filenames.
	wantOrder := []string{
		"### A",
		"**breaking change**: z.",
		"**patch**: a.",
		"### B",
		"**patch**: b.",
	}
	idx := 0
	for _, w := range wantOrder {
		i := strings.Index(s[idx:], w)
		if i < 0 {
			t.Fatalf("missing %q in section:\n%s", w, s)
		}
		idx += i + len(w)
	}

	if !strings.Contains(n, "## v0.1.0") {
		t.Fatalf("release notes missing version header:\n%s", n)
	}
}


