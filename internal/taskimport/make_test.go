package taskimport

import (
	"slices"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestMakeLines(t *testing.T) {
	text := "A = 1 \\\n    2\n\nbuild: \\\n  dep\n\techo a \\\n\t  b\n\t@echo c\n"
	want := []makeLine{
		{n: 1, text: "A = 1 2"},
		{n: 3, text: ""},
		{n: 4, text: "build: dep"},
		{n: 6, text: "echo a   b", recipe: true},
		{n: 8, text: "@echo c", recipe: true},
		{n: 9, text: ""},
	}
	if got := makeLines(text); !slices.Equal(got, want) {
		t.Errorf("makeLines = %+v\nwant        %+v", got, want)
	}
}

func TestMakeComment(t *testing.T) {
	tests := []struct{ in, text, doc string }{
		{"build: dep", "build: dep", ""},
		{"build: dep # note", "build: dep ", ""},
		{"build: dep ## Build it. ", "build: dep ", "Build it."},
		{`url = http://x\#frag # c`, "url = http://x#frag ", ""},
		{"# only", "", ""},
		{"V = a  # keeps the spaces", "V = a  ", ""},
	}
	for _, tt := range tests {
		if text, doc := makeComment(tt.in); text != tt.text || doc != tt.doc {
			t.Errorf("makeComment(%q) = %q, %q, want %q, %q", tt.in, text, doc, tt.text, tt.doc)
		}
	}
}

func TestMakeValue(t *testing.T) {
	tests := []struct {
		in     string
		expr   string
		refs   []string
		reason string
	}{
		{``, `""`, nil, ""},
		{`dist`, `"dist"`, nil, ""},
		{`$(OUT)/app`, `OUT + "/app"`, []string{"OUT"}, ""},
		{`pre-${A}-$(B)`, `"pre-" + A + "-" + B`, []string{"A", "B"}, ""},
		{`cost: $$5 "q"`, `"cost: $5 \"q\""`, nil, ""},
		{`$(shell date)`, "", nil, "its value uses a make function, a substitution or an automatic variable: $(shell date)"},
		{`$(wildcard *.go)`, "", nil, "its value uses a make function, a substitution or an automatic variable: $(wildcard *.go)"},
		{`$(SRC:.c=.o)`, "", nil, "its value uses a make function, a substitution or an automatic variable: $(SRC:.c=.o)"},
		{`$@`, "", nil, "its value uses a make function, a substitution or an automatic variable: $@"},
		{`$(open`, "", nil, "its value uses a make function, a substitution or an automatic variable: $(open"},
		{`$(a.b)`, "", nil, "its value uses a make function, a substitution or an automatic variable: $(a.b)"},
		{"bell\a", "", nil, "its value has a control character"},
	}
	for _, tt := range tests {
		expr, refs, reason := makeValue(tt.in)
		if expr != tt.expr || !slices.Equal(refs, tt.refs) || reason != tt.reason {
			t.Errorf("makeValue(%q) = %q, %q, %q\nwant %q, %q, %q", tt.in, expr, refs, reason, tt.expr, tt.refs, tt.reason)
		}
	}
}

func TestMakeExpand(t *testing.T) {
	good := map[string]bool{"OUT": true}
	bad := map[string]int{"NOW": 3}
	tests := []struct{ in, out, reason string }{
		{`go build -o $(OUT)/app`, `go build -o {{OUT}}/app`, ""},
		{`echo ${OUT} $$HOME $$$$`, `echo {{OUT}} $HOME $$`, ""},
		{`docker ps --format '{{.ID}}'`, `docker ps --format '{{{{.ID}}'`, ""},
		{`echo $(NOW)`, "", `it uses the variable "NOW" of line 3, which was not converted`},
		{`$(MAKE) -C sub`, "", `it uses $(MAKE), which the Makefile does not set (an environment or built-in make variable)`},
		{`cc -o $@ $<`, "", `it uses the automatic variable $@`},
		{`echo $(notdir $(OUT))`, "", `it uses a make function or substitution: $(notdir $(OUT))`},
		{`echo $(OUT:.c=.o)`, "", `it uses a make function or substitution: $(OUT:.c=.o)`},
		{`echo $V`, "", `it uses the make variable $V`},
		{`echo $(open`, "", `it uses a make function or substitution: $(open`},
		{`echo $`, `echo $`, ""},
		{`echo '{a:{b:{c:{d:$(OUT)}}}}' '{a:{b:{}}}}'`, `echo '{a:{b:{c:{d:{{OUT}}}}}}}}}}' '{a:{b:{}}}}}}}}'`, ""},
		{`echo {{$(OUT)`, `echo {{{{{{OUT}}`, ""},
		{`mkdir -p build/{$(OUT)}`, "", `a brace directly before $(OUT) cannot be written in a Runefile body`},
		{`echo {{{${OUT}`, "", `a brace directly before ${OUT} cannot be written in a Runefile body`},
	}
	for _, tt := range tests {
		out, reason := makeExpand(tt.in, good, bad)
		if out != tt.out || reason != tt.reason {
			t.Errorf("makeExpand(%q) = %q, %q, want %q, %q", tt.in, out, reason, tt.out, tt.reason)
		}
	}
}

func TestMakePrefixes(t *testing.T) {
	tests := []struct {
		in             string
		text           string
		noEcho, ignore bool
	}{
		{"echo a", "echo a", false, false},
		{"@echo a", "echo a", true, false},
		{"-rm x", "rm x", false, true},
		{"@-rm x", "rm x", true, true},
		{"-@ rm x", "rm x", true, true},
		{"+@make sub", "make sub", true, false},
		{"  @ echo a", "echo a", true, false},
	}
	for _, tt := range tests {
		c := makePrefixes(tt.in)
		if c.text != tt.text || c.noEcho != tt.noEcho || c.ignore != tt.ignore {
			t.Errorf("makePrefixes(%q) = %+v", tt.in, c)
		}
	}
}

func TestReadMakeErrors(t *testing.T) {
	tests := []struct {
		name, text string
		line       int
		msg        string
	}{
		{"a recipe before any rule", "\techo a\n", 1, "a command line before the first rule"},
		{"a line that is nothing", "build:\n\techo a\nnonsense here\n", 3, "this line is not a rule, a variable or a directive: nonsense here"},
		{"an open conditional", "ifdef X\nA = 1\n", 1, "ifdef has no endif"},
		{"an open define", "define X\necho\n", 1, "define has no endef"},
		{"a stray endif", "endif\n", 1, "endif without a conditional"},
		{"a cycle", ".PHONY: a b\na: b\nb: a\n", 2, "dependency cycle: a -> b -> a"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Convert([]byte(tt.text), Options{Format: FormatMake, SourceName: "Makefile"})
			pe, ok := err.(*ParseError)
			if !ok || pe.Line != tt.line || pe.Msg != tt.msg {
				t.Errorf("error = %v, want line %d: %s", err, tt.line, tt.msg)
			}
		})
	}
}

func TestMakeContinues(t *testing.T) {
	for line, want := range map[string]bool{
		"": false, "echo a": false, `echo a \`: true, `echo a\\`: false, `echo a\\\`: true, `\`: true,
	} {
		if got := makeContinues(line); got != want {
			t.Errorf("makeContinues(%q) = %v, want %v", line, got, want)
		}
	}
}

// A line that ends in an escaped backslash is whole: the next line is a
// command of its own.
func TestMakeLinesEscapedBackslash(t *testing.T) {
	got := makeLines("t:\n\techo a\\\\\n\techo b\n")
	want := []makeLine{
		{n: 1, text: "t:"},
		{n: 2, text: `echo a\\`, recipe: true},
		{n: 3, text: "echo b", recipe: true},
		{n: 4, text: ""},
	}
	if !slices.Equal(got, want) {
		t.Errorf("makeLines = %+v\nwant        %+v", got, want)
	}
}

func TestMakeEvaluated(t *testing.T) {
	for line, want := range map[string]bool{
		"$(info starting)": true, "$(shell mkdir -p build)": true, "$(eval $(call rule,a))": true,
		"${info a: b}": true, "$(TARGET): dep": false, "$(NAME) = x": false, "$(open": false,
		"$@": false, "build: dep": false, "": false,
	} {
		if got := makeEvaluated(line); got != want {
			t.Errorf("makeEvaluated(%q) = %v, want %v", line, got, want)
		}
	}
}

// A line make only evaluates is an item, also when it has a colon; it is no
// error of the Makefile and no target.
func TestReadMakeEvaluatedLine(t *testing.T) {
	text := "$(shell mkdir -p build)\n$(info Building: now)\n.PHONY: a\na:\n\techo a\n"
	res, err := Convert([]byte(text), Options{Format: FormatMake, SourceName: "Makefile"})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	var items []string
	for _, it := range res.Items {
		items = append(items, it.Name)
	}
	if want := []string{"$(shell mkdir -p build)", "$(info Building: now)"}; !slices.Equal(items, want) {
		t.Errorf("items = %q, want %q", items, want)
	}
	if len(res.Tasks) != 1 || res.Tasks[0].Source != "a" || res.Tasks[0].Status != Converted {
		t.Errorf("tasks = %+v, want a converted", res.Tasks)
	}
}

func TestMakePrefixed(t *testing.T) {
	values := map[string]string{"Q": "@", "V": " $(Q)", "GO": "go", "E": "", "IGN": "-", "LOOP": "$(LOOP)", "F": "$(shell x)"}
	for name, want := range map[string]bool{
		"Q": true, "V": true, "IGN": true, "GO": false, "E": false, "LOOP": false, "F": false, "UNSET": false,
	} {
		if got := makePrefixed(name, values, 0); got != want {
			t.Errorf("makePrefixed(%s) = %v, want %v", name, got, want)
		}
	}
}

// make reads the prefixes of a line after it expands it, so a variable can
// hold one. Rune would run "@echo" as a command.
func TestReadMakePrefixFromVariable(t *testing.T) {
	text := "Q ?= @\nGO = go\n.PHONY: quiet tool\nquiet:\n\t$(Q)echo hi\ntool:\n\t$(GO) version\n"
	res, err := Convert([]byte(text), Options{Format: FormatMake, SourceName: "Makefile"})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	quiet, tool := res.Tasks[0], res.Tasks[1]
	if want := []string{"the line starts with $(Q), whose value starts with a line prefix of make (@, - or +)"}; quiet.Status != Unsupported || !slices.Equal(quiet.Notes, want) {
		t.Errorf("quiet is %v with %q, want unsupported with %q", quiet.Status, quiet.Notes, want)
	}
	if tool.Status != Converted || !slices.Equal(tool.Body, []string{"{{GO}} version"}) {
		t.Errorf("tool is %v with body %q", tool.Status, tool.Body)
	}
	checkRunefile(t, res.Runefile)
}

// A value that is not UTF-8 is not written into the Runefile: Rune's shell
// refuses such text, and writing it by character would change it.
func TestReadMakeValueNotUTF8(t *testing.T) {
	text := "L = caf\xe9\n.PHONY: show\nshow:\n\techo $(L)\n"
	res, err := Convert([]byte(text), Options{Format: FormatMake, SourceName: "Makefile"})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if len(res.Items) != 1 || res.Items[0].Name != "variable L" || res.Tasks[0].Status != Unsupported {
		t.Errorf("items = %+v, show is %v; want the variable reported and show unsupported", res.Items, res.Tasks[0].Status)
	}
	if !utf8.ValidString(res.Runefile) {
		t.Errorf("the Runefile is not UTF-8:\n%q", res.Runefile)
	}
}

func TestMakeLinesQuotedContinuation(t *testing.T) {
	got := makeLines("t:\n\techo 'a \\\n\tb'\n\techo \"it's\" \\\n\tfine\n\techo a \\\n\tb\n")
	want := []bool{false, true, true, false, false}
	for i, l := range got {
		if l.quoted != want[i] {
			t.Errorf("line %d (%q): quoted = %v, want %v", l.n, l.text, l.quoted, want[i])
		}
	}
}

// An assignment written without spaces keeps its operator: "CFLAGS+=x"
// appends to CFLAGS, and is not a variable named "CFLAGS+".
func TestReadMakeAssignmentOperators(t *testing.T) {
	text := "CFLAGS=-Wall\nCFLAGS+=-O2\nGO?=go\nNOW!=date\n.PHONY: build tool stamp\n" +
		"build:\n\tcc $(CFLAGS) main.c\ntool:\n\t$(GO) version\nstamp:\n\techo $(NOW)\n"
	res, err := Convert([]byte(text), Options{Format: FormatMake, SourceName: "Makefile"})
	if err != nil {
		t.Fatal(err)
	}
	var items []string
	for _, it := range res.Items {
		items = append(items, it.Name)
	}
	if want := []string{"variable CFLAGS", "variable NOW"}; !slices.Equal(items, want) {
		t.Errorf("items = %q, want %q", items, want)
	}
	status := map[string]Status{}
	for _, task := range res.Tasks {
		status[task.Source] = task.Status
	}
	want := map[string]Status{"build": Unsupported, "tool": Converted, "stamp": Unsupported}
	for name, st := range want {
		if status[name] != st {
			t.Errorf("%s is %v, want %v", name, status[name], st)
		}
	}
	if !strings.Contains(res.Runefile, `GO := env("GO", "go")`) {
		t.Errorf("the Runefile has no GO variable:\n%s", res.Runefile)
	}
}

// A conditional between the command lines of a rule does not end the rule.
func TestReadMakeConditionalInRecipe(t *testing.T) {
	text := ".PHONY: build clean\nbuild:\n\techo a\nifeq ($(X),1)\n\techo b\nelse\n\techo d\nendif\n\techo c\nclean:\n\trm x\n"
	res, err := Convert([]byte(text), Options{Format: FormatMake, SourceName: "Makefile"})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	build, clean := res.Tasks[0], res.Tasks[1]
	if want := []string{"part of its recipe is in the conditional block of line 4"}; build.Status != Unsupported || !slices.Equal(build.Notes, want) {
		t.Errorf("build is %v with %q, want unsupported with %q", build.Status, build.Notes, want)
	}
	if clean.Status != Converted || !slices.Equal(clean.Body, []string{"rm x"}) {
		t.Errorf("clean is %v with body %q", clean.Status, clean.Body)
	}
	checkRunefile(t, res.Runefile)
}

// A conditional that holds rules of its own leaves the rule before it alone.
func TestReadMakeConditionalAfterRule(t *testing.T) {
	text := ".PHONY: build\nbuild:\n\techo a\nifdef X\nextra:\n\techo x\nendif\n"
	res, err := Convert([]byte(text), Options{Format: FormatMake, SourceName: "Makefile"})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if len(res.Tasks) != 1 || res.Tasks[0].Status != Converted || !slices.Equal(res.Tasks[0].Body, []string{"echo a"}) {
		t.Errorf("tasks = %+v, want build converted", res.Tasks)
	}
}

func TestMakeRecipeFirst(t *testing.T) {
	tests := []struct {
		text string
		want bool
	}{
		{"\techo a\n", true},
		{"\n# note\n\techo a\n", true},
		{"ifdef Y\n\techo a\nendif\n", true},
		{"else\n\techo a\n", true},
		{"extra:\n\techo a\n", false},
		{"A = 1\n\techo a\n", false},
		{"\t\n", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := makeRecipeFirst(makeLines(tt.text)); got != tt.want {
			t.Errorf("makeRecipeFirst(%q) = %v, want %v", tt.text, got, tt.want)
		}
	}
}

// A target whose name starts with the word of a conditional is a target.
func TestReadMakeTargetNamedLikeConditional(t *testing.T) {
	text := ".PHONY: ifdef-check\nifdef-check:\n\techo a\n"
	res, err := Convert([]byte(text), Options{Format: FormatMake, SourceName: "Makefile"})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if len(res.Tasks) != 1 || res.Tasks[0].Source != "ifdef-check" || res.Tasks[0].Status != Converted {
		t.Errorf("tasks = %+v, want ifdef-check converted", res.Tasks)
	}
}

// A make variable name can hold a dash, and so can a Rune name.
func TestReadMakeVariableWithDash(t *testing.T) {
	res, err := Convert([]byte("out-dir = dist\n.PHONY: show\nshow:\n\t@echo $(out-dir)\n"), Options{Format: FormatMake, SourceName: "Makefile"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Runefile, "out-dir := \"dist\"\n") || !strings.Contains(res.Runefile, "    @echo {{out-dir}}\n") || res.Unsupported != 0 {
		t.Errorf("Runefile:\n%s\nreport:\n%s", res.Runefile, res.Report)
	}
	checkRunefile(t, res.Runefile)
}
