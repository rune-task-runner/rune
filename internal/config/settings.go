package config

import (
	"fmt"
	"strings"

	"github.com/rune-task-runner/rune/internal/ast"
	"github.com/rune-task-runner/rune/internal/diag"
	"github.com/rune-task-runner/rune/internal/eval"
	"github.com/rune-task-runner/rune/internal/token"
)

// Settings holds the resolved module settings that influence execution.
type Settings struct {
	WorkingDir string   // set working-directory := "path"
	Quiet      bool     // set quiet
	Export     bool     // set export (export all variables to the task env)
	Fallback   bool     // set fallback
	Dotenv     string   // set dotenv := ".env"
	Shell      []string // set shell := ["bash", "-euo", "pipefail"]
	Python     []string // set python := ["python3"]
	Node       []string // set node := ["node"]
	AgentCmd   []string // set agent_cmd := ["claude", "-p"]
	Secrets    []string // set secrets := ["NAME", ...] — extra names whose values are masked
	Unmasked   []string // set unmasked := ["NAME", ...] — names exempt from built-in mask patterns
}

// ResolveSettings evaluates a file's settings into a Settings value. String and
// list settings are evaluated with ev; boolean settings use their bare form.
func ResolveSettings(f *ast.File, ev *eval.Evaluator) (Settings, diag.List) {
	var s Settings
	var diags diag.List

	evalStr := func(set *ast.Setting) string {
		if set.Value == nil {
			return ""
		}
		v, err := ev.Eval(set.Value)
		if err != nil {
			diags.Add(diag.New(err.Span, err.Msg))
			return ""
		}
		return v
	}
	// evalElems evaluates a list setting's elements (or its single value),
	// reporting each element to visit and recording any evaluation error. It is
	// the single eval/diagnostic loop behind both list projections below.
	evalElems := func(set *ast.Setting, visit func(v string, e ast.Expr)) {
		exprs := set.List
		if exprs == nil && set.Value != nil {
			exprs = []ast.Expr{set.Value}
		}
		for _, e := range exprs {
			v, err := ev.Eval(e)
			if err != nil {
				diags.Add(diag.New(err.Span, err.Msg))
				continue
			}
			visit(v, e)
		}
	}
	// evalList is the plain projection used by shell/python/node/agent_cmd — no
	// spans allocated, since those settings never need element locations.
	evalList := func(set *ast.Setting) []string {
		var out []string
		evalElems(set, func(v string, _ ast.Expr) { out = append(out, v) })
		return out
	}
	// evalNamed keeps each element's span so the secrets/unmasked conflict check
	// below can point at both offending elements (FR-009).
	type namedSpan struct {
		name string
		span token.Span
	}
	evalNamed := func(set *ast.Setting) []namedSpan {
		var out []namedSpan
		evalElems(set, func(v string, e ast.Expr) {
			out = append(out, namedSpan{name: v, span: e.Span()})
		})
		return out
	}
	names := func(entries []namedSpan) []string {
		if len(entries) == 0 {
			return nil
		}
		out := make([]string, len(entries))
		for i, e := range entries {
			out[i] = e.name
		}
		return out
	}
	var secretEntries, unmaskedEntries []namedSpan

	for _, set := range f.Settings {
		switch set.Name {
		case "working-directory":
			s.WorkingDir = evalStr(set)
		case "quiet":
			s.Quiet = true
		case "export":
			s.Export = true
		case "fallback":
			s.Fallback = true
		case "dotenv":
			s.Dotenv = evalStr(set)
		case "shell":
			s.Shell = evalList(set)
		case "python":
			s.Python = evalList(set)
		case "node":
			s.Node = evalList(set)
		case "agent_cmd", "agent_provider":
			s.AgentCmd = evalList(set)
		case "secrets":
			secretEntries = evalNamed(set)
			s.Secrets = names(secretEntries)
		case "unmasked":
			unmaskedEntries = evalNamed(set)
			s.Unmasked = names(unmaskedEntries)
		}
	}

	// Declaring a name secret AND exempting it is contradictory; fail before
	// any execution, citing both spans (contract §1). Matching is
	// case-insensitive, mirroring how the mask set resolves names.
	for _, u := range unmaskedEntries {
		for _, d := range secretEntries {
			if strings.EqualFold(u.name, d.name) {
				diags.Add(diag.New(u.span,
					fmt.Sprintf("%q is listed in both `set secrets` and `set unmasked`", u.name)).
					WithRelated(diag.RelatedLocation{Span: d.span, Message: "declared secret here"}))
			}
		}
	}
	return s, diags
}
