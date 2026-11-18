package taskimport

import (
	"regexp"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// cmdLine is one command of a source task, before it becomes a body line.
type cmdLine struct {
	text   string
	noEcho bool // the source does not print the command
	ignore bool // the source ignores the command's failure
}

// shellBuiltins are the commands a shell runs itself. Only a builtin can
// change the state of the shell, so that a later command sees the change; a
// program cannot.
var shellBuiltins = map[string]bool{
	".": true, ":": true, "[": true, "alias": true, "bg": true, "bind": true, "break": true,
	"builtin": true, "caller": true, "cd": true, "command": true, "compgen": true,
	"complete": true, "compopt": true, "continue": true, "declare": true, "dirs": true,
	"disown": true, "echo": true, "enable": true, "eval": true, "exec": true, "exit": true,
	"export": true, "false": true, "fc": true, "fg": true, "getopts": true, "hash": true,
	"help": true, "history": true, "jobs": true, "kill": true, "let": true, "local": true,
	"logout": true, "mapfile": true, "nameref": true, "popd": true, "printf": true,
	"pushd": true, "pwd": true, "read": true, "readarray": true, "readonly": true,
	"return": true, "set": true, "shift": true, "shopt": true, "source": true, "suspend": true,
	"test": true, "times": true, "trap": true, "true": true, "type": true, "typeset": true,
	"ulimit": true, "umask": true, "unalias": true, "unset": true, "wait": true,
}

// harmlessBuiltins change nothing a later command can see. printf is one of
// them unless it assigns with -v.
var harmlessBuiltins = map[string]bool{
	":": true, "[": true, "echo": true, "false": true, "printf": true, "pwd": true,
	"test": true, "true": true, "type": true,
}

var (
	escapedBraces = regexp.MustCompile(`\{\{\{\{`)
	interpolation = regexp.MustCompile(`\{\{.*?\}\}`)
)

// parseLine parses text as Rune's shell will: one line, one complete command.
// A Rune interpolation stands for any text, so it is parsed as a variable.
func parseLine(text string) (*syntax.File, error) {
	text = escapedBraces.ReplaceAllString(text, "x")
	text = interpolation.ReplaceAllString(text, "$$x")
	return syntax.NewParser().Parse(strings.NewReader(text), "")
}

// changesState reports whether running f can change what a later command of
// the same shell sees. It errs toward true: wrapping a harmless line in a
// subshell costs nothing, and missing one changes what the task does.
func changesState(f *syntax.File) bool {
	found := false
	syntax.Walk(f, func(n syntax.Node) bool {
		if found {
			return false
		}
		switch x := n.(type) {
		case *syntax.Subshell, *syntax.CmdSubst, *syntax.ProcSubst:
			return false
		case *syntax.DeclClause, *syntax.LetClause, *syntax.ArithmCmd, *syntax.ArithmExp,
			*syntax.FuncDecl, *syntax.ForClause, *syntax.CoprocClause:
			found = true
		case *syntax.ParamExp:
			if x.Exp != nil && (x.Exp.Op == syntax.AssignUnset || x.Exp.Op == syntax.AssignUnsetOrNull) {
				found = true
			}
		case *syntax.CallExpr:
			found = callChangesState(x)
		}
		return !found
	})
	return found
}

// callChangesState reports whether one simple command can change the shell:
// an assignment with no command, a command whose name is not a literal word
// (it can name any builtin), or a builtin that is not harmless.
func callChangesState(call *syntax.CallExpr) bool {
	if len(call.Args) == 0 {
		return true
	}
	name := call.Args[0].Lit()
	// A backslash is quoting too: the shell runs "\cd" as cd.
	if name == "" || strings.Contains(name, `\`) {
		return true
	}
	if name == "printf" {
		for _, arg := range call.Args[1:] {
			if arg.Lit() != "" && strings.HasPrefix(arg.Lit(), "-v") {
				return true
			}
			if arg.Lit() == "" || !strings.HasPrefix(arg.Lit(), "-") {
				break
			}
		}
	}
	return shellBuiltins[name] && !harmlessBuiltins[name]
}

// shellBody turns the commands of a source task into Runefile body lines.
// isolated says that the source runner starts a shell for every command, so a
// command that changes shell state must not reach the lines after it. A
// non-empty reason means the commands cannot be converted.
func shellBody(lines []cmdLine, isolated bool) (body []string, reason string) {
	var kept []cmdLine
	for _, l := range lines {
		l.text = strings.TrimSpace(l.text)
		if l.text != "" {
			kept = append(kept, l)
		}
	}
	for i, l := range kept {
		if first, _, multi := strings.Cut(l.text, "\n"); multi {
			return nil, "the command spans several lines: " + first
		}
		if l.text[0] == '@' || l.text[0] == '-' {
			return nil, `the command starts with "` + l.text[:1] + `", which Rune reads as a line prefix: ` + l.text
		}
		f, err := parseLine(l.text)
		if err != nil {
			return nil, "not a complete shell command on one line: " + l.text
		}
		text := l.text
		if isolated && i < len(kept)-1 && changesState(f) {
			text = "(" + l.text + ")"
			if !isSubshell(text) {
				return nil, "changes shell state and cannot be wrapped in a subshell: " + l.text
			}
		}
		prefix := ""
		if l.noEcho {
			prefix += "@"
		}
		if l.ignore {
			prefix += "-"
		}
		body = append(body, prefix+text)
	}
	return body, ""
}

// isSubshell reports whether text is exactly one subshell command. A trailing
// comment in the wrapped line would swallow the closing parenthesis.
func isSubshell(text string) bool {
	f, err := parseLine(text)
	if err != nil || len(f.Stmts) != 1 {
		return false
	}
	_, ok := f.Stmts[0].Cmd.(*syntax.Subshell)
	return ok
}

var braceEscaper = strings.NewReplacer("{{", "{{{{", "}}}}", "}}}}}}}}")

// escapeBraces writes text so that a Rune body prints it unchanged: Rune
// reads "{{" as the start of an interpolation, "{{{{" as two opening braces
// and "}}}}" as two closing ones.
func escapeBraces(text string) string {
	return braceEscaper.Replace(text)
}

var shellSafe = regexp.MustCompile(`^[A-Za-z0-9_@%+=:,./-]+$`)

// shellQuote writes s as one shell word of a Rune body line.
func shellQuote(s string) string {
	if !shellSafe.MatchString(s) {
		s = "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
	}
	return escapeBraces(s)
}
