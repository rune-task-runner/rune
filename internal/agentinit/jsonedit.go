package agentinit

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"unicode/utf8"
)

// The client files in JSON are edited as text. parseJSON reads a document
// into a tree whose nodes know their byte range, and jsonSet and jsonAppend
// splice new text in at those positions. Nothing is ever decoded and encoded
// again, which would lose comments, key order and layout.
//
// The reader accepts what editors accept in configuration files beyond
// JSON: // and /* */ comments, and a comma after the last member.

type jsonKind int

const (
	jsonObjectKind jsonKind = iota
	jsonListKind
	jsonScalarKind
)

// jsonNode is one value of a document.
type jsonNode struct {
	kind       jsonKind
	start, end int // the bytes of the value: doc[start:end]
	members    []jsonMember
	elems      []*jsonNode
	value      any // a scalar: string, float64, bool or nil
	// lastComma is the offset of a comma after the last member or element,
	// or -1 when there is none.
	lastComma int
}

type jsonMember struct {
	key      string
	keyStart int
	value    *jsonNode
}

// member returns the value of key. The last one wins when a key is written
// twice, as it does for the clients' own readers.
func (n *jsonNode) member(key string) *jsonNode {
	for _, m := range slices.Backward(n.members) {
		if m.key == key {
			return m.value
		}
	}
	return nil
}

// jsonObject is an object to render, with its keys in order.
type jsonObject []jsonField

type jsonField struct {
	key   string
	value any
}

// maxJSONDepth is the nesting encoding/json accepts.
const maxJSONDepth = 10000

type jsonParser struct {
	file string
	doc  []byte
	pos  int
}

// parseJSON reads doc. A document that is empty or only white space is an
// empty object. An error is a *ParseError with the position.
func parseJSON(file string, doc []byte) (*jsonNode, error) {
	p := &jsonParser{file: file, doc: doc}
	if err := p.skip(); err != nil {
		return nil, err
	}
	if p.pos == len(doc) {
		return &jsonNode{kind: jsonObjectKind, lastComma: -1}, nil
	}
	root, err := p.value(0)
	if err != nil {
		return nil, err
	}
	if err := p.skip(); err != nil {
		return nil, err
	}
	if p.pos != len(doc) {
		return nil, p.errorf(p.pos, "unexpected text after the value")
	}
	return root, nil
}

func (p *jsonParser) errorf(off int, format string, args ...any) error {
	line, col := position(p.doc, off)
	return &ParseError{File: p.file, Line: line, Col: col, Msg: fmt.Sprintf(format, args...)}
}

// skip moves past white space and comments.
func (p *jsonParser) skip() error {
	for p.pos < len(p.doc) {
		switch c := p.doc[p.pos]; {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			p.pos++
		case c == '/' && p.pos+1 < len(p.doc) && p.doc[p.pos+1] == '/':
			end := bytes.IndexByte(p.doc[p.pos:], '\n')
			if end < 0 {
				p.pos = len(p.doc)
			} else {
				p.pos += end
			}
		case c == '/' && p.pos+1 < len(p.doc) && p.doc[p.pos+1] == '*':
			end := bytes.Index(p.doc[p.pos+2:], []byte("*/"))
			if end < 0 {
				return p.errorf(p.pos, "comment is not closed")
			}
			p.pos += end + 4
		default:
			return nil
		}
	}
	return nil
}

func (p *jsonParser) value(depth int) (*jsonNode, error) {
	if depth > maxJSONDepth {
		return nil, p.errorf(p.pos, "nested too deeply")
	}
	if p.pos >= len(p.doc) {
		return nil, p.errorf(p.pos, "unexpected end of file")
	}
	switch p.doc[p.pos] {
	case '{':
		return p.object(depth)
	case '[':
		return p.list(depth)
	case '"':
		start := p.pos
		s, err := p.str()
		if err != nil {
			return nil, err
		}
		return &jsonNode{kind: jsonScalarKind, start: start, end: p.pos, value: s}, nil
	}
	return p.literal()
}

// str reads a string. encoding/json decodes it, so escapes and invalid
// bytes are read exactly as the standard reader reads them.
func (p *jsonParser) str() (string, error) {
	start := p.pos
	for i := start + 1; i < len(p.doc); i++ {
		switch p.doc[i] {
		case '\\':
			i++
		case '"':
			var s string
			if err := json.Unmarshal(p.doc[start:i+1], &s); err != nil {
				return "", p.errorf(start, "invalid string")
			}
			p.pos = i + 1
			return s, nil
		case '\n':
			return "", p.errorf(start, "string is not closed")
		}
	}
	return "", p.errorf(start, "string is not closed")
}

// literal reads a number, true, false or null.
func (p *jsonParser) literal() (*jsonNode, error) {
	start := p.pos
	end := start
	for end < len(p.doc) && strings.IndexByte("+-.0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ", p.doc[end]) >= 0 {
		end++
	}
	if end == start {
		return nil, p.errorf(start, "unexpected character %q", rune(p.doc[start]))
	}
	var v any
	if err := json.Unmarshal(p.doc[start:end], &v); err != nil {
		return nil, p.errorf(start, "invalid value %s", p.doc[start:end])
	}
	p.pos = end
	return &jsonNode{kind: jsonScalarKind, start: start, end: end, value: v}, nil
}

func (p *jsonParser) object(depth int) (*jsonNode, error) {
	n := &jsonNode{kind: jsonObjectKind, start: p.pos, lastComma: -1}
	p.pos++
	for {
		if err := p.skip(); err != nil {
			return nil, err
		}
		if p.pos >= len(p.doc) {
			return nil, p.errorf(n.start, "object is not closed")
		}
		if p.doc[p.pos] == '}' {
			p.pos++
			n.end = p.pos
			return n, nil
		}
		if len(n.members) > 0 && n.lastComma < 0 {
			return nil, p.errorf(p.pos, "expected , or }")
		}
		if p.doc[p.pos] != '"' {
			return nil, p.errorf(p.pos, "expected a key in quotation marks")
		}
		keyStart := p.pos
		key, err := p.str()
		if err != nil {
			return nil, err
		}
		if err := p.skip(); err != nil {
			return nil, err
		}
		if p.pos >= len(p.doc) || p.doc[p.pos] != ':' {
			return nil, p.errorf(p.pos, "expected : after the key")
		}
		p.pos++
		if err := p.skip(); err != nil {
			return nil, err
		}
		v, err := p.value(depth + 1)
		if err != nil {
			return nil, err
		}
		n.members = append(n.members, jsonMember{key: key, keyStart: keyStart, value: v})
		if n.lastComma, err = p.comma(); err != nil {
			return nil, err
		}
	}
}

func (p *jsonParser) list(depth int) (*jsonNode, error) {
	n := &jsonNode{kind: jsonListKind, start: p.pos, lastComma: -1}
	p.pos++
	for {
		if err := p.skip(); err != nil {
			return nil, err
		}
		if p.pos >= len(p.doc) {
			return nil, p.errorf(n.start, "list is not closed")
		}
		if p.doc[p.pos] == ']' {
			p.pos++
			n.end = p.pos
			return n, nil
		}
		if len(n.elems) > 0 && n.lastComma < 0 {
			return nil, p.errorf(p.pos, "expected , or ]")
		}
		v, err := p.value(depth + 1)
		if err != nil {
			return nil, err
		}
		n.elems = append(n.elems, v)
		if n.lastComma, err = p.comma(); err != nil {
			return nil, err
		}
	}
}

// comma reads the comma after a member or an element, if there is one, and
// returns its offset, or -1.
func (p *jsonParser) comma() (int, error) {
	if err := p.skip(); err != nil {
		return -1, err
	}
	if p.pos < len(p.doc) && p.doc[p.pos] == ',' {
		p.pos++
		return p.pos - 1, nil
	}
	return -1, nil
}

// decodeJSON returns a tree as plain values: map[string]any, []any, string,
// float64, bool and nil, as encoding/json decodes them.
func decodeJSON(n *jsonNode) any {
	switch n.kind {
	case jsonObjectKind:
		m := make(map[string]any, len(n.members))
		for _, mem := range n.members {
			m[mem.key] = decodeJSON(mem.value)
		}
		return m
	case jsonListKind:
		list := make([]any, len(n.elems))
		for i, e := range n.elems {
			list[i] = decodeJSON(e)
		}
		return list
	}
	return n.value
}

// plain turns a value to write into the form decodeJSON gives.
func plain(v any) any {
	switch v := v.(type) {
	case []string:
		list := make([]any, len(v))
		for i, s := range v {
			list[i] = s
		}
		return list
	case jsonObject:
		m := make(map[string]any, len(v))
		for _, f := range v {
			m[f.key] = plain(f.value)
		}
		return m
	}
	return v
}

// renderJSON writes a value. An object goes over several lines, each member
// at indent plus one unit, unless inline is set. A list is on one line.
func renderJSON(v any, indent, unit, nl string, inline bool) string {
	switch v := v.(type) {
	case jsonObject:
		if len(v) == 0 {
			return "{}"
		}
		parts := make([]string, len(v))
		for i, f := range v {
			parts[i] = quoteJSON(f.key) + ": " + renderJSON(f.value, indent+unit, unit, nl, inline)
		}
		if inline {
			return "{ " + strings.Join(parts, ", ") + " }"
		}
		sep := nl + indent + unit
		return "{" + sep + strings.Join(parts, ","+sep) + nl + indent + "}"
	case []string:
		parts := make([]string, len(v))
		for i, s := range v {
			parts[i] = quoteJSON(s)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case []any:
		parts := make([]string, len(v))
		for i, e := range v {
			parts[i] = renderJSON(e, indent, unit, nl, true)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case map[string]any:
		obj := make(jsonObject, 0, len(v))
		for _, k := range slices.Sorted(maps.Keys(v)) {
			obj = append(obj, jsonField{k, v[k]})
		}
		return renderJSON(obj, indent, unit, nl, inline)
	case string:
		return quoteJSON(v)
	}
	out, _ := json.Marshal(v)
	return string(out)
}

// quoteJSON writes a string. <, > and & stay as they are: a path is read by
// people too.
func quoteJSON(s string) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	// A string always encodes.
	_ = enc.Encode(s)
	return strings.TrimSuffix(b.String(), "\n")
}

// position is the line and column of an offset, both from 1. The column
// counts characters, not bytes.
func position(doc []byte, off int) (line, col int) {
	off = min(off, len(doc))
	lineStart := bytes.LastIndexByte(doc[:off], '\n') + 1
	return bytes.Count(doc[:lineStart], []byte("\n")) + 1, utf8.RuneCount(doc[lineStart:off]) + 1
}

// lineBreak is the line ending a document uses.
func lineBreak(doc []byte) string {
	if bytes.Contains(doc, []byte("\r\n")) {
		return "\r\n"
	}
	return "\n"
}

// lineIndent is the white space the line of off starts with.
func lineIndent(doc []byte, off int) string {
	start := bytes.LastIndexByte(doc[:off], '\n') + 1
	end := start
	for end < len(doc) && (doc[end] == ' ' || doc[end] == '\t') {
		end++
	}
	return string(doc[start:end])
}

// startsLine reports whether only white space stands before off on its line.
func startsLine(doc []byte, off int) bool {
	start := bytes.LastIndexByte(doc[:off], '\n') + 1
	return len(bytes.TrimLeft(doc[start:off], " \t")) == 0
}

// indentUnit is one level of indentation of a document: what its first
// member is indented by, or two spaces when that cannot be read.
func indentUnit(doc []byte, root *jsonNode) string {
	if root.kind == jsonObjectKind && len(root.members) > 0 {
		first := root.members[0].keyStart
		if unit := lineIndent(doc, first); unit != "" && startsLine(doc, first) {
			return unit
		}
	}
	return "  "
}

// splice is a replacement of doc[off:off+del] by text.
type splice struct {
	off, del int
	text     string
}

// applySplices applies replacements that do not overlap.
func applySplices(doc []byte, splices ...splice) []byte {
	slices.SortStableFunc(splices, func(a, b splice) int { return a.off - b.off })
	var out []byte
	at := 0
	for _, s := range splices {
		out = append(out, doc[at:s.off]...)
		out = append(out, s.text...)
		at = s.off + s.del
	}
	return append(out, doc[at:]...)
}

// insertItems adds items at the end of an object or a list. Each item is a
// function that renders itself for the indentation it lands at, or for one
// line.
func insertItems(doc []byte, root, n *jsonNode, items []func(indent string, inline bool) string) []byte {
	nl, unit := lineBreak(doc), indentUnit(doc, root)
	render := func(indent string, inline bool) []string {
		out := make([]string, len(items))
		for i, item := range items {
			out[i] = item(indent, inline)
		}
		return out
	}
	closing := n.end - 1
	// before is where a line of its own goes: the line break in front of
	// the closing bracket, when only white space stands between them.
	before := closing
	for before > n.start+1 && (doc[before-1] == ' ' || doc[before-1] == '\t') {
		before--
	}
	ownLine := before > n.start+1 && doc[before-1] == '\n'
	if ownLine {
		before--
		if before > n.start+1 && doc[before-1] == '\r' {
			before--
		}
	}

	firstStart, lastEnd := -1, -1
	switch {
	case len(n.members) > 0:
		firstStart, lastEnd = n.members[0].keyStart, n.members[len(n.members)-1].value.end
	case len(n.elems) > 0:
		firstStart, lastEnd = n.elems[0].start, n.elems[len(n.elems)-1].end
	}

	if firstStart < 0 {
		outer := lineIndent(doc, n.start)
		if len(bytes.TrimSpace(doc[n.start+1:closing])) == 0 {
			// Nothing inside: a list stays on one line, an object opens.
			if n.kind == jsonListKind {
				return applySplices(doc, splice{n.start + 1, closing - n.start - 1, strings.Join(render("", true), ", ")})
			}
			sep := nl + outer + unit
			text := sep + strings.Join(render(outer+unit, false), ","+sep) + nl + outer
			return applySplices(doc, splice{n.start + 1, closing - n.start - 1, text})
		}
		// Only comments inside: the items go after them.
		if ownLine {
			sep := nl + outer + unit
			return applySplices(doc, splice{before, 0, sep + strings.Join(render(outer+unit, false), ","+sep)})
		}
		return applySplices(doc, splice{closing, 0, " " + strings.Join(render("", true), ", ") + " "})
	}

	var splices []splice
	if n.lastComma < 0 {
		splices = append(splices, splice{lastEnd, 0, ","})
	}
	if ownLine {
		indent := lineIndent(doc, n.start) + unit
		if startsLine(doc, firstStart) {
			indent = lineIndent(doc, firstStart)
		}
		sep := nl + indent
		splices = append(splices, splice{before, 0, sep + strings.Join(render(indent, false), ","+sep)})
	} else {
		at := lastEnd
		if n.lastComma >= 0 {
			at = n.lastComma + 1
		}
		splices = append(splices, splice{at, 0, " " + strings.Join(render("", true), ", ")})
	}
	return applySplices(doc, splices...)
}

func dotted(path []string) string { return strings.Join(path, ".") }

// conflictAt is a conflict at the value that starts at off.
func conflictAt(file string, doc []byte, n *jsonNode, msg string, forceable bool) *Conflict {
	line, col := position(doc, n.start)
	end := n.end
	if nl := bytes.IndexAny(doc[n.start:n.end], "\r\n"); nl >= 0 {
		end = n.start + nl
	}
	return &Conflict{
		File: file, Line: line, Col: col, Width: max(1, utf8.RuneCount(doc[n.start:end])),
		Msg: msg, Forceable: forceable,
	}
}

// openJSON parses doc for an edit. A document with no value gets "{}" and a
// line break after whatever comments it holds, so that a new file, an empty
// one and one with only a comment take the same path.
func openJSON(file string, doc []byte) ([]byte, *jsonNode, *Conflict, error) {
	root, err := parseJSON(file, doc)
	if err != nil {
		return nil, nil, nil, err
	}
	// The object parseJSON returns for a document with no value stands
	// nowhere in the text, so there is no place to add a member to.
	if root.end == 0 {
		nl := lineBreak(doc)
		kept := bytes.TrimRight(doc, " \t\r\n")
		if len(kept) > 0 {
			kept = slices.Concat(kept, []byte(nl))
		}
		doc = slices.Concat(kept, []byte("{}"+nl))
		if root, err = parseJSON(file, doc); err != nil {
			return nil, nil, nil, err
		}
	}
	if root.kind != jsonObjectKind {
		return doc, root, conflictAt(file, doc, root, file+" is not an object; rune agent init cannot add to it", false), nil
	}
	return doc, root, nil, nil
}

// descend walks path from root as far as the document goes. It returns the
// object that holds the first missing key and the keys from there on; rest
// is empty when the whole path exists, and node is then its value. A value
// on the way that is not an object is a conflict.
func descend(file string, doc []byte, root *jsonNode, path []string) (node *jsonNode, rest []string, conflict *Conflict) {
	node = root
	for i, key := range path {
		if node.kind != jsonObjectKind {
			return nil, nil, conflictAt(file, doc, node, dotted(path[:i])+" is not an object; rune agent init cannot add to it", false)
		}
		next := node.member(key)
		if next == nil {
			return node, path[i:], nil
		}
		node = next
	}
	return node, nil, nil
}

// nested wraps value in objects for keys, the last key innermost.
func nested(keys []string, value any) any {
	for _, key := range slices.Backward(keys) {
		value = jsonObject{{key, value}}
	}
	return value
}

// memberItem renders `"key": value` for insertItems.
func memberItem(unit, nl, key string, value any) func(indent string, inline bool) string {
	return func(indent string, inline bool) string {
		return quoteJSON(key) + ": " + renderJSON(value, indent, unit, nl, inline)
	}
}

// jsonSet sets the value at an object path of doc, creating the objects on
// the way. state says what the document held: StateMissing when the key was
// added, StatePresent when it already had the value, StateDiffers when it
// had another one. Another value is a conflict and doc is returned as it
// was, unless force is set: then the value is replaced.
func jsonSet(file string, doc []byte, path []string, value any, force bool) (out []byte, state State, conflict *Conflict, err error) {
	doc, root, conflict, err := openJSON(file, doc)
	if err != nil || conflict != nil {
		return doc, StateDiffers, conflict, err
	}
	node, rest, conflict := descend(file, doc, root, path)
	if conflict != nil {
		return doc, StateDiffers, conflict, nil
	}
	nl, unit := lineBreak(doc), indentUnit(doc, root)
	if len(rest) > 0 {
		item := memberItem(unit, nl, rest[0], nested(rest[1:], value))
		return insertItems(doc, root, node, []func(string, bool) string{item}), StateMissing, nil, nil
	}
	if reflect.DeepEqual(decodeJSON(node), plain(value)) {
		return doc, StatePresent, nil, nil
	}
	wanted := renderJSON(value, lineIndent(doc, node.start), unit, nl, true)
	if force {
		return applySplices(doc, splice{node.start, node.end - node.start, wanted}), StateDiffers, nil, nil
	}
	msg := fmt.Sprintf("%s is %s; rune agent init writes %s. Edit the file, or run again with --force",
		dotted(path), renderJSON(decodeJSON(node), "", unit, nl, true), wanted)
	return doc, StateDiffers, conflictAt(file, doc, node, msg, true), nil
}

// jsonAppend makes sure the list at an object path of doc holds items, and
// returns the ones it added. The list and the objects on the way are
// created when missing. Nothing is removed, moved or written twice.
func jsonAppend(file string, doc []byte, path []string, items []string) (out []byte, added []string, conflict *Conflict, err error) {
	doc, root, conflict, err := openJSON(file, doc)
	if err != nil || conflict != nil {
		return doc, nil, conflict, err
	}
	node, rest, conflict := descend(file, doc, root, path)
	if conflict != nil {
		return doc, nil, conflict, nil
	}
	nl, unit := lineBreak(doc), indentUnit(doc, root)
	if len(rest) > 0 {
		item := memberItem(unit, nl, rest[0], nested(rest[1:], items))
		return insertItems(doc, root, node, []func(string, bool) string{item}), items, nil, nil
	}
	if node.kind != jsonListKind {
		return doc, nil, conflictAt(file, doc, node, dotted(path)+" is not a list; rune agent init cannot add to it", false), nil
	}
	var renders []func(string, bool) string
	for _, item := range items {
		if slices.ContainsFunc(node.elems, func(e *jsonNode) bool { return e.value == item }) {
			continue
		}
		added = append(added, item)
		renders = append(renders, func(string, bool) string { return quoteJSON(item) })
	}
	if len(added) == 0 {
		return doc, nil, nil, nil
	}
	return insertItems(doc, root, node, renders), added, nil, nil
}

// jsonChange is one intended change of a JSON file, for verifyJSON: a value
// set at a path, or strings a list at a path must hold.
type jsonChange struct {
	path  []string
	value any      // set when items is nil
	items []string // appended when not nil
}

// verifyJSON checks an edited document against the meaning of the old one:
// decoded, the new text must equal the old text with the changes applied and
// nothing else different. It is the guard behind the promise that an edit
// touches only what Rune owns.
func verifyJSON(file string, old, edited []byte, changes []jsonChange) error {
	before, err := parseJSON(file, old)
	if err != nil {
		return err
	}
	after, err := parseJSON(file, edited)
	if err != nil {
		return fmt.Errorf("%s: the planned text does not parse: %w", file, err)
	}
	want, ok := decodeJSON(before).(map[string]any)
	if !ok {
		return fmt.Errorf("%s is not an object", file)
	}
	for _, c := range changes {
		if c.items == nil {
			setPath(want, c.path, plain(c.value))
			continue
		}
		list, _ := lookupPath(want, c.path).([]any)
		for _, item := range c.items {
			if !slices.Contains(list, any(item)) {
				list = append(list, item)
			}
		}
		setPath(want, c.path, list)
	}
	if !reflect.DeepEqual(decodeJSON(after), any(want)) {
		return errors.New(file + ": the planned change did not verify; this is a bug in rune agent init")
	}
	return nil
}

// setPath sets a value at a path of nested maps, creating the maps.
func setPath(m map[string]any, path []string, value any) {
	for _, key := range path[:len(path)-1] {
		next, ok := m[key].(map[string]any)
		if !ok {
			next = map[string]any{}
			m[key] = next
		}
		m = next
	}
	m[path[len(path)-1]] = value
}

// lookupPath returns the value at a path of nested maps, or nil.
func lookupPath(m map[string]any, path []string) any {
	var v any = m
	for _, key := range path {
		next, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = next[key]
	}
	return v
}
