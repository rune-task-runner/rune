package agentinit

import (
	"bytes"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func TestParseClient(t *testing.T) {
	for _, name := range []string{"claude", "codex", "vscode"} {
		if c, err := ParseClient(name); err != nil || string(c) != name {
			t.Errorf("ParseClient(%q) = %q, %v", name, c, err)
		}
	}
	for _, name := range []string{"cursor", "", "Claude"} {
		_, err := ParseClient(name)
		if want := `unknown client "` + name + `": the clients are claude, codex and vscode`; err == nil || err.Error() != want {
			t.Errorf("ParseClient(%q) = %v, want %q", name, err, want)
		}
	}
}

func TestPathsAndBinary(t *testing.T) {
	tests := []struct {
		client Client
		binary string
		paths  []string
	}{
		{ClientClaude, "claude", []string{".mcp.json", ".claude/settings.json"}},
		{ClientCodex, "codex", []string{".codex/config.toml"}},
		{ClientVSCode, "code", []string{".vscode/mcp.json", ".mcp.json", ".github/agents/rune.agent.md"}},
	}
	for _, tt := range tests {
		if got := Binary(tt.client); got != tt.binary {
			t.Errorf("Binary(%s) = %q, want %q", tt.client, got, tt.binary)
		}
		if got := Paths(tt.client); !slices.Equal(got, tt.paths) {
			t.Errorf("Paths(%s) = %v, want %v", tt.client, got, tt.paths)
		}
	}
}

func TestFileStatus(t *testing.T) {
	tests := []struct {
		f    File
		want string
	}{
		{File{New: []byte("a")}, "created"},
		{File{Old: []byte("a"), New: []byte("b"), Existed: true}, "updated"},
		{File{Old: []byte("a"), New: []byte("a"), Existed: true}, "unchanged"},
		{File{Existed: true, New: []byte("a")}, "updated"},
		{File{}, "unchanged"},
	}
	for _, tt := range tests {
		if got := tt.f.Status(); got != tt.want {
			t.Errorf("%+v: Status() = %q, want %q", tt.f, got, tt.want)
		}
	}
}

func TestRegistration(t *testing.T) {
	pinned := Inputs{Runefile: "/p/Runefile", ShownRunefile: "'/p/Runefile'"}
	if got := registration(pinned); !slices.Equal(got, []string{"mcp", "--file", "/p/Runefile", "--no-reload"}) {
		t.Errorf("pinned: %v", got)
	}
	if got, want := serverRuns(pinned), `server "rune" runs: rune mcp --file '/p/Runefile' --no-reload`; got != want {
		t.Errorf("pinned: %q, want %q", got, want)
	}
	unpinned := Inputs{Runefile: "/p/Runefile", Unpinned: true}
	if got := registration(unpinned); !slices.Equal(got, []string{"mcp", "--no-reload"}) {
		t.Errorf("unpinned: %v", got)
	}
	if got, want := serverRuns(unpinned), `server "rune" runs: rune mcp --no-reload`; got != want {
		t.Errorf("unpinned: %q, want %q", got, want)
	}
}

func TestServerChanges(t *testing.T) {
	in := Inputs{Unpinned: true}
	tests := []struct {
		added, replaced []string
		want            []string
	}{
		{[]string{"type", "command", "args"}, nil, []string{`server "rune" runs: rune mcp --no-reload`}},
		{[]string{"args"}, []string{"command"}, []string{`added args to server "rune"`, `replaced command of server "rune"`}},
		{nil, nil, nil},
	}
	for _, tt := range tests {
		if got := serverChanges(in, tt.added, tt.replaced, 3); !slices.Equal(got, tt.want) {
			t.Errorf("serverChanges(%v, %v) = %q, want %q", tt.added, tt.replaced, got, tt.want)
		}
	}
}

func TestErrorTexts(t *testing.T) {
	pe := &ParseError{File: ".mcp.json", Line: 3, Col: 7, Msg: "expected , or }"}
	if got, want := pe.Error(), ".mcp.json:3:7: expected , or }"; got != want {
		t.Errorf("ParseError: %q, want %q", got, want)
	}
	me := &ManualError{File: ".codex/config.toml", Line: 2, Col: 1, Setting: "features.shell_tool = false"}
	if got, want := me.Error(), ".codex/config.toml:2:1: rune agent init cannot add to this file in place. Set this yourself: features.shell_tool = false"; got != want {
		t.Errorf("ManualError: %q, want %q", got, want)
	}
}

func TestPlanUnknownClient(t *testing.T) {
	if _, err := Plan("zed", Inputs{}); err == nil || !strings.Contains(err.Error(), `unknown client "zed"`) {
		t.Errorf("err = %v", err)
	}
}

// A conflict in one file changes none, and the notes follow the options.
func TestPlanConflictChangesNoFile(t *testing.T) {
	in := Inputs{Runefile: "/p/Runefile", ShownRunefile: "/p/Runefile", ShownDir: "/p", Files: map[string][]byte{
		claudeSettingsPath: []byte(`{"permissions": "none"}`),
	}}
	res, err := Plan(ClientClaude, in)
	if err != nil || len(res.Conflicts) != 1 {
		t.Fatalf("err = %v, conflicts = %v", err, res)
	}
	for _, f := range res.Files {
		if f.Status() != "unchanged" || f.Changes != nil {
			t.Errorf("%s is %s with changes %v although the plan has a conflict", f.Path, f.Status(), f.Changes)
		}
	}
	if got := res.Notes[2]; !strings.Contains(got, "--unpinned") {
		t.Errorf("pinned note = %q", got)
	}
	in.Unpinned, in.Files = true, nil
	res, err = Plan(ClientClaude, in)
	if err != nil || !strings.HasPrefix(res.Notes[2], "the server serves the Runefile found") {
		t.Errorf("unpinned note: %v, %v", res, err)
	}
}

// The planner works from bytes to bytes. A file, a process or the network
// in it would break what the command promises: that planning reads nothing
// and starts nothing (spec 055, FR-004).
func TestPackageImportsNoIO(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("no Go files found: %v", err)
	}
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), file, src, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range parsed.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			if path == "os" || path == "os/exec" || path == "io/ioutil" || path == "net" || strings.HasPrefix(path, "net/") {
				t.Errorf("%s imports %s", file, path)
			}
		}
	}
}

func TestClients(t *testing.T) {
	for _, c := range Clients() {
		if _, ok := adapterOf(c); !ok {
			t.Errorf("%s is listed and has no adapter", c)
		}
	}
	if got, want := ClientList("or"), "claude, codex or vscode"; got != want {
		t.Errorf("ClientList = %q, want %q", got, want)
	}
}

// Some editors start a UTF-8 file with a byte order mark. The file is
// planned as if the mark were not there, and keeps it.
func TestPlanKeepsAByteOrderMark(t *testing.T) {
	const mark = "\xEF\xBB\xBF"
	in := Inputs{Runefile: "/p/Runefile", ShownRunefile: "/p/Runefile", ShownDir: "/p"}
	tests := []struct {
		client Client
		path   string
		text   string
	}{
		{ClientClaude, mcpJSONPath, "{\n  \"mcpServers\": {\n    \"docs\": { \"command\": \"docs\" }\n  }\n}\n"},
		{ClientCodex, codexConfigPath, "# mine\n[features]\nweb = false\n"},
		{ClientClaude, claudeSettingsPath, ""},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			plan := func(text string) File {
				t.Helper()
				in.Files = map[string][]byte{tt.path: []byte(text)}
				res, err := Plan(tt.client, in)
				if err != nil || len(res.Conflicts) > 0 {
					t.Fatalf("Plan: %v %+v", err, res)
				}
				for _, f := range res.Files {
					if f.Path == tt.path {
						return f
					}
				}
				t.Fatalf("no file %s", tt.path)
				return File{}
			}
			plain, marked := plan(tt.text), plan(mark+tt.text)
			if string(marked.Old) != mark+tt.text || string(marked.New) != mark+string(plain.New) {
				t.Errorf("with a mark: %q, want the mark and %q", marked.New, plain.New)
			}
			// A second run finds the setup and changes nothing.
			if again := plan(string(marked.New)); again.Status() != "unchanged" {
				t.Errorf("a second run leaves the file %s", again.Status())
			}
		})
	}

	in.Files = map[string][]byte{mcpJSONPath: []byte(mark + `{"mcpServers": []}`)}
	res, err := Plan(ClientClaude, in)
	if err != nil || len(res.Conflicts) != 1 || res.Conflicts[0].Col != 16 {
		t.Errorf("a conflict behind a mark: %+v, %v; want column 16, as without the mark", res, err)
	}
	if f := res.Files[0]; !bytes.Equal(f.New, f.Old) || !bytes.HasPrefix(f.Old, []byte(mark)) {
		t.Errorf("a conflict changed the file: %q", f.New)
	}
}
