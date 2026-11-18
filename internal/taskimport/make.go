package taskimport

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	// The name is matched lazily: in "CFLAGS+=-O2" the operator is "+=", and
	// a greedy name would take the "+" and leave a plain "=".
	makeAssign    = regexp.MustCompile(`^([^\s:#=]+?)\s*(\?=|\+=|!=|:::=|::=|:=|=)\s*(.*)$`)
	makeDirective = regexp.MustCompile(`^(-include|sinclude|include|export|unexport|override|undefine|vpath|private)(\s+(.*))?$`)
	makeIf        = regexp.MustCompile(`^(ifeq|ifneq|ifdef|ifndef)(?:\s|$)`)
	makeDefine    = regexp.MustCompile(`^define\s+(\S+)`)
	makeTargetVar = regexp.MustCompile(`^\s*[^\s:#=]+\s*(\?=|\+=|:=|=)`)
	makePlainName = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
	makeVarName   = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]*$`)
)

// makeGlobal are the special variables, targets and directives that change
// how every recipe of a Makefile runs.
var makeGlobal = map[string]bool{
	"SHELL": true, ".SHELLFLAGS": true, ".RECIPEPREFIX": true, ".ONESHELL": true, ".SILENT": true,
	".IGNORE": true, ".POSIX": true, ".EXPORT_ALL_VARIABLES": true, "export": true, "unexport": true,
}

// makeLine is one logical line: continuation lines are joined to the line
// they continue, which gives its number.
type makeLine struct {
	n      int
	text   string
	recipe bool // it starts with a tab; text is what follows the tab
	quoted bool // a recipe line that continues inside single quotes
}

// makeRule is one target of a rule, before conversion.
type makeRule struct {
	line    int
	target  string
	prereqs []string
	doc     string
	reason  string // why the rule cannot convert, if known from its header
	cmds    []string
}

// makeLines joins the lines that end in a backslash. In a recipe the shell
// sees the next line without its leading tab; elsewhere make puts one space
// in place of the line break and the white space around it.
func makeLines(text string) []makeLine {
	var out []makeLine
	lines := strings.Split(text, "\n")
	for i := 0; i < len(lines); i++ {
		l := makeLine{n: i + 1, text: lines[i]}
		if l.recipe = strings.HasPrefix(l.text, "\t"); l.recipe {
			l.text = l.text[1:]
		}
		for makeContinues(l.text) && i+1 < len(lines) {
			i++
			if l.recipe {
				// The shell joins the lines, except inside single quotes,
				// where the backslash and the line break stay.
				l.text = strings.TrimSuffix(l.text, `\`)
				l.quoted = l.quoted || strings.Count(l.text, "'")%2 == 1
				l.text += strings.TrimPrefix(lines[i], "\t")
			} else {
				l.text = strings.TrimRight(strings.TrimSuffix(l.text, `\`), " \t") + " " + strings.TrimLeft(lines[i], " \t")
			}
		}
		out = append(out, l)
	}
	return out
}

// makeContinues reports whether a line ends in a backslash that is not
// itself escaped: make joins the next line only after an odd number of them.
func makeContinues(line string) bool {
	return (len(line)-len(strings.TrimRight(line, `\`)))%2 == 1
}

// makeComment removes the comment of a line that is not a recipe line. A
// comment that starts with "##" is the description of a rule.
func makeComment(line string) (text, doc string) {
	for i := 0; i < len(line); i++ {
		if line[i] != '#' {
			continue
		}
		if i > 0 && line[i-1] == '\\' {
			line = line[:i-1] + line[i:]
			i--
			continue
		}
		if strings.HasPrefix(line[i:], "##") {
			doc = strings.TrimSpace(strings.TrimLeft(line[i:], "#"))
		}
		return line[:i], doc
	}
	return line, ""
}

// readMake reads the supported subset of a Makefile: command targets with
// ordinary recipes and variables with literal values. Build rules and
// everything make evaluates are reported.
func readMake(text string) (*source, error) {
	src := &source{}
	var (
		rules   []*makeRule
		current []*makeRule // the targets of the rule being read
		inRule  bool        // a rule was read last, so recipe lines may follow
		byName  = map[string]*makeRule{}
		phony   = map[string]bool{}
		vars    []*srcVar
		varOf   = map[string]*srcVar{}
		values  = map[string]string{} // the first value of each variable, as written
		doc     string
	)
	lines := makeLines(text)
	for i := 0; i < len(lines); i++ {
		l := lines[i]
		if l.recipe {
			// The recipe of a rule that is reported as an item goes with it.
			if !inRule && strings.TrimSpace(l.text) != "" {
				return nil, &ParseError{l.n, "a command line before the first rule"}
			}
			for _, r := range current {
				r.cmds = append(r.cmds, l.text)
				if l.quoted && r.reason == "" {
					r.reason = "a command line continues with a backslash inside single quotes"
				}
			}
			continue
		}
		text, ruleDoc := makeComment(l.text)
		trim := strings.TrimSpace(text)
		pendingDoc := doc
		doc = ""
		if trim == "" {
			// A comment of its own right above a rule describes it. A blank
			// line or a comment does not end the recipe of a rule.
			if c := strings.TrimSpace(l.text); strings.HasPrefix(c, "#") {
				doc = strings.TrimSpace(strings.TrimLeft(c, "#"))
			}
			continue
		}
		ruleBefore, wasInRule := current, inRule
		current, inRule = nil, false

		switch {
		case makeDefine.MatchString(trim):
			end := makeSkip(lines, i, "define", "endef")
			if end < 0 {
				return nil, &ParseError{l.n, "define has no endef"}
			}
			src.items = append(src.items, Item{
				Line: l.n, Name: "define " + makeDefine.FindStringSubmatch(trim)[1],
				Note: "multi-line variables are not converted",
			})
			i = end
		case makeIf.MatchString(trim):
			end := makeSkip(lines, i, "if", "endif")
			word := makeIf.FindStringSubmatch(trim)[1]
			if end < 0 {
				return nil, &ParseError{l.n, word + " has no endif"}
			}
			src.items = append(src.items, Item{
				Line: l.n, Name: word,
				Note: fmt.Sprintf("conditional blocks are not evaluated: lines %d to %d are not imported", l.n, lines[end].n),
			})
			// A conditional inside a recipe does not end the rule: the command
			// lines after it are still its recipe, and what the rule runs is
			// not known without evaluating the condition.
			if wasInRule && makeRecipeFirst(lines[i+1:end]) {
				for _, r := range ruleBefore {
					if r.reason == "" {
						r.reason = fmt.Sprintf("part of its recipe is in the conditional block of line %d", l.n)
					}
				}
				current, inRule = ruleBefore, true
			}
			i = end
		case trim == "endif" || trim == "else" || strings.HasPrefix(trim, "else ") || trim == "endef":
			return nil, &ParseError{l.n, strings.Fields(trim)[0] + " without a conditional"}
		case makeDirective.MatchString(trim):
			m := makeDirective.FindStringSubmatch(trim)
			name := strings.TrimPrefix(m[1], "-")
			if name == "sinclude" {
				name = "include"
			}
			note := "the directive is not converted"
			if name == "include" {
				note = "the file is not read, so its rules and variables are not imported"
			}
			src.items = append(src.items, Item{Line: l.n, Name: strings.TrimSpace(m[1] + " " + m[3]), Note: note, Global: makeGlobal[name]})
			// A variable the directive also sets is not converted either.
			if a := makeAssign.FindStringSubmatch(m[3]); a != nil {
				makeBadVar(&vars, varOf, a[1], l.n, "it is set by the "+name+" directive")
			}
		case makeEvaluated(trim):
			src.items = append(src.items, Item{Line: l.n, Name: trim, Note: "make evaluates the line, and what it does is not imported"})
		case makeAssign.MatchString(trim):
			m := makeAssign.FindStringSubmatch(strings.TrimLeft(text, " \t"))
			if _, set := values[m[1]]; !set {
				values[m[1]] = m[3]
			}
			makeVariable(src, &vars, varOf, l.n, m[1], m[2], m[3])
		case strings.Contains(trim, ":"):
			targets, rest, _ := strings.Cut(trim, ":")
			names := strings.Fields(targets)
			inRule = true
			switch {
			case len(names) == 0:
				return nil, &ParseError{l.n, "a rule without a target"}
			case makeTargetVar.MatchString(rest):
				inRule = false
				src.items = append(src.items, Item{Line: l.n, Name: "target-specific variable", Note: "a variable set for one target is not converted"})
				continue
			case names[0] == ".PHONY":
				if strings.Contains(rest, "$") {
					src.items = append(src.items, Item{Line: l.n, Name: ".PHONY", Note: "the list uses a make variable, so its targets are not known to be phony"})
				}
				for _, p := range strings.Fields(rest) {
					phony[p] = true
				}
				continue
			case strings.HasPrefix(names[0], ".") && !strings.Contains(names[0], "/"):
				src.items = append(src.items, Item{
					Line: l.n, Name: names[0], Global: makeGlobal[names[0]],
					Note: "special targets and suffix rules are not converted",
				})
				continue
			case strings.Contains(targets, "%"):
				src.items = append(src.items, Item{
					Line: l.n, Name: "pattern rule " + strings.TrimSpace(targets),
					Note: "Rune has no pattern rules and does not build by timestamp",
				})
				continue
			}
			reason := ""
			switch {
			case strings.Contains(trim, "$"):
				reason = "the rule line uses a make variable or function"
			case strings.HasPrefix(rest, ":"):
				reason = "a double-colon rule"
			case len(names) > 1:
				reason = "a rule with several targets"
			case strings.Contains(rest, ";"):
				reason = "a command on the rule line after a semicolon"
			case strings.Contains(rest, "|"):
				reason = "order-only prerequisites"
			}
			if ruleDoc == "" {
				ruleDoc = pendingDoc
			}
			for _, name := range names {
				if prev := byName[name]; prev != nil {
					prev.reason = "the target has more than one rule"
					continue
				}
				r := &makeRule{line: l.n, target: name, prereqs: strings.Fields(rest), doc: ruleDoc, reason: reason}
				rules = append(rules, r)
				byName[name] = r
				current = append(current, r)
			}
		default:
			return nil, &ParseError{l.n, "this line is not a rule, a variable or a directive: " + trim}
		}
	}

	good, bad := src.addVars(vars)
	for _, r := range rules {
		src.tasks = append(src.tasks, makeTask(r, phony[r.target], good, bad, values))
	}
	return src, nil
}

// makeEvaluated reports whether a line is one reference and nothing else,
// such as $(info ...), $(eval ...) or $(shell ...): make evaluates it for
// what it does, and it is no rule and no variable.
func makeEvaluated(line string) bool {
	if !strings.HasPrefix(line, "$(") && !strings.HasPrefix(line, "${") {
		return false
	}
	n, _ := makeRef(line)
	return n == len(line) && line[n-1] == map[byte]byte{'(': ')', '{': '}'}[line[1]]
}

// makeSkip returns the index of the line that closes the block opened at
// lines[start], or -1. Conditionals nest; a define does not.
func makeSkip(lines []makeLine, start int, open, closing string) int {
	depth := 0
	for i := start; i < len(lines); i++ {
		if lines[i].recipe {
			continue
		}
		text, _ := makeComment(lines[i].text)
		word := strings.TrimSpace(text)
		switch {
		case open == "if" && makeIf.MatchString(word), open == "define" && i == start:
			depth++
		case word == closing:
			if depth--; depth == 0 {
				return i
			}
		}
	}
	return -1
}

// makeRecipeFirst reports whether the lines of a conditional block start with
// a command line, which makes the block a part of the recipe before it.
func makeRecipeFirst(block []makeLine) bool {
	for _, l := range block {
		if l.recipe {
			if strings.TrimSpace(l.text) != "" {
				return true
			}
			continue
		}
		text, _ := makeComment(l.text)
		word := strings.TrimSpace(text)
		if word == "" || makeIf.MatchString(word) || word == "endif" || word == "else" || strings.HasPrefix(word, "else ") {
			continue
		}
		return false
	}
	return false
}

// makeVariable records one assignment. A special variable is an item; any
// other becomes a variable when its value is literal text and references.
func makeVariable(src *source, vars *[]*srcVar, varOf map[string]*srcVar, line int, name, op, value string) {
	switch {
	case name == ".DEFAULT_GOAL":
		// A bare rune lists the tasks; the documentation says so once.
		return
	case makeGlobal[name], strings.HasPrefix(name, "."), name == "MAKEFLAGS", name == "VPATH":
		note := "special make variables are not converted"
		if makeGlobal[name] {
			note += ": imported tasks run in Rune's built-in shell with Rune's defaults"
		}
		src.items = append(src.items, Item{Line: line, Name: name, Global: makeGlobal[name], Note: note})
		return
	case varOf[name] != nil:
		if varOf[name].reason == "" {
			varOf[name].reason = fmt.Sprintf("it is set more than once (line %d)", line)
		}
		return
	case op == "+=":
		makeBadVar(vars, varOf, name, line, "it is appended to with +=")
		return
	case op == "!=":
		makeBadVar(vars, varOf, name, line, "its value runs a command (!=), which import never runs")
		return
	}
	v := &srcVar{name: name, line: line}
	v.expr, v.refs, v.reason = makeValue(value)
	// := takes the value a variable has at this line; one set later is empty.
	if op != "=" && op != "?=" && v.reason == "" {
		for _, ref := range v.refs {
			if varOf[ref] == nil {
				v.reason = fmt.Sprintf("it uses %q before it is set", ref)
				break
			}
		}
	}
	if op == "?=" && v.reason == "" {
		v.expr = fmt.Sprintf("env(%q, %s)", name, v.expr)
	}
	*vars = append(*vars, v)
	varOf[name] = v
}

// makeBadVar records a variable that does not convert, so that a recipe
// that uses it is reported.
func makeBadVar(vars *[]*srcVar, varOf map[string]*srcVar, name string, line int, reason string) {
	if v := varOf[name]; v != nil {
		if v.reason == "" {
			v.reason = reason
		}
		return
	}
	v := &srcVar{name: name, line: line, reason: reason}
	*vars = append(*vars, v)
	varOf[name] = v
}

// makeRef reads the "$..." at the start of s. It returns its length and,
// for a plain variable reference, the name.
func makeRef(s string) (n int, name string) {
	if len(s) < 2 {
		return len(s), ""
	}
	closing := map[byte]byte{'(': ')', '{': '}'}[s[1]]
	if closing == 0 {
		return 2, ""
	}
	depth := 0
	for i := 1; i < len(s); i++ {
		switch s[i] {
		case s[1]:
			depth++
		case closing:
			if depth--; depth == 0 {
				if inner := s[2:i]; makeVarName.MatchString(inner) {
					return i + 1, inner
				}
				return i + 1, ""
			}
		}
	}
	return len(s), ""
}

// makeValue converts the value of a variable to a Runefile expression:
// literal text and references to other variables. refs are their names.
func makeValue(value string) (expr string, refs []string, reason string) {
	var parts []string
	var lit strings.Builder
	flush := func() bool {
		if lit.Len() == 0 {
			return true
		}
		q, ok := quote(lit.String())
		parts = append(parts, q)
		lit.Reset()
		return ok
	}
	for i := 0; i < len(value); i++ {
		if value[i] != '$' {
			lit.WriteByte(value[i])
			continue
		}
		if strings.HasPrefix(value[i:], "$$") {
			lit.WriteByte('$')
			i++
			continue
		}
		n, name := makeRef(value[i:])
		if name == "" {
			return "", nil, "its value uses a make function, a substitution or an automatic variable: " + value[i:i+n]
		}
		if !flush() {
			return "", nil, "its value has a control character"
		}
		parts = append(parts, name)
		refs = append(refs, name)
		i += n - 1
	}
	if !flush() {
		return "", nil, "its value has a control character"
	}
	if len(parts) == 0 {
		return `""`, nil, ""
	}
	return strings.Join(parts, " + "), refs, ""
}

// makeExpand converts one recipe line: a reference to a converted variable
// becomes a Rune interpolation and "$$" a single "$". Anything else make
// would expand makes the line unconvertible.
func makeExpand(text string, good map[string]bool, bad map[string]int) (out, reason string) {
	var b strings.Builder
	for i := 0; i < len(text); i++ {
		c := text[i]
		if c != '$' || i == len(text)-1 {
			b.WriteByte(c)
			continue
		}
		if text[i+1] == '$' {
			b.WriteByte('$')
			i++
			continue
		}
		n, name := makeRef(text[i:])
		ref := text[i : i+n]
		switch {
		case good[name]:
			// Rune reads "{{{" as an interpolation that starts with a brace.
			if s := b.String(); (len(s)-len(strings.TrimRight(s, "{")))%2 == 1 {
				return "", "a brace directly before " + ref + " cannot be written in a Runefile body"
			}
			b.WriteString("\x00" + name + "\x01")
		case name != "" && bad[name] != 0:
			return "", fmt.Sprintf("it uses the variable %q of line %d, which was not converted", name, bad[name])
		case name != "":
			return "", fmt.Sprintf("it uses %s, which the Makefile does not set (an environment or built-in make variable)", ref)
		case n > 2:
			return "", "it uses a make function or substitution: " + ref
		case strings.ContainsRune("@<^?*+|%", rune(ref[1])):
			return "", "it uses the automatic variable " + ref
		default:
			return "", "it uses the make variable " + ref
		}
		i += n - 1
	}
	// The literal braces are escaped before the interpolations are written,
	// so that only the references of the Makefile are interpolations.
	out = escapeBraces(b.String())
	return strings.NewReplacer("\x00", "{{", "\x01", "}}").Replace(out), ""
}

// makePrefixes removes the prefixes of a recipe line. "+" only matters to
// make -n, which has no counterpart.
func makePrefixes(text string) cmdLine {
	var c cmdLine
	for {
		text = strings.TrimLeft(text, " \t")
		switch {
		case strings.HasPrefix(text, "@"):
			c.noEcho = true
		case strings.HasPrefix(text, "-"):
			c.ignore = true
		case strings.HasPrefix(text, "+"):
		default:
			c.text = text
			return c
		}
		text = text[1:]
	}
}

// makePrefixed reports whether the value of a variable starts with one of
// make's line prefixes, by itself or through the variable it starts with.
func makePrefixed(name string, values map[string]string, depth int) bool {
	value := strings.TrimLeft(values[name], " \t")
	switch {
	case value == "":
		return false
	case strings.ContainsRune("@-+", rune(value[0])):
		return true
	case value[0] == '$' && depth < 8:
		_, ref := makeRef(value)
		return ref != "" && makePrefixed(ref, values, depth+1)
	}
	return false
}

// makeTask converts one target. Only a target declared .PHONY is known to
// be a command; another one with a plain name converts with a note, and a
// file target does not convert. values are the variables as written.
func makeTask(r *makeRule, phony bool, good map[string]bool, bad map[string]int, values map[string]string) *Task {
	t := &Task{Source: r.target, Line: r.line, Doc: r.doc}
	for _, p := range r.prereqs {
		t.Deps = append(t.Deps, Call{Name: p})
	}
	switch {
	case r.reason != "":
		t.unsupported(r.reason)
		return t
	case phony:
	case makePlainName.MatchString(r.target):
		t.review(fmt.Sprintf("not declared .PHONY: make skips it when a file named %q is up to date; Rune always runs it", r.target))
	default:
		t.unsupported("a file target: make rebuilds it only when its prerequisites are newer, and Rune does not build by timestamp")
		return t
	}
	var cmds []cmdLine
	for _, line := range r.cmds {
		c := makePrefixes(line)
		// make reads the prefixes after it expands the line; Rune reads them
		// before, and would run a prefix that comes from a variable as text.
		if strings.HasPrefix(c.text, "$") {
			if n, name := makeRef(c.text); good[name] && makePrefixed(name, values, 0) {
				t.unsupported(fmt.Sprintf("the line starts with %s, whose value starts with a line prefix of make (@, - or +)", c.text[:n]))
				return t
			}
		}
		text, why := makeExpand(c.text, good, bad)
		if why != "" {
			t.unsupported(why)
			return t
		}
		c.text = text
		cmds = append(cmds, c)
	}
	body, why := shellBody(cmds, true)
	if why != "" {
		t.unsupported(why)
	}
	t.Body = body
	return t
}
