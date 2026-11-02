// Package parser is a hand-written recursive-descent parser for the Runefile
// declarative grammar, with a Pratt sub-parser (expr.go) for the expression
// sublanguage. It consumes the lexer's token stream and produces an *ast.File,
// emitting spanned diagnostics on any error (Principle II).
package parser

import (
	"strings"

	"github.com/rune-task-runner/rune/internal/ast"
	"github.com/rune-task-runner/rune/internal/diag"
	"github.com/rune-task-runner/rune/internal/lexer"
	"github.com/rune-task-runner/rune/internal/token"
)

// Parse lexes and parses src (named file) into an *ast.File plus all lexical and
// syntactic diagnostics.
func Parse(file, src string) (*ast.File, diag.List) {
	toks, diags := lexer.Lex(file, src)
	p := &parser{file: file, toks: toks, diags: diags}
	f := p.parseFile()
	return f, p.diags
}

// ParseExprFragment parses a standalone expression (e.g. the contents of a
// {{ ... }} interpolation). It returns the expression and any diagnostics.
// Surrounding whitespace is dropped first: {{ expr }} is the documented form,
// and the lexer reads a leading space as an indent.
func ParseExprFragment(file, src string) (ast.Expr, diag.List) {
	toks, diags := lexer.Lex(file, strings.TrimSpace(src))
	p := &parser{file: file, toks: toks, diags: diags}
	expr := p.parseExpr()
	if p.curKind() != token.NEWLINE && p.curKind() != token.EOF {
		p.codef(diag.CodeUnexpectedToken, p.cur().Span, "unexpected %s after expression", describe(p.cur()))
	}
	return expr, p.diags
}

type parser struct {
	file  string
	toks  []token.Token
	pos   int
	diags diag.List
}

// --- token helpers ---

func (p *parser) cur() token.Token    { return p.toks[p.pos] }
func (p *parser) curKind() token.Kind { return p.toks[p.pos].Kind }

func (p *parser) peek(n int) token.Token {
	i := p.pos + n
	if i >= len(p.toks) {
		return p.toks[len(p.toks)-1] // EOF
	}
	return p.toks[i]
}

func (p *parser) advance() token.Token {
	t := p.toks[p.pos]
	if p.pos < len(p.toks)-1 {
		p.pos++
	}
	return t
}

func (p *parser) accept(k token.Kind) (token.Token, bool) {
	if p.curKind() == k {
		return p.advance(), true
	}
	return p.cur(), false
}

func (p *parser) expect(k token.Kind, context string) (token.Token, bool) {
	if p.curKind() == k {
		return p.advance(), true
	}
	p.codef(diag.CodeUnexpectedToken, p.cur().Span, "expected %s%s, found %s", k, ctx(context), describe(p.cur()))
	return p.cur(), false
}

// codef emits a parse error carrying a stable diagnostic code (spec FR-010).
func (p *parser) codef(code string, span token.Span, format string, args ...any) {
	p.diags.Codef(code, span, format, args...)
}

func ctx(c string) string {
	if c == "" {
		return ""
	}
	return " " + c
}

func describe(t token.Token) string {
	switch t.Kind {
	case token.EOF:
		return "end of file"
	case token.NEWLINE:
		return "end of line"
	case token.IDENT, token.STRING, token.COMMENT, token.BODYTEXT:
		return t.String()
	default:
		return t.Kind.String()
	}
}

// recoverToNewline advances to (and past) the next NEWLINE for error recovery.
func (p *parser) recoverToNewline() {
	for {
		switch p.curKind() {
		case token.NEWLINE:
			p.advance()
			return
		case token.EOF:
			return
		default:
			p.advance()
		}
	}
}

// --- file / items ---

func (p *parser) parseFile() *ast.File {
	f := &ast.File{Path: p.file}
	if len(p.toks) > 0 {
		f.Sp = p.toks[0].Span
	}

	var pendingComments []token.Token

	for p.curKind() != token.EOF {
		switch p.curKind() {
		case token.NEWLINE:
			p.advance()
		case token.COMMENT:
			c := p.advance()
			if n := len(pendingComments); n > 0 && c.Span.Start.Line != pendingComments[n-1].Span.Start.Line+1 {
				pendingComments = pendingComments[:0]
			}
			pendingComments = append(pendingComments, c)
		default:
			doc := docFor(pendingComments, p.cur().Span.Start.Line)
			pendingComments = pendingComments[:0]
			p.parseItem(f, doc)
		}
	}
	return f
}

// docFor returns the joined doc comment if the comment run ends on the line
// immediately above itemLine, else "".
func docFor(comments []token.Token, itemLine int) string {
	if len(comments) == 0 {
		return ""
	}
	last := comments[len(comments)-1]
	if last.Span.Start.Line+1 != itemLine {
		return ""
	}
	parts := make([]string, len(comments))
	for i, c := range comments {
		parts[i] = c.Lit
	}
	return strings.Join(parts, "\n")
}

func (p *parser) parseItem(f *ast.File, doc string) {
	switch p.curKind() {
	case token.SET:
		if s := p.parseSetting(); s != nil {
			f.Settings = append(f.Settings, s)
		}
	case token.IMPORT:
		if im := p.parseImport(); im != nil {
			f.Imports = append(f.Imports, im)
		}
	case token.MOD:
		if m := p.parseMod(); m != nil {
			f.Mods = append(f.Mods, m)
		}
	case token.LBRACK:
		if t := p.parseTask(doc); t != nil {
			f.Tasks = append(f.Tasks, t)
		}
	case token.IDENT:
		if p.peek(1).Kind == token.ASSIGN {
			if a := p.parseAssignment(); a != nil {
				f.Assignments = append(f.Assignments, a)
			}
		} else {
			if t := p.parseTask(doc); t != nil {
				f.Tasks = append(f.Tasks, t)
			}
		}
	default:
		p.codef(diag.CodeUnexpectedToken, p.cur().Span, "unexpected %s at top level", describe(p.cur()))
		p.recoverToNewline()
	}
}

// --- settings / assignments ---

func (p *parser) parseSetting() *ast.Setting {
	set := p.advance() // SET
	name, ok := p.expect(token.IDENT, "after 'set'")
	if !ok {
		p.recoverToNewline()
		return nil
	}
	s := &ast.Setting{Name: name.Lit, Sp: set.Span.To(name.Span)}
	if _, ok := p.accept(token.ASSIGN); ok {
		if p.curKind() == token.LBRACK {
			s.List = p.parseExprList()
		} else {
			s.Value = p.parseExpr()
		}
	} else {
		s.Bool = true
	}
	p.expect(token.NEWLINE, "after setting")
	return s
}

func (p *parser) parseAssignment() *ast.Assignment {
	name := p.advance() // IDENT
	p.advance()         // ASSIGN
	a := &ast.Assignment{Name: name.Lit, Sp: name.Span}
	a.Expr = p.parseExpr()
	if a.Expr != nil {
		a.Sp = name.Span.To(a.Expr.Span())
	}
	p.expect(token.NEWLINE, "after assignment")
	return a
}

func (p *parser) parseImport() *ast.Import {
	kw := p.advance() // IMPORT
	im := &ast.Import{Sp: kw.Span}
	if _, ok := p.accept(token.QUESTION); ok {
		im.Optional = true
	}
	path, ok := p.expect(token.STRING, "import path")
	if !ok {
		p.recoverToNewline()
		return nil
	}
	im.Path = path.Lit
	im.Sp = kw.Span.To(path.Span)
	p.expect(token.NEWLINE, "after import")
	return im
}

func (p *parser) parseMod() *ast.Mod {
	kw := p.advance() // MOD
	name, ok := p.expect(token.IDENT, "module name")
	if !ok {
		p.recoverToNewline()
		return nil
	}
	m := &ast.Mod{Name: name.Lit, Sp: kw.Span.To(name.Span)}
	if path, ok := p.accept(token.STRING); ok {
		m.Path = path.Lit
		m.Sp = kw.Span.To(path.Span)
	}
	p.expect(token.NEWLINE, "after mod")
	return m
}
