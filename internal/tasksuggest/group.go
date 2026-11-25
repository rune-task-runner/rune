package tasksuggest

import (
	"path"
	"regexp"
	"strings"
	"time"

	"mvdan.cc/sh/v3/syntax"

	"github.com/rune-task-runner/rune/internal/observe"
)

// Group is the runs of one command in one working directory.
type Group struct {
	// Dir is the working directory relative to the project root, with
	// forward slashes; "" is the root.
	Dir string
	// Text is the command in its canonical form (research R7).
	Text     string
	Runs     int
	Attempts int
	Outcomes map[Outcome]int
	Last     time.Time
	// Descriptions counts the runs per description of the agent.
	Descriptions map[string]int
	// ReviewReason says why the group is not turned into a task; "" for
	// a command Rune can write as a task.
	ReviewReason string

	sessions map[string]bool
	descLast map[string]time.Time
	file     *syntax.File
	runsRune bool
}

// Sessions is the number of sessions the group ran in.
func (g *Group) Sessions() int { return len(g.sessions) }

// The reasons a listed group is for review only.
const (
	reasonLines    = "it spans more than one line"
	reasonSecret   = "a value was removed as a secret; write this task by hand"
	reasonSequence = "it runs commands in sequence with ;"
	reasonFeature  = "it uses a shell feature Rune does not turn into a task"
)

// stateBuiltins change the state of the shell they run in. A task body runs
// in a shell of its own, so such a command would not do there what it did
// for the agent.
var stateBuiltins = map[string]bool{
	"cd": true, "pushd": true, "popd": true, "export": true, "unset": true, "set": true,
	"source": true, ".": true, "eval": true, "exec": true, "alias": true, "unalias": true,
	"shopt": true, "trap": true, "umask": true, "ulimit": true, "declare": true,
	"typeset": true, "local": true, "readonly": true,
}

// parse reads a command as shell syntax: as data, never run or expanded.
func parse(text string) (*syntax.File, error) {
	return syntax.NewParser().Parse(strings.NewReader(text), "")
}

// printNode writes a node in canonical form: every word as it was quoted,
// the spacing between words and operators normalized.
func printNode(n syntax.Node) string {
	var b strings.Builder
	// Printing a tree the parser made does not fail.
	_ = syntax.NewPrinter().Print(&b, n)
	return strings.TrimRight(b.String(), "\n")
}

// groupExecutions puts each execution in the group of its command and
// working directory, after the cd rule. Groups are in the order their first
// execution appears.
func groupExecutions(execs []Execution) []*Group {
	byKey := map[string]*Group{}
	var out []*Group
	for _, e := range execs {
		dir, text, f := normalize(e.Command, e.Cwd)
		key := dir + "\x00" + text
		g := byKey[key]
		if g == nil {
			g = &Group{
				Dir: dir, Text: text, Outcomes: map[Outcome]int{}, Descriptions: map[string]int{},
				sessions: map[string]bool{}, descLast: map[string]time.Time{}, file: f,
			}
			if f == nil {
				g.ReviewReason = reasonFeature
			} else {
				g.runsRune = runsRune(f)
				g.ReviewReason = reviewReason(f, text)
			}
			byKey[key] = g
			out = append(out, g)
		}
		if e.Outcome == Attempt {
			g.Attempts++
			continue
		}
		g.Runs++
		g.Outcomes[e.Outcome]++
		g.sessions[e.Session] = true
		g.Last = maxTime(g.Last, e.Last)
		if e.Description != "" {
			g.Descriptions[e.Description]++
			g.descLast[e.Description] = maxTime(g.descLast[e.Description], e.Last)
		}
	}
	return out
}

func maxTime(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

// normalize parses a command, applies the cd rule, and returns the working
// directory and the canonical text. A command that does not parse keeps its
// text and has no tree.
func normalize(command, cwd string) (dir, text string, f *syntax.File) {
	f, err := parse(command)
	if err != nil {
		return cwd, command, nil
	}
	if d, ok := stripCD(f, cwd); ok {
		cwd = d
	}
	return cwd, printNode(f), f
}

// stripCD drops a leading `cd DIR &&` from a chain of && and returns the
// directory the rest runs in. DIR must be one plain relative path that,
// from cwd, stays inside the project (research R9 step 1).
func stripCD(f *syntax.File, cwd string) (string, bool) {
	if len(f.Stmts) != 1 {
		return cwd, false
	}
	slot := &f.Stmts[0]
	for {
		b, ok := andChain(*slot)
		if !ok {
			return cwd, false
		}
		if _, deeper := andChain(b.X); deeper {
			slot = &b.X
			continue
		}
		dir, ok := cdTarget(b.X, cwd)
		if !ok {
			return cwd, false
		}
		*slot = b.Y
		return dir, true
	}
}

// andChain returns the && of a statement that is only that.
func andChain(s *syntax.Stmt) (*syntax.BinaryCmd, bool) {
	b, ok := s.Cmd.(*syntax.BinaryCmd)
	if !ok || b.Op != syntax.AndStmt || plainStmt(s) != nil {
		return nil, false
	}
	return b, true
}

// plainStmt returns s itself when it is negated, in the background or has
// redirections, and nil when it is a plain statement.
func plainStmt(s *syntax.Stmt) *syntax.Stmt {
	if s.Negated || s.Background || s.Coprocess || s.Disown || len(s.Redirs) > 0 {
		return s
	}
	return nil
}

// cdTarget reads `cd DIR` and returns where it leads from cwd.
func cdTarget(s *syntax.Stmt, cwd string) (string, bool) {
	call, ok := s.Cmd.(*syntax.CallExpr)
	if !ok || plainStmt(s) != nil || len(call.Assigns) != 0 || len(call.Args) != 2 || call.Args[0].Lit() != "cd" {
		return "", false
	}
	arg := call.Args[1].Lit()
	if arg == "" || strings.HasPrefix(arg, "-") || strings.HasPrefix(arg, "~") || path.IsAbs(arg) || strings.ContainsAny(arg, `\:`) {
		return "", false
	}
	joined := path.Clean(path.Join(cwd, arg))
	switch {
	case joined == "..", strings.HasPrefix(joined, "../"):
		return "", false
	case joined == ".":
		return "", true
	}
	return joined, true
}

// runsRune reports whether any command of the tree runs rune itself.
func runsRune(f *syntax.File) bool {
	found := false
	syntax.Walk(f, func(n syntax.Node) bool {
		if call, ok := n.(*syntax.CallExpr); ok && len(call.Args) > 0 {
			name := path.Base(strings.ReplaceAll(call.Args[0].Lit(), `\`, "/"))
			if name == "rune" || name == "rune.exe" {
				found = true
			}
		}
		return !found
	})
	return found
}

// reviewReason says why a command is not written as a task, or "" when it
// can be (research R9 step 6).
func reviewReason(f *syntax.File, text string) string {
	// The printer writes the commands of a sequence on lines of their own,
	// so a sequence is told apart before the line rule.
	switch {
	case strings.Contains(text, observe.Mark):
		return reasonSecret
	case f != nil && len(f.Stmts) != 1:
		return reasonSequence
	case strings.Contains(text, "\n"):
		return reasonLines
	}
	reason := ""
	syntax.Walk(f, func(n syntax.Node) bool {
		if reason == "" {
			reason = nodeReason(n)
		}
		return reason == ""
	})
	return reason
}

// nodeReason says why one node keeps a command from being a task.
func nodeReason(n syntax.Node) string {
	switch n := n.(type) {
	case *syntax.Stmt:
		if n.Background {
			return "it runs a command in the background with &"
		}
		if n.Negated || n.Coprocess || n.Disown {
			return reasonFeature
		}
	case *syntax.CmdSubst:
		return "it uses a command substitution"
	case *syntax.ProcSubst:
		return "it uses a process substitution"
	case *syntax.ArithmExp, *syntax.ArithmCmd, *syntax.LetClause:
		return "it uses arithmetic expansion"
	case *syntax.Subshell:
		return "it runs a subshell"
	case *syntax.Block:
		return "it runs a group in braces"
	case *syntax.IfClause:
		return "it uses if"
	case *syntax.WhileClause:
		if n.Until {
			return "it uses until"
		}
		return "it uses while"
	case *syntax.ForClause:
		if n.Select {
			return "it uses select"
		}
		return "it uses for"
	case *syntax.CaseClause:
		return "it uses case"
	case *syntax.FuncDecl:
		return "it defines a function"
	case *syntax.BinaryCmd:
		switch n.Op {
		case syntax.AndStmt, syntax.OrStmt, syntax.Pipe, syntax.PipeAll:
		default:
			return reasonFeature
		}
	case *syntax.CallExpr:
		if len(n.Args) == 0 {
			return reasonFeature
		}
		if name := n.Args[0].Lit(); stateBuiltins[name] {
			return "it changes the shell's state with " + name
		}
	case *syntax.DeclClause:
		return "it changes the shell's state with " + n.Variant.Value
	case *syntax.ParamExp:
		if !simpleParam(n) {
			return reasonFeature
		}
	case *syntax.TestClause, *syntax.TimeClause, *syntax.CoprocClause, *syntax.TestDecl, *syntax.ArrayExpr, *syntax.ExtGlob, *syntax.BraceExp:
		return reasonFeature
	}
	return ""
}

// simpleParam reports whether an expansion is a plain $NAME or ${NAME}.
func simpleParam(p *syntax.ParamExp) bool {
	return p.Param != nil && p.Flags == nil && !p.Excl && !p.Length && !p.Width && !p.IsSet &&
		p.NestedParam == nil && p.Index == nil && len(p.Modifiers) == 0 && p.Slice == nil &&
		p.Repl == nil && p.Names == 0 && p.Exp == nil
}

// plainValue matches a word a parameter can take: one that needs no quotes,
// expands nothing and is not a flag.
var plainValue = regexp.MustCompile(`^[A-Za-z0-9_./:@%+,=][A-Za-z0-9_./:@%+,=-]{0,255}$`)

// TaskInfo is what matching needs of a task of the Runefile.
type TaskInfo struct {
	Name string
	// Dir is the task's working directory relative to the root, with
	// forward slashes; "" is the root.
	Dir string
	// Body is the task's only body line, without an @ or - prefix; "" when
	// the task has none or more than one, or does not run in Rune's shell.
	Body string
}

// placeholder stands for a parameter interpolation of a task's body while
// the body is read as shell syntax.
const placeholder = "RUNEPARAMPLACEHOLDER"

var interpolation = regexp.MustCompile(`\{\{\{\{|\}\}\}\}|\{\{.*?\}\}`)

// taskShape reads a body line as a command: the escaped braces as the
// shell sees them and each interpolation as a placeholder word.
func taskShape(body string) (*syntax.File, string, bool) {
	text := interpolation.ReplaceAllStringFunc(body, func(m string) string {
		switch m {
		case "{{{{":
			return "{{"
		case "}}}}":
			return "}}"
		}
		return placeholder
	})
	f, err := parse(text)
	if err != nil {
		return nil, "", false
	}
	return f, printNode(f), true
}

// matches reports whether a task runs the group's command (research R9
// step 4): the same canonical text, or one simple command whose
// placeholder words each stand for one plain word.
func (t TaskInfo) matches(g *Group) bool {
	if t.Body == "" || g.file == nil {
		return false
	}
	f, text, ok := taskShape(t.Body)
	if !ok {
		return false
	}
	// The cd rule holds for a body as for a command: `cd web && npm test`
	// is npm test in web.
	dir := t.Dir
	if d, stripped := stripCD(f, dir); stripped {
		dir, text = d, printNode(f)
	}
	if dir != g.Dir {
		return false
	}
	if text == g.Text {
		return true
	}
	if !strings.Contains(text, placeholder) {
		return false
	}
	want, ok := simpleCall(f)
	got, ok2 := simpleCall(g.file)
	if !ok || !ok2 || len(want.Args) != len(got.Args) || printAssigns(want) != printAssigns(got) {
		return false
	}
	for i, w := range want.Args {
		if !wordMatches(w, got.Args[i]) {
			return false
		}
	}
	return true
}

// simpleCall returns the call of a tree that is one plain simple command.
func simpleCall(f *syntax.File) (*syntax.CallExpr, bool) {
	if len(f.Stmts) != 1 || plainStmt(f.Stmts[0]) != nil {
		return nil, false
	}
	call, ok := f.Stmts[0].Cmd.(*syntax.CallExpr)
	return call, ok && len(call.Args) > 0
}

func printAssigns(call *syntax.CallExpr) string {
	parts := make([]string, len(call.Assigns))
	for i, a := range call.Assigns {
		parts[i] = printNode(a)
	}
	return strings.Join(parts, " ")
}

// wordMatches compares a word of a task with a word of a command: a
// placeholder takes any plain word, and --flag=placeholder any plain value
// after the same --flag=.
func wordMatches(task, cmd *syntax.Word) bool {
	tw, cw := printNode(task), printNode(cmd)
	if tw == cw {
		return true
	}
	lit := cmd.Lit()
	if tw == placeholder {
		return plainValue.MatchString(lit)
	}
	if prefix, ok := strings.CutSuffix(tw, placeholder); ok && strings.HasPrefix(prefix, "-") && strings.HasSuffix(prefix, "=") {
		value, ok := strings.CutPrefix(lit, prefix)
		return ok && plainValue.MatchString(value)
	}
	return false
}
