package tasksuggest

import (
	"cmp"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"mvdan.cc/sh/v3/syntax"

	"github.com/rune-task-runner/rune/internal/taskimport"
	"github.com/rune-task-runner/rune/internal/token"
)

// family is groups of one simple command that differ only in the word at
// pos: one task with a parameter for that word (research R10).
type family struct {
	pos int
	// prefix is "--flag=" when only the value after it differs.
	prefix  string
	members []member
}

type member struct {
	group *Group
	value string
}

func (f *family) runs() int {
	n := 0
	for _, m := range f.members {
		n += m.group.Runs
	}
	return n
}

// firstText is the smallest canonical text of the family, to order
// families of equal runs and position.
func (f *family) firstText() string {
	texts := make([]string, len(f.members))
	for i, m := range f.members {
		texts[i] = m.group.Text
	}
	return slices.Min(texts)
}

// findFamilies picks the families among groups that can be tasks, and
// returns them with the groups they took. A group joins one family at most.
func findFamilies(groups []*Group, minRuns int) []*family {
	byKey := map[string]*family{}
	var keys []string
	for _, g := range groups {
		call, ok := simpleCall(g.file)
		if !ok || g.ReviewReason != "" {
			continue
		}
		words := make([]string, len(call.Args))
		for i, w := range call.Args {
			words[i] = printNode(w)
		}
		for i := 1; i < len(call.Args); i++ {
			prefix, value, ok := familyValue(call.Args[i].Lit())
			if !ok {
				continue
			}
			rest := slices.Clone(words)
			rest[i] = prefix
			key := strings.Join([]string{g.Dir, printAssigns(call), strconv.Itoa(i), strings.Join(rest, "\x00")}, "\x01")
			f := byKey[key]
			if f == nil {
				f = &family{pos: i, prefix: prefix}
				byKey[key] = f
				keys = append(keys, key)
			}
			f.members = append(f.members, member{g, value})
		}
	}
	cands := make([]*family, 0, len(keys))
	for _, k := range keys {
		if len(byKey[k].members) > 1 {
			cands = append(cands, byKey[k])
		}
	}
	slices.SortStableFunc(cands, func(a, b *family) int {
		return cmp.Or(cmp.Compare(b.runs(), a.runs()), cmp.Compare(a.pos, b.pos), strings.Compare(a.firstText(), b.firstText()))
	})
	used := map[*Group]bool{}
	var out []*family
	for _, f := range cands {
		f.members = slices.DeleteFunc(f.members, func(m member) bool { return used[m.group] })
		if len(f.members) < 2 || f.runs() < minRuns {
			continue
		}
		for _, m := range f.members {
			used[m.group] = true
		}
		out = append(out, f)
	}
	return out
}

// familyValue reads the word a parameter can stand for: a plain word, or
// the plain value of a --flag=value word, with that --flag=.
func familyValue(lit string) (prefix, value string, ok bool) {
	if strings.HasPrefix(lit, "-") {
		flag, v, found := strings.Cut(lit, "=")
		if !found {
			return "", "", false
		}
		prefix, lit = flag+"=", v
	}
	return prefix, lit, plainValue.MatchString(lit)
}

var paramName = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)

// param names the parameter after the flag before the word, or the flag of
// a --flag= word, and is "value" when there is no usable flag.
func (f *family) param(call *syntax.CallExpr) string {
	flag := strings.TrimSuffix(f.prefix, "=")
	if flag == "" {
		if before := call.Args[f.pos-1].Lit(); strings.HasPrefix(before, "-") && !strings.Contains(before, "=") {
			flag = before
		}
	}
	name := strings.ReplaceAll(strings.ToLower(strings.TrimLeft(flag, "-")), "-", "_")
	if !paramName.MatchString(name) || token.Lookup(name) != token.IDENT {
		return "value"
	}
	return name
}

// suggestion turns a family into one suggestion with a parameter. ok is
// false when the command cannot carry the placeholder.
func (f *family) suggestion() (*Suggestion, bool) {
	slices.SortStableFunc(f.members, func(a, b member) int {
		return cmp.Or(cmp.Compare(b.group.Runs, a.group.Runs), b.group.Last.Compare(a.group.Last), strings.Compare(a.value, b.value))
	})
	def := f.members[0].group
	if strings.Contains(def.Text, placeholder) {
		return nil, false
	}
	call, _ := simpleCall(def.file)
	name := f.param(call)
	word := call.Args[f.pos]
	call.Args[f.pos] = &syntax.Word{Parts: []syntax.WordPart{&syntax.Lit{Value: f.prefix + placeholder}}}
	text := printNode(def.file)
	call.Args[f.pos] = word

	p := &Param{Name: name, Default: f.members[0].value}
	s := &Suggestion{
		Dir:   def.Dir,
		Body:  strings.Replace(taskimport.EscapeBraces(text), placeholder, "{{quote("+name+")}}", 1),
		Param: p,
		key:   text,
		file:  def.file,
		// The word that differs is the parameter, not part of the name.
		namePos: f.pos,
	}
	for _, m := range f.members {
		p.Values = append(p.Values, ValueCount{Value: m.value, Text: m.group.Text, Runs: m.group.Runs})
		s.add(m.group)
	}
	return s, true
}
