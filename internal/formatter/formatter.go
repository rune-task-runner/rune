package formatter

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/rune-task-runner/rune/internal/ast"
	"github.com/rune-task-runner/rune/internal/token"
)

// Format rewrites a parsed Runefile in canonical form: settings, then
// assignments, then imports/mods, then tasks in declaration order, bodies
// indented four spaces. It is the single canonical formatter shared by the CLI
// (`rune --fmt`) and the language server's textDocument/formatting (spec FR-020).
func Format(f *ast.File) string {
	var b strings.Builder
	wrote := false

	section := func() {
		if wrote {
			b.WriteByte('\n')
		}
	}

	if len(f.Settings) > 0 {
		for _, s := range f.Settings {
			b.WriteString(formatSetting(s))
			b.WriteByte('\n')
		}
		wrote = true
	}
	if len(f.Assignments) > 0 {
		section()
		for _, a := range f.Assignments {
			fmt.Fprintf(&b, "%s := %s\n", a.Name, Expr(a.Expr))
		}
		wrote = true
	}
	for _, im := range f.Imports {
		section()
		q := ""
		if im.Optional {
			q = "?"
		}
		fmt.Fprintf(&b, "import%s %s\n", q, strconv.Quote(im.Path))
		wrote = true
	}
	for _, m := range f.Mods {
		if m.Path != "" {
			fmt.Fprintf(&b, "mod %s %s\n", m.Name, strconv.Quote(m.Path))
		} else {
			fmt.Fprintf(&b, "mod %s\n", m.Name)
		}
		wrote = true
	}
	for _, t := range f.Tasks {
		section()
		b.WriteString(formatTask(t))
		wrote = true
	}
	return b.String()
}

func formatSetting(s *ast.Setting) string {
	switch {
	case s.Bool:
		return "set " + s.Name
	case s.List != nil:
		parts := make([]string, len(s.List))
		for i, e := range s.List {
			parts[i] = Expr(e)
		}
		return fmt.Sprintf("set %s := [%s]", s.Name, strings.Join(parts, ", "))
	default:
		return fmt.Sprintf("set %s := %s", s.Name, Expr(s.Value))
	}
}

func formatTask(t *ast.Task) string {
	var b strings.Builder
	if t.Doc != "" {
		for _, line := range strings.Split(t.Doc, "\n") {
			fmt.Fprintf(&b, "# %s\n", line)
		}
	}
	for _, a := range t.Attributes {
		fmt.Fprintf(&b, "%s\n", formatAttr(a))
	}

	b.WriteString(t.Name)
	for _, p := range t.Params {
		b.WriteByte(' ')
		b.WriteString(formatParam(p))
	}
	if t.Executor != "" {
		fmt.Fprintf(&b, " (%s)", t.Executor)
	}
	b.WriteByte(':')
	for _, d := range t.Deps {
		b.WriteByte(' ')
		b.WriteString(formatDep(d))
	}
	if len(t.PostHooks) > 0 {
		b.WriteString(" &&")
		for _, d := range t.PostHooks {
			b.WriteByte(' ')
			b.WriteString(formatDep(d))
		}
	}
	if len(t.FailHooks) > 0 {
		b.WriteString(" ||")
		for _, d := range t.FailHooks {
			b.WriteByte(' ')
			b.WriteString(formatDep(d))
		}
	}
	b.WriteByte('\n')

	for _, bl := range t.Body {
		b.WriteString("    ")
		if bl.NoEcho {
			b.WriteByte('@')
		}
		if bl.ContinueOnError {
			b.WriteByte('-')
		}
		b.WriteString(bl.Raw)
		b.WriteByte('\n')
	}
	return b.String()
}

func formatParam(p *ast.Param) string {
	anno := p.Constraint.String() // "" for an unannotated param
	switch p.Kind {
	case ast.ParamVariadicPlus:
		return "+" + p.Name + anno
	case ast.ParamVariadicStar:
		return "*" + p.Name + anno
	case ast.ParamDefaulted:
		return p.Name + anno + "=" + Expr(p.Default)
	default:
		return p.Name + anno
	}
}

func formatDep(d *ast.DepCall) string {
	if len(d.Args) == 0 {
		return d.Name
	}
	parts := make([]string, len(d.Args))
	for i, a := range d.Args {
		parts[i] = Expr(a)
	}
	return fmt.Sprintf("(%s %s)", d.Name, strings.Join(parts, " "))
}

func formatAttr(a *ast.Attribute) string {
	switch a.Kind {
	case ast.AttrCache:
		var b strings.Builder
		fmt.Fprintf(&b, "[cache(inputs = [%s]", formatExprList(a.Inputs))
		if a.HasOutputs {
			fmt.Fprintf(&b, ", outputs = [%s]", formatExprList(a.Outputs))
		}
		b.WriteString(")]")
		return b.String()
	case ast.AttrEnv, ast.AttrParamDoc:
		return fmt.Sprintf("[%s(%s, %s)]", a.Kind, strconv.Quote(a.Str), strconv.Quote(a.Str2))
	case ast.AttrAgentAllow, ast.AttrPlan:
		return fmt.Sprintf("[%s(%s)]", a.Kind, ast.FormatAllow(a.Allow))
	default:
		if a.Str != "" {
			return fmt.Sprintf("[%s(%s)]", a.Kind, strconv.Quote(a.Str))
		}
		return "[" + a.Kind + "]"
	}
}

func formatExprList(es []ast.Expr) string {
	parts := make([]string, len(es))
	for i, e := range es {
		parts[i] = Expr(e)
	}
	return strings.Join(parts, ", ")
}

// Expr renders an expression back into canonical source syntax. It is the
// text the formatter writes, and what an export shows for a default that is
// not a literal (spec 056).
func Expr(e ast.Expr) string {
	switch x := e.(type) {
	case nil:
		return ""
	case *ast.StringLit:
		return strconv.Quote(x.Value)
	case *ast.VarRef:
		return x.Name
	case *ast.Binary:
		op := "+"
		if x.Op == token.SLASH {
			op = "/"
		}
		return fmt.Sprintf("%s %s %s", operand(x.Left, false), op, operand(x.Right, true))
	case *ast.FuncCall:
		return fmt.Sprintf("%s(%s)", x.Name, formatExprList(x.Args))
	case *ast.Conditional:
		var b strings.Builder
		for i, br := range x.Branches {
			if i == 0 {
				b.WriteString("if ")
			} else {
				b.WriteString(" else if ")
			}
			fmt.Fprintf(&b, "%s %s %s { %s }", Expr(br.Left), cmpString(br.Op), Expr(br.Right), Expr(br.Result))
		}
		fmt.Fprintf(&b, " else { %s }", Expr(x.Else))
		return b.String()
	default:
		return ""
	}
}

// operand renders one side of a + or /. The parser keeps no grouping
// parentheses, so they are written again where the text would read as
// another expression without them: the two operators share one precedence
// and group to the left, and an expression that starts with `if` ends at the
// closing brace of its else.
func operand(e ast.Expr, right bool) string {
	text := Expr(e)
	switch e.(type) {
	case *ast.Binary:
		if right {
			return "(" + text + ")"
		}
	case *ast.Conditional:
		if !right {
			return "(" + text + ")"
		}
	}
	return text
}

func cmpString(k token.Kind) string {
	switch k {
	case token.EQ:
		return "=="
	case token.NEQ:
		return "!="
	case token.MATCH:
		return "=~"
	default:
		return "?"
	}
}
