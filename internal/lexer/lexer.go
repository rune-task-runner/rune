// Package lexer is a hand-written, state-function lexer (Pike style) for the
// Runefile language. It emits a flat token stream with byte/line/col spans on
// every token, including the layout tokens INDENT, DEDENT and NEWLINE that drive
// the significant-indentation grammar.
//
// Indentation model: the only indented construct is a task body. The lexer scans
// structural lines at column 0; the first more-indented line opens a body
// (INDENT), every following line that stays more indented than the header is
// captured as raw body text, and the first line that returns to the header's
// indentation closes the body (DEDENT). Body text is NOT tokenized — it is kept
// verbatim (including {{ ... }} interpolation) for the evaluator.
package lexer

import (
	"strings"

	"github.com/rune-task-runner/rune/internal/diag"
	"github.com/rune-task-runner/rune/internal/token"
)

// Lex tokenizes src (named file for diagnostics) and returns the token stream
// and any lexical diagnostics. The stream always ends with an EOF token.
func Lex(file, src string) ([]token.Token, diag.List) {
	l := &lexer{file: file, src: src, line: 1}
	l.run()
	return l.tokens, l.diags
}

type lexer struct {
	file string
	src  string

	pos       int // current byte offset
	line      int // current line (1-based)
	lineStart int // byte offset of the current line's first byte

	tokens []token.Token
	diags  diag.List

	// Body state. inBody is true while capturing a task body.
	inBody        bool
	bodyIndent    int  // indentation width of the body's header line (0 at top level)
	bodyWidth     int  // indentation width of the first body line
	bodyTab       bool // body indents with tabs
	bodySpace     bool // body indents with spaces
	pendingBlanks int  // blank lines seen inside a body, flushed when more body follows

	groupDepth int // nesting depth of () [] {} (suppresses NEWLINE)
}

func (l *lexer) run() {
	for l.pos < len(l.src) {
		l.lexLine()
	}
	if l.inBody {
		l.emit0(token.DEDENT, "")
		l.inBody = false
	}
	l.emit0(token.EOF, "")
}

// --- position helpers ---

func (l *lexer) pin() token.Position {
	return token.Position{Offset: l.pos, Line: l.line, Col: l.pos - l.lineStart + 1}
}

func (l *lexer) emit(kind token.Kind, lit string, start, end token.Position) {
	l.tokens = append(l.tokens, token.Token{
		Kind: kind,
		Lit:  lit,
		Span: token.Span{File: l.file, Start: start, End: end},
	})
}

// emit0 emits a zero-width token at the current position.
func (l *lexer) emit0(kind token.Kind, lit string) {
	p := l.pin()
	l.emit(kind, lit, p, p)
}

func (l *lexer) at(off int) byte {
	if off < len(l.src) {
		return l.src[off]
	}
	return 0
}

// advance consumes one byte, maintaining line/column bookkeeping.
func (l *lexer) advance() byte {
	c := l.src[l.pos]
	l.pos++
	if c == '\n' {
		l.line++
		l.lineStart = l.pos
	}
	return c
}

// --- line framing ---

// lexLine processes one physical line: indentation handling, then either body
// capture or structural token scanning.
func (l *lexer) lexLine() {
	width, raw, hasTab, hasSpace := l.scanIndent()

	// Blank line (only whitespace before newline/EOF). Use a real end-of-input
	// check, not byte==0: an embedded NUL is not EOF and must not be treated as a
	// blank line, or the run() loop would spin without consuming it.
	if c := l.at(l.pos); c == '\n' || l.pos >= len(l.src) {
		if c == '\n' {
			l.advance()
		}
		if l.inBody {
			l.pendingBlanks++
		}
		return
	}

	if l.inBody {
		if width > l.bodyIndent {
			l.flushPendingBlanks()
			l.lexBodyLine(width, raw, hasTab, hasSpace)
			return
		}
		// Dedent back to / below the header: the body ends here.
		l.pendingBlanks = 0
		l.emit0(token.DEDENT, "")
		l.inBody = false
		// fall through and reprocess this line structurally
	}

	if width > 0 {
		// More indented than the surrounding structural level: open a body.
		l.emit0(token.INDENT, "")
		l.inBody = true
		l.bodyIndent = 0
		l.bodyWidth = width
		l.bodyTab = hasTab
		l.bodySpace = hasSpace
		if hasTab && hasSpace {
			l.diags.Codef(diag.CodeInvalidIndent, l.wsSpan(width), "inconsistent indentation: mixed tabs and spaces")
		}
		l.flushPendingBlanks()
		l.lexBodyLine(width, raw, hasTab, hasSpace)
		return
	}

	l.lexStructural()
}

// scanIndent consumes leading spaces/tabs at the start of a line and reports the
// indentation width (character count) plus which whitespace kinds appeared.
func (l *lexer) scanIndent() (width int, raw string, hasTab, hasSpace bool) {
	start := l.pos
	for {
		switch l.at(l.pos) {
		case ' ':
			hasSpace = true
			l.advance()
		case '\t':
			hasTab = true
			l.advance()
		default:
			return l.pos - start, l.src[start:l.pos], hasTab, hasSpace
		}
	}
}

// wsSpan builds a span covering the leading whitespace of the current line.
func (l *lexer) wsSpan(width int) token.Span {
	start := token.Position{Offset: l.lineStart, Line: l.line, Col: 1}
	end := token.Position{Offset: l.lineStart + width, Line: l.line, Col: width + 1}
	return token.Span{File: l.file, Start: start, End: end}
}

func (l *lexer) flushPendingBlanks() {
	for i := 0; i < l.pendingBlanks; i++ {
		l.emit0(token.BODYTEXT, "")
		l.emit0(token.NEWLINE, "")
	}
	l.pendingBlanks = 0
}

// --- body lines ---

// lexBodyLine captures one body line: it checks indentation consistency, strips
// the body's base indentation, peels leading @/- sigils, and emits BODYTEXT.
func (l *lexer) lexBodyLine(width int, raw string, hasTab, hasSpace bool) {
	if hasTab && hasSpace {
		l.diags.Codef(diag.CodeInvalidIndent, l.wsSpan(width), "inconsistent indentation: mixed tabs and spaces")
	} else if (hasTab && l.bodySpace && !l.bodyTab) || (hasSpace && l.bodyTab && !l.bodySpace) {
		l.diags.Codef(diag.CodeInvalidIndent, l.wsSpan(width), "inconsistent indentation: body uses %s but this line uses %s",
			indentKind(l.bodyTab, l.bodySpace), indentKind(hasTab, hasSpace))
	}
	if width < l.bodyWidth {
		l.diags.Codef(diag.CodeInvalidIndent, l.wsSpan(width), "inconsistent indentation in task body")
	}

	// Reconstruct text with the body's base indentation stripped (extra
	// indentation on more-nested lines is preserved as part of the text).
	var extra string
	if width >= l.bodyWidth && len(raw) >= l.bodyWidth {
		extra = raw[l.bodyWidth:]
	}

	// Read the rest of the physical line.
	contentStart := l.pin()
	restStart := l.pos
	for l.at(l.pos) != '\n' && l.at(l.pos) != 0 {
		l.advance()
	}
	rest := l.src[restStart:l.pos]
	end := l.pin()
	// Tolerate CRLF line endings: drop the trailing carriage return so body text
	// is identical whether the Runefile was authored with LF or CRLF.
	if strings.HasSuffix(rest, "\r") {
		rest = rest[:len(rest)-1]
		end = token.Position{Offset: end.Offset - 1, Line: end.Line, Col: end.Col - 1}
	}
	if l.at(l.pos) == '\n' {
		l.advance()
	}

	text := extra + rest

	// Peel leading @/- sigils (only when at base indentation, i.e. no extra ws).
	// Each may appear at most once, in either order.
	i := 0
	if extra == "" {
		seenAt, seenDash := false, false
		for i < len(text) {
			if text[i] == '@' && !seenAt {
				seenAt = true
				i++
			} else if text[i] == '-' && !seenDash {
				seenDash = true
				i++
			} else {
				break
			}
		}
	}

	// Emit sigil tokens with their source positions, then the body text.
	for j := 0; j < i; j++ {
		p := token.Position{Offset: contentStart.Offset + j, Line: contentStart.Line, Col: contentStart.Col + j}
		pe := token.Position{Offset: p.Offset + 1, Line: p.Line, Col: p.Col + 1}
		if text[j] == '@' {
			l.emit(token.AT, "", p, pe)
		} else {
			l.emit(token.DASH, "", p, pe)
		}
	}

	bodyText := text[i:]
	textStart := token.Position{Offset: contentStart.Offset + i, Line: contentStart.Line, Col: contentStart.Col + i}
	l.emit(token.BODYTEXT, bodyText, textStart, end)
	l.emit(token.NEWLINE, "", end, end)
}

func indentKind(tab, space bool) string {
	switch {
	case tab && space:
		return "mixed tabs and spaces"
	case tab:
		return "tabs"
	case space:
		return "spaces"
	default:
		return "no indentation"
	}
}

// --- structural tokens ---

// lexStructural scans tokens on a structural line until a NEWLINE at group depth
// zero (group continuations span physical lines transparently).
func (l *lexer) lexStructural() {
	for {
		// True end of input (not an embedded NUL, which is handled as an illegal
		// character below so the scanner always makes progress).
		if l.pos >= len(l.src) {
			l.emit0(token.NEWLINE, "")
			return
		}
		c := l.at(l.pos)
		switch {
		case c == '\n':
			if l.groupDepth > 0 {
				l.advance()
				l.skipInlineSpace()
				continue
			}
			start := l.pin()
			l.advance()
			l.emit(token.NEWLINE, "", start, start)
			return
		case c == ' ' || c == '\t' || c == '\r':
			l.advance()
		case c == '#':
			l.lexComment()
		case c == '"' || c == '\'':
			l.lexString()
		case isNameStart(c):
			l.lexName()
		default:
			l.lexOperator()
		}
	}
}

func (l *lexer) skipInlineSpace() {
	for {
		switch l.at(l.pos) {
		case ' ', '\t', '\r':
			l.advance()
		default:
			return
		}
	}
}

func (l *lexer) lexComment() {
	start := l.pin()
	l.advance() // '#'
	textStart := l.pos
	for l.at(l.pos) != '\n' && l.at(l.pos) != 0 {
		l.advance()
	}
	text := strings.TrimSpace(l.src[textStart:l.pos])
	l.emit(token.COMMENT, text, start, l.pin())
}

func (l *lexer) lexName() {
	start := l.pin()
	begin := l.pos
	for isNameContinue(l.at(l.pos)) {
		l.advance()
	}
	lit := l.src[begin:l.pos]
	l.emit(token.Lookup(lit), lit, start, l.pin())
}

func (l *lexer) lexOperator() {
	start := l.pin()
	c := l.at(l.pos)
	two := func(k token.Kind) {
		l.advance()
		l.advance()
		l.emit(k, "", start, l.pin())
	}
	one := func(k token.Kind) {
		l.advance()
		l.emit(k, "", start, l.pin())
	}
	switch c {
	case ':':
		switch l.at(l.pos + 1) {
		case '=':
			two(token.ASSIGN)
		case ':':
			two(token.COLONCOLON)
		default:
			one(token.COLON)
		}
	case '&':
		if l.at(l.pos+1) == '&' {
			two(token.AMPAMP)
		} else {
			l.illegal(start)
		}
	case '|':
		if l.at(l.pos+1) == '|' {
			two(token.PIPEPIPE)
		} else {
			l.illegal(start)
		}
	case '=':
		switch l.at(l.pos + 1) {
		case '=':
			two(token.EQ)
		case '~':
			two(token.MATCH)
		default:
			one(token.EQUALS)
		}
	case '!':
		if l.at(l.pos+1) == '=' {
			two(token.NEQ)
		} else {
			l.illegal(start)
		}
	case '+':
		one(token.PLUS)
	case '*':
		one(token.STAR)
	case '/':
		one(token.SLASH)
	case '@':
		one(token.AT)
	case '-':
		one(token.DASH)
	case ',':
		one(token.COMMA)
	case '?':
		one(token.QUESTION)
	case '(':
		l.groupDepth++
		one(token.LPAREN)
	case ')':
		if l.groupDepth > 0 {
			l.groupDepth--
		}
		one(token.RPAREN)
	case '[':
		l.groupDepth++
		one(token.LBRACK)
	case ']':
		if l.groupDepth > 0 {
			l.groupDepth--
		}
		one(token.RBRACK)
	case '{':
		l.groupDepth++
		one(token.LBRACE)
	case '}':
		if l.groupDepth > 0 {
			l.groupDepth--
		}
		one(token.RBRACE)
	default:
		l.illegal(start)
	}
}

func (l *lexer) illegal(start token.Position) {
	ch := l.src[l.pos : l.pos+1]
	l.advance()
	l.emit(token.ILLEGAL, ch, start, l.pin())
	l.diags.Codef(diag.CodeUnexpectedToken, token.Span{File: l.file, Start: start, End: l.pin()}, "unexpected character %q", ch)
}

func isNameStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isNameContinue(c byte) bool {
	return isNameStart(c) || (c >= '0' && c <= '9') || c == '-'
}
