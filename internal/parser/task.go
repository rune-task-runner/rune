package parser

import (
	"strings"

	"github.com/rune-task-runner/rune/internal/ast"
	"github.com/rune-task-runner/rune/internal/diag"
	"github.com/rune-task-runner/rune/internal/token"
)

// parseTask parses optional attribute lines, the signature (name, params,
// executor), deps, post-hooks, and the indented body.
func (p *parser) parseTask(doc string) *ast.Task {
	t := &ast.Task{Doc: doc}

	for p.curKind() == token.LBRACK {
		t.Attributes = append(t.Attributes, p.parseAttributeLine()...)
	}

	name, ok := p.expect(token.IDENT, "task name")
	if !ok {
		p.recoverToNewline()
		return nil
	}
	t.Name = name.Lit
	t.Sp = name.Span

	// [doc("...")] overrides a comment doc.
	for _, a := range t.Attributes {
		if a.Kind == ast.AttrDoc && a.Str != "" {
			t.Doc = a.Str
		}
	}

	// Parameters.
	for {
		switch p.curKind() {
		case token.IDENT:
			t.Params = append(t.Params, p.parseParam())
		case token.PLUS, token.STAR:
			t.Params = append(t.Params, p.parseParam())
		default:
			goto execclause
		}
	}

execclause:
	// Optional (executor).
	if _, ok := p.accept(token.LPAREN); ok {
		ex, ok := p.expect(token.IDENT, "executor name")
		if ok {
			t.Executor = ex.Lit
		}
		p.expect(token.RPAREN, "after executor")
	}

	// A legacy unspaced header like `deploy env:build` had exactly one colon,
	// now consumed as a type annotation — so the header colon is missing. Fail
	// with the exact fix (constitution v1.1.0 re-parse exception condition 3)
	// instead of a bare "expected COLON". Any annotated param qualifies, not
	// just the last: `deploy env:build test` re-parses `test` as a second
	// parameter after the annotation, and that header deserves the same hint.
	if p.curKind() != token.COLON {
		for i := len(t.Params) - 1; i >= 0; i-- {
			c := t.Params[i].Constraint
			if c == nil {
				continue
			}
			msg := "missing ':' after task signature — %q parsed as a type annotation on parameter %q"
			if _, known := ast.ConstraintKindFromName(c.KindName); !known {
				msg += " (supported types: " + strings.Join(ast.SupportedConstraintKinds, ", ") + ")"
			}
			msg += "; if %q is a dependency, add a space: \"%s: %s\""
			p.codef(diag.CodeMalformedTaskDecl, c.Sp, msg,
				t.Params[i].Name+c.String(), t.Params[i].Name,
				c.KindName, t.Params[i].Name, c.KindName)
			p.recoverToNewline()
			return t
		}
	}
	if _, ok := p.expect(token.COLON, "after task signature"); !ok {
		p.recoverToNewline()
		return t
	}

	// Dependencies (run before).
	for p.atDepStart() {
		if d := p.parseDepCall(); d != nil {
			t.Deps = append(t.Deps, d)
		}
	}

	// Post-hooks (&& run after, on success).
	if _, ok := p.accept(token.AMPAMP); ok {
		for p.atDepStart() {
			if d := p.parseDepCall(); d != nil {
				t.PostHooks = append(t.PostHooks, d)
			}
		}
	}

	// Failure hooks (|| run after, on failure). Clause order is fixed:
	// deps, then &&, then || — a && after || falls through to the NEWLINE
	// expectation below and is a parse error.
	if _, ok := p.accept(token.PIPEPIPE); ok {
		for p.atDepStart() {
			if d := p.parseDepCall(); d != nil {
				t.FailHooks = append(t.FailHooks, d)
			}
		}
	}

	p.expect(token.NEWLINE, "after task header")

	// Body.
	if _, ok := p.accept(token.INDENT); ok {
		for p.curKind() != token.DEDENT && p.curKind() != token.EOF {
			if bl := p.parseBodyLine(); bl != nil {
				t.Body = append(t.Body, bl)
			}
		}
		p.expect(token.DEDENT, "to close task body")
	}
	if len(t.Body) > 0 {
		t.Sp = name.Span.To(t.Body[len(t.Body)-1].Sp)
	}
	return t
}

func (p *parser) parseParam() *ast.Param {
	switch p.curKind() {
	case token.PLUS:
		plus := p.advance()
		name, _ := p.expect(token.IDENT, "after '+'")
		par := &ast.Param{Name: name.Lit, Kind: ast.ParamVariadicPlus, Sp: plus.Span.To(name.Span)}
		p.parseTypeAnno(par, name.Span)
		return par
	case token.STAR:
		star := p.advance()
		name, _ := p.expect(token.IDENT, "after '*'")
		par := &ast.Param{Name: name.Lit, Kind: ast.ParamVariadicStar, Sp: star.Span.To(name.Span)}
		p.parseTypeAnno(par, name.Span)
		return par
	default:
		name := p.advance() // IDENT
		par := &ast.Param{Name: name.Lit, Kind: ast.ParamRequired, Sp: name.Span}
		p.parseTypeAnno(par, name.Span)
		if _, ok := p.accept(token.EQUALS); ok {
			par.Kind = ast.ParamDefaulted
			par.Default = p.parseExpr()
			if par.Default != nil {
				par.Sp = par.Sp.To(par.Default.Span())
			}
		}
		return par
	}
}

// parseTypeAnno parses an inline `:kind` / `:enum("a","b")` type annotation
// (spec 023). The colon must be offset-adjacent to both the parameter name and
// the kind ident — the lexer discards whitespace, so adjacency is recovered
// from span offsets. A spaced colon is always the task-header colon, keeping
// every pre-023 spaced form byte-compatible (SC-003). Kind-name validity is
// the analyzer's job (RUNE2012); syntax errors here are RUNE1006.
func (p *parser) parseTypeAnno(par *ast.Param, nameSpan token.Span) {
	if p.curKind() != token.COLON || p.peek(1).Kind != token.IDENT {
		return
	}
	colon := p.cur()
	if colon.Span.Start.Offset != nameSpan.End.Offset ||
		p.peek(1).Span.Start.Offset != colon.Span.End.Offset {
		return
	}
	p.advance() // ':'
	kindTok := p.advance()
	kind, knownKind := ast.ConstraintKindFromName(kindTok.Lit)
	c := &ast.Constraint{Kind: kind, KindName: kindTok.Lit, Sp: colon.Span.To(kindTok.Span)}

	// A value list must be adjacent too: a spaced "(" after the parameter
	// list is the executor clause.
	if p.curKind() == token.LPAREN && p.cur().Span.Start.Offset == kindTok.Span.End.Offset {
		p.advance() // '('
		for p.curKind() != token.RPAREN && p.curKind() != token.NEWLINE && p.curKind() != token.EOF {
			s, ok := p.accept(token.STRING)
			if !ok {
				p.codef(diag.CodeMalformedConstraint, p.cur().Span,
					"enum values must be string literals, found %s", describe(p.cur()))
				for p.curKind() != token.RPAREN && p.curKind() != token.NEWLINE && p.curKind() != token.EOF {
					p.advance()
				}
				break
			}
			c.Values = append(c.Values, s.Lit)
			if _, ok := p.accept(token.COMMA); !ok {
				break
			}
		}
		if rp, ok := p.expect(token.RPAREN, "to close the value list"); ok {
			c.Sp = c.Sp.To(rp.Span)
		}
		switch {
		// An unknown kind with a value list is left to the analyzer: RUNE2012
		// ("unknown parameter type") is the useful message for a typo like
		// enun("a"), not a complaint about its value list.
		case kindTok.Lit != "enum" && knownKind:
			p.codef(diag.CodeMalformedConstraint, c.Sp,
				"a value list is only valid for enum, not %q", kindTok.Lit)
		case kindTok.Lit == "enum" && len(c.Values) == 0:
			p.codef(diag.CodeMalformedConstraint, c.Sp, "enum requires at least one value")
		}
	} else if kindTok.Lit == "enum" {
		p.codef(diag.CodeMalformedConstraint, c.Sp,
			`enum requires a value list, e.g. env:enum("staging","prod")`)
	}
	par.Constraint = c
	par.Sp = par.Sp.To(c.Sp)
}

// atDepStart reports whether the cursor is at the start of a dependency call.
func (p *parser) atDepStart() bool {
	switch p.curKind() {
	case token.IDENT, token.LPAREN:
		return true
	default:
		return false
	}
}

func (p *parser) parseDepCall() *ast.DepCall {
	// Parenthesized form passes arguments: ( name args... ).
	if lp, ok := p.accept(token.LPAREN); ok {
		name, span := p.parseQualifiedName()
		d := &ast.DepCall{Name: name, Sp: lp.Span.To(span)}
		for p.curKind() != token.RPAREN && p.curKind() != token.NEWLINE && p.curKind() != token.EOF {
			d.Args = append(d.Args, p.parseExpr())
		}
		rp, _ := p.expect(token.RPAREN, "to close dependency call")
		d.Sp = lp.Span.To(rp.Span)
		return d
	}
	name, span := p.parseQualifiedName()
	return &ast.DepCall{Name: name, Sp: span}
}

// parseQualifiedName parses an optionally namespaced name (a::b::c).
func (p *parser) parseQualifiedName() (string, token.Span) {
	first, ok := p.expect(token.IDENT, "name")
	if !ok {
		return "", first.Span
	}
	name := first.Lit
	span := first.Span
	for p.curKind() == token.COLONCOLON {
		p.advance()
		next, ok := p.expect(token.IDENT, "after '::'")
		if !ok {
			break
		}
		name += "::" + next.Lit
		span = span.To(next.Span)
	}
	return name, span
}

func (p *parser) parseBodyLine() *ast.BodyLine {
	bl := &ast.BodyLine{}
	for {
		switch p.curKind() {
		case token.AT:
			bl.NoEcho = true
			p.advance()
			continue
		case token.DASH:
			bl.ContinueOnError = true
			p.advance()
			continue
		}
		break
	}
	bt, ok := p.expect(token.BODYTEXT, "in task body")
	if !ok {
		p.recoverToNewline()
		return nil
	}
	bl.Raw = bt.Lit
	bl.Sp = bt.Span
	p.expect(token.NEWLINE, "after body line")
	return bl
}
