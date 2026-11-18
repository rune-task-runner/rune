package taskimport

import (
	"strconv"
	"strings"

	"github.com/rune-task-runner/rune/internal/token"
)

func nameStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func nameChar(c byte) bool {
	return nameStart(c) || (c >= '0' && c <= '9') || c == '-'
}

// validName reports whether s can name a task or a variable in a Runefile.
// A keyword of the language cannot.
func validName(s string) bool {
	if s == "" || !nameStart(s[0]) || token.Lookup(s) != token.IDENT {
		return false
	}
	for i := 1; i < len(s); i++ {
		if !nameChar(s[i]) {
			return false
		}
	}
	return true
}

// sanitize turns any source name into a valid Rune name. It does not make
// the name distinct from other names.
func sanitize(s string) string {
	var b strings.Builder
	dash := false
	for i := 0; i < len(s); i++ {
		if nameChar(s[i]) && s[i] != '-' {
			b.WriteByte(s[i])
			dash = false
			continue
		}
		if !dash {
			b.WriteByte('-')
			dash = true
		}
	}
	name := strings.Trim(b.String(), "-")
	if name == "" {
		return "task"
	}
	if !validName(name) {
		return "task-" + name
	}
	return name
}

// mapNames gives every source name one distinct Rune name. A name that is
// already valid keeps it, whatever its position; the others are sanitized and
// numbered in source order when the result is taken.
func mapNames(sources []string) []string {
	out := make([]string, len(sources))
	taken := make(map[string]bool, len(sources))
	for i, s := range sources {
		if validName(s) && !taken[s] {
			out[i] = s
			taken[s] = true
		}
	}
	for i, s := range sources {
		if out[i] != "" {
			continue
		}
		base := sanitize(s)
		name := base
		for n := 2; taken[name]; n++ {
			name = base + "-" + strconv.Itoa(n)
		}
		out[i] = name
		taken[name] = true
	}
	return out
}
