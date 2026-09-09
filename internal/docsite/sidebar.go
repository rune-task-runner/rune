package docsite

import (
	"fmt"
	"path"
	"sort"
	"strings"
)

// SidebarGroup is one Starlight sidebar group, marshalled into
// website/src/generated/sidebar.json and imported by astro.config.mjs.
type SidebarGroup struct {
	Label string        `json:"label"`
	Items []SidebarItem `json:"items"`
}

// SidebarItem is one entry in a group: either a content Slug or an external
// Link, never both. Label overrides the page's own title when set.
type SidebarItem struct {
	Label string `json:"label,omitempty"`
	Slug  string `json:"slug,omitempty"`
	Link  string `json:"link,omitempty"`
}

// Sidebar builds the Starlight sidebar from nav, resolving globs against pages
// (a list of docs/-relative Markdown paths). A manifest entry that matches no
// file is an error: a sidebar pointing at a page that does not exist would ship
// a 404.
func Sidebar(nav *Nav, pages []string) ([]SidebarGroup, error) {
	out := make([]SidebarGroup, 0, len(nav.Groups))
	for _, g := range nav.Groups {
		group := SidebarGroup{Label: g.Label}
		seen := map[string]bool{}

		for _, p := range g.Pages {
			matches, err := expandPage(p.Path, pages)
			if err != nil {
				return nil, err
			}
			for _, relPath := range matches {
				slug := Slug(relPath)
				if seen[slug] {
					continue // an explicit entry already covered by a later glob
				}
				seen[slug] = true
				item := SidebarItem{Slug: slug}
				// A label override only makes sense for a single named page.
				if len(matches) == 1 {
					item.Label = p.Label
				}
				group.Items = append(group.Items, item)
			}
		}

		for _, l := range g.Links {
			group.Items = append(group.Items, SidebarItem{Label: l.Label, Link: l.URL})
		}
		out = append(out, group)
	}
	return out, nil
}

// NavPages returns every docs/-relative page the manifest references, globs
// expanded, in sidebar order and without duplicates.
func NavPages(nav *Nav, pages []string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, g := range nav.Groups {
		for _, p := range g.Pages {
			matches, err := expandPage(p.Path, pages)
			if err != nil {
				return nil, err
			}
			for _, relPath := range matches {
				if seen[relPath] {
					continue
				}
				seen[relPath] = true
				out = append(out, relPath)
			}
		}
	}
	return out, nil
}

// expandPage resolves one manifest entry to the pages it names. A literal path
// must exist; a glob must match at least one page.
func expandPage(pattern string, pages []string) ([]string, error) {
	if !strings.ContainsAny(pattern, "*?[") {
		for _, p := range pages {
			if p == pattern {
				return []string{pattern}, nil
			}
		}
		return nil, fmt.Errorf("nav entry %q has no matching file under docs/", pattern)
	}

	var matches []string
	for _, p := range pages {
		ok, err := path.Match(pattern, p)
		if err != nil {
			return nil, fmt.Errorf("nav entry %q is not a valid pattern: %w", pattern, err)
		}
		if ok {
			matches = append(matches, p)
		}
	}
	if len(matches) == 0 {
		return nil, fmt.Errorf("nav entry %q matched no file under docs/", pattern)
	}
	sort.Strings(matches)
	return matches, nil
}

// MatchExcluded reports whether a docs/-relative path is excluded from the site
// by the manifest. A trailing "/**" matches the whole subtree.
func MatchExcluded(nav *Nav, relPath string) bool {
	for _, pattern := range nav.Exclude {
		if prefix, ok := strings.CutSuffix(pattern, "/**"); ok {
			if relPath == prefix || strings.HasPrefix(relPath, prefix+"/") {
				return true
			}
			continue
		}
		if ok, _ := path.Match(pattern, relPath); ok {
			return true
		}
	}
	return false
}
