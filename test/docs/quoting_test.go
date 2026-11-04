package docs

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// TestDocsQuoting keeps every documentation example and the
// repository's own Runefile free of RUNE2017 (spec 030 FR-021, SC-005), so the
// docs never teach the unsafe form. A block that shows it on purpose is
// fenced as ```text.
func TestDocsQuoting(t *testing.T) {
	type source struct{ name, path string }
	var sources []source
	for _, md := range docMarkdownFiles(t) {
		src, err := os.ReadFile(md)
		if err != nil {
			t.Fatal(err)
		}
		for i, b := range fencedBlocks(string(src), "rune") {
			tmp := filepath.Join(t.TempDir(), "Runefile")
			if err := os.WriteFile(tmp, []byte(b), 0o644); err != nil {
				t.Fatal(err)
			}
			sources = append(sources, source{fmt.Sprintf("%s ```rune block #%d", relPath(md), i+1), tmp})
		}
	}
	for _, d := range exampleDirs(t) {
		p := filepath.Join(repoRoot, "docs", "examples", d, "Runefile")
		sources = append(sources, source{relPath(p), p})
	}
	sources = append(sources, source{"Runefile", filepath.Join(repoRoot, "Runefile")})

	for _, s := range sources {
		r := runRune(t, filepath.Dir(s.path), "analyze", s.path, "--json")
		var report struct {
			Diagnostics []struct {
				Code    string `json:"code"`
				Message string `json:"message"`
				Range   struct {
					Start struct {
						Line int `json:"line"`
					} `json:"start"`
				} `json:"range"`
			} `json:"diagnostics"`
		}
		if err := json.Unmarshal([]byte(r.stdout), &report); err != nil {
			t.Errorf("%s: analyze --json: %v\nstdout: %s\nstderr: %s", s.name, err, r.stdout, r.stderr)
			continue
		}
		for _, d := range report.Diagnostics {
			if d.Code == "RUNE2017" {
				t.Errorf("%s:%d: %s", s.name, d.Range.Start.Line, d.Message)
			}
		}
	}
}
