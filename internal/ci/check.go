package ci

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"

	"mvdan.cc/sh/v3/syntax"

	"github.com/rune-task-runner/rune/internal/ast"
	"github.com/rune-task-runner/rune/internal/diag"
	"github.com/rune-task-runner/rune/internal/token"
)

// machineBuiltins read the machine that evaluates them: its environment,
// files, PATH, clock, randomness or architecture. A value they give at
// generation would not be the value a job computes.
var machineBuiltins = map[string]bool{
	"env": true, "arch": true, "num_cpus": true, "path_exists": true, "read": true,
	"sha256_file": true, "absolute_path": true, "require": true, "which": true,
	"datetime": true, "uuid": true,
}

// pureBuiltins give the same value on every machine. os and os_family are
// evaluated for the runners' OS.
var pureBuiltins = map[string]bool{
	"os": true, "os_family": true, "join": true, "clean": true, "extension": true,
	"file_name": true, "file_stem": true, "parent_dir": true, "uppercase": true,
	"lowercase": true, "trim": true, "trim_start": true, "trim_end": true,
	"capitalize": true, "replace": true, "replace_regex": true, "sha256": true,
	"error": true, "quote": true, "quote_each": true,
}

// refuse records a diagnostic once, however many nodes lead to it.
func (b *builder) refuse(span token.Span, format string, args ...any) {
	d := diag.New(span, fmt.Sprintf(format, args...))
	for _, old := range b.diags {
		if old.Span == d.Span && old.Message == d.Message {
			return
		}
	}
	b.diags = append(b.diags, d)
}

// pureDefaults checks the defaults of the parameters of t that a call with
// supplied arguments leaves to their default.
func (b *builder) pureDefaults(t *ast.Task, supplied int) bool {
	ok := true
	params := map[string]bool{}
	for i, p := range t.Params {
		if i >= supplied && p.Kind == ast.ParamDefaulted && !b.pure(t, p.Default, params) {
			ok = false
		}
		params[p.Name] = true
	}
	return ok
}

// pureCall checks the arguments of a dependency call, in the scope of the
// calling task, and the defaults it leaves to the target.
func (b *builder) pureCall(cur *ast.Task, call *ast.DepCall, target *ast.Task) bool {
	params := map[string]bool{}
	for _, p := range cur.Params {
		params[p.Name] = true
	}
	ok := true
	for _, a := range call.Args {
		if !b.pure(cur, a, params) {
			ok = false
		}
	}
	return b.pureDefaults(target, len(call.Args)) && ok
}

// pure reports whether e, and every variable it reads, calls no built-in
// that reads the machine. params are the names that are parameters, whose
// values are already bound. Each such call is refused where it is written.
func (b *builder) pure(t *ast.Task, e ast.Expr, params map[string]bool) bool {
	ok := true
	seen := map[string]bool{}
	var walk func(e ast.Expr, params map[string]bool)
	walk = func(e ast.Expr, params map[string]bool) {
		switch x := e.(type) {
		case *ast.FuncCall:
			if machineBuiltins[x.Name] {
				ok = false
				b.refuse(x.Sp, "%s() reads this machine, so CI would compute another value for task %q; pass the value as a literal or a parameter", x.Name, t.Name)
			}
			for _, a := range x.Args {
				walk(a, params)
			}
		case *ast.VarRef:
			if params[x.Name] || seen[x.Name] {
				return
			}
			seen[x.Name] = true
			// A variable's expression cannot see task parameters, as eval
			// resolves it, so a name in it is always a variable.
			if a := b.in.Assigns[x.Name]; a != nil {
				walk(a.Expr, nil)
			}
		case *ast.Binary:
			walk(x.Left, params)
			walk(x.Right, params)
		case *ast.Conditional:
			for _, br := range x.Branches {
				walk(br.Left, params)
				walk(br.Right, params)
				walk(br.Result, params)
			}
			walk(x.Else, params)
		}
	}
	walk(e, params)
	return ok
}

// checkNode refuses what no job may run, and text a job cannot carry.
func (b *builder) checkNode(n *Node) {
	t := n.Task
	b.checkTask(t)
	for _, call := range t.FailHooks {
		hook := b.in.Tasks[call.Name]
		if hook == nil {
			continue
		}
		b.checkTask(hook)
		if len(hook.Deps) > 0 || len(hook.PostHooks) > 0 {
			b.refuse(call.Sp, "failure hook %q of task %q has dependencies or post-hooks, which would run again inside the job that failed; give the hook neither", hook.Name, t.Name)
		}
	}
	if err := CheckText(t.Name); err != nil {
		b.refuse(t.Sp, "task %q: %v", t.Name, err)
	}
	for _, w := range n.Argv {
		if err := CheckText(w); err != nil {
			b.refuse(t.Sp, "an argument of task %q: %v", t.Name, err)
		}
	}
	if c := t.Attr(ast.AttrCache); c != nil {
		for _, o := range c.Outputs {
			if lit, ok := o.(*ast.StringLit); ok {
				if err := CheckOutput(lit.Value); err != nil {
					b.refuse(lit.Sp, "task %q: %v", t.Name, err)
				}
			}
		}
	}
}

// checkTask refuses a task that needs a person or an agent's credentials.
func (b *builder) checkTask(t *ast.Task) {
	if a := t.Attr(ast.AttrConfirm); a != nil {
		b.refuse(a.Sp, "task %q has [confirm]: a CI job has no person to ask, and rune ci github never answers for one; run it by hand", t.Name)
	}
	if a := t.Attr(ast.AttrApplies); a != nil {
		b.refuse(a.Sp, "task %q has [applies]: it runs only a plan a person approved; run it by hand", t.Name)
	}
	if t.Executor == ast.ExecAgent {
		b.refuse(t.Sp, "task %q runs with the agent executor, which needs an agent's credentials; a CI job gets none", t.Name)
	}
}

// checkBackground refuses a node that starts a background process when
// another job needs it: the process ends with its own job.
func (b *builder) checkBackground() {
	needed := map[*Node]bool{}
	for _, n := range b.nodes {
		for _, m := range n.Needs {
			needed[m] = true
		}
	}
	for _, n := range b.nodes {
		if !needed[n] {
			continue
		}
		if line := backgroundLine(n.Task); line != nil {
			b.refuse(line.Sp, "task %q starts a background process, and another job needs the task: the process ends with its own job, so the next job cannot reach it", n.Task.Name)
		}
	}
}

// interpolation is one {{...}} of a body line; it stands for one word.
var interpolation = regexp.MustCompile(`\{\{.*?\}\}`)

// backgroundLine returns the first body line of a shell task that runs a
// command in the background, or nil. A line that does not parse as shell is
// not judged.
func backgroundLine(t *ast.Task) *ast.BodyLine {
	if t.Attr(ast.AttrScript) != nil || (t.Executor != "" && t.Executor != ast.ExecSh) {
		return nil
	}
	for _, line := range t.Body {
		text := strings.NewReplacer("{{{{", "{{", "}}}}", "}}").Replace(line.Raw)
		text = interpolation.ReplaceAllString(text, "x")
		file, err := syntax.NewParser().Parse(strings.NewReader(text), "")
		if err != nil {
			continue
		}
		found := false
		syntax.Walk(file, func(node syntax.Node) bool {
			if s, ok := node.(*syntax.Stmt); ok && s.Background {
				found = true
			}
			return !found
		})
		if found {
			return line
		}
	}
	return nil
}

// CheckText refuses text a workflow cannot carry: an expression GitHub would
// evaluate, a control character or a line break.
func CheckText(s string) error {
	if strings.Contains(s, "${{") {
		return fmt.Errorf("%q holds \"${{\", which GitHub would evaluate as an expression", s)
	}
	if strings.ContainsFunc(s, unicode.IsControl) {
		return fmt.Errorf("%q holds a control character or a line break, which a job cannot pass safely", s)
	}
	return nil
}

// CheckOutput refuses a [cache] output that is not inside the project: a
// bundle carries project files only.
func CheckOutput(pattern string) error {
	p := filepath.FromSlash(pattern)
	if filepath.IsAbs(p) || strings.HasPrefix(pattern, "/") || !filepath.IsLocal(p) {
		return fmt.Errorf("[cache] output %q is outside the project, and a bundle carries project files only", pattern)
	}
	return nil
}
