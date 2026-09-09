package docsite

import (
	"regexp"
	"strings"
)

// descriptionLimit caps a generated page description. Search engines and
// Starlight's own cards both truncate well past this, so 160 is a readable
// ceiling rather than a hard technical limit.
const descriptionLimit = 160

var (
	h1Re     = regexp.MustCompile(`^#\s+(.*?)\s*#*\s*$`)
	linkRe   = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
	imageRe  = regexp.MustCompile(`!\[([^\]]*)\]\([^)]*\)`)
	emphasis = regexp.MustCompile("[*_`]")
	spacesRe = regexp.MustCompile(`\s+`)
)

// Title returns the text of the first level-one ATX heading in src, along with
// src minus that heading line. Starlight renders its own <h1> from frontmatter,
// so a retained heading would show the title twice. Headings inside fenced code
// blocks are ignored. When src has no such heading, Title returns ("", src).
func Title(src string) (string, string) {
	lines := strings.Split(src, "\n")
	inFence := false
	for i, line := range lines {
		if isFenceDelimiter(line) {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		m := h1Re.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		title := flattenInline(m[1])
		body := append(append([]string{}, lines[:i]...), lines[i+1:]...)
		return title, strings.Join(body, "\n")
	}
	return "", src
}

// Description derives a one-line summary from the first prose paragraph of src:
// inline markup flattened to plain text, wrapped lines joined, and the result
// truncated to descriptionLimit on a word boundary with a trailing ellipsis.
// Callouts, blockquotes, code fences, tables, lists, headings and HTML blocks
// are skipped when locating that paragraph. Returns "" when there is no prose.
func Description(src string) string {
	var para []string
	inFence := false
	for _, line := range strings.Split(src, "\n") {
		trimmed := strings.TrimSpace(line)

		if isFenceDelimiter(trimmed) {
			inFence = !inFence
			if len(para) > 0 {
				break
			}
			continue
		}
		if inFence {
			continue
		}
		if trimmed == "" {
			if len(para) > 0 {
				break
			}
			continue
		}
		if isProseStart(trimmed) {
			para = append(para, trimmed)
			continue
		}
		// A non-prose block: if a paragraph was already collected, it ends here.
		if len(para) > 0 {
			break
		}
	}
	if len(para) == 0 {
		return ""
	}
	return truncateWords(flattenInline(strings.Join(para, " ")), descriptionLimit)
}

func isFenceDelimiter(line string) bool {
	trimmed := strings.TrimSpace(line)
	return strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~")
}

// isProseStart reports whether a non-empty trimmed line begins ordinary prose,
// as opposed to a structural block that should not become a description.
func isProseStart(trimmed string) bool {
	switch trimmed[0] {
	case '#', '>', '|', '<', '-', '*', '+', '=', '!', ':':
		return false
	}
	// An ordered-list marker: digits followed by "." or ")".
	i := 0
	for i < len(trimmed) && trimmed[i] >= '0' && trimmed[i] <= '9' {
		i++
	}
	if i > 0 && i < len(trimmed) && (trimmed[i] == '.' || trimmed[i] == ')') {
		return false
	}
	return true
}

// flattenInline reduces inline Markdown to plain text: images drop out, links
// keep their text, emphasis and code markers are removed, and runs of
// whitespace collapse to single spaces.
func flattenInline(s string) string {
	s = imageRe.ReplaceAllString(s, "")
	s = linkRe.ReplaceAllString(s, "$1")
	s = emphasis.ReplaceAllString(s, "")
	return strings.TrimSpace(spacesRe.ReplaceAllString(s, " "))
}

// truncateWords shortens s to at most limit runes, including the ellipsis it
// appends, cutting at the last space so no word is split. It counts runes, not
// bytes, so a multi-byte character can neither be split nor overflow the limit.
func truncateWords(s string, limit int) string {
	r := []rune(s)
	if len(r) <= limit {
		return s
	}
	cut := string(r[:limit-1]) // leave room for the ellipsis
	if i := strings.LastIndex(cut, " "); i > 0 {
		cut = cut[:i]
	}
	return strings.TrimRight(cut, " ,;:.") + "…"
}
