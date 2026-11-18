package taskimport

import (
	"slices"
	"testing"
)

func TestNPMScripts(t *testing.T) {
	text := `{
  "name": "demo",
  "version": "1.2.3",
  "private": true,
  "scripts": {
    "test": "jest",
    "build": "tsc -p .",
    "count": 3,
    "test": "vitest"
  },
  "dependencies": {"a": "1"}
}
`
	pkg, err := npmScripts(text)
	if err != nil {
		t.Fatal(err)
	}
	if pkg.name != "demo" || pkg.version != "1.2.3" {
		t.Errorf("name, version = %q, %q", pkg.name, pkg.version)
	}
	want := []npmScript{
		{name: "build", line: 7, text: "tsc -p .", isText: true},
		{name: "count", line: 8},
		{name: "test", line: 9, text: "vitest", isText: true},
	}
	if !slices.Equal(pkg.scripts, want) {
		t.Errorf("scripts = %+v\nwant      %+v", pkg.scripts, want)
	}
}

// npm reads the last value of a repeated key, so only the last "scripts"
// holds scripts.
func TestNPMScriptsRepeatedKey(t *testing.T) {
	pkg, err := npmScripts(`{"scripts": {"old": "echo old"}, "scripts": {"build": "tsc"}}`)
	if err != nil {
		t.Fatal(err)
	}
	if want := []npmScript{{name: "build", line: 1, text: "tsc", isText: true}}; !slices.Equal(pkg.scripts, want) {
		t.Errorf("scripts = %+v, want %+v", pkg.scripts, want)
	}
	pkg, err = npmScripts(`{"scripts": {"old": "echo old"}, "scripts": null}`)
	if err != nil || len(pkg.scripts) != 0 {
		t.Errorf("scripts = %+v, %v, want none", pkg.scripts, err)
	}
}

func TestNPMScriptsMissing(t *testing.T) {
	for _, text := range []string{`{}`, `{"scripts": {}}`, `{"scripts": null}`, `{"scripts": "x", "name": 5}`} {
		pkg, err := npmScripts(text)
		if err != nil || len(pkg.scripts) != 0 || pkg.name != "" {
			t.Errorf("npmScripts(%s) = %+v, %v, want no scripts and no error", text, pkg, err)
		}
	}
}

func TestNPMScriptsErrors(t *testing.T) {
	tests := []struct {
		text string
		line int
	}{
		{"{\n  \"scripts\": {\n    \"a\": \"x\",,\n  }\n}", 3},
		{"[1, 2]", 1},
		{"", 1},
		{"{\n  \"scripts\": {\n    \"a\": \"x\"\n", 4},
	}
	for _, tt := range tests {
		_, err := npmScripts(tt.text)
		pe, ok := err.(*ParseError)
		if !ok || pe.Line != tt.line || pe.Msg == "" {
			t.Errorf("npmScripts(%q) error = %v, want a parse error at line %d", tt.text, err, tt.line)
		}
	}
}

func TestNPMEnvUse(t *testing.T) {
	for script, want := range map[string]string{
		`echo $npm_lifecycle_event $npm_package_name ${npm_package_version}`: "",
		`node build.js`:                        "",
		`echo $npm_config_registry`:            "npm_config_registry",
		`echo %npm_package_main%`:              "npm_package_main",
		`cd $INIT_CWD && ls`:                   "INIT_CWD",
		`echo $npm_package_name $npm_execpath`: "npm_execpath",
		`echo MY_INIT_CWD_X snpm_config`:       "",
	} {
		if got := npmEnvUse(script); got != want {
			t.Errorf("npmEnvUse(%q) = %q, want %q", script, got, want)
		}
	}
}

func TestNPMCalls(t *testing.T) {
	for script, want := range map[string]string{
		`npm run lint && npm run test`: "npm",
		`npx tsc`:                      "npx",
		`yarn build`:                   "yarn",
		`cd web && pnpm i`:             "pnpm",
		`npmx run`:                     "",
		`echo my-npm`:                  "",
		`node scripts/npm.js`:          "",
		`tsc`:                          "",
	} {
		if got := npmCalls(script); got != want {
			t.Errorf("npmCalls(%q) = %q, want %q", script, got, want)
		}
	}
}

func TestReadNPMNoScripts(t *testing.T) {
	res, err := Convert([]byte(`{"name": "x"}`), Options{Format: FormatNPM, SourceName: "package.json"})
	if err != nil || len(res.Tasks) != 0 {
		t.Errorf("Convert = %+v, %v, want no tasks and no error", res, err)
	}
}
