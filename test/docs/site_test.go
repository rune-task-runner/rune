package docs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rune-task-runner/rune/internal/docsite"
)

// siteRootPage is the one docs/ page deliberately absent from docs/nav.yaml:
// docs/README.md is the map for readers on GitHub, while the site's root is the
// hand-written landing page in website/src/landing/index.mdx.
const siteRootPage = "README.md"

func loadNav(t *testing.T) *docsite.Nav {
	t.Helper()
	navPath := filepath.Join(repoRoot, "docs", "nav.yaml")
	src, err := os.ReadFile(navPath)
	if err != nil {
		t.Fatalf("read docs/nav.yaml: %v", err)
	}
	nav, err := docsite.ParseNav(src, "docs/nav.yaml")
	if err != nil {
		t.Fatalf("parse docs/nav.yaml: %v", err)
	}
	return nav
}

func siteOptions() docsite.Options {
	opts := docsite.DefaultOptions()
	opts.DocsDir = filepath.Join(repoRoot, "docs")
	opts.NavPath = filepath.Join(repoRoot, "docs", "nav.yaml")
	opts.LandingPath = filepath.Join(repoRoot, "website", "src", "landing", "index.mdx")
	return opts
}

// TestNavCoversEveryPage is the drift guard: adding a Markdown page under docs/
// without listing it in docs/nav.yaml (or excluding it) fails here, so a page
// can never be silently missing from the site.
func TestNavCoversEveryPage(t *testing.T) {
	nav := loadNav(t)

	listed := map[string]bool{}
	for _, g := range nav.Groups {
		for _, p := range g.Pages {
			if strings.ContainsAny(p.Path, "*?[") {
				continue // globs are checked by TestNavGlobsMatchSomething
			}
			if listed[p.Path] {
				t.Errorf("docs/nav.yaml lists %q more than once", p.Path)
			}
			listed[p.Path] = true
		}
	}

	for _, abs := range docMarkdownFiles(t) {
		rel, err := filepath.Rel(filepath.Join(repoRoot, "docs"), abs)
		if err != nil || strings.HasPrefix(rel, "..") {
			continue // README.md / CONTRIBUTING.md at the repository root
		}
		rel = filepath.ToSlash(rel)
		if rel == siteRootPage || docsite.MatchExcluded(nav, rel) || listed[rel] {
			continue
		}
		if matchesAnyGlob(t, nav, rel) {
			continue
		}
		t.Errorf("docs/%s is in neither docs/nav.yaml nor its exclude list — "+
			"add it to a group, or to exclude if it should not be published", rel)
	}
}

func matchesAnyGlob(t *testing.T, nav *docsite.Nav, rel string) bool {
	t.Helper()
	for _, g := range nav.Groups {
		for _, p := range g.Pages {
			if !strings.ContainsAny(p.Path, "*?[") {
				continue
			}
			if ok, err := filepath.Match(p.Path, rel); err == nil && ok {
				return true
			}
		}
	}
	return false
}

// TestNavEntriesExist fails on a manifest entry with no file behind it, which
// would otherwise ship a sidebar link to a 404.
func TestNavEntriesExist(t *testing.T) {
	nav := loadNav(t)
	docsDir := filepath.Join(repoRoot, "docs")
	for _, g := range nav.Groups {
		for _, p := range g.Pages {
			if strings.ContainsAny(p.Path, "*?[") {
				continue
			}
			if _, err := os.Stat(filepath.Join(docsDir, filepath.FromSlash(p.Path))); err != nil {
				t.Errorf("docs/nav.yaml group %q lists %q, which does not exist", g.Label, p.Path)
			}
		}
	}
}

// TestNavGlobsMatchSomething catches a glob left behind after a rename.
func TestNavGlobsMatchSomething(t *testing.T) {
	nav := loadNav(t)
	plan, err := docsite.Build(siteOptions())
	if err != nil {
		t.Fatalf("docsite.Build: %v", err)
	}
	for _, g := range nav.Groups {
		for _, p := range g.Pages {
			if !strings.ContainsAny(p.Path, "*?[") {
				continue
			}
			found := false
			for _, published := range plan.Pages {
				if ok, err := filepath.Match(p.Path, published); err == nil && ok {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("docs/nav.yaml glob %q in group %q matched no page", p.Path, g.Label)
			}
		}
	}
}

// TestGeneratedPagesHaveFrontmatter asserts every generated page carries a
// non-empty title, which Starlight requires.
func TestGeneratedPagesHaveFrontmatter(t *testing.T) {
	plan, err := docsite.Build(siteOptions())
	if err != nil {
		t.Fatalf("docsite.Build: %v", err)
	}
	for p, content := range plan.Files {
		if !strings.HasSuffix(p, ".md") {
			continue
		}
		s := string(content)
		if !strings.HasPrefix(s, "---\ntitle: \"") {
			t.Errorf("%s does not start with a title frontmatter block: %q", p, firstLine(s))
			continue
		}
		if strings.HasPrefix(s, "---\ntitle: \"\"") {
			t.Errorf("%s has an empty title", p)
		}
	}
}

// TestGeneratedPagesHaveNoMarkdownLinks is the site's core invariant: every
// relative .md target must have been rewritten to a site path or a blob URL.
func TestGeneratedPagesHaveNoMarkdownLinks(t *testing.T) {
	plan, err := docsite.Build(siteOptions())
	if err != nil {
		t.Fatalf("docsite.Build: %v", err)
	}
	for p, content := range plan.Files {
		if !strings.HasSuffix(p, ".md") {
			continue
		}
		for _, m := range markdownLinkRe.FindAllStringSubmatch(string(content), -1) {
			target := strings.TrimSpace(m[1])
			if strings.Contains(target, "://") || strings.HasPrefix(target, "mailto:") {
				continue
			}
			if strings.HasSuffix(strings.SplitN(target, "#", 2)[0], ".md") {
				t.Errorf("%s: relative .md link %q survived rewriting", p, target)
			}
		}
	}
}

// TestExcludedTreesAreNotPublished pins the two exclusions.
func TestExcludedTreesAreNotPublished(t *testing.T) {
	plan, err := docsite.Build(siteOptions())
	if err != nil {
		t.Fatalf("docsite.Build: %v", err)
	}
	for p := range plan.Files {
		for _, excluded := range []string{"/guides/", "/release-guru/"} {
			if strings.Contains(p, excluded) {
				t.Errorf("excluded tree %q was published as %q", excluded, p)
			}
		}
	}
}

func firstLine(s string) string {
	if i := strings.Index(s, "\n"); i >= 0 {
		return s[:i]
	}
	return s
}
