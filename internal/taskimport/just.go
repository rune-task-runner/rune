package taskimport

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

const justExprReason = "its value calls a function or is not built from string literals, variables, + and /"

var (
	justSet      = regexp.MustCompile(`^set\s+([a-z-]+)`)
	justAlias    = regexp.MustCompile(`^alias\s+(\S+)\s*:=\s*(\S+)`)
	justInclude  = regexp.MustCompile(`^(import|mod)\??\s+[^\s:=]`)
	justExport   = regexp.MustCompile(`^(?:export\s+([A-Za-z_][A-Za-z0-9_-]*)\s*:=|unexport\s+([A-Za-z_][A-Za-z0-9_-]*))`)
	justAssign   = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_-]*)\s*:=\s*(.*)$`)
	justAttrForm = regexp.MustCompile(`^([a-z-]+)(?:\((.*)\))?$`)
)

// justPlainAttrs and justTextAttrs are the just attributes that mean the same
// in a Runefile, without and with one string argument.
var (
	justPlainAttrs = map[string]bool{
		"private": true, "confirm": true, "no-cd": true, "linux": true, "macos": true,
		"windows": true, "unix": true, "no-exit-message": true,
	}
	justTextAttrs = map[string]bool{"confirm": true, "group": true, "doc": true, "working-directory": true}
)

// justRecipe is a recipe as the file has it, before conversion.
type justRecipe struct {
	line       int
	quiet      bool
	name       string
	params     string
	deps       string
	doc        string
	attrs      []string
	attrReason string
	body       []string
	duplicate  bool // the name is defined more than once
}

// readJust reads the supported subset of a justfile. Everything else is
// reported as an item or makes its recipe unsupported; only text that is no
// justfile construct at all is an error.
func readJust(text string) (*source, error) {
	src := &source{}
	var (
		recipes []justRecipe
		vars    []*srcVar
		first   = map[string]int{} // index in recipes of the first recipe of a name
		doc     string
		attrs   []string
		attrWhy string
	)
	lines := strings.Split(text, "\n")
	for i := 0; i < len(lines); i++ {
		line, n := lines[i], i+1
		trim := strings.TrimSpace(line)
		// A description and attributes belong to the recipe right below them.
		pendingDoc, pendingAttrs, pendingWhy := doc, attrs, attrWhy
		doc, attrs, attrWhy = "", nil, ""
		switch {
		case trim == "":
		case line[0] == ' ' || line[0] == '\t':
			return nil, &ParseError{n, "an indented line outside a task"}
		case trim[0] == '#':
			doc, attrs, attrWhy = strings.TrimSpace(trim[1:]), pendingAttrs, pendingWhy
		case trim[0] == '[':
			got, why := justAttrs(trim)
			doc, attrs = pendingDoc, append(pendingAttrs, got...)
			if attrWhy = pendingWhy; attrWhy == "" {
				attrWhy = why
			}
		case justSet.MatchString(trim):
			_, i = justStatement(lines, i)
			src.items = append(src.items, Item{
				Line: n, Name: "set " + justSet.FindStringSubmatch(trim)[1], Global: true,
				Note: "settings are not converted: imported tasks run in Rune's built-in shell with Rune's defaults",
			})
		case justAlias.MatchString(trim):
			m := justAlias.FindStringSubmatch(trim)
			src.items = append(src.items, Item{
				Line: n, Name: "alias " + m[1],
				Note: fmt.Sprintf("Rune has no aliases; run %q", m[2]),
			})
		case justInclude.MatchString(trim):
			src.items = append(src.items, Item{
				Line: n, Name: justInclude.FindStringSubmatch(trim)[1],
				Note: "the file is not read, so its tasks and variables are not imported",
			})
		case justExport.MatchString(trim):
			m := justExport.FindStringSubmatch(trim)
			// Every task of the justfile can read it as a shell variable, and
			// no imported task has it.
			vars = append(vars, &srcVar{
				name: m[1] + m[2], line: n, global: true,
				reason: "it is exported to the environment of every task, and no imported task has it",
			})
		case justAssign.MatchString(trim):
			var stmt string
			stmt, i = justStatement(lines, i)
			m := justAssign.FindStringSubmatch(strings.TrimSpace(stmt))
			v := &srcVar{name: m[1], line: n}
			v.expr, v.refs, v.reason = justExpr(m[2])
			vars = append(vars, v)
		default:
			quiet, name, params, deps, ok := justHeader(trim)
			if !ok {
				return nil, &ParseError{n, "this line is not a task, a variable or a setting: " + trim}
			}
			r := justRecipe{
				line: n, quiet: quiet, name: name, params: params, deps: deps,
				doc: pendingDoc, attrs: pendingAttrs, attrReason: pendingWhy,
			}
			indent := ""
			for i+1 < len(lines) {
				next := lines[i+1]
				if strings.TrimSpace(next) == "" {
					i++
					continue
				}
				if next[0] != ' ' && next[0] != '\t' {
					break
				}
				if indent == "" {
					indent = next[:len(next)-len(strings.TrimLeft(next, " \t"))]
				}
				if !strings.HasPrefix(next, indent) {
					return nil, &ParseError{i + 2, "the line is indented less than the first line of the task"}
				}
				r.body = append(r.body, next[len(indent):])
				i++
			}
			// just allows one recipe per operating system under one name; a
			// Runefile has one task per name.
			if at, again := first[name]; again {
				recipes[at].duplicate = true
				continue
			}
			first[name] = len(recipes)
			recipes = append(recipes, r)
		}
	}

	good, bad := src.addVars(vars)
	byName := map[string]*Task{}
	for _, r := range recipes {
		t := justTask(r, good, bad)
		src.tasks = append(src.tasks, t)
		byName[t.Source] = t
	}
	// just refuses a call whose arguments do not fit, and so does Rune.
	for _, t := range src.tasks {
		if t.Status == Unsupported {
			continue
		}
		for _, c := range slices.Concat(t.Deps, t.Post) {
			target := byName[c.Name]
			if target == nil || target.Status == Unsupported {
				continue
			}
			lo, hi := justArity(target.Params)
			if n := len(justFields(c.Args)); n < lo || (hi >= 0 && n > hi) {
				return nil, &ParseError{t.Line, fmt.Sprintf("task %q calls %q with %d argument(s), which it does not accept", t.Source, c.Name, n)}
			}
		}
	}
	return src, nil
}

// justArity is the least and the most arguments a recipe with these
// parameters takes; the most is -1 when it has a variadic parameter.
func justArity(params []string) (lo, hi int) {
	for _, p := range params {
		switch {
		case p[0] == '+':
			lo, hi = lo+1, -1
		case p[0] == '*':
			hi = -1
		case strings.Contains(p, "="):
			hi++
		default:
			lo, hi = lo+1, hi+1
		}
	}
	return lo, hi
}

// justTask converts one recipe. Any construct outside the supported subset
// makes the whole recipe unsupported.
func justTask(r justRecipe, vars map[string]bool, badVars map[string]int) *Task {
	t := &Task{Source: r.name, Line: r.line, Doc: r.doc, Attrs: r.attrs}
	if r.duplicate {
		t.unsupported("the task is defined more than once, which Rune has no counterpart for")
		return t
	}
	if r.attrReason != "" {
		t.unsupported(r.attrReason)
	}
	params, names, why := justParams(r.params)
	if why != "" {
		t.unsupported(why)
	}
	t.Params = params
	known := func(name string) bool { return slices.Contains(names, name) || vars[name] }
	var whyDeps string
	if t.Deps, t.Post, whyDeps = justDeps(r.deps, known); whyDeps != "" {
		t.unsupported(whyDeps)
	}

	if len(r.body) > 0 && strings.HasPrefix(r.body[0], "#!") {
		t.unsupported("a shebang task runs as a script, which is not converted")
		return t
	}
	var cmds []cmdLine
	for i := 0; i < len(r.body); i++ {
		text := r.body[i]
		// just joins a line that ends in a backslash with the next one and
		// drops that line's indentation.
		for strings.HasSuffix(text, `\`) && i+1 < len(r.body) {
			i++
			text = strings.TrimSuffix(text, `\`) + strings.TrimLeft(r.body[i], " \t")
		}
		c := cmdLine{noEcho: r.quiet}
		text = strings.TrimLeft(text, " \t")
		for at, dash := false, false; text != ""; text = text[1:] {
			if text[0] == '@' && !at {
				at, c.noEcho = true, !r.quiet
			} else if text[0] == '-' && !dash {
				dash, c.ignore = true, true
			} else {
				break
			}
		}
		if why := justInterpolations(text, known, badVars); why != "" {
			t.unsupported(why)
			return t
		}
		c.text = justEscape(text)
		cmds = append(cmds, c)
	}
	body, why := shellBody(cmds, true)
	if why != "" {
		t.unsupported(why)
	}
	t.Body = body
	return t
}

// justInterpolations checks that every {{...}} of a body line names a
// parameter or a converted variable, which is all a Runefile body shares
// with just.
func justInterpolations(text string, known func(string) bool, badVars map[string]int) string {
	for i := 0; i < len(text)-1; i++ {
		if !strings.HasPrefix(text[i:], "{{") {
			continue
		}
		if strings.HasPrefix(text[i:], "{{{{") {
			i += 3
			continue
		}
		end := strings.Index(text[i:], "}}")
		if end < 0 {
			return "an interpolation is not closed: " + text
		}
		name := strings.TrimSpace(text[i+2 : i+end])
		if line, isBad := badVars[name]; isBad && !known(name) {
			return fmt.Sprintf("it uses the variable %q of line %d, which was not converted", name, line)
		}
		if !known(name) {
			return fmt.Sprintf("the interpolation {{%s}} is not a plain variable or parameter", name)
		}
		i += end + 1
	}
	return ""
}

// justEscape writes a body line that justInterpolations accepted for Rune.
// The two read "{{{{" and an interpolation alike, but only Rune reads "}}}}"
// as two closing braces, so that text is escaped outside an interpolation.
func justEscape(text string) string {
	var b strings.Builder
	for i := 0; i < len(text); {
		rest := text[i:]
		n := 1
		switch {
		case strings.HasPrefix(rest, "{{{{"):
			n = 4
		case strings.HasPrefix(rest, "{{"):
			n = len(rest)
			if end := strings.Index(rest, "}}"); end >= 0 {
				n = end + 2
			}
		case strings.HasPrefix(rest, "}}}}"):
			b.WriteString("}}}}}}}}")
			i += 4
			continue
		}
		b.WriteString(rest[:n])
		i += n
	}
	return b.String()
}

// justStatement returns the statement that starts at lines[i] and the index
// of its last line. A setting or a value runs on over the following lines
// while a bracket it opened is not closed.
func justStatement(lines []string, i int) (string, int) {
	var parts []string
	depth := 0
	for ; i < len(lines); i++ {
		parts = append(parts, strings.TrimSpace(lines[i]))
		var quote byte
	scan:
		for j := 0; j < len(lines[i]); j++ {
			c := lines[i][j]
			switch {
			case quote != 0:
				if c == '\\' && quote == '"' {
					j++
				} else if c == quote {
					quote = 0
				}
			case c == '\'' || c == '"' || c == '`':
				quote = c
			case c == '#':
				break scan
			case c == '[' || c == '(' || c == '{':
				depth++
			case c == ']' || c == ')' || c == '}':
				depth--
			}
		}
		if depth <= 0 {
			break
		}
	}
	return strings.Join(parts, " "), min(i, len(lines)-1)
}

// justString decodes one string literal. Only the two plain forms are
// accepted, with the escapes a Runefile string also has.
func justString(s string) (string, bool) {
	if len(s) < 2 || (s[0] != '\'' && s[0] != '"') || s[len(s)-1] != s[0] {
		return "", false
	}
	inner := s[1 : len(s)-1]
	if s[0] == '\'' {
		return inner, !strings.Contains(inner, "'")
	}
	var b strings.Builder
	for i := 0; i < len(inner); i++ {
		c := inner[i]
		if c == '"' {
			return "", false
		}
		if c != '\\' {
			b.WriteByte(c)
			continue
		}
		if i++; i == len(inner) {
			return "", false
		}
		switch inner[i] {
		case 'n':
			b.WriteByte('\n')
		case 't':
			b.WriteByte('\t')
		case 'r':
			b.WriteByte('\r')
		case '"', '\\':
			b.WriteByte(inner[i])
		default:
			return "", false
		}
	}
	return b.String(), true
}

// justFields splits s at white space outside quotes and parentheses. It
// returns nil when a quote or a parenthesis is not closed.
func justFields(s string) []string {
	var (
		fields []string
		start  = -1
		quote  byte
		depth  int
	)
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0:
			if c == '\\' && quote == '"' {
				i++
			} else if c == quote {
				quote = 0
			}
		case c == '\'' || c == '"' || c == '`':
			quote = c
			if start < 0 {
				start = i
			}
		case c == '(':
			depth++
			if start < 0 {
				start = i
			}
		case c == ')' && depth > 0:
			depth--
		case (c == ' ' || c == '\t') && depth == 0:
			if start >= 0 {
				fields = append(fields, s[start:i])
				start = -1
			}
		default:
			if start < 0 {
				start = i
			}
		}
	}
	if quote != 0 || depth != 0 {
		return nil
	}
	if start >= 0 {
		fields = append(fields, s[start:])
	}
	return fields
}

// justExpr converts the value of a variable: string literals and names
// joined with + and /. refs are the names it uses.
func justExpr(s string) (expr string, refs []string, reason string) {
	var parts []string
	wantOperand := true
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == ' ' || c == '\t':
			i++
		case c == '#':
			i = len(s)
		case c == '`':
			return "", nil, "its value runs a command (backtick), which import never runs"
		case !wantOperand && (c == '+' || c == '/'):
			if c == '/' {
				// just joins with a plain "/"; Rune's own / also trims the
				// slashes around the joint.
				parts = append(parts, `"/"`)
			}
			wantOperand = true
			i++
		case wantOperand && (c == '\'' || c == '"'):
			end := i + 1
			for end < len(s) && s[end] != c {
				if s[end] == '\\' && c == '"' {
					end++
				}
				end++
			}
			if end >= len(s) {
				return "", nil, justExprReason
			}
			value, ok := justString(s[i : end+1])
			if !ok {
				return "", nil, justExprReason
			}
			lit, ok := quote(value)
			if !ok {
				return "", nil, "its value has a control character"
			}
			parts = append(parts, lit)
			wantOperand = false
			i = end + 1
		case wantOperand && nameStart(c):
			end := i
			for end < len(s) && nameChar(s[end]) {
				end++
			}
			name := s[i:end]
			rest := strings.TrimLeft(s[end:], " \t")
			if !validName(name) || strings.HasPrefix(rest, "(") {
				return "", nil, justExprReason
			}
			parts = append(parts, name)
			refs = append(refs, name)
			wantOperand = false
			i = end
		default:
			return "", nil, justExprReason
		}
	}
	if wantOperand {
		return "", nil, justExprReason
	}
	return strings.Join(parts, " + "), refs, ""
}

// justHeader splits a recipe header into its name, its parameters and its
// dependencies. The colon that ends the parameters is the first one outside
// a quote; one followed by "=" belongs to an assignment.
func justHeader(line string) (quiet bool, name, params, deps string, ok bool) {
	rest := line
	if strings.HasPrefix(rest, "@") {
		quiet, rest = true, rest[1:]
	}
	end := 0
	for end < len(rest) && nameChar(rest[end]) {
		end++
	}
	if end == 0 || !nameStart(rest[0]) {
		return false, "", "", "", false
	}
	name, rest = rest[:end], rest[end:]
	var quote byte
	for i := 0; i < len(rest); i++ {
		c := rest[i]
		switch {
		case quote != 0:
			if c == '\\' && quote == '"' {
				i++
			} else if c == quote {
				quote = 0
			}
		case c == '\'' || c == '"' || c == '`':
			quote = c
		case c == ':':
			if strings.HasPrefix(rest[i:], ":=") {
				return false, "", "", "", false
			}
			return quiet, name, strings.TrimSpace(rest[:i]), strings.TrimSpace(rest[i+1:]), true
		}
	}
	return false, "", "", "", false
}

// justParams converts the parameters of a recipe. names are their names.
func justParams(s string) (params, names []string, reason string) {
	fields := justFields(s)
	if fields == nil && strings.TrimSpace(s) != "" {
		return nil, nil, "the parameters are not understood: " + s
	}
	variadic, defaulted := false, false
	for _, f := range fields {
		if strings.HasPrefix(f, "$") {
			return nil, nil, fmt.Sprintf("parameter %q is exported to the environment", f)
		}
		kind := ""
		if f[0] == '+' || f[0] == '*' {
			kind, f = f[:1], f[1:]
		}
		name, def, hasDefault := strings.Cut(f, "=")
		if !validName(name) {
			return nil, nil, fmt.Sprintf("parameter %q is not a valid Rune name", name)
		}
		switch {
		case slices.Contains(names, name):
			return nil, nil, fmt.Sprintf("parameter %q is declared twice", name)
		case variadic:
			return nil, nil, "a parameter follows the variadic parameter"
		case defaulted && !hasDefault && kind == "":
			return nil, nil, fmt.Sprintf("the required parameter %q follows one with a default", name)
		}
		variadic, defaulted = kind != "", defaulted || hasDefault
		p := kind + name
		if hasDefault {
			if kind != "" {
				return nil, nil, fmt.Sprintf("variadic parameter %q has a default", name)
			}
			value, ok := justString(def)
			lit, printable := quote(value)
			if !ok || !printable {
				return nil, nil, fmt.Sprintf("parameter %q has a default that is not a string literal", name)
			}
			p += "=" + lit
		}
		params = append(params, p)
		names = append(names, name)
	}
	return params, names, ""
}

// justDeps converts the dependency list of a recipe: the calls before "&&"
// run before the recipe, the ones after it run after. known reports whether
// a name is a parameter of the recipe or a converted variable.
func justDeps(s string, known func(string) bool) (deps, post []Call, reason string) {
	notUnderstood := "the dependency list is not understood: " + s
	// The comment goes first: a quote in it is not the start of a string.
	s = justCode(s)
	fields := justFields(s)
	if fields == nil && strings.TrimSpace(s) != "" {
		return nil, nil, notUnderstood
	}
	after := false
	for _, f := range fields {
		var call Call
		switch {
		case f == "&&":
			if after {
				return nil, nil, notUnderstood
			}
			after = true
			continue
		case strings.HasPrefix(f, "(") && strings.HasSuffix(f, ")"):
			inner := justFields(f[1 : len(f)-1])
			if len(inner) == 0 {
				return nil, nil, notUnderstood
			}
			call.Name = inner[0]
			var args []string
			for _, a := range inner[1:] {
				if value, ok := justString(a); ok {
					if lit, printable := quote(value); printable {
						args = append(args, lit)
						continue
					}
				}
				if !known(a) {
					return nil, nil, fmt.Sprintf("the argument %q of dependency %q is not a string literal, a parameter or a variable", a, call.Name)
				}
				args = append(args, a)
			}
			call.Args = strings.Join(args, " ")
		default:
			call.Name = f
		}
		if !justName(call.Name) {
			return nil, nil, notUnderstood
		}
		if after {
			post = append(post, call)
		} else {
			deps = append(deps, call)
		}
	}
	return deps, post, ""
}

// justCode returns s without the comment that ends it: a "#" outside a
// string starts one.
func justCode(s string) string {
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0:
			if c == '\\' && quote == '"' {
				i++
			} else if c == quote {
				quote = 0
			}
		case c == '\'' || c == '"' || c == '`':
			quote = c
		case c == '#':
			return s[:i]
		}
	}
	return s
}

// justName reports whether s can name a recipe.
func justName(s string) bool {
	if s == "" || !nameStart(s[0]) {
		return false
	}
	for i := 1; i < len(s); i++ {
		if !nameChar(s[i]) {
			return false
		}
	}
	return true
}

// justAttrs converts one attribute line. The attributes that mean the same
// in a Runefile are kept; any other gives a reason, and the ones understood
// are still returned so that a recipe that fails stays private.
func justAttrs(line string) (attrs []string, reason string) {
	inner, closed := strings.CutSuffix(strings.TrimPrefix(line, "["), "]")
	for _, part := range justSplit(inner) {
		part = strings.TrimSpace(part)
		m := justAttrForm.FindStringSubmatch(part)
		switch {
		case !closed || m == nil:
		case m[2] == "" && !strings.HasSuffix(part, ")") && justPlainAttrs[m[1]]:
			attrs = append(attrs, m[1])
			continue
		case justTextAttrs[m[1]]:
			value, ok := justString(strings.TrimSpace(m[2]))
			if lit, printable := quote(value); ok && printable {
				attrs = append(attrs, m[1]+"("+lit+")")
				continue
			}
		}
		if reason == "" {
			reason = "the attribute [" + part + "] is not converted"
		}
	}
	return attrs, reason
}

// justSplit splits s at the commas outside quotes and parentheses.
func justSplit(s string) []string {
	var (
		parts []string
		start int
		quote byte
		depth int
	)
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0:
			if c == '\\' && quote == '"' {
				i++
			} else if c == quote {
				quote = 0
			}
		case c == '\'' || c == '"':
			quote = c
		case c == '(':
			depth++
		case c == ')':
			depth--
		case c == ',' && depth == 0:
			parts = append(parts, s[start:i])
			start = i + 1
		}
	}
	return append(parts, s[start:])
}
