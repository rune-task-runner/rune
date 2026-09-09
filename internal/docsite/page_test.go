package docsite

import (
	"strings"
	"testing"
)

func TestFrontmatter(t *testing.T) {
	got := Frontmatter(Front{Title: "Caching", Description: "Opt-in skipping."})
	want := "---\ntitle: \"Caching\"\ndescription: \"Opt-in skipping.\"\n---\n\n"
	if got != want {
		t.Errorf("Frontmatter() = %q, want %q", got, want)
	}
}

func TestFrontmatterQuotesDangerousValues(t *testing.T) {
	got := Frontmatter(Front{Title: `Diagnostics & the "RUNE####" codes`, Description: ""})
	if !strings.Contains(got, `title: "Diagnostics & the \"RUNE####\" codes"`) {
		t.Errorf("Frontmatter() did not escape embedded quotes: %q", got)
	}
	if strings.Contains(got, "description:") {
		t.Errorf("an empty description should be omitted, got %q", got)
	}
}

func TestTransform(t *testing.T) {
	src := "# Caching\n\nOpt-in content-hash skipping. See [parallelism](parallelism.md).\n"
	got, front := Transform(src, "how-to/caching.md", testConfig())

	if front.Title != "Caching" {
		t.Errorf("front.Title = %q, want %q", front.Title, "Caching")
	}
	if front.Description != "Opt-in content-hash skipping. See parallelism." {
		t.Errorf("front.Description = %q", front.Description)
	}
	if !strings.HasPrefix(got, "---\ntitle: \"Caching\"\n") {
		t.Errorf("output does not start with frontmatter: %q", got)
	}
	if strings.Contains(got, "# Caching") {
		t.Errorf("the H1 should be stripped, got %q", got)
	}
	if !strings.Contains(got, "(/rune/how-to/parallelism/)") {
		t.Errorf("the link was not rewritten: %q", got)
	}
}

func TestTransformFallsBackToTheSlugForATitlelessPage(t *testing.T) {
	_, front := Transform("Just prose.\n", "how-to/caching.md", testConfig())
	if front.Title != "caching" {
		t.Errorf("front.Title = %q, want the slug's last segment %q", front.Title, "caching")
	}
}
