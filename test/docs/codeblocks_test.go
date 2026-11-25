package docs

import (
	"os"
	"path/filepath"
	"testing"
)

// selfContainedPages lists docs whose fenced ```rune blocks are each a
// complete, self-contained Runefile and therefore MUST statically validate.
// Pages with deliberate fragments (lone expressions, partial snippets) fence
// them as ```text and are not listed here. As more pages are brought into
// compliance, add them so coverage tightens over time.
var selfContainedPages = []string{
	"docs/overview.md",
	"docs/getting-started.md",
	// How-to guides (folded from docs/guides) — self-contained ```rune blocks.
	// dependencies-and-hooks and imports-and-modules are intentionally excluded:
	// their ```rune blocks are deliberate fragments (multi-file imports needing
	// sibling files; partial snippets), per the convention above.
	"docs/how-to/caching.md",
	"docs/how-to/executors.md",
	"docs/how-to/os-filtering.md",
	"docs/how-to/parallelism.md",
	"docs/how-to/parameters.md",
	"docs/how-to/settings-and-dotenv.md",
	"docs/how-to/import-from-other-runners.md",
	"docs/how-to/learn-tasks-from-agent.md",
	"docs/how-to/ci-github.md",
	// Use-case walkthroughs — each shows a complete, runnable Runefile.
	"docs/use-cases/python-project.md",
	"docs/use-cases/node-project.md",
	"docs/use-cases/mcp-agents.md",
}

// TestRuneCodeBlocksValidate extracts every ```rune block from the
// self-contained pages and asserts it validates (`--list`, exit 0), so prose
// examples cannot drift from the real DSL.
func TestRuneCodeBlocksValidate(t *testing.T) {
	for _, page := range selfContainedPages {
		t.Run(page, func(t *testing.T) {
			path := filepath.Join(repoRoot, filepath.FromSlash(page))
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", page, err)
			}
			blocks := fencedBlocks(string(src), "rune")
			if len(blocks) == 0 {
				t.Skipf("%s: no ```rune blocks to validate", page)
			}
			for i, b := range blocks {
				tmp := filepath.Join(t.TempDir(), "Runefile")
				if err := os.WriteFile(tmp, []byte(b), 0o644); err != nil {
					t.Fatal(err)
				}
				if got := validate(t, tmp); got.code != 0 {
					t.Errorf("%s: ```rune block #%d failed `--list` (exit %d)\n--- block ---\n%s--- stderr ---\n%s",
						page, i+1, got.code, b, got.stderr)
				}
			}
		})
	}
}
