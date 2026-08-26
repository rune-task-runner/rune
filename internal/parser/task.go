package parser

import (
	"github.com/rune-task-runner/rune/internal/ast"
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
		return &ast.Param{Name: name.Lit, Kind: ast.ParamVariadicPlus, Sp: plus.Span.To(name.Span)}
	case token.STAR:
		star := p.advance()
		name, _ := p.expect(token.IDENT, "after '*'")
		return &ast.Param{Name: name.Lit, Kind: ast.ParamVariadicStar, Sp: star.Span.To(name.Span)}
	default:
		name := p.advance() // IDENT
		par := &ast.Param{Name: name.Lit, Kind: ast.ParamRequired, Sp: name.Span}
		if _, ok := p.accept(token.EQUALS); ok {
			par.Kind = ast.ParamDefaulted
			par.Default = p.parseExpr()
			if par.Default != nil {
				par.Sp = name.Span.To(par.Default.Span())
			}
		}
		return par
	}
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
