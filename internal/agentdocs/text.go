package agentdocs

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// MaxText is the longest description, returns text or parameter text an
// export shows, in bytes. It is the limit of Rune's other agent-facing
// texts; a test in internal/cli keeps the two equal.
const MaxText = 8 * 1024

// summaryRunes is how much of a description an index line of a skill shows.
const summaryRunes = 100

const (
	zeroWidthNonJoiner = rune(0x200c)
	zeroWidthJoiner    = rune(0x200d)
)

// Text from a Runefile reaches the output through prose, value or code and
// in no other way. Together they keep every backtick, $, < and > of the
// Runefile out of the output: a client that loads the file runs a command
// written between backticks after "!", replaces $ placeholders, imports
// @path and may strip HTML comments (spec 056 R8 to R10).

var proseEscaper = strings.NewReplacer(
	"&", "&amp;", "<", "&lt;", ">", "&gt;", "`", "&#96;", "$", "&#36;", "@", "&#64;",
	`\`, `\\`, "*", `\*`, "_", `\_`, "[", `\[`, "]", `\]`, "#", `\#`, "~", `\~`, "|", `\|`,
)

// prose renders a description as one paragraph of Markdown that shows the
// characters the author wrote and can open no block, span, element, comment
// or marker line.
func prose(s string) string {
	s = collapse(s)
	cut := len(s) > MaxText
	if cut {
		end := MaxText
		for end > 0 && !utf8.RuneStart(s[end]) {
			end--
		}
		s = s[:end]
	}
	s = escapeProse(s)
	if cut {
		s += " [truncated]"
	}
	return s
}

// summary is the short form of a description for an index line: cut first
// and escaped after, so that a cut never splits an escape.
func summary(s string) string {
	s = collapse(s)
	if utf8.RuneCountInString(s) > summaryRunes {
		end := 0
		for range summaryRunes {
			_, size := utf8.DecodeRuneInString(s[end:])
			end += size
		}
		return escapeProse(s[:end]) + "..."
	}
	return escapeProse(s)
}

// collapse turns every run of white space and of characters that are not
// printable into one space and trims the ends. The two zero-width joiners
// stay: Persian and other scripts need them inside a word, and so do joined
// emoji. A character that changes the direction of text does not.
func collapse(s string) string {
	var b strings.Builder
	gap := false
	for _, r := range s {
		if unicode.IsSpace(r) || !unicode.IsPrint(r) && r != zeroWidthNonJoiner && r != zeroWidthJoiner {
			gap = true
			continue
		}
		if gap && b.Len() > 0 {
			b.WriteByte(' ')
		}
		gap = false
		b.WriteRune(r)
	}
	return b.String()
}

// escapeProse escapes collapsed text in one pass and then guards its first
// character, which stands at the start of a line.
func escapeProse(s string) string {
	s = proseEscaper.Replace(s)
	if s == "" {
		return s
	}
	switch s[0] {
	case '-', '+', '=':
		return `\` + s
	}
	digits := 0
	for digits < len(s) && s[digits] >= '0' && s[digits] <= '9' {
		digits++
	}
	if digits > 0 && digits < len(s) && (s[digits] == '.' || s[digits] == ')') {
		return s[:digits] + `\` + s[digits:]
	}
	return s
}

// value renders a string of the Runefile as a quoted string in a code span.
// The quotes show where it starts and ends, also when it is empty.
func value(s string) string {
	return "`" + codeEscape(strconv.Quote(s)) + "`"
}

// code renders the canonical text of an expression in a code span.
func code(expr string) string {
	return "`" + codeEscape(expr) + "`"
}

// codeEscape writes the four characters that must not come from a Runefile
// as escapes that a quoted string reads back.
func codeEscape(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '`', '$', '<', '>':
			fmt.Fprintf(&b, "\\u%04x", r)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// shQuote writes a value as one word of a POSIX shell. A word that starts
// with "=" is quoted too: zsh reads it as the path of a command.
func shQuote(s string) string {
	if s != "" && s[0] != '=' && !strings.ContainsFunc(s, func(r rune) bool { return !shBare(r) }) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// shBare reports whether a shell reads r as itself outside quotes.
func shBare(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return true
	}
	return strings.ContainsRune("_-./:=,+@%", r)
}

// exampleSafe reports whether a value may stand in an example command. A
// value that starts with "-" could be taken for a flag, such as an enum
// value "--yes".
func exampleSafe(s string) bool {
	if strings.HasPrefix(s, "-") {
		return false
	}
	return !strings.ContainsFunc(s, func(r rune) bool {
		return r == '`' || r == '$' || r == '<' || r == '>' || !unicode.IsPrint(r)
	})
}
