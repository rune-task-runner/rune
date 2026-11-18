package taskimport

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"
)

var (
	npmVariable = regexp.MustCompile(`\b(npm_[A-Za-z0-9_]+|INIT_CWD)\b`)
	npmProgram  = regexp.MustCompile(`(^|[\s;&|(])(npm|npx|yarn|pnpm)($|[\s;&|)])`)
)

// npmProvided are the variables npm sets that an imported task also has.
var npmProvided = map[string]bool{
	"npm_lifecycle_event": true, "npm_package_name": true, "npm_package_version": true,
}

// npmLifecycle are the scripts npm also runs by itself, during install,
// publish and version.
var npmLifecycle = map[string]bool{
	"preinstall": true, "install": true, "postinstall": true, "prepublish": true,
	"preprepare": true, "prepare": true, "postprepare": true, "prepublishOnly": true,
	"prepack": true, "postpack": true, "publish": true, "postpublish": true,
	"preversion": true, "version": true, "postversion": true, "dependencies": true,
}

// npmScript is one entry of "scripts".
type npmScript struct {
	name   string
	line   int
	text   string
	isText bool // the value is a string
}

// npmPackage is what the import reads of a package.json.
type npmPackage struct {
	name, version string
	scripts       []npmScript // in file order; a repeated name keeps its last value
}

// npmScripts reads the name, the version and the scripts of a package.json.
// The scripts are read token by token, which keeps their order and tells
// where each one is.
func npmScripts(text string) (*npmPackage, error) {
	dec := json.NewDecoder(strings.NewReader(text))
	lineAt := func(offset int64) int {
		return 1 + strings.Count(text[:min(int(offset), len(text))], "\n")
	}
	fail := func(err error) (*npmPackage, error) {
		line := lineAt(dec.InputOffset())
		var syn *json.SyntaxError
		if errors.As(err, &syn) {
			line = lineAt(syn.Offset)
		}
		if errors.Is(err, io.EOF) {
			err = errors.New("unexpected end of the file")
		}
		return nil, &ParseError{line, err.Error()}
	}
	if tok, err := dec.Token(); err != nil {
		return fail(err)
	} else if tok != json.Delim('{') {
		return nil, &ParseError{1, "package.json is not an object"}
	}
	pkg := &npmPackage{}
	for dec.More() {
		key, err := dec.Token()
		if err != nil {
			return fail(err)
		}
		var raw json.RawMessage
		if key != "scripts" {
			if err := dec.Decode(&raw); err != nil {
				return fail(err)
			}
			var s string
			if json.Unmarshal(raw, &s) == nil {
				switch key {
				case "name":
					pkg.name = s
				case "version":
					pkg.version = s
				}
			}
			continue
		}
		// npm reads the last value of a repeated key.
		pkg.scripts = nil
		tok, err := dec.Token()
		if err != nil {
			return fail(err)
		}
		if tok != json.Delim('{') {
			// A scripts value that is no object holds no script. An array
			// still has to be read to its end.
			if tok == json.Delim('[') {
				for dec.More() {
					if err := dec.Decode(&raw); err != nil {
						return fail(err)
					}
				}
				if _, err := dec.Token(); err != nil {
					return fail(err)
				}
			}
			continue
		}
		for dec.More() {
			name, err := dec.Token()
			if err != nil {
				return fail(err)
			}
			s := npmScript{name: name.(string), line: lineAt(dec.InputOffset())}
			if err := dec.Decode(&raw); err != nil {
				return fail(err)
			}
			s.isText = json.Unmarshal(raw, &s.text) == nil && string(raw) != "null"
			pkg.scripts = slices.DeleteFunc(pkg.scripts, func(old npmScript) bool { return old.name == s.name })
			pkg.scripts = append(pkg.scripts, s)
		}
		if _, err := dec.Token(); err != nil {
			return fail(err)
		}
	}
	if _, err := dec.Token(); err != nil {
		return fail(err)
	}
	return pkg, nil
}

// npmEnvUse returns the first variable a script mentions that only npm
// sets, or "".
func npmEnvUse(script string) string {
	for _, name := range npmVariable.FindAllString(script, -1) {
		if !npmProvided[name] {
			return name
		}
	}
	return ""
}

// npmCalls returns the package manager a script runs, or "".
func npmCalls(script string) string {
	if m := npmProgram.FindStringSubmatch(script); m != nil {
		return m[2]
	}
	return ""
}

// readNPM turns the scripts of a package.json into tasks that do what
// `npm run` adds to a script: the pre and post scripts, node_modules/.bin on
// the path, the npm_ variables that are known without npm, and extra
// arguments passed on to the script.
func readNPM(text string) (*source, error) {
	pkg, err := npmScripts(text)
	if err != nil {
		return nil, err
	}
	src := &source{}
	if len(pkg.scripts) == 0 {
		return src, nil
	}
	src.vars = []Var{{Name: "path_sep", Expr: `if os() == "windows" { ";" } else { ":" }`}}
	exists := map[string]bool{}
	for _, s := range pkg.scripts {
		exists[s.name] = true
	}
	// isHook reports whether npm runs name around another script.
	isHook := func(name string) bool {
		for _, prefix := range []string{"pre", "post"} {
			if rest, ok := strings.CutPrefix(name, prefix); ok && exists[rest] {
				return true
			}
		}
		return false
	}
	for _, s := range pkg.scripts {
		t := &Task{Source: s.name, Line: s.line, Params: []string{"*args"}}
		src.tasks = append(src.tasks, t)
		env := [][2]string{{"npm_lifecycle_event", s.name}, {"npm_package_name", pkg.name}, {"npm_package_version", pkg.version}}
		for _, e := range env {
			if lit, ok := quote(e[1]); ok && e[1] != "" {
				t.Attrs = append(t.Attrs, fmt.Sprintf("env(%q, %s)", e[0], lit))
			}
		}
		if !s.isText {
			t.unsupported("the script is not text")
			continue
		}
		if name := npmEnvUse(s.text); name != "" {
			t.unsupported(fmt.Sprintf("it uses $%s, which only npm sets", name))
			continue
		}
		body, why := shellBody([]cmdLine{{text: escapeBraces(s.text)}}, false)
		if why != "" {
			t.unsupported(why)
			continue
		}
		t.Body = []string{`@export PATH="$PWD/node_modules/.bin{{path_sep}}$PATH"`}
		for _, line := range body {
			t.Body = append(t.Body, line+" {{quote_each(args)}}")
		}
		for _, hook := range []struct {
			name  string
			calls *[]Call
		}{{"pre" + s.name, &t.Deps}, {"post" + s.name, &t.Post}} {
			switch {
			case !exists[hook.name]:
			case isHook(s.name):
				// npm runs the hooks of a script it was asked for, not the
				// hooks of a hook.
				t.review(fmt.Sprintf("npm run %s also runs %s; the imported task does not", s.name, hook.name))
			default:
				*hook.calls = append(*hook.calls, Call{Name: hook.name})
			}
		}
		if program := npmCalls(s.text); program != "" {
			t.review(fmt.Sprintf("calls %s, which must still be installed", program))
		}
		if npmLifecycle[s.name] {
			t.review("npm also runs this script by itself; Rune runs it only by name")
		}
	}
	return src, nil
}
