package agentinit

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"reflect"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/pelletier/go-toml/v2"
	"github.com/pelletier/go-toml/v2/unstable"
)

// .codex/config.toml is edited as text, like the JSON files. go-toml reads
// the meaning of the file, and its position-keeping parser says where each
// key and table header is, so a key is added or a value replaced at that
// place and every comment stays.

// tomlValue writes a value: a boolean, a string or a list of strings.
// Strings are basic strings, in double quotes.
func tomlValue(v any) string {
	switch v := v.(type) {
	case bool:
		if v {
			return "true"
		}
		return "false"
	case []string:
		parts := make([]string, len(v))
		for i, s := range v {
			parts[i] = tomlValue(s)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case string:
		var b strings.Builder
		b.WriteByte('"')
		for _, r := range v {
			switch {
			case r == '"' || r == '\\':
				b.WriteByte('\\')
				b.WriteRune(r)
			case r < 0x20 || r == 0x7f:
				fmt.Fprintf(&b, `\u%04X`, r)
			default:
				b.WriteRune(r)
			}
		}
		b.WriteByte('"')
		return b.String()
	}
	return fmt.Sprint(v)
}

// decodeTOML reads the meaning of a document. An error is a *ParseError.
func decodeTOML(file string, doc []byte) (map[string]any, error) {
	m := map[string]any{}
	if err := toml.Unmarshal(doc, &m); err != nil {
		line, col := 1, 1
		msg := err.Error()
		var decode *toml.DecodeError
		if errors.As(err, &decode) {
			line, col = decode.Position()
		}
		return nil, &ParseError{File: file, Line: line, Col: col, Msg: strings.TrimPrefix(msg, "toml: ")}
	}
	return m, nil
}

// tomlLayout is where a key and its table are in a document.
type tomlLayout struct {
	// keyStart and valueStart..valueEnd are the key's own line, when the
	// key is written as `key = value`; valueStart is -1 otherwise.
	valueStart, valueEnd int
	// tableEnd is the end of the last line that belongs to the table's
	// header section, or -1 when the table has no header.
	tableEnd int
	// blocker is the start of a construct that defines the table another
	// way (an inline table, dotted keys), or -1.
	blocker int
}

// layoutTOML walks the top-level expressions of doc and finds the key
// table.key.
func layoutTOML(doc []byte, table []string, key string) tomlLayout {
	target := append(slices.Clone(table), key)
	lay := tomlLayout{valueStart: -1, tableEnd: -1, blocker: -1}
	var current []string
	inArray := false

	p := unstable.Parser{}
	p.Reset(doc)
	for p.NextExpression() {
		e := p.Expression()
		var keys []string
		lastKeyEnd, firstKeyStart := 0, -1
		for it := e.Key(); it.Next(); {
			k := it.Node()
			keys = append(keys, string(k.Data))
			if firstKeyStart < 0 {
				firstKeyStart = int(k.Raw.Offset)
			}
			lastKeyEnd = int(k.Raw.Offset + k.Raw.Length)
		}
		switch e.Kind {
		case unstable.Table, unstable.ArrayTable:
			current, inArray = keys, e.Kind == unstable.ArrayTable
			if !inArray && slices.Equal(current, table) {
				lay.tableEnd = lastKeyEnd + bytes.IndexByte(doc[lastKeyEnd:], ']') + 1
			}
		case unstable.KeyValue:
			if inArray {
				continue
			}
			full := append(slices.Clone(current), keys...)
			end := int(e.Raw.Offset + e.Raw.Length)
			inTable := slices.Equal(current, table)
			switch {
			case slices.Equal(full, target):
				start := lastKeyEnd + bytes.IndexByte(doc[lastKeyEnd:], '=') + 1
				for start < end && (doc[start] == ' ' || doc[start] == '\t') {
					start++
				}
				lay.valueStart, lay.valueEnd = start, end
			case len(full) < len(target) && slices.Equal(full, target[:len(full)]),
				!inTable && len(full) > len(table) && slices.Equal(full[:len(table)], table):
				if lay.blocker < 0 {
					lay.blocker = firstKeyStart
				}
			}
			if inTable {
				lay.tableEnd = end
			}
		}
	}
	return lay
}

// tomlSet sets table.key in doc. state says what the document held, as for
// jsonSet. Another value is a conflict and doc is returned as it was, unless
// force is set. A document that defines the table in a form that cannot be
// added to gives a *ManualError.
func tomlSet(file string, doc []byte, table []string, key string, value any, force bool) (out []byte, state State, conflict *Conflict, err error) {
	before, err := decodeTOML(file, doc)
	if err != nil {
		return doc, StateDiffers, nil, err
	}
	target := append(slices.Clone(table), key)
	want := plain(value)
	found := lookupPath(before, target)
	if reflect.DeepEqual(found, want) {
		return doc, StatePresent, nil, nil
	}

	lay := layoutTOML(doc, table, key)
	nl := lineBreak(doc)
	header := "[" + strings.Join(table, ".") + "]"
	line := key + " = " + tomlValue(value)
	manual := func() error {
		l, c := 1, 1
		if lay.blocker >= 0 {
			l, c = position(doc, lay.blocker)
		}
		return &ManualError{File: file, Line: l, Col: c, Setting: dotted(target) + " = " + tomlValue(value)}
	}

	state = StateMissing
	switch {
	case found != nil && lay.valueStart < 0:
		// The key is set somewhere that is not a line of its own.
		return doc, StateDiffers, nil, manual()
	case found != nil:
		state = StateDiffers
		if !force {
			l, c := position(doc, lay.valueStart)
			raw := doc[lay.valueStart:lay.valueEnd]
			firstLine, _, _ := bytes.Cut(raw, []byte("\n"))
			return doc, state, &Conflict{
				File: file, Line: l, Col: c, Width: max(1, utf8.RuneCount(bytes.TrimRight(firstLine, "\r"))),
				Msg: fmt.Sprintf("%s is %s; rune agent init writes %s. Edit the file, or run again with --force",
					dotted(target), strings.Join(strings.Fields(string(raw)), " "), tomlValue(value)),
				Forceable: true,
			}, nil
		}
		out = applySplices(doc, splice{lay.valueStart, lay.valueEnd - lay.valueStart, tomlValue(value)})
	case lay.tableEnd >= 0:
		// After the last line of the table.
		at := len(doc)
		if i := bytes.IndexByte(doc[lay.tableEnd:], '\n'); i >= 0 {
			at = lay.tableEnd + i
			if at > 0 && doc[at-1] == '\r' {
				at--
			}
		}
		out = applySplices(doc, splice{at, 0, nl + line})
	default:
		var b bytes.Buffer
		b.Write(doc)
		if len(doc) > 0 && !bytes.HasSuffix(doc, []byte("\n")) {
			b.WriteString(nl)
		}
		if len(bytes.TrimSpace(doc)) > 0 {
			b.WriteString(nl)
		}
		b.WriteString(header + nl + line + nl)
		out = b.Bytes()
	}

	// The edited text must mean what the old one meant, with the one key
	// set. An edit that fails this is not written.
	after, err := decodeTOML(file, out)
	if err != nil {
		return doc, state, nil, manual()
	}
	setPath(before, target, want)
	if !sameValue(after, before) {
		return doc, state, nil, manual()
	}
	return out, state, nil, nil
}

// sameValue compares two decoded documents. It is reflect.DeepEqual, except
// that a float that is not a number equals itself: TOML has nan, and a file
// that holds one would otherwise never compare equal to its own text.
func sameValue(a, b any) bool {
	switch a := a.(type) {
	case map[string]any:
		b, ok := b.(map[string]any)
		if !ok || len(a) != len(b) {
			return false
		}
		for k, v := range a {
			if other, ok := b[k]; !ok || !sameValue(v, other) {
				return false
			}
		}
		return true
	case []any:
		b, ok := b.([]any)
		if !ok || len(a) != len(b) {
			return false
		}
		for i := range a {
			if !sameValue(a[i], b[i]) {
				return false
			}
		}
		return true
	case float64:
		b, ok := b.(float64)
		return ok && (a == b || (math.IsNaN(a) && math.IsNaN(b)))
	}
	return reflect.DeepEqual(a, b)
}
