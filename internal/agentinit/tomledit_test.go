package agentinit

import (
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"
)

func TestTOMLValue(t *testing.T) {
	tests := []struct {
		value any
		want  string
	}{
		{false, "false"},
		{true, "true"},
		{"rune", `"rune"`},
		{`C:\my "dir"` + "\ttab\x7f", `"C:\\my \"dir\"\u0009tab\u007F"`},
		{"é", `"é"`},
		{[]string{"mcp", "--file", "/a b/Runefile"}, `["mcp", "--file", "/a b/Runefile"]`},
		{[]string{}, "[]"},
		{7, "7"},
	}
	for _, tt := range tests {
		got := tomlValue(tt.value)
		if got != tt.want {
			t.Errorf("tomlValue(%#v) = %s, want %s", tt.value, got, tt.want)
		}
		// What is written reads back as the value.
		m, err := decodeTOML("f.toml", []byte("k = "+got+"\n"))
		if err != nil {
			t.Errorf("tomlValue(%#v) = %s does not parse: %v", tt.value, got, err)
			continue
		}
		if want := plain(tt.value); !reflect.DeepEqual(m["k"], want) && tt.value != 7 {
			t.Errorf("tomlValue(%#v) reads back as %#v", tt.value, m["k"])
		}
	}
}

func TestDecodeTOMLError(t *testing.T) {
	_, err := decodeTOML("f.toml", []byte("a = 1\nb = = 2\n"))
	var pe *ParseError
	if !errors.As(err, &pe) {
		t.Fatalf("err = %v, want a *ParseError", err)
	}
	if pe.File != "f.toml" || pe.Line != 2 || pe.Col < 1 || pe.Msg == "" || strings.HasPrefix(pe.Msg, "toml: ") {
		t.Errorf("got %+v, want line 2 and a message without the library's prefix", *pe)
	}
}

func TestTOMLSet(t *testing.T) {
	features := []string{"features"}
	server := []string{"mcp_servers", "rune"}
	tests := []struct {
		name  string
		doc   string
		table []string
		key   string
		value any
		want  string
		state State
	}{
		{
			"after the last key of the table", "[features]\nweb = false\n\n[other]\nx = 1\n",
			features, "shell_tool", false,
			"[features]\nweb = false\nshell_tool = false\n\n[other]\nx = 1\n", StateMissing,
		},
		{
			"after a key with a trailing comment", "[features]\nweb = false # why\n",
			features, "shell_tool", false,
			"[features]\nweb = false # why\nshell_tool = false\n", StateMissing,
		},
		{
			"after a multi-line value", "[features]\nlist = [\n  1, # one\n  2,\n]\n# tail\n",
			features, "shell_tool", false,
			"[features]\nlist = [\n  1, # one\n  2,\n]\nshell_tool = false\n# tail\n", StateMissing,
		},
		{
			"table with a header and no key", "[features] # mine\n\n[other]\n",
			features, "shell_tool", false,
			"[features] # mine\nshell_tool = false\n\n[other]\n", StateMissing,
		},
		{
			"table at the end of a file without a line break", "[features]",
			features, "shell_tool", false,
			"[features]\nshell_tool = false", StateMissing,
		},
		{
			"sub-table does not move the insertion point", "[features]\nweb = false\n[features.sub]\nx = 1\n",
			features, "shell_tool", false,
			"[features]\nweb = false\nshell_tool = false\n[features.sub]\nx = 1\n", StateMissing,
		},
		{
			"new table at the end", "model = \"x\"\n",
			features, "shell_tool", false,
			"model = \"x\"\n\n[features]\nshell_tool = false\n", StateMissing,
		},
		{
			"new table when the file has no final line break", "model = \"x\"",
			features, "shell_tool", false,
			"model = \"x\"\n\n[features]\nshell_tool = false\n", StateMissing,
		},
		{
			"empty file", "",
			server, "command", "rune",
			"[mcp_servers.rune]\ncommand = \"rune\"\n", StateMissing,
		},
		{
			"white space only", "\n",
			features, "shell_tool", false,
			"\n[features]\nshell_tool = false\n", StateMissing,
		},
		{
			"equal value changes nothing", "[features]\nshell_tool = false\n",
			features, "shell_tool", false,
			"[features]\nshell_tool = false\n", StatePresent,
		},
		{
			"equal list in an inline table changes nothing", "mcp_servers = { rune = { args = ['mcp'] } }\n",
			server, "args",
			[]string{"mcp"},
			"mcp_servers = { rune = { args = ['mcp'] } }\n", StatePresent,
		},
		{
			"quoted header", "[mcp_servers.\"rune\"]\ncommand = \"rune\"\n",
			server, "args",
			[]string{"mcp"},
			"[mcp_servers.\"rune\"]\ncommand = \"rune\"\nargs = [\"mcp\"]\n", StateMissing,
		},
		{
			"array of tables is not the table", "[[features]]\nx = 1\n",
			[]string{"agents"},
			"enabled", false,
			"[[features]]\nx = 1\n\n[agents]\nenabled = false\n", StateMissing,
		},
		{
			"windows line endings", "# c\r\n[features]\r\nweb = false\r\n",
			features, "shell_tool", false,
			"# c\r\n[features]\r\nweb = false\r\nshell_tool = false\r\n", StateMissing,
		},
		{
			"new table with windows line endings", "a = 1\r\n",
			features, "shell_tool", false,
			"a = 1\r\n\r\n[features]\r\nshell_tool = false\r\n", StateMissing,
		},
		{
			"path with a backslash and a quotation mark", "",
			server, "args",
			[]string{"mcp", "--file", `C:\my "dir"\Runefile`},
			"[mcp_servers.rune]\nargs = [\"mcp\", \"--file\", \"C:\\\\my \\\"dir\\\"\\\\Runefile\"]\n", StateMissing,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, state, conflict, err := tomlSet("f.toml", []byte(tt.doc), tt.table, tt.key, tt.value, false)
			if err != nil || conflict != nil {
				t.Fatalf("err = %v, conflict = %+v", err, conflict)
			}
			if string(out) != tt.want || state != tt.state {
				t.Errorf("got %q (%s)\nwant %q (%s)", out, state, tt.want, tt.state)
			}
			m, err := decodeTOML("f.toml", out)
			if err != nil {
				t.Fatal(err)
			}
			if got := lookupPath(m, append(tt.table[:len(tt.table):len(tt.table)], tt.key)); !reflect.DeepEqual(got, plain(tt.value)) {
				t.Errorf("reads back as %#v, want %#v", got, tt.value)
			}
		})
	}
}

func TestTOMLSetConflict(t *testing.T) {
	doc := "# top\n[features]\nshell_tool = true   # why\nother = 1\n"
	out, state, conflict, err := tomlSet("f.toml", []byte(doc), []string{"features"}, "shell_tool", false, false)
	if err != nil || conflict == nil {
		t.Fatalf("err = %v, conflict = %v, want a conflict", err, conflict)
	}
	want := Conflict{
		File: "f.toml", Line: 3, Col: 14, Width: 4, Forceable: true,
		Msg: "features.shell_tool is true; rune agent init writes false. Edit the file, or run again with --force",
	}
	if *conflict != want || state != StateDiffers || string(out) != doc {
		t.Errorf("conflict = %+v (%s), want %+v", *conflict, state, want)
	}

	out, state, conflict, err = tomlSet("f.toml", []byte(doc), []string{"features"}, "shell_tool", false, true)
	if err != nil || conflict != nil || state != StateDiffers {
		t.Fatalf("force: err = %v, conflict = %v, state = %s", err, conflict, state)
	}
	if want := strings.Replace(doc, "true", "false", 1); string(out) != want {
		t.Errorf("force changed more than the value:\n%s\nwant\n%s", out, want)
	}

	// A value over several lines is shown on one line and underlined on its
	// first.
	multi := "[mcp_servers.rune]\nargs = [\n  \"mcp\",\n]\n"
	_, _, conflict, _ = tomlSet("f.toml", []byte(multi), []string{"mcp_servers", "rune"}, "args", []string{"mcp", "--no-reload"}, false)
	if conflict == nil || conflict.Line != 2 || conflict.Col != 8 || conflict.Width != 1 ||
		!strings.HasPrefix(conflict.Msg, `mcp_servers.rune.args is [ "mcp", ]; rune agent init writes ["mcp", "--no-reload"].`) {
		t.Errorf("conflict = %+v", conflict)
	}
	out, _, _, err = tomlSet("f.toml", []byte(multi), []string{"mcp_servers", "rune"}, "args", []string{"mcp", "--no-reload"}, true)
	if err != nil || string(out) != "[mcp_servers.rune]\nargs = [\"mcp\", \"--no-reload\"]\n" {
		t.Errorf("force on a multi-line value: %q, %v", out, err)
	}

	// A dotted key under another header is still the key.
	dottedDoc := "[mcp_servers]\nrune.command = \"other\"\n"
	out, _, _, err = tomlSet("f.toml", []byte(dottedDoc), []string{"mcp_servers", "rune"}, "command", "rune", true)
	if err != nil || string(out) != "[mcp_servers]\nrune.command = \"rune\"\n" {
		t.Errorf("force on a dotted key: %q, %v", out, err)
	}
}

func TestTOMLSetManual(t *testing.T) {
	tests := []struct {
		name      string
		doc       string
		table     []string
		key       string
		value     any
		line, col int
		setting   string
	}{
		{
			"inline table", "model = \"x\"\nmcp_servers = { rune = { command = \"rune\" } }\n",
			[]string{"mcp_servers", "rune"},
			"args",
			[]string{"mcp"},
			2, 1, `mcp_servers.rune.args = ["mcp"]`,
		},
		{
			"another value inside an inline table", "mcp_servers = { rune = { command = \"x\" } }\n",
			[]string{"mcp_servers", "rune"},
			"command", "rune", 1, 1, `mcp_servers.rune.command = "rune"`,
		},
		{
			"dotted key at the top", "a = 1\nfeatures.web = false\n",
			[]string{"features"},
			"shell_tool", false, 2, 1, "features.shell_tool = false",
		},
		{
			"dotted key under another header", "[mcp_servers]\n  rune.command = \"rune\"\n",
			[]string{"mcp_servers", "rune"},
			"args",
			[]string{"mcp"},
			2, 3, `mcp_servers.rune.args = ["mcp"]`,
		},
		{
			"a scalar where the table should be", "features = \"none\"\n",
			[]string{"features"},
			"shell_tool", false, 1, 1, "features.shell_tool = false",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, _, conflict, err := tomlSet("f.toml", []byte(tt.doc), tt.table, tt.key, tt.value, true)
			var manual *ManualError
			if !errors.As(err, &manual) || conflict != nil {
				t.Fatalf("err = %v, conflict = %v, want a *ManualError", err, conflict)
			}
			if manual.File != "f.toml" || manual.Line != tt.line || manual.Col != tt.col || manual.Setting != tt.setting {
				t.Errorf("got %+v, want %d:%d %q", *manual, tt.line, tt.col, tt.setting)
			}
			if string(out) != tt.doc {
				t.Errorf("the document changed: %q", out)
			}
		})
	}
}

func TestTOMLSetInvalidFile(t *testing.T) {
	_, _, _, err := tomlSet("f.toml", []byte("[features\n"), []string{"features"}, "shell_tool", false, false)
	var pe *ParseError
	if !errors.As(err, &pe) || pe.Line != 1 {
		t.Errorf("err = %v, want a *ParseError on line 1", err)
	}
}

// TOML has nan, which no comparison finds equal to itself. A file that holds
// one is still a file the command can add to.
func TestTOMLSetWithNaN(t *testing.T) {
	doc := "ratio = nan\nlist = [nan, 1.5]\n\n[features]\nweb = false\n"
	out, state, conflict, err := tomlSet("f.toml", []byte(doc), []string{"features"}, "shell_tool", false, false)
	if err != nil || conflict != nil || state != StateMissing {
		t.Fatalf("err = %v, conflict = %v, state = %s", err, conflict, state)
	}
	if want := doc + "shell_tool = false\n"; string(out) != want {
		t.Errorf("got %q, want %q", out, want)
	}
}

func TestSameValue(t *testing.T) {
	nan := math.NaN()
	tests := []struct {
		name string
		a, b any
		want bool
	}{
		{"equal maps", map[string]any{"a": []any{1.5, "x"}, "b": true}, map[string]any{"a": []any{1.5, "x"}, "b": true}, true},
		{"nan equals nan", map[string]any{"a": []any{nan}}, map[string]any{"a": []any{nan}}, true},
		{"nan and a number", nan, 1.0, false},
		{"float and string", 1.0, "1", false},
		{"another value", map[string]any{"a": 1.0}, map[string]any{"a": 2.0}, false},
		{"a missing key", map[string]any{"a": 1.0, "b": 1.0}, map[string]any{"a": 1.0, "c": 1.0}, false},
		{"one key more", map[string]any{"a": 1.0}, map[string]any{"a": 1.0, "b": 1.0}, false},
		{"map and list", map[string]any{}, []any{}, false},
		{"list and map", []any{}, map[string]any{}, false},
		{"lists of another length", []any{1.0}, []any{1.0, 2.0}, false},
		{"lists of another order", []any{1.0, 2.0}, []any{2.0, 1.0}, false},
		{"integers", int64(3), int64(3), true},
		{"integer and float", int64(3), 3.0, false},
	}
	for _, tt := range tests {
		if got := sameValue(tt.a, tt.b); got != tt.want {
			t.Errorf("%s: sameValue = %v, want %v", tt.name, got, tt.want)
		}
	}
}
