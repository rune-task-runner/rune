package docsite

import (
	"path"
	"strings"
)

// Slug maps a docs/-relative Markdown path to its Starlight content slug, with
// no leading or trailing slash. A README.md collapses to its directory, matching
// Starlight's index-page handling, and names are lowercased so GRAMMAR.md lands
// on a predictable URL. docs/README.md maps to "", the site root.
func Slug(relPath string) string {
	p := strings.TrimPrefix(path.Clean(strings.ReplaceAll(relPath, "\\", "/")), "./")
	p = strings.TrimSuffix(p, path.Ext(p))
	if base := path.Base(p); strings.EqualFold(base, "readme") || strings.EqualFold(base, "index") {
		p = path.Dir(p)
		if p == "." {
			return ""
		}
	}
	return strings.ToLower(p)
}
