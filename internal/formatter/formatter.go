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
			fmt.Fprintf(&b, "%s := %s\n", a.Name, formatExpr(a.Expr))
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
			parts[i] = formatExpr(e)
		}
		return fmt.Sprintf("set %s := [%s]", s.Name, strings.Join(parts, ", "))
	default:
		return fmt.Sprintf("set %s := %s", s.Name, formatExpr(s.Value))
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
	switch p.Kind {
	case ast.ParamVariadicPlus:
		return "+" + p.Name
	case ast.ParamVariadicStar:
		return "*" + p.Name
	case ast.ParamDefaulted:
		return p.Name + "=" + formatExpr(p.Default)
	default:
		return p.Name
	}
}

func formatDep(d *ast.DepCall) string {
	if len(d.Args) == 0 {
		return d.Name
	}
	parts := make([]string, len(d.Args))
	for i, a := range d.Args {
		parts[i] = formatExpr(a)
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
	case ast.AttrEnv:
		return fmt.Sprintf("[env(%s, %s)]", strconv.Quote(a.Str), strconv.Quote(a.Str2))
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
		parts[i] = formatExpr(e)
	}
	return strings.Join(parts, ", ")
}

// formatExpr renders an expression back into canonical source syntax.
func formatExpr(e ast.Expr) string {
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
		return fmt.Sprintf("%s %s %s", formatExpr(x.Left), op, formatExpr(x.Right))
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
			fmt.Fprintf(&b, "%s %s %s { %s }", formatExpr(br.Left), cmpString(br.Op), formatExpr(br.Right), formatExpr(br.Result))
		}
		fmt.Fprintf(&b, " else { %s }", formatExpr(x.Else))
		return b.String()
	default:
		return ""
	}
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
