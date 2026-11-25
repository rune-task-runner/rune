package taskimport

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Quote writes s as a Runefile string literal. It reports false when s holds
// a character the Runefile lexer has no escape for, or is not UTF-8: Rune's
// shell refuses such text, and writing it by character would change it.
func Quote(s string) (string, bool) {
	if !utf8.ValidString(s) {
		return "", false
	}
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '\\', '"':
			b.WriteByte('\\')
			b.WriteRune(r)
		case '\n':
			b.WriteString(`\n`)
		case '\t':
			b.WriteString(`\t`)
		case '\r':
			b.WriteString(`\r`)
		default:
			if r < 0x20 || r == 0x7f {
				return "", false
			}
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String(), true
}

// emitRunefile writes the finished tasks as Runefile text, in source order.
func emitRunefile(src *source, opts Options) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Imported from %s by rune import.\n", opts.SourceName)
	if len(src.vars) > 0 {
		b.WriteByte('\n')
		for _, v := range src.vars {
			fmt.Fprintf(&b, "%s := %s\n", v.Name, v.Expr)
		}
	}
	for _, t := range src.tasks {
		b.WriteByte('\n')
		if t.Doc != "" {
			fmt.Fprintf(&b, "# %s\n", t.Doc)
		}
		for _, a := range t.Attrs {
			fmt.Fprintf(&b, "[%s]\n", a)
		}
		b.WriteString(t.Name)
		for _, p := range t.Params {
			b.WriteString(" " + p)
		}
		b.WriteByte(':')
		for _, c := range t.Deps {
			b.WriteString(" " + emitCall(c))
		}
		if len(t.Post) > 0 {
			b.WriteString(" &&")
			for _, c := range t.Post {
				b.WriteString(" " + emitCall(c))
			}
		}
		b.WriteByte('\n')
		for _, line := range t.Body {
			fmt.Fprintf(&b, "    %s\n", line)
		}
	}
	return b.String()
}

func emitCall(c Call) string {
	if c.Args == "" {
		return c.Name
	}
	return "(" + c.Name + " " + c.Args + ")"
}

// showName writes a name of the source for the report. A name with a line
// break or another control character is quoted, so that every report line
// stays one line.
func showName(name string) string {
	if strings.ContainsFunc(name, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return strconv.Quote(name)
	}
	return name
}

// emitReport writes one line per task and per item, ordered by source line,
// then the counts. It also returns how many lines it wrote before the
// summary and how many of them are unsupported.
func emitReport(src *source, opts Options) (report string, lines, unsupported int) {
	type entry struct {
		line   int
		status Status
		text   string
	}
	var entries []entry
	for _, t := range src.tasks {
		text := showName(t.Source)
		if t.Name != t.Source {
			text += " -> " + t.Name
		}
		if len(t.Notes) > 0 {
			text += ": " + strings.Join(t.Notes, "; ")
		}
		entries = append(entries, entry{t.Line, t.Status, text})
	}
	for _, it := range src.items {
		entries = append(entries, entry{it.Line, Unsupported, showName(it.Name) + ": " + it.Note})
	}
	// Stable, so a task stays before an item of the same line and equal lines
	// keep the order of the source.
	slices.SortStableFunc(entries, func(a, b entry) int { return a.line - b.line })

	var b strings.Builder
	var counts [4]int
	for _, e := range entries {
		counts[e.status]++
		fmt.Fprintf(&b, "%s:%d: %s: %s\n", opts.SourceName, e.line, e.status, e.text)
	}
	fmt.Fprintf(&b, "summary: %d converted, %d review, %d unsupported, %d wrappers\n",
		counts[Converted], counts[Review], counts[Unsupported], counts[Wrapper])
	return b.String(), len(entries), counts[Unsupported]
}
