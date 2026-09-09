package docsite

import (
	"encoding/json"
	"strings"
	"testing"
)

func testNav(t *testing.T) *Nav {
	t.Helper()
	src := `base: /rune
exclude:
  - guides/**
  - release-guru/**
groups:
  - group: Start here
    pages:
      - overview.md
      - path: user-guide/README.md
        label: User guide
  - group: Examples
    pages:
      - examples/README.md
      - "examples/*/README.md"
  - group: Editors
    links:
      - label: Editor setup (LSP)
        url: https://example.invalid/editors
`
	nav, err := ParseNav([]byte(src), "docs/nav.yaml")
	if err != nil {
		t.Fatalf("ParseNav: %v", err)
	}
	return nav
}

func testPages() []string {
	return []string{
		"overview.md",
		"user-guide/README.md",
		"examples/README.md",
		"examples/caching/README.md",
		"examples/go-service/README.md",
	}
}

func TestSidebar(t *testing.T) {
	got, err := Sidebar(testNav(t), testPages())
	if err != nil {
		t.Fatalf("Sidebar: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}

	start := got[0]
	if start.Label != "Start here" {
		t.Errorf("Label = %q", start.Label)
	}
	if start.Items[0].Slug != "overview" || start.Items[0].Label != "" {
		t.Errorf("Items[0] = %+v, want slug overview with no label override", start.Items[0])
	}
	if start.Items[1].Slug != "user-guide" || start.Items[1].Label != "User guide" {
		t.Errorf("Items[1] = %+v, want slug user-guide labelled \"User guide\"", start.Items[1])
	}

	// The glob expands alphabetically and does not duplicate the explicit entry.
	examples := got[1]
	wantSlugs := []string{"examples", "examples/caching", "examples/go-service"}
	if len(examples.Items) != len(wantSlugs) {
		t.Fatalf("examples items = %d, want %d (%+v)", len(examples.Items), len(wantSlugs), examples.Items)
	}
	for i, want := range wantSlugs {
		if examples.Items[i].Slug != want {
			t.Errorf("examples.Items[%d].Slug = %q, want %q", i, examples.Items[i].Slug, want)
		}
	}

	editors := got[2]
	if editors.Items[0].Link != "https://example.invalid/editors" {
		t.Errorf("link item = %+v", editors.Items[0])
	}
	if editors.Items[0].Label != "Editor setup (LSP)" {
		t.Errorf("link label = %q", editors.Items[0].Label)
	}
}

// TestSidebarJSONShape pins the JSON Starlight consumes: slug entries must not
// emit an empty "link", and vice versa.
func TestSidebarJSONShape(t *testing.T) {
	groups, err := Sidebar(testNav(t), testPages())
	if err != nil {
		t.Fatalf("Sidebar: %v", err)
	}
	data, err := json.Marshal(groups)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	s := string(data)
	if strings.Contains(s, `"link":""`) || strings.Contains(s, `"slug":""`) || strings.Contains(s, `"label":""`) {
		t.Errorf("empty fields must be omitted, got %s", s)
	}
	if !strings.Contains(s, `{"slug":"overview"}`) {
		t.Errorf("expected a bare slug item, got %s", s)
	}
}

func TestSidebarRejectsAMissingPage(t *testing.T) {
	_, err := Sidebar(testNav(t), []string{"overview.md"})
	if err == nil {
		t.Fatal("Sidebar() = nil error, want an error for a manifest entry with no file")
	}
	if !strings.Contains(err.Error(), "user-guide/README.md") {
		t.Errorf("error = %q, want it to name the missing page", err)
	}
}

func TestSidebarRejectsAGlobThatMatchesNothing(t *testing.T) {
	nav := testNav(t)
	nav.Groups[1].Pages = []Page{{Path: "recipes/*/README.md"}}
	_, err := Sidebar(nav, testPages())
	if err == nil || !strings.Contains(err.Error(), "recipes/*/README.md") {
		t.Fatalf("err = %v, want an error naming the unmatched glob", err)
	}
}

func TestMatchExcluded(t *testing.T) {
	nav := testNav(t)
	tests := map[string]bool{
		"guides/caching.md":            true,
		"guides/README.md":             true,
		"release-guru/SKILL.md":        true,
		"release-guru/references/x.md": true,
		"how-to/caching.md":            false,
		"overview.md":                  false,
	}
	for in, want := range tests {
		t.Run(in, func(t *testing.T) {
			if got := MatchExcluded(nav, in); got != want {
				t.Errorf("MatchExcluded(%q) = %v, want %v", in, got, want)
			}
		})
	}
}

func TestNavPages(t *testing.T) {
	got, err := NavPages(testNav(t), testPages())
	if err != nil {
		t.Fatalf("NavPages: %v", err)
	}
	want := []string{
		"overview.md",
		"user-guide/README.md",
		"examples/README.md",
		"examples/caching/README.md",
		"examples/go-service/README.md",
	}
	if len(got) != len(want) {
		t.Fatalf("NavPages() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("NavPages()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
