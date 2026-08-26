package token

import (
	"fmt"
	"strconv"
)

// Kind enumerates the lexical token kinds produced by the lexer.
type Kind int

const (
	ILLEGAL Kind = iota // an unrecognized rune (carries a diagnostic)
	EOF                 // end of input

	// Layout (significant indentation).
	NEWLINE // end of a logical line
	INDENT  // start of a more-indented block (a task body)
	DEDENT  // end of an indented block

	COMMENT // # ... (declaration context only; bodies are raw text)
	IDENT   // a name: letter { letter | digit | _ | - }
	STRING  // a string literal; Lit holds the decoded value

	// Keywords.
	SET
	IMPORT
	MOD
	IF
	ELSE

	// Punctuation & operators.
	ASSIGN     // :=
	COLON      // :
	COLONCOLON // :: (namespace separator)
	COMMA      // ,
	AT         // @  (body: suppress echo)
	DASH       // -  (body: continue on error)
	PLUS       // +  (concat / variadic-plus)
	STAR       // *  (variadic-star)
	SLASH      // /  (path join)
	AMPAMP     // &&
	PIPEPIPE   // ||
	EQ         // ==
	NEQ        // !=
	MATCH      // =~
	EQUALS     // =  (param default, cache(inputs=...))
	LPAREN     // (
	RPAREN     // )
	LBRACK     // [
	RBRACK     // ]
	LBRACE     // {
	RBRACE     // }
	QUESTION   // ?  (import? optional-import marker)

	BODYTEXT // raw text of a task body line (may contain {{ ... }})
)

var kindNames = map[Kind]string{
	ILLEGAL:    "ILLEGAL",
	EOF:        "EOF",
	NEWLINE:    "NEWLINE",
	INDENT:     "INDENT",
	DEDENT:     "DEDENT",
	COMMENT:    "COMMENT",
	IDENT:      "IDENT",
	STRING:     "STRING",
	SET:        "SET",
	IMPORT:     "IMPORT",
	MOD:        "MOD",
	IF:         "IF",
	ELSE:       "ELSE",
	ASSIGN:     "ASSIGN",
	COLON:      "COLON",
	COLONCOLON: "COLONCOLON",
	COMMA:      "COMMA",
	AT:         "AT",
	DASH:       "DASH",
	PLUS:       "PLUS",
	STAR:       "STAR",
	SLASH:      "SLASH",
	AMPAMP:     "AMPAMP",
	PIPEPIPE:   "PIPEPIPE",
	EQ:         "EQ",
	NEQ:        "NEQ",
	MATCH:      "MATCH",
	EQUALS:     "EQUALS",
	LPAREN:     "LPAREN",
	RPAREN:     "RPAREN",
	LBRACK:     "LBRACK",
	RBRACK:     "RBRACK",
	LBRACE:     "LBRACE",
	RBRACE:     "RBRACE",
	QUESTION:   "QUESTION",
	BODYTEXT:   "BODYTEXT",
}

// String returns the canonical name of the kind (used in golden token streams).
func (k Kind) String() string {
	if n, ok := kindNames[k]; ok {
		return n
	}
	return fmt.Sprintf("Kind(%d)", int(k))
}

var keywords = map[string]Kind{
	"set":    SET,
	"import": IMPORT,
	"mod":    MOD,
	"if":     IF,
	"else":   ELSE,
}

// Lookup maps an identifier to its keyword kind, or IDENT if it is not a
// reserved word.
func Lookup(ident string) Kind {
	if k, ok := keywords[ident]; ok {
		return k
	}
	return IDENT
}

// Token is a single lexical token with its decoded literal and source span.
type Token struct {
	Kind Kind
	Lit  string // decoded literal text where meaningful (IDENT, STRING, COMMENT, BODYTEXT)
	Span Span
}

// String renders the token for golden token streams, e.g. IDENT("greet") or
// ASSIGN. Layout tokens render bare.
func (t Token) String() string {
	switch t.Kind {
	case IDENT, STRING, COMMENT, BODYTEXT, ILLEGAL:
		return fmt.Sprintf("%s(%s)", t.Kind, strconv.Quote(t.Lit))
	default:
		return t.Kind.String()
	}
}
