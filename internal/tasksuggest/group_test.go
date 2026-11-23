package tasksuggest

import (
	"testing"
)

func groupsOf(calls ...call) []*Group {
	return groupExecutions(Executions(records(calls...)))
}

func TestGrouping(t *testing.T) {
	groups := groupsOf(join(
		times(2, call{cmd: "go test   ./..."}),
		times(1, call{cmd: "go  test ./..."}),
		times(1, call{cmd: `go test "./..."`}),
		times(1, call{cmd: "go test ./...", cwd: "web"}),
		[]call{{cmd: "go test ./...", outcome: Attempt}},
	)...)
	if len(groups) != 3 {
		t.Fatalf("%d groups", len(groups))
	}
	if g := groups[0]; g.Text != "go test ./..." || g.Dir != "" || g.Runs != 3 || g.Attempts != 1 || g.Outcomes[Succeeded] != 3 {
		t.Errorf("spacing only: %+v", g)
	}
	if g := groups[1]; g.Text != `go test "./..."` || g.Runs != 1 {
		t.Errorf("other quoting: %+v", g)
	}
	if g := groups[2]; g.Dir != "web" || g.Runs != 1 {
		t.Errorf("another directory: %+v", g)
	}
}

func TestGroupCounts(t *testing.T) {
	g := groupsOf(
		call{cmd: "make", desc: "Build", session: "a", outcome: Succeeded},
		call{cmd: "make", desc: "Build", session: "b", outcome: Failed, exit: 2},
		call{cmd: "make", desc: "Compile", session: "b", outcome: Interrupted},
		call{cmd: "make", session: "c", outcome: Started},
	)[0]
	if g.Runs != 4 || g.Sessions() != 3 || g.Descriptions["Build"] != 2 || g.Descriptions["Compile"] != 1 || len(g.Descriptions) != 2 {
		t.Errorf("got %+v", g)
	}
	for o, n := range map[Outcome]int{Succeeded: 1, Failed: 1, Interrupted: 1, Started: 1} {
		if g.Outcomes[o] != n {
			t.Errorf("outcome %v: %d", o, g.Outcomes[o])
		}
	}
}

func TestStripCD(t *testing.T) {
	tests := []struct {
		cmd, cwd, dir, text string
	}{
		{"cd web && npm test", "", "web", "npm test"},
		{"cd web/app && npm test", "x", "x/web/app", "npm test"},
		{"cd .. && npm test", "web", "", "npm test"},
		{"cd a && cd b && x", "", "a", "cd b && x"},
		{"cd a && x && y", "", "a", "x && y"},
		{"cd ../x && make", "", "", "cd ../x && make"},
		{"cd /abs && make", "", "", "cd /abs && make"},
		{"cd ~/x && make", "", "", "cd ~/x && make"},
		{"cd $D && make", "", "", "cd $D && make"},
		{`cd "web" && make`, "", "", `cd "web" && make`},
		{"cd -P web && make", "", "", "cd -P web && make"},
		{"cd web; make", "", "", "cd web\nmake"},
		{"cd web || make", "", "", "cd web || make"},
		{"cd web > x && make", "", "", "cd web >x && make"},
		{"! cd web && make", "", "", "! cd web && make"},
		{"x && cd web && make", "", "", "x && cd web && make"},
		{`cd w\eb && make`, "", "", `cd w\eb && make`},
		{"make", "", "", "make"},
	}
	for _, tt := range tests {
		dir, text, f := normalize(tt.cmd, tt.cwd)
		if dir != tt.dir || text != tt.text || f == nil {
			t.Errorf("normalize(%q, %q) = %q, %q", tt.cmd, tt.cwd, dir, text)
		}
	}
	if dir, text, f := normalize("echo 'open", "w"); dir != "w" || text != "echo 'open" || f != nil {
		t.Errorf("a command that does not parse: %q %q %v", dir, text, f)
	}
}

func TestRunsRune(t *testing.T) {
	for cmd, want := range map[string]bool{
		"rune test":               true,
		"rune -- lint":            true,
		"./rune build":            true,
		"/usr/local/bin/rune.exe": true,
		"go build && rune test":   true,
		"echo $(rune --list)":     true,
		"runes":                   false,
		"go run ./cmd/rune":       false,
		"echo rune":               false,
	} {
		f, err := parse(cmd)
		if err != nil {
			t.Fatal(err)
		}
		if got := runsRune(f); got != want {
			t.Errorf("runsRune(%q) = %v", cmd, got)
		}
	}
}

func TestReviewReason(t *testing.T) {
	tests := map[string]string{
		"go test ./...":                     "",
		"go test ./... 2>&1 | tail -20":     "",
		"make && make install":              "",
		"make || echo failed":               "",
		"make |& tee log":                   "",
		"go test > out.txt":                 "",
		"echo $HOME ${USER}":                "",
		`echo "a b" 'c'`:                    "",
		"CGO_ENABLED=0 go build":            "",
		"cat <<< word":                      "",
		"go test \\\n ./...":                reasonLines,
		"go test $(go list ./...)":          "it uses a command substitution",
		"go test `go list ./...`":           "it uses a command substitution",
		"diff <(a) <(b)":                    "it uses a process substitution",
		"echo $((1+2))":                     "it uses arithmetic expansion",
		"((i++))":                           "it uses arithmetic expansion",
		"let i=1":                           "it uses arithmetic expansion",
		"(cd web && make)":                  "it runs a subshell",
		"{ make; }":                         "it runs a group in braces",
		"if true; then make; fi":            "it uses if",
		"while true; do make; done":         "it uses while",
		"until make; do sleep 1; done":      "it uses until",
		"for f in *.go; do gofmt $f; done":  "it uses for",
		"select x in a b; do echo $x; done": "it uses select",
		"case $x in a) make;; esac":         "it uses case",
		"f() { make; }":                     "it defines a function",
		"make; make install":                reasonSequence,
		"make &":                            "it runs a command in the background with &",
		"cd web":                            "it changes the shell's state with cd",
		"source .env":                       "it changes the shell's state with source",
		". ./env.sh":                        "it changes the shell's state with .",
		"export X=1":                        "it changes the shell's state with export",
		"local x=1":                         "it changes the shell's state with local",
		"X=1":                               reasonFeature,
		"! make":                            reasonFeature,
		"echo ${x:-y}":                      reasonFeature,
		"echo ${#x}":                        reasonFeature,
		"[[ -f x ]]":                        reasonFeature,
		"time make":                         reasonFeature,
		"deploy --token ***":                reasonSecret,
	}
	for cmd, want := range tests {
		f, err := parse(cmd)
		if err != nil {
			t.Fatalf("%q: %v", cmd, err)
		}
		if got := reviewReason(f, printNode(f)); got != want {
			t.Errorf("reviewReason(%q) = %q, want %q", cmd, got, want)
		}
	}
	if got := reviewReason(nil, "a\nb"); got != reasonLines {
		t.Errorf("two lines: %q", got)
	}
}

func TestTaskMatches(t *testing.T) {
	group := func(cmd, dir string) *Group {
		return groupsOf(call{cmd: cmd, cwd: dir, outcome: Succeeded})[0]
	}
	tests := []struct {
		name string
		task TaskInfo
		g    *Group
		want bool
	}{
		{"same command", TaskInfo{Body: "go vet ./..."}, group("go vet ./...", ""), true},
		{"other spacing", TaskInfo{Body: "go  vet   ./..."}, group("go vet ./...", ""), true},
		{"same directory", TaskInfo{Body: "npm test", Dir: "web"}, group("cd web && npm test", ""), true},
		{"other directory", TaskInfo{Body: "npm test"}, group("npm test", "web"), false},
		{"cd in the body", TaskInfo{Body: "cd web && npm test"}, group("cd web && npm test", ""), true},
		{"cd in the body, run in that directory", TaskInfo{Body: "cd web && npm test"}, group("npm test", "web"), true},
		{"cd in the body, other directory", TaskInfo{Body: "cd web && npm test"}, group("npm test", ""), false},
		{"cd in the body from the task's directory", TaskInfo{Body: "cd .. && make", Dir: "web"}, group("make", ""), true},
		{"other command", TaskInfo{Body: "go vet ./..."}, group("go test ./...", ""), false},
		{"no body", TaskInfo{}, group("go vet ./...", ""), false},
		{"parameter", TaskInfo{Body: "go test -run {{quote(run)}} ./..."}, group("go test -run TestX ./...", ""), true},
		{"bare parameter", TaskInfo{Body: "go test -run {{run}} ./..."}, group("go test -run TestX ./...", ""), true},
		{"flag parameter", TaskInfo{Body: "go test --run={{quote(run)}} ./..."}, group("go test --run=TestX ./...", ""), true},
		{"flag parameter, other flag", TaskInfo{Body: "go test --run={{quote(run)}} ./..."}, group("go test --bench=TestX ./...", ""), false},
		{"parameter, quoted word", TaskInfo{Body: "go test -run {{quote(run)}} ./..."}, group(`go test -run "Test X" ./...`, ""), false},
		{"parameter, other word count", TaskInfo{Body: "go test -run {{quote(run)}}"}, group("go test -run TestX ./...", ""), false},
		{"parameter, other assignments", TaskInfo{Body: "A=1 go test -run {{quote(run)}}"}, group("go test -run TestX", ""), false},
		{"parameter in a pipeline", TaskInfo{Body: "go test -run {{quote(run)}} | tail"}, group("go test -run TestX | tail", ""), false},
		{"escaped braces", TaskInfo{Body: "echo {{{{x}}}}"}, group("echo {{x}}", ""), true},
		{"body that does not parse", TaskInfo{Body: "echo 'x"}, group("echo x", ""), false},
		{"command that does not parse", TaskInfo{Body: "echo 'x"}, group("echo 'x", ""), false},
	}
	for _, tt := range tests {
		if got := tt.task.matches(tt.g); got != tt.want {
			t.Errorf("%s: %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestSimpleCall(t *testing.T) {
	for cmd, want := range map[string]bool{
		"go test ./...": true,
		"A=1 go test":   true,
		"a; b":          false,
		"go test > out": false,
		"a | b":         false,
		"X=1":           false,
		"! go test":     false,
		"(go test)":     false,
	} {
		f, err := parse(cmd)
		if err != nil {
			t.Fatal(err)
		}
		if _, got := simpleCall(f); got != want {
			t.Errorf("simpleCall(%q) = %v", cmd, got)
		}
	}
}
