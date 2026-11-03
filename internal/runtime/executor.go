// Package runtime selects and drives the executor for a task body: the default
// pure-Go shell (internal/runtime/shell), a temp-file interpreter for
// python/node/custom or a `set shell` override (internal/runtime/interp), or the
// AI-agent driver (internal/runtime/agent). A `set shell` list runs without a
// shell's bare -c, since the body arrives as a script file.
package runtime

import (
	"slices"
	"strings"

	"github.com/rune-task-runner/rune/internal/ast"
	"github.com/rune-task-runner/rune/internal/config"
)

// Kind identifies which executor runs a task body.
type Kind int

const (
	KindShell  Kind = iota // mvdan/sh (default, never the system shell)
	KindInterp             // temp-file + exec a real interpreter
	KindAgent              // drive an installed agent CLI (US4)
)

// Selection is the resolved executor for a task.
type Selection struct {
	Kind    Kind
	Command []string // interpreter command (KindInterp): e.g. ["python3"]
	Display string   // human-facing name for diagnostics
}

// Select resolves a task's executor from its declared executor, any
// [script("...")] attribute, and the module settings. A `set shell := [...]`
// override switches even default bodies to temp-file exec of the named shell. For
// both, a shell's command-string flag is removed so the body runs from the script
// file with the list's other flags (spec 029).
func Select(task *ast.Task, settings config.Settings) Selection {
	if attr := task.Attr(ast.AttrScript); attr != nil && attr.Str != "" {
		return Selection{Kind: KindInterp, Command: commandWithoutStringFlag(splitCommand(attr.Str)), Display: attr.Str}
	}
	switch task.Executor {
	case "", ast.ExecSh:
		if len(settings.Shell) > 0 {
			return Selection{Kind: KindInterp, Command: commandWithoutStringFlag(settings.Shell), Display: "shell:" + settings.Shell[0]}
		}
		return Selection{Kind: KindShell, Display: "sh"}
	case ast.ExecPython:
		return Selection{Kind: KindInterp, Command: defaultCmd(settings.Python, "python3"), Display: "python"}
	case ast.ExecNode:
		return Selection{Kind: KindInterp, Command: defaultCmd(settings.Node, "node"), Display: "node"}
	case ast.ExecAgent:
		return Selection{Kind: KindAgent, Display: "agent"}
	default:
		// A custom executor name is treated as the interpreter command itself.
		return Selection{Kind: KindInterp, Command: []string{task.Executor}, Display: task.Executor}
	}
}

func defaultCmd(list []string, def string) []string {
	if len(list) > 0 {
		return list
	}
	return []string{def}
}

func splitCommand(s string) []string {
	return strings.Fields(s)
}

// commandWithoutStringFlag drops a shell's command-string flag (-c, or the c in
// -cu or -euc) when the list gives the shell no code of its own. The script path
// is appended to the command, so with -c the shell takes that path as its code and
// runs the file as a child script that ignores the list's flags (issue #34). A -c
// that belongs to a wrapper (nix develop -c bash) or another program (php -c, node
// -c), or that is followed by the list's own code, works as written and is kept.
// So is a list with -s: without -c, the shell would read stdin instead of the file.
// The result is a new slice because settings.Shell is shared by every task.
func commandWithoutStringFlag(cmd []string) []string {
	shell := slices.IndexFunc(cmd, isKnownShell)
	if shell < 0 {
		return slices.Clone(cmd)
	}
	first := slices.IndexFunc(cmd[shell+1:], isStringFlag)
	if first < 0 || slices.ContainsFunc(cmd[shell+1:], isStdinFlag) {
		return slices.Clone(cmd)
	}
	first += shell + 1
	for i := first + 1; i < len(cmd); i++ {
		if isOperand(cmd[i-1], cmd[i]) {
			return slices.Clone(cmd)
		}
	}
	out := make([]string, 0, len(cmd))
	out = append(out, cmd[:shell+1]...)
	for _, arg := range cmd[shell+1:] {
		if isStringFlag(arg) {
			if arg = strings.ReplaceAll(arg, "c", ""); arg == "-" {
				continue
			}
		}
		out = append(out, arg)
	}
	return out
}

// isKnownShell reports whether arg names a shell whose -c reads its code from the
// next operand. Matching on the base name keeps paths and Windows names working.
func isKnownShell(arg string) bool {
	name := arg[strings.LastIndexAny(arg, `/\`)+1:]
	switch strings.TrimSuffix(strings.ToLower(name), ".exe") {
	case "sh", "bash", "dash", "zsh", "ksh", "mksh", "ash", "fish":
		return true
	}
	return false
}

func isStringFlag(arg string) bool {
	return strings.HasPrefix(arg, "-") && isShortCluster(arg) && strings.Contains(arg, "c")
}

func isStdinFlag(arg string) bool {
	return strings.HasPrefix(arg, "-") && isShortCluster(arg) && strings.Contains(arg, "s")
}

// isShortCluster reports whether arg is single-letter options after one sign
// ("-eu", "+o"), which rules out long options and values such as "-O2".
func isShortCluster(arg string) bool {
	if len(arg) < 2 || (arg[0] != '-' && arg[0] != '+') {
		return false
	}
	for _, r := range arg[1:] {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') {
			return false
		}
	}
	return true
}

// isOperand reports whether arg, coming after prev, is a non-option argument. The
// value of -o or -O (also inside a cluster such as -euo) is not one.
func isOperand(prev, arg string) bool {
	if strings.HasPrefix(arg, "-") || strings.HasPrefix(arg, "+") {
		return false
	}
	return !isShortCluster(prev) || !strings.ContainsAny(prev, "oO")
}
