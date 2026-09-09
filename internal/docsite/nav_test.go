package docsite

import (
	"strings"
	"testing"
)

func TestParseNav(t *testing.T) {
	src := `# The site's structure.
base: /rune

exclude:
  - guides/**
  - release-guru/**

groups:
  - group: Start here
    pages:
      - overview.md
      - path: user-guide/README.md
        label: "User guide"
  - group: Examples
    pages:
      - "examples/*/README.md"
  - group: Editors
    links:
      - label: Editor setup (LSP)
        url: https://github.com/rune-task-runner/rune/blob/main/editors/README.md
`
	nav, err := ParseNav([]byte(src), "docs/nav.yaml")
	if err != nil {
		t.Fatalf("ParseNav: %v", err)
	}
	if nav.Base != "/rune" {
		t.Errorf("Base = %q, want %q", nav.Base, "/rune")
	}
	if got, want := len(nav.Exclude), 2; got != want {
		t.Fatalf("len(Exclude) = %d, want %d", got, want)
	}
	if nav.Exclude[1] != "release-guru/**" {
		t.Errorf("Exclude[1] = %q, want %q", nav.Exclude[1], "release-guru/**")
	}
	if got, want := len(nav.Groups), 3; got != want {
		t.Fatalf("len(Groups) = %d, want %d", got, want)
	}

	start := nav.Groups[0]
	if start.Label != "Start here" {
		t.Errorf("Groups[0].Label = %q, want %q", start.Label, "Start here")
	}
	if got, want := len(start.Pages), 2; got != want {
		t.Fatalf("len(Groups[0].Pages) = %d, want %d", got, want)
	}
	if start.Pages[0] != (Page{Path: "overview.md"}) {
		t.Errorf("Pages[0] = %+v, want {Path: overview.md}", start.Pages[0])
	}
	if start.Pages[1] != (Page{Path: "user-guide/README.md", Label: "User guide"}) {
		t.Errorf("Pages[1] = %+v, want path user-guide/README.md label \"User guide\"", start.Pages[1])
	}

	if nav.Groups[1].Pages[0].Path != "examples/*/README.md" {
		t.Errorf("glob entry = %q, want examples/*/README.md", nav.Groups[1].Pages[0].Path)
	}

	editors := nav.Groups[2]
	if got, want := len(editors.Links), 1; got != want {
		t.Fatalf("len(Groups[2].Links) = %d, want %d", got, want)
	}
	if editors.Links[0].Label != "Editor setup (LSP)" {
		t.Errorf("Links[0].Label = %q", editors.Links[0].Label)
	}
	if !strings.HasSuffix(editors.Links[0].URL, "editors/README.md") {
		t.Errorf("Links[0].URL = %q", editors.Links[0].URL)
	}
}

func TestParseNavErrors(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string // substring the error must contain
	}{
		{
			name: "unknown top-level key",
			src:  "base: /rune\nsidebars:\n  - a\n",
			want: `nav.yaml:2: unknown top-level key "sidebars"`,
		},
		{
			name: "missing base",
			src:  "groups:\n  - group: A\n    pages:\n      - overview.md\n",
			want: `missing required key "base"`,
		},
		{
			name: "no groups",
			src:  "base: /rune\n",
			want: `missing required key "groups"`,
		},
		{
			name: "duplicate top-level key",
			src:  "base: /rune\nbase: /other\n",
			want: `duplicate key "base"`,
		},
		{
			name: "group without label",
			src:  "base: /rune\ngroups:\n  - pages:\n      - overview.md\n",
			want: `expected "- group: <label>"`,
		},
		{
			name: "empty group",
			src:  "base: /rune\ngroups:\n  - group: A\n",
			want: `group "A" has no pages and no links`,
		},
		{
			name: "unknown group key",
			src:  "base: /rune\ngroups:\n  - group: A\n    items:\n      - overview.md\n",
			want: `unknown key "items"`,
		},
		{
			name: "link without url",
			src:  "base: /rune\ngroups:\n  - group: A\n    links:\n      - label: X\n",
			want: `link "X" has no "url"`,
		},
		{
			name: "page mapping without path",
			src:  "base: /rune\ngroups:\n  - group: A\n    pages:\n      - label: X\n",
			want: `page entry has no "path"`,
		},
		{
			name: "line without a colon",
			src:  "base: /rune\ngroups\n",
			want: `nav.yaml:2: expected "key: value"`,
		},
		{
			name: "tab indentation",
			src:  "base: /rune\ngroups:\n\t- group: A\n",
			want: "tabs are not allowed",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseNav([]byte(tt.src), "docs/nav.yaml")
			if err == nil {
				t.Fatalf("ParseNav(%q) = nil error, want error containing %q", tt.src, tt.want)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %q, want it to contain %q", err.Error(), tt.want)
			}
		})
	}
}
