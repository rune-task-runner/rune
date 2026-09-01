package parser

import (
	"github.com/rune-task-runner/rune/internal/ast"
	"github.com/rune-task-runner/rune/internal/diag"
	"github.com/rune-task-runner/rune/internal/token"
)

// parseAttributeLine parses one "[ item, item, ... ]" line of attributes and
// consumes the trailing NEWLINE.
func (p *parser) parseAttributeLine() []*ast.Attribute {
	p.advance() // consume LBRACK
	var attrs []*ast.Attribute
	if p.curKind() != token.RBRACK {
		if a := p.parseAttrItem(); a != nil {
			attrs = append(attrs, a)
		}
		for p.curKind() == token.COMMA {
			p.advance()
			if a := p.parseAttrItem(); a != nil {
				attrs = append(attrs, a)
			}
		}
	}
	p.expect(token.RBRACK, "to close attribute list")
	p.expect(token.NEWLINE, "after attribute list")
	return attrs
}

func (p *parser) parseAttrItem() *ast.Attribute {
	name, ok := p.expect(token.IDENT, "attribute name")
	if !ok {
		return nil
	}
	a := &ast.Attribute{Kind: name.Lit, Sp: name.Span}

	switch name.Lit {
	case ast.AttrPrivate, ast.AttrParallel, ast.AttrLinux, ast.AttrMacos,
		ast.AttrWindows, ast.AttrUnix, ast.AttrNoCD, ast.AttrNetwork,
		ast.AttrNoExitMessage, ast.AttrContext:
		// No arguments (confirm may also be bare).
		return a

	case ast.AttrConfirm:
		if p.curKind() == token.LPAREN {
			p.advance()
			if s, ok := p.accept(token.STRING); ok {
				a.Str = s.Lit
			}
			rp, _ := p.expect(token.RPAREN, "to close confirm(...)")
			a.Sp = name.Span.To(rp.Span)
		}
		return a

	case ast.AttrGroup, ast.AttrDoc, ast.AttrScript, ast.AttrWorkingDirectory, ast.AttrReturns:
		a.Str = p.parseSingleStringArg(name.Lit)
		return a

	case ast.AttrEnv, ast.AttrParamDoc:
		p.expect(token.LPAREN, "to open "+name.Lit+"(...)")
		if s, ok := p.accept(token.STRING); ok {
			a.Str = s.Lit
		}
		p.expect(token.COMMA, "between "+name.Lit+" arguments")
		if s, ok := p.accept(token.STRING); ok {
			a.Str2 = s.Lit
		}
		rp, _ := p.expect(token.RPAREN, "to close "+name.Lit+"(...)")
		a.Sp = name.Span.To(rp.Span)
		return a

	case ast.AttrCache:
		p.parseCacheArgs(a)
		return a

	default:
		// Unknown attribute: parse and discard any parenthesized payload so the
		// analyzer can report it without a parse cascade.
		if p.curKind() == token.LPAREN {
			p.skipBalancedParens()
		}
		p.codef(diag.CodeInvalidAttribute, name.Span, "unknown attribute %q", name.Lit)
		return a
	}
}

func (p *parser) parseSingleStringArg(attr string) string {
	if _, ok := p.expect(token.LPAREN, "to open "+attr+"(...)"); !ok {
		return ""
	}
	var val string
	if s, ok := p.accept(token.STRING); ok {
		val = s.Lit
	} else {
		p.codef(diag.CodeInvalidAttribute, p.cur().Span, "%s(...) requires a string argument", attr)
	}
	p.expect(token.RPAREN, "to close "+attr+"(...)")
	return val
}

func (p *parser) parseCacheArgs(a *ast.Attribute) {
	if _, ok := p.expect(token.LPAREN, "to open cache(...)"); !ok {
		return
	}
	for p.curKind() != token.RPAREN && p.curKind() != token.NEWLINE && p.curKind() != token.EOF {
		key, ok := p.expect(token.IDENT, "cache argument name")
		if !ok {
			break
		}
		p.expect(token.EQUALS, "after cache argument name")
		list := p.parseExprList()
		switch key.Lit {
		case "inputs":
			a.Inputs = list
		case "outputs":
			a.Outputs = list
			a.HasOutputs = true
		default:
			p.codef(diag.CodeInvalidAttribute, key.Span, "unknown cache argument %q (expected inputs/outputs)", key.Lit)
		}
		if _, ok := p.accept(token.COMMA); !ok {
			break
		}
	}
	rp, _ := p.expect(token.RPAREN, "to close cache(...)")
	a.Sp = a.Sp.To(rp.Span)
}

// skipBalancedParens consumes a balanced (...) group for error recovery.
func (p *parser) skipBalancedParens() {
	if p.curKind() != token.LPAREN {
		return
	}
	depth := 0
	for {
		switch p.curKind() {
		case token.LPAREN:
			depth++
			p.advance()
		case token.RPAREN:
			depth--
			p.advance()
			if depth == 0 {
				return
			}
		case token.NEWLINE, token.EOF:
			return
		default:
			p.advance()
		}
	}
}
