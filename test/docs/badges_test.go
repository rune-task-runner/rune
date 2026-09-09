package docs

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Badge integrity harness — enforces specs/009-docs-and-badges/contracts/badges.md.
// It checks the README badge row's shape and targeting (never live HTTP: CI has no
// network, mirroring links_test.go's policy). Since the repo transfer to the
// rune-task-runner org, the repo host and the module path are the same
// rune-task-runner/rune — the historical repo-vs-module split guard is gone; the
// exact-URL assertions in requiredBadges pin every badge to the canonical owner.

// requiredBadge is one entry the README badge row must contain: a distinctive
// fragment of the image URL and of the link (href) target.
type requiredBadge struct {
	name     string
	imgFrag  string // must appear in an <img src=…> / ![](…)
	linkFrag string // must appear in an <a href=…> / ](…)
}

var requiredBadges = []requiredBadge{
	{"CI", "img.shields.io/github/actions/workflow/status/rune-task-runner/rune/ci.yml", "github.com/rune-task-runner/rune/actions/workflows/ci.yml"},
	{"Release", "img.shields.io/github/v/tag/rune-task-runner/rune", "github.com/rune-task-runner/rune/tags"},
	{"License", "img.shields.io/badge/License-MIT", "github.com/rune-task-runner/rune/blob/main/LICENSE"},
	{"Go version", "img.shields.io/github/go-mod/go-version/rune-task-runner/rune", ""},
	{"Go Report Card", "goreportcard.com/badge/github.com/rune-task-runner/rune", "goreportcard.com/report/github.com/rune-task-runner/rune"},
	{"Go Reference", "pkg.go.dev/badge/github.com/rune-task-runner/rune", "pkg.go.dev/github.com/rune-task-runner/rune"},
	{"Docs", "img.shields.io/badge/docs-site", "rune-task-runner.github.io/rune"},
}

func readmeSource(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repoRoot, "README.md"))
	if err != nil {
		t.Fatalf("read README.md: %v", err)
	}
	return string(data)
}

// TestREADMEBadgesPresent asserts every required badge (image + link target) is in
// the README.
func TestREADMEBadgesPresent(t *testing.T) {
	src := readmeSource(t)
	for _, b := range requiredBadges {
		if !strings.Contains(src, b.imgFrag) {
			t.Errorf("%s badge: image URL fragment %q not found in README", b.name, b.imgFrag)
		}
		if b.linkFrag != "" && !strings.Contains(src, b.linkFrag) {
			t.Errorf("%s badge: link target fragment %q not found in README", b.name, b.linkFrag)
		}
	}
}

var imgTagRe = regexp.MustCompile(`(?s)<img\s[^>]*>`)

var mdImageRe = regexp.MustCompile(`!\[([^\]]*)\]\(([^)]+)\)`)

// TestREADMEBadgesHaveAltText asserts every badge image degrades gracefully: HTML
// <img> tags carry non-empty alt, and any markdown image (![alt](url)) has non-empty
// alt (FR-005).
func TestREADMEBadgesHaveAltText(t *testing.T) {
	src := readmeSource(t)
	for _, tag := range imgTagRe.FindAllString(src, -1) {
		if !isBadgeImg(tag) {
			continue
		}
		alt := attr(tag, "alt")
		if strings.TrimSpace(alt) == "" {
			t.Errorf("badge <img> without non-empty alt text: %s", collapse(tag))
		}
	}
	// Defensive: the README badge row is HTML-only today (no markdown images), so
	// this branch is currently a no-op — kept so a future markdown-image badge is
	// still held to the alt-text rule.
	for _, m := range mdImageRe.FindAllStringSubmatch(src, -1) {
		if !isBadgeURL(m[2]) {
			continue
		}
		if strings.TrimSpace(m[1]) == "" {
			t.Errorf("badge markdown image without alt text: ![](%s)", m[2])
		}
	}
}

// TestREADMENoPlaceholderBadges guards against copy-paste template leftovers in the
// badge URLs. It scans only badge lines so unrelated prose (which may legitimately
// say "TODO" or reference example.com) never trips a false "badge placeholder".
func TestREADMENoPlaceholderBadges(t *testing.T) {
	src := readmeSource(t)
	for _, line := range strings.Split(src, "\n") {
		if !strings.Contains(line, "<img") && !isBadgeURL(line) {
			continue
		}
		for _, ph := range []string{"USER/REPO", "OWNER/REPO", "owner/repo", "your-org", "YOUR_", "example.com", "TODO"} {
			if strings.Contains(line, ph) {
				t.Errorf("README badge contains placeholder token %q: %s", ph, collapse(line))
			}
		}
	}
}

// TestCIWorkflowFileExists confirms the CI badge references a real workflow file.
func TestCIWorkflowFileExists(t *testing.T) {
	if _, err := os.Stat(filepath.Join(repoRoot, ".github", "workflows", "ci.yml")); err != nil {
		t.Errorf(".github/workflows/ci.yml referenced by the CI badge does not exist: %v", err)
	}
}

func isBadgeImg(tag string) bool { return isBadgeURL(attr(tag, "src")) }

func isBadgeURL(url string) bool {
	return strings.Contains(url, "shields.io") ||
		strings.Contains(url, "goreportcard.com/badge") ||
		strings.Contains(url, "pkg.go.dev/badge")
}

// attrRes holds the compiled matchers for the only HTML attributes this file
// reads. Each matches either quote style; group 1 is the double-quoted value,
// group 2 the single-quoted one.
var attrRes = map[string]*regexp.Regexp{
	"alt": regexp.MustCompile(`alt\s*=\s*(?:"([^"]*)"|'([^']*)')`),
	"src": regexp.MustCompile(`src\s*=\s*(?:"([^"]*)"|'([^']*)')`),
}

// attr extracts the value of an HTML attribute from a tag (double or single quotes).
func attr(tag, name string) string {
	re, ok := attrRes[name]
	if !ok {
		return ""
	}
	m := re.FindStringSubmatch(tag)
	if m == nil {
		return ""
	}
	if m[1] != "" {
		return m[1]
	}
	return m[2]
}

func collapse(s string) string { return strings.Join(strings.Fields(s), " ") }
