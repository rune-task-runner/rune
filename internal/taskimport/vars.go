package taskimport

import "fmt"

// srcVar is a top-level variable as a reader found it. reason says why it
// does not convert; refs are the variables its value uses.
type srcVar struct {
	name   string
	line   int
	expr   string
	refs   []string
	reason string
	global bool // not converting it changes how every task runs
}

// addVars settles which variables convert and records them: a variable
// converts when its own value does and every variable it uses converts, so
// one that takes part in a cycle does not. The converted ones become
// variables of the source and the others items. good holds the names that
// convert, bad the line of each one that does not.
func (s *source) addVars(vars []*srcVar) (good map[string]bool, bad map[string]int) {
	good, bad = map[string]bool{}, map[string]int{}
	for _, v := range vars {
		if v.reason == "" && !validName(v.name) {
			v.reason = "its name is not a valid Rune name"
		}
	}
	for changed := true; changed; {
		changed = false
		for _, v := range vars {
			if v.reason != "" || good[v.name] {
				continue
			}
			ready := true
			for _, ref := range v.refs {
				ready = ready && good[ref]
			}
			if ready {
				good[v.name] = true
				changed = true
			}
		}
	}
	for _, v := range vars {
		if good[v.name] {
			s.vars = append(s.vars, Var{Name: v.name, Expr: v.expr})
			continue
		}
		if v.reason == "" {
			for _, ref := range v.refs {
				if !good[ref] {
					v.reason = fmt.Sprintf("it uses %q, which is not a converted variable", ref)
					break
				}
			}
		}
		bad[v.name] = v.line
		s.items = append(s.items, Item{Line: v.line, Name: "variable " + v.name, Note: v.reason, Global: v.global})
	}
	return good, bad
}
