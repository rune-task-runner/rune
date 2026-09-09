package docsite

import (
	"strings"
	"testing"
)

// testConfig mirrors the real configuration, excluding the same page trees the
// committed manifest excludes.
func testConfig() Config {
	return Config{
		BasePath: "/rune",
		BlobBase: "https://github.com/rune-task-runner/rune/blob/main",
		Excluded: func(relPath string) bool {
			return strings.HasPrefix(relPath, "guides/") || strings.HasPrefix(relPath, "release-guru/")
		},
	}
}

func TestRewriteLinks(t *testing.T) {
	tests := []struct {
		name     string
		fromPath string
		in       string
		want     string
	}{
		{
			name:     "sibling page",
			fromPath: "how-to/caching.md",
			in:       "See [parallelism](parallelism.md).",
			want:     "See [parallelism](/rune/how-to/parallelism/).",
		},
		{
			name:     "parent page",
			fromPath: "how-to/caching.md",
			in:       "See [overview](../overview.md).",
			want:     "See [overview](/rune/overview/).",
		},
		{
			name:     "directory README collapses",
			fromPath: "how-to/caching.md",
			in:       "Part of the [guides](README.md).",
			want:     "Part of the [guides](/rune/how-to/).",
		},
		{
			name:     "uppercase filename lowercases",
			fromPath: "runefile.md",
			in:       "See the [grammar](GRAMMAR.md).",
			want:     "See the [grammar](/rune/grammar/).",
		},
		{
			name:     "anchor is preserved",
			fromPath: "how-to/settings-and-dotenv.md",
			in:       "See [variables](../runefile.md#variables-and-settings).",
			want:     "See [variables](/rune/runefile/#variables-and-settings).",
		},
		{
			name:     "query is preserved",
			fromPath: "overview.md",
			in:       "See [cli](cli.md?tab=flags).",
			want:     "See [cli](/rune/cli/?tab=flags).",
		},
		{
			name:     "docs root README maps to the site root",
			fromPath: "how-to/caching.md",
			in:       "Back to the [index](../README.md).",
			want:     "Back to the [index](/rune/).",
		},
		{
			name:     "image asset",
			fromPath: "README.md",
			in:       `<img src="img/icon.png">` + "\n![logo](img/icon.png)",
			want:     `<img src="img/icon.png">` + "\n![logo](/rune/img/icon.png)",
		},
		{
			name:     "non-Markdown file inside docs becomes a blob URL",
			fromPath: "examples/caching/README.md",
			in:       "See the [Runefile](Runefile).",
			want:     "See the [Runefile](https://github.com/rune-task-runner/rune/blob/main/docs/examples/caching/Runefile).",
		},
		{
			name:     "path escaping docs/ becomes a blob URL",
			fromPath: "README.md",
			in:       "See [editor setup](../editors/README.md).",
			want:     "See [editor setup](https://github.com/rune-task-runner/rune/blob/main/editors/README.md).",
		},
		{
			name:     "excluded page becomes a blob URL",
			fromPath: "how-to/caching.md",
			in:       "Old home: [guides](../guides/caching.md).",
			want:     "Old home: [guides](https://github.com/rune-task-runner/rune/blob/main/docs/guides/caching.md).",
		},
		{
			name:     "external links are untouched",
			fromPath: "installation.md",
			in:       "See [Go](https://go.dev) or [mail](mailto:a@example.invalid).",
			want:     "See [Go](https://go.dev) or [mail](mailto:a@example.invalid).",
		},
		{
			name:     "in-page anchor is untouched",
			fromPath: "cli.md",
			in:       "Jump to [flags](#global-flags).",
			want:     "Jump to [flags](#global-flags).",
		},
		{
			name:     "already-absolute target is untouched",
			fromPath: "cli.md",
			in:       "See [site](/rune/overview/).",
			want:     "See [site](/rune/overview/).",
		},
		{
			name:     "link text containing brackets and parens survives",
			fromPath: "overview.md",
			in:       "See [Editor setup (LSP)](installation.md).",
			want:     "See [Editor setup (LSP)](/rune/installation/).",
		},
		{
			name:     "reference-style definition is rewritten",
			fromPath: "overview.md",
			in:       "See [caching][c].\n\n[c]: how-to/caching.md\n",
			want:     "See [caching][c].\n\n[c]: /rune/how-to/caching/\n",
		},
		{
			name:     "links inside a fenced code block are untouched",
			fromPath: "overview.md",
			in:       "```md\n[a](overview.md)\n```\n[b](overview.md)",
			want:     "```md\n[a](overview.md)\n```\n[b](/rune/overview/)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := RewriteLinks(tt.in, tt.fromPath, testConfig()); got != tt.want {
				t.Errorf("RewriteLinks() =\n%q\nwant\n%q", got, tt.want)
			}
		})
	}
}

// TestRewriteLinksLeavesNoMarkdownTargets is the invariant the whole site rests
// on: a rewritten page must not link to a relative .md path.
func TestRewriteLinksLeavesNoMarkdownTargets(t *testing.T) {
	src := "See [a](overview.md), [b](how-to/caching.md#pitfalls) and [c](../CONTRIBUTING.md).\n"
	got := RewriteLinks(src, "README.md", testConfig())
	for _, target := range markdownLinkTargets(got) {
		if strings.HasSuffix(target, ".md") && !strings.HasPrefix(target, "http") {
			t.Errorf("relative .md target %q survived rewriting: %q", target, got)
		}
	}
}
