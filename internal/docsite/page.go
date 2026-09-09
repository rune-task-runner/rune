package docsite

import (
	"path"
	"strconv"
	"strings"
)

// Front is the Starlight frontmatter of one generated page. Starlight requires a
// title; the description feeds page metadata and sidebar cards.
type Front struct {
	Title       string
	Description string
}

// Frontmatter renders f as a YAML header, terminated by the closing delimiter
// and a blank line. Values are emitted as double-quoted scalars so ampersands,
// colons and quotation marks in real page titles cannot break the document.
func Frontmatter(f Front) string {
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString("title: " + strconv.Quote(f.Title) + "\n")
	if f.Description != "" {
		b.WriteString("description: " + strconv.Quote(f.Description) + "\n")
	}
	b.WriteString("---\n\n")
	return b.String()
}

// Transform converts one docs/ page into the Markdown Starlight builds from:
// frontmatter derived from the page's own heading and first paragraph, followed
// by the body with its H1 removed and every link rewritten. It returns the
// generated text and the frontmatter it used.
func Transform(src, fromPath string, cfg Config) (string, Front) {
	title, body := Title(src)
	if title == "" {
		// No heading: fall back to the slug's last segment so Starlight always
		// has a title. The drift test in test/docs flags these for a human.
		title = path.Base(Slug(fromPath))
	}
	front := Front{Title: title, Description: Description(body)}
	return Frontmatter(front) + RewriteLinks(body, fromPath, cfg), front
}
