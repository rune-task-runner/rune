package docsite

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixtureOptions(t *testing.T) Options {
	t.Helper()
	root := filepath.Join("testdata", "docs", "index-page")
	return Options{
		DocsDir:     root,
		NavPath:     filepath.Join(root, "nav.yaml"),
		OutDir:      "website/src/content/docs",
		AssetsDir:   "website/public",
		SidebarPath: "website/src/generated/sidebar.json",
		LandingPath: "",
		Config: Config{
			BasePath: "/rune",
			BlobBase: "https://github.com/rune-task-runner/rune/blob/main",
		},
	}
}

func TestBuild(t *testing.T) {
	plan, err := Build(fixtureOptions(t))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	index, ok := plan.Files["website/src/content/docs/index.md"]
	if !ok {
		t.Fatalf("no index.md in plan; files = %v", planPaths(plan))
	}
	if !strings.HasPrefix(string(index), "---\ntitle: \"Fixture docs\"\n") {
		t.Errorf("index.md frontmatter = %q", firstLines(string(index), 4))
	}
	if !strings.Contains(string(index), "(/rune/how-to/caching/)") {
		t.Errorf("index.md link not rewritten: %q", index)
	}
	if !strings.Contains(string(index), "blob/main/CONTRIBUTING.md") {
		t.Errorf("index.md out-of-docs link not sent to the blob URL: %q", index)
	}

	caching, ok := plan.Files["website/src/content/docs/how-to/caching.md"]
	if !ok {
		t.Fatalf("no how-to/caching.md in plan; files = %v", planPaths(plan))
	}
	if !strings.Contains(string(caching), "(/rune/)") {
		t.Errorf("link to the docs root should be the site root: %q", caching)
	}

	if _, ok := plan.Files["website/src/generated/sidebar.json"]; !ok {
		t.Errorf("no sidebar.json in plan; files = %v", planPaths(plan))
	}
	if got, want := len(plan.Pages), 2; got != want {
		t.Errorf("len(Pages) = %d, want %d (%v)", got, want, plan.Pages)
	}
}

func TestBuildOmitsExcludedPages(t *testing.T) {
	opts := fixtureOptions(t)
	draft := filepath.Join(opts.DocsDir, "drafts", "wip.md")
	if err := os.MkdirAll(filepath.Dir(draft), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(draft, []byte("# WIP\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Dir(draft)) })

	plan, err := Build(opts)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	for p := range plan.Files {
		if strings.Contains(p, "drafts") {
			t.Errorf("excluded page was generated: %q", p)
		}
	}
}

func TestBuildLeavesNoMarkdownLinks(t *testing.T) {
	plan, err := Build(fixtureOptions(t))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	for p, content := range plan.Files {
		if !strings.HasSuffix(p, ".md") {
			continue
		}
		for _, target := range markdownLinkTargets(string(content)) {
			if strings.HasSuffix(target, ".md") && !strings.Contains(target, "://") {
				t.Errorf("%s: relative .md target %q survived", p, target)
			}
		}
	}
}

func TestPlanWriteAndCheck(t *testing.T) {
	plan, err := Build(fixtureOptions(t))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	root := t.TempDir()

	if err := plan.Write(root); err != nil {
		t.Fatalf("Write: %v", err)
	}
	diff, err := plan.Check(root)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(diff) != 0 {
		t.Errorf("Check right after Write reported differences: %v", diff)
	}

	// A stale file must be reported and then removed by the next Write.
	stale := filepath.Join(root, "website/src/content/docs/stale.md")
	if err := os.WriteFile(stale, []byte("stale\n"), 0o644); err != nil {
		t.Fatalf("write stale: %v", err)
	}
	diff, err = plan.Check(root)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(diff) == 0 {
		t.Error("Check did not report the stale file")
	}
	if err := plan.Write(root); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("stale file survived a rewrite: err = %v", err)
	}
}

func TestPlanCheckReportsAMissingTree(t *testing.T) {
	plan, err := Build(fixtureOptions(t))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	diff, err := plan.Check(t.TempDir())
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(diff) == 0 {
		t.Error("Check against an empty root reported no differences")
	}
}

func planPaths(p *Plan) []string {
	out := make([]string, 0, len(p.Files))
	for k := range p.Files {
		out = append(out, k)
	}
	return out
}

func firstLines(s string, n int) string {
	parts := strings.SplitN(s, "\n", n+1)
	if len(parts) > n {
		parts = parts[:n]
	}
	return strings.Join(parts, "\n")
}
