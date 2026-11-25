package ci

import (
	"fmt"
	"strings"
)

// maxJobID keeps job IDs short enough to read in the GitHub interface.
const maxJobID = 64

// Slug is the readable part of a job ID and of a cache key: the task name
// and arguments, lowercased, with every run of other characters than
// a-z, 0-9 and _ written as one -, starting with a letter.
func Slug(task string, argv []string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(strings.Join(append([]string{task}, argv...), "-")) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' {
			b.WriteRune(r)
			dash = false
		} else if !dash {
			b.WriteByte('-')
			dash = true
		}
	}
	s := strings.Trim(b.String(), "-")
	if s == "" || s[0] < 'a' || s[0] > 'z' {
		s = strings.TrimSuffix("t-"+s, "-")
	}
	return s
}

// cut shortens s to at most n bytes, without a trailing -.
func cut(s string, n int) string {
	if len(s) > n {
		s = strings.TrimRight(s[:n], "-")
	}
	return s
}

// jobIDs gives every node of the graphs a unique job ID, in graph and node
// order. With several graphs every ID ends in the OS of its graph; a clash
// gets -2, -3 before that.
func jobIDs(graphs []*Graph) map[*Node]string {
	ids := map[*Node]string{}
	used := map[string]bool{}
	for _, g := range graphs {
		suffix := ""
		if len(graphs) > 1 {
			suffix = "-" + osName(g.OS)
		}
		for _, n := range g.Nodes {
			base := Slug(n.Task.Name, n.Argv)
			id := cut(base, maxJobID-len(suffix)) + suffix
			for i := 2; used[id]; i++ {
				tail := fmt.Sprintf("-%d", i)
				id = cut(base, maxJobID-len(suffix)-len(tail)) + tail + suffix
			}
			used[id] = true
			ids[n] = id
		}
	}
	return ids
}

// Label is how a job names its node: the task and its arguments as they are
// written on a command line, and the OS when there are several.
func Label(n *Node, goos string, several bool) string {
	words := []string{n.Task.Name}
	for _, a := range n.Argv {
		words = append(words, ShellWord(a))
	}
	label := strings.Join(words, " ")
	if several {
		label += " (" + osName(goos) + ")"
	}
	return label
}

// ShellWord writes s as one word that bash passes on unchanged: bare when it
// holds only characters no shell expands, else in double quotes with \, $,
// ` and " escaped. Text with a line break never reaches it (CheckText).
func ShellWord(s string) string {
	if s != "" && !strings.ContainsFunc(s, unsafeInWord) {
		return s
	}
	return `"` + strings.NewReplacer(`\`, `\\`, `$`, `\$`, "`", "\\`", `"`, `\"`).Replace(s) + `"`
}

// unsafeInWord reports a character a shell may expand or split a word at.
func unsafeInWord(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return false
	default:
		return !strings.ContainsRune("_./:@%+=,-", r)
	}
}

// osName is the Runefile's name of a GOOS, as --os takes it.
func osName(goos string) string {
	if goos == "darwin" {
		return "macos"
	}
	return goos
}

// runsOn is the GitHub-hosted runner of a GOOS.
func runsOn(goos string) string {
	if goos == "darwin" {
		return "macos-latest"
	}
	return "ubuntu-latest"
}
