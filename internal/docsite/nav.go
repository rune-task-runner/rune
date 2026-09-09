package docsite

import (
	"fmt"
	"strconv"
	"strings"
)

// Nav is the docs/nav.yaml manifest: the site's navigation structure, kept in
// one reviewable file so the sidebar cannot drift from the documentation set.
type Nav struct {
	Base    string   // site base path, e.g. "/rune"
	Exclude []string // docs/-relative globs that are not published
	Groups  []Group  // sidebar groups, in order
}

// Group is one sidebar group. It carries pages, external links, or both.
type Group struct {
	Label string
	Pages []Page
	Links []Link
}

// Page is one sidebar entry backed by a docs/ page. Path is a docs/-relative
// Markdown path or a glob; Label, when set, overrides the page's derived title
// in the sidebar only.
type Page struct {
	Path  string
	Label string
}

// Link is one sidebar entry pointing outside the site.
type Link struct {
	Label string
	URL   string
}

// ParseNav parses the docs/nav.yaml manifest. It accepts a deliberately small
// subset of YAML — scalars, string lists, and lists of one-level mappings — and
// rejects everything else with a file:line error rather than guessing, so a
// malformed manifest can never silently produce a wrong sidebar. filename is
// used only in error messages.
func ParseNav(src []byte, filename string) (*Nav, error) {
	lines, err := scanNavLines(src, filename)
	if err != nil {
		return nil, err
	}
	p := &navParser{file: filename, lines: lines}
	nav := &Nav{}
	seen := map[string]bool{}

	for {
		ln, ok := p.peek()
		if !ok {
			break
		}
		if ln.indent != 0 {
			return nil, p.errf(ln, "unexpected indentation at top level")
		}
		key, value, err := p.splitKey(ln)
		if err != nil {
			return nil, err
		}
		if seen[key] {
			return nil, p.errf(ln, "duplicate key %q", key)
		}
		seen[key] = true
		p.next()

		switch key {
		case "base":
			if value == "" {
				return nil, p.errf(ln, "key %q needs a value", "base")
			}
			nav.Base = value
		case "exclude":
			nav.Exclude, err = p.scalarList(ln.indent)
		case "groups":
			nav.Groups, err = p.groups(ln.indent)
		default:
			return nil, p.errf(ln, "unknown top-level key %q (want base, exclude or groups)", key)
		}
		if err != nil {
			return nil, err
		}
	}

	if nav.Base == "" {
		return nil, fmt.Errorf("%s: missing required key %q", filename, "base")
	}
	if len(nav.Groups) == 0 {
		return nil, fmt.Errorf("%s: missing required key %q", filename, "groups")
	}
	return nav, nil
}

// navLine is one significant line of the manifest. Blank and comment lines are
// dropped by scanNavLines, so the parser never sees them.
type navLine struct {
	num    int
	indent int
	text   string
}

func scanNavLines(src []byte, filename string) ([]navLine, error) {
	var out []navLine
	for i, raw := range strings.Split(string(src), "\n") {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		lead := raw[:len(raw)-len(strings.TrimLeft(raw, " \t"))]
		if strings.ContainsRune(lead, '\t') {
			return nil, fmt.Errorf("%s:%d: tabs are not allowed for indentation, use spaces", filename, i+1)
		}
		out = append(out, navLine{num: i + 1, indent: len(lead), text: trimmed})
	}
	return out, nil
}

type navParser struct {
	file  string
	lines []navLine
	pos   int
}

func (p *navParser) peek() (navLine, bool) {
	if p.pos >= len(p.lines) {
		return navLine{}, false
	}
	return p.lines[p.pos], true
}

func (p *navParser) next() { p.pos++ }

func (p *navParser) errf(ln navLine, format string, args ...any) error {
	return fmt.Errorf("%s:%d: %s", p.file, ln.num, fmt.Sprintf(format, args...))
}

// splitKey splits "key: value" (value may be empty for a nested block).
func (p *navParser) splitKey(ln navLine) (key, value string, err error) {
	text := strings.TrimPrefix(ln.text, "- ")
	i := strings.Index(text, ":")
	if i < 0 {
		return "", "", p.errf(ln, "expected %q", "key: value")
	}
	return strings.TrimSpace(text[:i]), unquoteNav(strings.TrimSpace(text[i+1:])), nil
}

// unquoteNav strips surrounding double quotes, honouring Go-compatible escapes.
func unquoteNav(s string) string {
	if len(s) >= 2 && strings.HasPrefix(s, `"`) && strings.HasSuffix(s, `"`) {
		if v, err := strconv.Unquote(s); err == nil {
			return v
		}
		return s[1 : len(s)-1]
	}
	return s
}

// scalarList consumes "- value" items indented deeper than parentIndent.
func (p *navParser) scalarList(parentIndent int) ([]string, error) {
	var out []string
	for {
		ln, ok := p.peek()
		if !ok || ln.indent <= parentIndent {
			return out, nil
		}
		if !strings.HasPrefix(ln.text, "- ") {
			return nil, p.errf(ln, "expected a list item %q", "- value")
		}
		out = append(out, unquoteNav(strings.TrimSpace(strings.TrimPrefix(ln.text, "- "))))
		p.next()
	}
}

// groups consumes the "- group: <label>" items indented deeper than parentIndent.
func (p *navParser) groups(parentIndent int) ([]Group, error) {
	var out []Group
	for {
		ln, ok := p.peek()
		if !ok || ln.indent <= parentIndent {
			if len(out) == 0 {
				return nil, nil
			}
			return out, nil
		}
		key, value, err := p.splitKey(ln)
		if err != nil {
			return nil, err
		}
		if !strings.HasPrefix(ln.text, "- ") || key != "group" || value == "" {
			return nil, p.errf(ln, "expected %q", "- group: <label>")
		}
		g := Group{Label: value}
		itemIndent := ln.indent
		p.next()

		for {
			sub, ok := p.peek()
			if !ok || sub.indent <= itemIndent {
				break
			}
			subKey, subValue, err := p.splitKey(sub)
			if err != nil {
				return nil, err
			}
			if subValue != "" {
				return nil, p.errf(sub, "key %q takes a nested list, not an inline value", subKey)
			}
			p.next()
			switch subKey {
			case "pages":
				g.Pages, err = p.pages(sub.indent)
			case "links":
				g.Links, err = p.links(sub.indent)
			default:
				return nil, p.errf(sub, "unknown key %q in group %q (want pages or links)", subKey, g.Label)
			}
			if err != nil {
				return nil, err
			}
		}

		if len(g.Pages) == 0 && len(g.Links) == 0 {
			return nil, p.errf(ln, "group %q has no pages and no links", g.Label)
		}
		out = append(out, g)
	}
}

// pages consumes page entries in either the scalar form ("- overview.md") or the
// mapping form ("- path: x" followed by an indented "label: y").
func (p *navParser) pages(parentIndent int) ([]Page, error) {
	var out []Page
	for {
		ln, ok := p.peek()
		if !ok || ln.indent <= parentIndent {
			return out, nil
		}
		if !strings.HasPrefix(ln.text, "- ") {
			return nil, p.errf(ln, "expected a list item %q", "- <path>")
		}
		body := strings.TrimSpace(strings.TrimPrefix(ln.text, "- "))
		itemIndent := ln.indent
		p.next()

		if !strings.Contains(body, ":") {
			out = append(out, Page{Path: unquoteNav(body)})
			continue
		}
		key, value, err := p.splitKey(navLine{num: ln.num, indent: ln.indent, text: body})
		if err != nil {
			return nil, err
		}
		page := Page{}
		if err := assignPageField(p, ln, &page, key, value); err != nil {
			return nil, err
		}
		for {
			sub, ok := p.peek()
			if !ok || sub.indent <= itemIndent || strings.HasPrefix(sub.text, "- ") {
				break
			}
			subKey, subValue, err := p.splitKey(sub)
			if err != nil {
				return nil, err
			}
			if err := assignPageField(p, sub, &page, subKey, subValue); err != nil {
				return nil, err
			}
			p.next()
		}
		if page.Path == "" {
			return nil, p.errf(ln, "page entry has no %q", "path")
		}
		out = append(out, page)
	}
}

func assignPageField(p *navParser, ln navLine, page *Page, key, value string) error {
	switch key {
	case "path":
		page.Path = value
	case "label":
		page.Label = value
	default:
		return p.errf(ln, "unknown key %q in page entry (want path or label)", key)
	}
	return nil
}

// links consumes "- label: X" / "url: Y" entries.
func (p *navParser) links(parentIndent int) ([]Link, error) {
	var out []Link
	for {
		ln, ok := p.peek()
		if !ok || ln.indent <= parentIndent {
			return out, nil
		}
		if !strings.HasPrefix(ln.text, "- ") {
			return nil, p.errf(ln, "expected a list item %q", "- label: <label>")
		}
		key, value, err := p.splitKey(ln)
		if err != nil {
			return nil, err
		}
		link := Link{}
		if err := assignLinkField(p, ln, &link, key, value); err != nil {
			return nil, err
		}
		itemIndent := ln.indent
		p.next()

		for {
			sub, ok := p.peek()
			if !ok || sub.indent <= itemIndent || strings.HasPrefix(sub.text, "- ") {
				break
			}
			subKey, subValue, err := p.splitKey(sub)
			if err != nil {
				return nil, err
			}
			if err := assignLinkField(p, sub, &link, subKey, subValue); err != nil {
				return nil, err
			}
			p.next()
		}
		if link.Label == "" {
			return nil, p.errf(ln, "link entry has no %q", "label")
		}
		if link.URL == "" {
			return nil, p.errf(ln, "link %q has no %q", link.Label, "url")
		}
		out = append(out, link)
	}
}

func assignLinkField(p *navParser, ln navLine, link *Link, key, value string) error {
	switch key {
	case "label":
		link.Label = value
	case "url":
		link.URL = value
	default:
		return p.errf(ln, "unknown key %q in link entry (want label or url)", key)
	}
	return nil
}
