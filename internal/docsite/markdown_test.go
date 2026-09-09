package docsite

import (
	"strings"
	"testing"
)

func TestTitle(t *testing.T) {
	tests := []struct {
		name      string
		src       string
		wantTitle string
		wantBody  string
	}{
		{
			name:      "plain page",
			src:       "# Caching\n\nOpt-in content-hash skipping.\n",
			wantTitle: "Caching",
			wantBody:  "\nOpt-in content-hash skipping.\n",
		},
		{
			name:      "heading after an HTML block and a badge row",
			src:       "<div align=\"center\">\n\n![badge](https://example.invalid/b.svg)\n\n# Rune\n\nBody.\n",
			wantTitle: "Rune",
			wantBody:  "<div align=\"center\">\n\n![badge](https://example.invalid/b.svg)\n\n\nBody.\n",
		},
		{
			name:      "inline code and entities in the heading",
			src:       "# Diagnostics & the `RUNE####` codes\n\nBody.\n",
			wantTitle: "Diagnostics & the RUNE#### codes",
			wantBody:  "\nBody.\n",
		},
		{
			name:      "only the first heading is taken",
			src:       "# First\n\n# Second\n",
			wantTitle: "First",
			wantBody:  "\n# Second\n",
		},
		{
			name:      "no heading",
			src:       "Just prose.\n",
			wantTitle: "",
			wantBody:  "Just prose.\n",
		},
		{
			name:      "hash inside a code fence is not a heading",
			src:       "```sh\n# not a heading\n```\n\n# Real\n",
			wantTitle: "Real",
			wantBody:  "```sh\n# not a heading\n```\n\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotTitle, gotBody := Title(tt.src)
			if gotTitle != tt.wantTitle {
				t.Errorf("title = %q, want %q", gotTitle, tt.wantTitle)
			}
			if gotBody != tt.wantBody {
				t.Errorf("body = %q, want %q", gotBody, tt.wantBody)
			}
			if strings.Contains(gotBody, "# "+tt.wantTitle) && tt.wantTitle != "" {
				t.Errorf("body still contains the H1 %q", tt.wantTitle)
			}
		})
	}
}

func TestDescription(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "first paragraph",
			src:  "\nOpt-in content-hash skipping for expensive tasks.\n\nMore.\n",
			want: "Opt-in content-hash skipping for expensive tasks.",
		},
		{
			name: "links and emphasis are flattened",
			src:  "\nSee the [language guide](../runefile.md) for the **full** syntax and `set export`.\n",
			want: "See the language guide for the full syntax and set export.",
		},
		{
			name: "a leading callout is skipped",
			src:  "\n> [!NOTE]\n> Moved.\n\nThe real prose starts here.\n",
			want: "The real prose starts here.",
		},
		{
			name: "a leading blockquote is skipped",
			src:  "\n> How to configure a Runefile.\n\nActual prose.\n",
			want: "Actual prose.",
		},
		{
			name: "a leading code fence is skipped",
			src:  "\n```rune\nbuild:\n    go build ./...\n```\n\nActual prose.\n",
			want: "Actual prose.",
		},
		{
			name: "a leading table is skipped",
			src:  "\n| a | b |\n|---|---|\n| 1 | 2 |\n\nActual prose.\n",
			want: "Actual prose.",
		},
		{
			name: "a leading list is skipped",
			src:  "\n- one\n- two\n\nActual prose.\n",
			want: "Actual prose.",
		},
		{
			name: "a leading HTML block is skipped",
			src:  "\n<div align=\"center\">\n</div>\n\nActual prose.\n",
			want: "Actual prose.",
		},
		{
			name: "a wrapped paragraph joins into one line",
			src:  "\nRune runs your project's commands\nfrom one readable file.\n",
			want: "Rune runs your project's commands from one readable file.",
		},
		{
			name: "no prose at all",
			src:  "\n- only\n- lists\n",
			want: "",
		},
		{
			name: "empty input",
			src:  "",
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Description(tt.src); got != tt.want {
				t.Errorf("Description() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDescriptionTruncatesOnWordBoundary(t *testing.T) {
	long := "Rune runs your project's commands from one readable file, the Runefile, so humans " +
		"run tasks from the command line while AI agents and editors run the very same tasks " +
		"through the Model Context Protocol."
	got := Description("\n" + long + "\n")
	if n := len([]rune(got)); n > descriptionLimit {
		t.Errorf("length = %d runes, want <= %d (%q)", n, descriptionLimit, got)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("truncated description %q should end with an ellipsis", got)
	}
	if strings.HasSuffix(strings.TrimSuffix(got, "…"), " ") {
		t.Errorf("truncated description %q should not keep a trailing space before the ellipsis", got)
	}
	if !strings.HasPrefix(long, strings.TrimSuffix(got, "…")) {
		t.Errorf("truncated description %q is not a prefix of the source", got)
	}
}
