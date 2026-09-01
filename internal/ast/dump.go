package ast

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/rune-task-runner/rune/internal/token"
)

// Dump renders a File as a stable, indented tree. It is used by golden AST tests
// and for ad-hoc debugging; it is deterministic (declaration order preserved).
func Dump(f *File) string {
	var b strings.Builder
	b.WriteString("File\n")
	for _, s := range f.Settings {
		fmt.Fprintf(&b, "  Setting %s", s.Name)
		switch {
		case s.Bool:
			b.WriteString(" = true")
		case s.List != nil:
			fmt.Fprintf(&b, " = [%s]", dumpExprs(s.List))
		case s.Value != nil:
			fmt.Fprintf(&b, " = %s", dumpExpr(s.Value))
		}
		b.WriteByte('\n')
	}
	for _, a := range f.Assignments {
		fmt.Fprintf(&b, "  Assignment %s = %s\n", a.Name, dumpExpr(a.Expr))
	}
	for _, im := range f.Imports {
		opt := ""
		if im.Optional {
			opt = "?"
		}
		fmt.Fprintf(&b, "  Import%s %s\n", opt, strconv.Quote(im.Path))
	}
	for _, m := range f.Mods {
		if m.Path != "" {
			fmt.Fprintf(&b, "  Mod %s %s\n", m.Name, strconv.Quote(m.Path))
		} else {
			fmt.Fprintf(&b, "  Mod %s\n", m.Name)
		}
	}
	for _, t := range f.Tasks {
		dumpTask(&b, t)
	}
	return b.String()
}

func dumpTask(b *strings.Builder, t *Task) {
	fmt.Fprintf(b, "  Task %s", t.Name)
	if t.Executor != "" {
		fmt.Fprintf(b, " (%s)", t.Executor)
	}
	b.WriteByte('\n')
	if t.Doc != "" {
		fmt.Fprintf(b, "    doc: %s\n", strconv.Quote(t.Doc))
	}
	for _, a := range t.Attributes {
		dumpAttr(b, a)
	}
	for _, par := range t.Params {
		fmt.Fprintf(b, "    Param %s %s", par.Name, paramKindName(par.Kind))
		if par.Constraint != nil {
			fmt.Fprintf(b, " %s", par.Constraint.String())
		}
		if par.Default != nil {
			fmt.Fprintf(b, " = %s", dumpExpr(par.Default))
		}
		b.WriteByte('\n')
	}
	if len(t.Deps) > 0 {
		fmt.Fprintf(b, "    Deps: %s\n", dumpDeps(t.Deps))
	}
	if len(t.PostHooks) > 0 {
		fmt.Fprintf(b, "    PostHooks: %s\n", dumpDeps(t.PostHooks))
	}
	if len(t.FailHooks) > 0 {
		fmt.Fprintf(b, "    FailHooks: %s\n", dumpDeps(t.FailHooks))
	}
	if len(t.Body) > 0 {
		b.WriteString("    Body\n")
		for _, bl := range t.Body {
			prefix := ""
			if bl.NoEcho {
				prefix += "@"
			}
			if bl.ContinueOnError {
				prefix += "-"
			}
			fmt.Fprintf(b, "      %s%s\n", prefix, strconv.Quote(bl.Raw))
		}
	}
}

func dumpAttr(b *strings.Builder, a *Attribute) {
	switch a.Kind {
	case AttrCache:
		fmt.Fprintf(b, "    Attr cache(inputs=[%s]", dumpExprs(a.Inputs))
		if a.HasOutputs {
			fmt.Fprintf(b, ", outputs=[%s]", dumpExprs(a.Outputs))
		}
		b.WriteString(")\n")
	case AttrEnv, AttrParamDoc:
		fmt.Fprintf(b, "    Attr %s(%s, %s)\n", a.Kind, strconv.Quote(a.Str), strconv.Quote(a.Str2))
	default:
		if a.Str != "" {
			fmt.Fprintf(b, "    Attr %s(%s)\n", a.Kind, strconv.Quote(a.Str))
		} else {
			fmt.Fprintf(b, "    Attr %s\n", a.Kind)
		}
	}
}

func dumpDeps(deps []*DepCall) string {
	parts := make([]string, len(deps))
	for i, d := range deps {
		if len(d.Args) > 0 {
			parts[i] = fmt.Sprintf("(%s %s)", d.Name, dumpExprs(d.Args))
		} else {
			parts[i] = d.Name
		}
	}
	return strings.Join(parts, ", ")
}

func paramKindName(k ParamKind) string {
	switch k {
	case ParamRequired:
		return "required"
	case ParamDefaulted:
		return "defaulted"
	case ParamVariadicPlus:
		return "variadic+"
	case ParamVariadicStar:
		return "variadic*"
	default:
		return "?"
	}
}

func dumpExprs(es []Expr) string {
	parts := make([]string, len(es))
	for i, e := range es {
		parts[i] = dumpExpr(e)
	}
	return strings.Join(parts, ", ")
}

// DumpExpr renders an expression in the same compact form Dump uses (exported
// for --dump JSON output).
func DumpExpr(e Expr) string { return dumpExpr(e) }

// dumpExpr renders an expression compactly: "lit", name, (+ a b), (/ a b),
// f(args), or if(cond -> result; ... else).
func dumpExpr(e Expr) string {
	switch x := e.(type) {
	case nil:
		return "<nil>"
	case *StringLit:
		return strconv.Quote(x.Value)
	case *VarRef:
		return x.Name
	case *Binary:
		op := "+"
		if x.Op == token.SLASH {
			op = "/"
		}
		return fmt.Sprintf("(%s %s %s)", op, dumpExpr(x.Left), dumpExpr(x.Right))
	case *FuncCall:
		return fmt.Sprintf("%s(%s)", x.Name, dumpExprs(x.Args))
	case *Conditional:
		var parts []string
		for _, br := range x.Branches {
			parts = append(parts, fmt.Sprintf("%s %s %s -> %s",
				dumpExpr(br.Left), cmpName(br.Op), dumpExpr(br.Right), dumpExpr(br.Result)))
		}
		return fmt.Sprintf("if(%s; else -> %s)", strings.Join(parts, "; "), dumpExpr(x.Else))
	default:
		return fmt.Sprintf("<expr %T>", e)
	}
}

func cmpName(k token.Kind) string {
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
