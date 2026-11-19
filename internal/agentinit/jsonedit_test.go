package agentinit

import (
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func mustDecode(t *testing.T, doc string) any {
	t.Helper()
	root, err := parseJSON("f.json", []byte(doc))
	if err != nil {
		t.Fatalf("parseJSON(%q): %v", doc, err)
	}
	return decodeJSON(root)
}

func TestParseJSON(t *testing.T) {
	tests := []struct {
		name string
		doc  string
		want any
	}{
		{"object", `{"a": 1, "b": {"c": [true, false, null]}}`, map[string]any{"a": 1.0, "b": map[string]any{"c": []any{true, false, nil}}}},
		{"string escapes", `{"s": "a\"b\\c\n\u00e9\ud83d\ude00"}`, map[string]any{"s": "a\"b\\c\né😀"}},
		{"numbers", `[0, -1.5, 2e3]`, []any{0.0, -1.5, 2000.0}},
		{"line comment", "{\n  // note\n  \"a\": 1 // tail\n}", map[string]any{"a": 1.0}},
		{"block comment", `{ /* x */ "a": /* y */ 1 }`, map[string]any{"a": 1.0}},
		{"comment at end of file", `{"a": 1} // done`, map[string]any{"a": 1.0}},
		{"trailing commas", `{"a": [1, 2,], }`, map[string]any{"a": []any{1.0, 2.0}}},
		{"empty", ``, map[string]any{}},
		{"white space", " \n\t ", map[string]any{}},
		{"duplicate key, last wins", `{"a": 1, "a": 2}`, map[string]any{"a": 2.0}},
		{"scalar root", `"x"`, "x"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := mustDecode(t, tt.doc); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestParseJSONErrors(t *testing.T) {
	tests := []struct {
		name      string
		doc       string
		line, col int
		msg       string
	}{
		{"object not closed", "{\n  \"a\": 1", 1, 1, "object is not closed"},
		{"list not closed", `[1, 2`, 1, 1, "list is not closed"},
		{"missing comma in object", "{\n  \"a\": 1\n  \"b\": 2\n}", 3, 3, "expected , or }"},
		{"missing comma in list", `[1 2]`, 1, 4, "expected , or ]"},
		{"key without quotes", `{a: 1}`, 1, 2, "expected a key in quotation marks"},
		{"missing colon", `{"a" 1}`, 1, 6, "expected : after the key"},
		{"string not closed", `{"a": "x`, 1, 7, "string is not closed"},
		{"string over two lines", "{\"a\": \"x\ny\"}", 1, 7, "string is not closed"},
		{"invalid escape", `{"a": "\q"}`, 1, 7, "invalid string"},
		{"invalid value", `{"a": tru}`, 1, 7, "invalid value tru"},
		{"unexpected character", `{"a": ?}`, 1, 7, `unexpected character '?'`},
		{"end of file", `{"a":`, 1, 6, "unexpected end of file"},
		{"text after the value", `{} x`, 1, 4, "unexpected text after the value"},
		{"comment not closed", `{ /* x`, 1, 3, "comment is not closed"},
		{"column counts characters", `{"é": ?}`, 1, 7, `unexpected character '?'`},
		{"too deep", strings.Repeat("[", maxJSONDepth+2), 1, maxJSONDepth + 2, "nested too deeply"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseJSON("f.json", []byte(tt.doc))
			var pe *ParseError
			if !errors.As(err, &pe) {
				t.Fatalf("err = %v, want a *ParseError", err)
			}
			if pe.File != "f.json" || pe.Line != tt.line || pe.Col != tt.col || pe.Msg != tt.msg {
				t.Errorf("got %d:%d %q, want %d:%d %q", pe.Line, pe.Col, pe.Msg, tt.line, tt.col, tt.msg)
			}
		})
	}
}

// FuzzParseJSON holds the reader to the standard one: whatever
// encoding/json reads, parseJSON reads to the same value.
func FuzzParseJSON(f *testing.F) {
	for _, seed := range []string{
		`{}`, `[]`, `{"a": [1, 2.5, -3e2, true, false, null, "x\n\u00e9"]}`, `"s"`, `0`,
		`{"a": {"b": {"c": []}}, "a": 1}`, " \t\r\n{ \"k\" : \"v\" } ", `[[[[]]]]`, "\"\xff\"",
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, doc []byte) {
		var want any
		if err := json.Unmarshal(doc, &want); err != nil {
			return
		}
		root, err := parseJSON("f.json", doc)
		if err != nil {
			t.Fatalf("encoding/json reads %q, parseJSON does not: %v", doc, err)
		}
		if got := decodeJSON(root); !reflect.DeepEqual(got, want) {
			t.Fatalf("%q: got %#v, want %#v", doc, got, want)
		}
	})
}

func TestRenderJSON(t *testing.T) {
	obj := jsonObject{{"a", "x"}, {"b", []string{"p", "q"}}, {"c", jsonObject{{"d", true}}}}
	if got, want := renderJSON(obj, "  ", "\t", "\n", false), "{\n  \t\"a\": \"x\",\n  \t\"b\": [\"p\", \"q\"],\n  \t\"c\": {\n  \t\t\"d\": true\n  \t}\n  }"; got != want {
		t.Errorf("over lines: got %q, want %q", got, want)
	}
	if got, want := renderJSON(obj, "", "  ", "\n", true), `{ "a": "x", "b": ["p", "q"], "c": { "d": true } }`; got != want {
		t.Errorf("inline: got %q, want %q", got, want)
	}
	for _, tt := range []struct {
		v    any
		want string
	}{
		{jsonObject{}, "{}"},
		{[]string{}, "[]"},
		{[]any{"a", 1.0, nil}, `["a", 1, null]`},
		{map[string]any{"b": 1.0, "a": []any{}}, `{ "a": [], "b": 1 }`},
		{"<a&b>", `"<a&b>"`},
		{false, "false"},
	} {
		if got := renderJSON(tt.v, "", "  ", "\n", true); got != tt.want {
			t.Errorf("renderJSON(%#v) = %q, want %q", tt.v, got, tt.want)
		}
	}
}

func TestPositionAndLayoutHelpers(t *testing.T) {
	doc := []byte("ab\n  é\"x\"\r\n\tz")
	for _, tt := range []struct{ off, line, col int }{{0, 1, 1}, {2, 1, 3}, {3, 2, 1}, {7, 2, 4}, {99, 3, 3}} {
		if line, col := position(doc, tt.off); line != tt.line || col != tt.col {
			t.Errorf("position(%d) = %d:%d, want %d:%d", tt.off, line, col, tt.line, tt.col)
		}
	}
	if got := lineBreak(doc); got != "\r\n" {
		t.Errorf("lineBreak = %q, want \\r\\n", got)
	}
	if got := lineBreak([]byte("a\nb")); got != "\n" {
		t.Errorf("lineBreak = %q, want \\n", got)
	}
	if got := lineIndent(doc, 7); got != "  " {
		t.Errorf("lineIndent = %q, want two spaces", got)
	}
	if !startsLine(doc, 5) || startsLine(doc, 7) {
		t.Errorf("startsLine: want true at 5 and false at 7")
	}
	root, _ := parseJSON("f", []byte("{\n\t\"a\": 1\n}"))
	if got := indentUnit([]byte("{\n\t\"a\": 1\n}"), root); got != "\t" {
		t.Errorf("indentUnit = %q, want a tab", got)
	}
	for _, doc := range []string{`{"a": 1}`, `{}`, `[1]`, "{\n\"a\": 1\n}"} {
		root, _ := parseJSON("f", []byte(doc))
		if got := indentUnit([]byte(doc), root); got != "  " {
			t.Errorf("indentUnit(%q) = %q, want two spaces", doc, got)
		}
	}
}

func TestApplySplices(t *testing.T) {
	got := applySplices([]byte("0123456789"), splice{8, 1, "X"}, splice{2, 0, "a"}, splice{2, 0, "b"}, splice{4, 2, ""})
	if string(got) != "01ab2367X9" {
		t.Errorf("got %q", got)
	}
}

func TestJSONSet(t *testing.T) {
	path := []string{"servers", "rune", "command"}
	tests := []struct {
		name  string
		doc   string
		path  []string
		value any
		want  string
		state State
	}{
		{
			"missing key in a multi-line object", "{\n    \"servers\": {\n        \"rune\": {\n            \"type\": \"stdio\"\n        }\n    }\n}\n",
			path, "rune",
			"{\n    \"servers\": {\n        \"rune\": {\n            \"type\": \"stdio\",\n            \"command\": \"rune\"\n        }\n    }\n}\n", StateMissing,
		},
		{
			"missing key in a one-line object", `{"servers":{"rune":{"type":"stdio"}}}`,
			path, "rune", `{"servers":{"rune":{"type":"stdio", "command": "rune"}}}`, StateMissing,
		},
		{
			"empty object opens over lines", "{}\n",
			[]string{"a"},
			"x", "{\n  \"a\": \"x\"\n}\n", StateMissing,
		},
		{
			"nested empty object uses the file's unit", "{\n\t\"servers\": {}\n}\n",
			[]string{"servers", "rune"},
			"x", "{\n\t\"servers\": {\n\t\t\"rune\": \"x\"\n\t}\n}\n", StateMissing,
		},
		{
			"missing objects on the path are created", "{\n  \"other\": 1\n}\n",
			path, "rune",
			"{\n  \"other\": 1,\n  \"servers\": {\n    \"rune\": {\n      \"command\": \"rune\"\n    }\n  }\n}\n", StateMissing,
		},
		{
			"empty document", "",
			[]string{"a", "b"},
			[]string{"x", "y"},
			"{\n  \"a\": {\n    \"b\": [\"x\", \"y\"]\n  }\n}\n", StateMissing,
		},
		{
			"only a line comment", "// mine\n",
			[]string{"a"},
			"x", "// mine\n{\n  \"a\": \"x\"\n}\n", StateMissing,
		},
		{
			"only a block comment, no line break", "/* mine */",
			[]string{"a"},
			"x", "/* mine */\n{\n  \"a\": \"x\"\n}\n", StateMissing,
		},
		{
			"only a comment, with windows line endings", "// mine\r\n\r\n",
			[]string{"a"},
			"x", "// mine\r\n{\r\n  \"a\": \"x\"\r\n}\r\n", StateMissing,
		},
		{
			"equal value changes nothing", `{"servers": {"rune": {"command": "rune"}}}`,
			path, "rune", `{"servers": {"rune": {"command": "rune"}}}`, StatePresent,
		},
		{
			"equal list changes nothing", `{"a": ["x",  "y"]}`,
			[]string{"a"},
			[]string{"x", "y"},
			`{"a": ["x",  "y"]}`, StatePresent,
		},
		{
			"comment after the last member stays on its line", "{\n  \"a\": 1 // keep\n}\n",
			[]string{"b"},
			"x", "{\n  \"a\": 1, // keep\n  \"b\": \"x\"\n}\n", StateMissing,
		},
		{
			"trailing comma is not doubled", "{\n  \"a\": 1,\n}\n",
			[]string{"b"},
			"x", "{\n  \"a\": 1,\n  \"b\": \"x\"\n}\n", StateMissing,
		},
		{
			"trailing comma on one line", `{"a": 1,}`,
			[]string{"b"},
			"x", `{"a": 1, "b": "x"}`, StateMissing,
		},
		{
			"object with only a comment", "{\n  // nothing yet\n}\n",
			[]string{"b"},
			"x", "{\n  // nothing yet\n  \"b\": \"x\"\n}\n", StateMissing,
		},
		{
			"one-line object with only a comment", `{ /* nothing */ }`,
			[]string{"b"},
			"x", `{ /* nothing */  "b": "x" }`, StateMissing,
		},
		{
			"first member on the opening line", "{\"a\": 1,\n \"c\": 2\n}",
			[]string{"b"},
			"x", "{\"a\": 1,\n \"c\": 2,\n  \"b\": \"x\"\n}", StateMissing,
		},
		{
			"windows line endings", "{\r\n  \"a\": 1\r\n}\r\n",
			[]string{"b", "c"},
			"x", "{\r\n  \"a\": 1,\r\n  \"b\": {\r\n    \"c\": \"x\"\r\n  }\r\n}\r\n", StateMissing,
		},
		{
			"backslash and quotation mark", "{}\n",
			[]string{"p"},
			`C:\my "dir"`, "{\n  \"p\": \"C:\\\\my \\\"dir\\\"\"\n}\n", StateMissing,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, state, conflict, err := jsonSet("f.json", []byte(tt.doc), tt.path, tt.value, false)
			if err != nil || conflict != nil {
				t.Fatalf("err = %v, conflict = %+v", err, conflict)
			}
			if string(out) != tt.want || state != tt.state {
				t.Errorf("got %q (%s)\nwant %q (%s)", out, state, tt.want, tt.state)
			}
			// The value reads back as it was given.
			got := lookupPath(mustDecode(t, string(out)).(map[string]any), tt.path)
			if !reflect.DeepEqual(got, plain(tt.value)) {
				t.Errorf("reads back as %#v, want %#v", got, tt.value)
			}
		})
	}
}

func TestJSONSetConflicts(t *testing.T) {
	doc := "{\n  \"servers\": {\n    \"rune\": { \"args\": [\"mcp\"], \"env\": {} }, // mine\n    \"list\": [],\n  }\n}\n"
	args := []string{"servers", "rune", "args"}

	out, state, conflict, err := jsonSet("f.json", []byte(doc), args, []string{"mcp", "--no-reload"}, false)
	if err != nil || conflict == nil {
		t.Fatalf("err = %v, conflict = %v, want a conflict", err, conflict)
	}
	want := Conflict{
		File: "f.json", Line: 3, Col: 23, Width: 7, Forceable: true,
		Msg: `servers.rune.args is ["mcp"]; rune agent init writes ["mcp", "--no-reload"]. Edit the file, or run again with --force`,
	}
	if *conflict != want || state != StateDiffers || string(out) != doc {
		t.Errorf("conflict = %+v (%s), want %+v; doc changed: %v", *conflict, state, want, string(out) != doc)
	}

	out, state, conflict, err = jsonSet("f.json", []byte(doc), args, []string{"mcp", "--no-reload"}, true)
	if err != nil || conflict != nil || state != StateDiffers {
		t.Fatalf("force: err = %v, conflict = %v, state = %s", err, conflict, state)
	}
	if want := strings.Replace(doc, `["mcp"]`, `["mcp", "--no-reload"]`, 1); string(out) != want {
		t.Errorf("force changed more than the value:\n%s\nwant\n%s", out, want)
	}

	for _, tt := range []struct {
		name string
		doc  string
		path []string
		msg  string
		line int
	}{
		{"non-object on the path", doc, []string{"servers", "list", "x"}, "servers.list is not an object; rune agent init cannot add to it", 4},
		{"root is a list", "[1]", []string{"a"}, "f.json is not an object; rune agent init cannot add to it", 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			// force does not help where the document has another shape.
			out, _, conflict, err := jsonSet("f.json", []byte(tt.doc), tt.path, "x", true)
			if err != nil || conflict == nil {
				t.Fatalf("err = %v, conflict = %v, want a conflict", err, conflict)
			}
			if conflict.Msg != tt.msg || conflict.Forceable || conflict.Line != tt.line || string(out) != tt.doc {
				t.Errorf("conflict = %+v", *conflict)
			}
		})
	}

	if _, _, _, err := jsonSet("f.json", []byte(`{"a": `), []string{"a"}, "x", false); err == nil {
		t.Error("an invalid document gives no error")
	}
}

// A value that spans lines is underlined on its first line only.
func TestConflictWidthStopsAtLineEnd(t *testing.T) {
	doc := "{\n  \"a\": [\n    1\n  ]\n}"
	_, _, conflict, _ := jsonSet("f.json", []byte(doc), []string{"a"}, "x", false)
	if conflict == nil || conflict.Line != 2 || conflict.Col != 8 || conflict.Width != 1 {
		t.Errorf("conflict = %+v, want 2:8 width 1", conflict)
	}
}

func TestJSONAppend(t *testing.T) {
	path := []string{"permissions", "deny"}
	tests := []struct {
		name  string
		doc   string
		items []string
		want  string
		added []string
	}{
		{
			"missing list is created inline", "{\n  \"permissions\": {}\n}\n",
			[]string{"A", "B"},
			"{\n  \"permissions\": {\n    \"deny\": [\"A\", \"B\"]\n  }\n}\n",
			[]string{"A", "B"},
		},
		{
			"one-line list", `{"permissions": {"deny": ["X"]}}`,
			[]string{"A", "B"},
			`{"permissions": {"deny": ["X", "A", "B"]}}`,
			[]string{"A", "B"},
		},
		{
			"multi-line list", "{\n  \"permissions\": {\n    \"deny\": [\n      \"X\"\n    ]\n  }\n}\n",
			[]string{"A", "B"},
			"{\n  \"permissions\": {\n    \"deny\": [\n      \"X\",\n      \"A\",\n      \"B\"\n    ]\n  }\n}\n",
			[]string{"A", "B"},
		},
		{
			"present strings are not added again", `{"permissions": {"deny": ["B", "X"]}}`,
			[]string{"A", "B"},
			`{"permissions": {"deny": ["B", "X", "A"]}}`,
			[]string{"A"},
		},
		{
			"all present", `{"permissions": {"deny": ["B", "A"]}}`,
			[]string{"A", "B"},
			`{"permissions": {"deny": ["B", "A"]}}`, nil,
		},
		{
			"empty list", `{"permissions": {"deny": [ ]}}`,
			[]string{"A", "B"},
			`{"permissions": {"deny": ["A", "B"]}}`,
			[]string{"A", "B"},
		},
		{
			"list with other kinds of values", `{"permissions": {"deny": [1, {"A": 1}, null]}}`,
			[]string{"A"},
			`{"permissions": {"deny": [1, {"A": 1}, null, "A"]}}`,
			[]string{"A"},
		},
		{
			"empty document", "",
			[]string{"A"},
			"{\n  \"permissions\": {\n    \"deny\": [\"A\"]\n  }\n}\n",
			[]string{"A"},
		},
		{
			"only a comment", "// mine\n",
			[]string{"A"},
			"// mine\n{\n  \"permissions\": {\n    \"deny\": [\"A\"]\n  }\n}\n",
			[]string{"A"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, added, conflict, err := jsonAppend("f.json", []byte(tt.doc), path, tt.items)
			if err != nil || conflict != nil {
				t.Fatalf("err = %v, conflict = %+v", err, conflict)
			}
			if string(out) != tt.want || !slices.Equal(added, tt.added) {
				t.Errorf("got %q, added %v\nwant %q, added %v", out, added, tt.want, tt.added)
			}
		})
	}

	for _, tt := range []struct{ name, doc, msg string }{
		{"not a list", `{"permissions": {"deny": "all"}}`, "permissions.deny is not a list; rune agent init cannot add to it"},
		{"not an object on the path", `{"permissions": "none"}`, "permissions is not an object; rune agent init cannot add to it"},
		{"root is a list", `[]`, "f.json is not an object; rune agent init cannot add to it"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			out, added, conflict, err := jsonAppend("f.json", []byte(tt.doc), path, []string{"A"})
			if err != nil || conflict == nil {
				t.Fatalf("err = %v, conflict = %v, want a conflict", err, conflict)
			}
			if conflict.Msg != tt.msg || conflict.Forceable || added != nil || string(out) != tt.doc {
				t.Errorf("conflict = %+v, added = %v", *conflict, added)
			}
		})
	}
	if _, _, _, err := jsonAppend("f.json", []byte(`{`), path, []string{"A"}); err == nil {
		t.Error("an invalid document gives no error")
	}
}

func TestVerifyJSON(t *testing.T) {
	old := `{"keep": 1, "permissions": {"deny": ["X"]}}`
	changes := []jsonChange{
		{path: []string{"servers", "rune", "command"}, value: "rune"},
		{path: []string{"permissions", "deny"}, items: []string{"X", "A"}},
		{path: []string{"permissions", "allow"}, items: []string{"B"}},
	}
	good := `{"keep": 1, "permissions": {"deny": ["X", "A"], "allow": ["B"]}, "servers": {"rune": {"command": "rune"}}}`
	if err := verifyJSON("f.json", []byte(old), []byte(good), changes); err != nil {
		t.Errorf("a correct edit does not verify: %v", err)
	}
	if err := verifyJSON("f.json", nil, []byte(`{"a": ["x"]}`), []jsonChange{{path: []string{"a"}, value: []string{"x"}}}); err != nil {
		t.Errorf("an edit of an empty file does not verify: %v", err)
	}
	for name, edited := range map[string]string{
		"another key was dropped": `{"permissions": {"deny": ["X", "A"], "allow": ["B"]}, "servers": {"rune": {"command": "rune"}}}`,
		"a value differs":         `{"keep": 2, "permissions": {"deny": ["X", "A"], "allow": ["B"]}, "servers": {"rune": {"command": "rune"}}}`,
		"a rule was dropped":      `{"keep": 1, "permissions": {"deny": ["A"], "allow": ["B"]}, "servers": {"rune": {"command": "rune"}}}`,
		"a change is missing":     `{"keep": 1, "permissions": {"deny": ["X", "A"]}, "servers": {"rune": {"command": "rune"}}}`,
		"does not parse":          `{"keep": 1`,
	} {
		t.Run(name, func(t *testing.T) {
			if err := verifyJSON("f.json", []byte(old), []byte(edited), changes); err == nil {
				t.Error("verifies, want an error")
			}
		})
	}
	if err := verifyJSON("f.json", []byte(`[`), []byte(`{}`), nil); err == nil {
		t.Error("an old text that does not parse verifies")
	}
	if err := verifyJSON("f.json", []byte(`[]`), []byte(`[]`), nil); err == nil {
		t.Error("an old text that is not an object verifies")
	}
}

func TestSetPathAndLookupPath(t *testing.T) {
	m := map[string]any{"a": "scalar"}
	setPath(m, []string{"a", "b", "c"}, 1)
	setPath(m, []string{"x"}, 2)
	want := map[string]any{"a": map[string]any{"b": map[string]any{"c": 1}}, "x": 2}
	if !reflect.DeepEqual(m, want) {
		t.Errorf("got %#v, want %#v", m, want)
	}
	if got := lookupPath(m, []string{"a", "b", "c"}); got != 1 {
		t.Errorf("lookupPath = %v, want 1", got)
	}
	if got := lookupPath(m, []string{"x", "y"}); got != nil {
		t.Errorf("lookupPath through a scalar = %v, want nil", got)
	}
	if got := lookupPath(m, []string{"missing"}); got != nil {
		t.Errorf("lookupPath of a missing key = %v, want nil", got)
	}
}
