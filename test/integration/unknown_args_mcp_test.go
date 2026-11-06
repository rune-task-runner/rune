package integration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// unknownArgsRunefile is the spec 032 quickstart scratch Runefile: every body
// writes a marker file, so "nothing ran" is checkable from the outside.
const unknownArgsRunefile = `# Builds the project.
build target="debug":
    @echo built {{quote(target)}} > build.done

# Deploys the service.
deploy env:enum("staging","prod") replicas="2":
    @echo deployed {{quote(env)}} x{{quote(replicas)}} > deploy.done

# Records each name.
each +names:
    @echo {{quote_each(names)}} > each.done

# Removes caches. Takes no arguments.
clean_all:
    @echo cleaned > clean_all.done

# Prepares a release.
prep:
    @echo prep > prep.done

# Announces a release.
notify:
    @echo notify > notify.done

# Recovers from a failed release.
recover:
    @echo recover > recover.done

# Cuts a release.
release version="1": prep && notify || recover
    @echo released {{quote(version)}} > release.done

# Wipes local state.
[confirm("Wipe local state?")]
wipe:
    @echo wiped > wipe.done
`

// argCall starts `rune mcp` with flags in dir, calls task once with the raw
// JSON arguments, and returns the result's text and isError flag.
func argCall(t *testing.T, dir string, flags []string, task, arguments string) (text string, isError bool) {
	t.Helper()
	line, _ := mcpServerCall(t, dir, append([]string{"mcp"}, flags...), task, arguments)
	return toolReply(t, line)
}

// markers lists the marker files in dir, so a refusal can assert that no body,
// dependency, or hook ran.
func markers(t *testing.T, dir string) []string {
	t.Helper()
	found, err := filepath.Glob(filepath.Join(dir, "*.done"))
	if err != nil {
		t.Fatal(err)
	}
	return found
}

func markerText(t *testing.T, dir, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(b))
}

// Quickstart A to L (FR-001, SC-001): a call with an undeclared name returns
// the exact text and runs nothing. Scenario I is the destructive refusal, which
// comes first. Scenario D has a dependency, a post-hook, and a failure hook.
func TestUnknownArgsMCP_Refused(t *testing.T) {
	cases := []struct {
		scenario, task, args, want string
	}{
		{"A", "build", `{"targte":"release"}`, `unknown argument "targte" for task "build" (did you mean "target"?)`},
		{"B", "deploy", `{"env":"prod","replicaz":"5"}`, `unknown argument "replicaz" for task "deploy" (did you mean "replicas"?)`},
		{"C", "deploy", `{"evn":"prod"}`, `unknown argument "evn" for task "deploy" (did you mean "env"?)`},
		{"D", "release", `{"versoin":"2"}`, `unknown argument "versoin" for task "release" (did you mean "version"?)`}, //nolint:misspell // intentional typo: tests the refusal
		{"E", "build", `{"zzzzzz":"1"}`, `unknown argument "zzzzzz" for task "build"`},
		{"F", "clean_all", `{"force":true}`, `unknown argument "force" for task "clean_all"`},
		{
			"G", "deploy", `{"replicaz":"5","evn":"prod"}`,
			`unknown argument "evn" for task "deploy" (did you mean "env"?)` + "\n" +
				`unknown argument "replicaz" for task "deploy" (did you mean "replicas"?)`,
		},
		{"H", "deploy", `{"env":"nope","replicaz":"5"}`, `unknown argument "replicaz" for task "deploy" (did you mean "replicas"?)`},
		{"I", "wipe", `{"forcee":true}`, `task "wipe" is destructive; to permit it, add wipe to --allow-destructive and restart the server`},
		{"J", "build", `{"Target":"release"}`, `unknown argument "Target" for task "build" (did you mean "target"?)`},
		{"K", "build", `{"targte":"s3cret"}`, `unknown argument "targte" for task "build" (did you mean "target"?)`},
		{"L", "each", `{"namez":["a","b"]}`, `unknown argument "namez" for task "each" (did you mean "names"?)`},
	}
	for _, c := range cases {
		t.Run(c.scenario, func(t *testing.T) {
			dir := writeRunefile(t, unknownArgsRunefile)
			text, isError := argCall(t, dir, nil, c.task, c.args)
			if !isError || text != c.want {
				t.Errorf("isError=%v text=%q, want %q", isError, text, c.want)
			}
			if strings.Contains(text, "[exit") || strings.Contains(text, "s3cret") {
				t.Errorf("text %q has an exit line or an argument value", text)
			}
			if got := markers(t, dir); len(got) != 0 {
				t.Errorf("markers %q exist: something ran", got)
			}
		})
	}
}

// Quickstart M to R (FR-012, SC-004): a call using only declared names behaves
// as it always has.
func TestUnknownArgsMCP_DeclaredUnchanged(t *testing.T) {
	run := func(t *testing.T, task, args string) (dir, text string, isError bool) {
		t.Helper()
		dir = writeRunefile(t, unknownArgsRunefile)
		text, isError = argCall(t, dir, nil, task, args)
		return dir, text, isError
	}

	t.Run("M", func(t *testing.T) {
		dir, text, isError := run(t, "build", `{"target":"release"}`)
		if isError || markerText(t, dir, "build.done") != "built release" {
			t.Errorf("isError=%v text=%q", isError, text)
		}
	})
	t.Run("N", func(t *testing.T) {
		for _, args := range []string{`{}`, `null`} {
			dir, text, isError := run(t, "build", args)
			if isError || markerText(t, dir, "build.done") != "built debug" {
				t.Errorf("arguments %s: isError=%v text=%q", args, isError, text)
			}
		}
	})
	t.Run("O", func(t *testing.T) {
		dir, text, isError := run(t, "each", `{"names":["a","b"]}`)
		if isError || !exists(t, dir, "each.done") {
			t.Errorf("isError=%v text=%q", isError, text)
		}
	})
	t.Run("P", func(t *testing.T) {
		dir, text, isError := run(t, "deploy", `{}`)
		const want = `task "deploy" is missing required argument "env"`
		if !isError || text != want {
			t.Errorf("isError=%v text=%q, want %q", isError, text, want)
		}
		if got := markers(t, dir); len(got) != 0 {
			t.Errorf("markers %q exist: something ran", got)
		}
	})
	t.Run("Q", func(t *testing.T) {
		dir, text, isError := run(t, "build", `[]`)
		if !isError || !strings.HasPrefix(text, "invalid arguments: json: cannot unmarshal") {
			t.Errorf("isError=%v text=%q", isError, text)
		}
		if got := markers(t, dir); len(got) != 0 {
			t.Errorf("markers %q exist: something ran", got)
		}
	})
	t.Run("R", func(t *testing.T) {
		dir, text, isError := run(t, "release", `{"version":"2"}`)
		if isError || !exists(t, dir, "prep.done") || !exists(t, dir, "notify.done") || exists(t, dir, "recover.done") {
			t.Fatalf("isError=%v text=%q markers=%q", isError, text, markers(t, dir))
		}
		if got := markerText(t, dir, "release.done"); got != "released 2" {
			t.Errorf("release.done = %q, want released 2", got)
		}
	})
}

// Quickstart U: a grant permits calling the task, not calling it with a typo.
func TestUnknownArgsMCP_GrantedDestructive(t *testing.T) {
	dir := writeRunefile(t, unknownArgsRunefile)
	text, isError := argCall(t, dir, []string{"--allow-destructive", "wipe"}, "wipe", `{"forcee":true}`)
	const want = `unknown argument "forcee" for task "wipe"`
	if !isError || text != want {
		t.Errorf("isError=%v text=%q, want %q", isError, text, want)
	}
	if exists(t, dir, "wipe.done") {
		t.Error("the destructive task ran although refused")
	}
}

// Quickstart V (FR-003): the text names the Runefile task, not the tool.
func TestUnknownArgsMCP_ImportedTask(t *testing.T) {
	dir := writeRunefile(t, "mod tools \"tools.rune\"\n")
	module := "# Builds a tool.\nbuild target=\"debug\":\n    @echo tool {{quote(target)}} > tool.done\n"
	if err := os.WriteFile(filepath.Join(dir, "tools.rune"), []byte(module), 0o644); err != nil {
		t.Fatal(err)
	}
	text, isError := argCall(t, dir, nil, "tools__build", `{"targte":"x"}`)
	const want = `unknown argument "targte" for task "tools::build" (did you mean "target"?)`
	if !isError || text != want {
		t.Errorf("isError=%v text=%q, want %q", isError, text, want)
	}
	if exists(t, dir, "tool.done") {
		t.Error("the task ran although refused")
	}
}

// Quickstart S (SC-003): every tool on stdio declares that no other names are
// allowed, including one with no parameters and one that is destructive.
func TestUnknownArgsMCP_ToolsList(t *testing.T) {
	reply := mcpToolsList(t, writeRunefile(t, unknownArgsRunefile))
	var frame struct {
		Result struct {
			Tools []struct {
				Name        string         `json:"name"`
				InputSchema map[string]any `json:"inputSchema"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(reply), &frame); err != nil {
		t.Fatalf("decoding tools/list reply %q: %v", reply, err)
	}
	seen := map[string]map[string]any{}
	for _, tl := range frame.Result.Tools {
		seen[tl.Name] = tl.InputSchema
	}
	for _, name := range []string{"build", "deploy", "each", "clean_all", "prep", "notify", "recover", "release", "wipe"} {
		schema, ok := seen[name]
		if !ok {
			t.Errorf("tool %s is not listed", name)
			continue
		}
		if schema["additionalProperties"] != false {
			t.Errorf("%s: additionalProperties = %v, want false", name, schema["additionalProperties"])
		}
	}
	want := map[string]any{"additionalProperties": false, "properties": map[string]any{}, "type": "object"}
	if !reflect.DeepEqual(seen["clean_all"], want) {
		t.Errorf("clean_all schema = %v, want %v", seen["clean_all"], want)
	}
}

// Quickstart W (FR-015): a null or object value is refused instead of reaching
// the body as <nil> or map[k:v], and a required parameter gets no pass for it.
func TestUnknownArgsMCP_NullAndObjectValues(t *testing.T) {
	const runefile = `# Removes a path.
rm path:
    @echo removing {{quote(path)}} > rm.done

# Builds the project.
build target="debug":
    @echo built {{quote(target)}} > build.done

# Records each name.
each +names:
    @echo {{quote_each(names)}} > each.done
`
	cases := []struct{ task, args, want string }{
		{"rm", `{"path":null}`, `invalid arguments: parameter "path": null is not a valid value`},
		{"build", `{"target":null}`, `invalid arguments: parameter "target": null is not a valid value`},
		{"build", `{"target":{"k":"v"}}`, `invalid arguments: parameter "target": an object is not a valid value`},
		{"each", `{"names":["a",null]}`, `invalid arguments: parameter "names": null is not a valid value`},
	}
	for _, c := range cases {
		t.Run(c.task+" "+c.args, func(t *testing.T) {
			dir := writeRunefile(t, runefile)
			text, isError := argCall(t, dir, nil, c.task, c.args)
			if !isError || text != c.want {
				t.Errorf("isError=%v text=%q, want %q", isError, text, c.want)
			}
			if got := markers(t, dir); len(got) != 0 {
				t.Errorf("markers %q exist: something ran", got)
			}
		})
	}

	dir := writeRunefile(t, runefile)
	if text, isError := argCall(t, dir, nil, "build", `{"target":true}`); isError || markerText(t, dir, "build.done") != "built true" {
		t.Errorf("a boolean value stopped reaching the task: isError=%v text=%q", isError, text)
	}
}
