package taskimport

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

var (
	yamlErrorLine = regexp.MustCompile(`line (\d+)`)
	taskAction    = regexp.MustCompile(`\{\{.*?\}\}`)
	taskVarRef    = regexp.MustCompile(`^\{\{\s*\.([A-Za-z_][A-Za-z0-9_]*)\s*\}\}$`)
)

const taskEnvNote = "Task keeps a value the environment already has; Rune's [env] replaces it"

// taskGlobal are the top-level keys that change how every task runs.
var taskGlobal = map[string]bool{"dotenv": true, "set": true, "shopt": true, "method": true, "run": true}

// taskfile is what the top level of a Taskfile gives every task.
type taskfile struct {
	good   map[string]bool // converted variables
	bad    map[string]int  // line of each variable that did not convert
	env    []string        // [env] attributes of the top-level env
	silent bool
}

// readTaskfile reads the supported subset of a version 3 Taskfile. Includes
// are never followed and no variable command is run.
func readTaskfile(text string) (*source, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(text), &doc); err != nil {
		line, msg := 1, strings.TrimPrefix(err.Error(), "yaml: ")
		if m := yamlErrorLine.FindStringSubmatch(msg); m != nil {
			line, _ = strconv.Atoi(m[1])
			msg = strings.TrimPrefix(msg, m[0]+": ")
		}
		return nil, &ParseError{line, msg}
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, &ParseError{1, "the Taskfile is not a mapping"}
	}
	root := doc.Content[0]
	src := &source{}
	tf := &taskfile{}
	var (
		tasks   *yaml.Node
		vars    []*srcVar
		version bool
	)
	for i := 0; i+1 < len(root.Content); i += 2 {
		key, value := root.Content[i], root.Content[i+1]
		switch key.Value {
		case "version":
			if !taskVersion(value.Value) {
				return nil, &ParseError{key.Line, fmt.Sprintf("Taskfile version %q is not supported (want 3)", value.Value)}
			}
			version = true
		case "tasks":
			if value.Kind != yaml.MappingNode {
				return nil, &ParseError{key.Line, "tasks is not a mapping"}
			}
			tasks = value
		case "vars":
			vars = taskVars(value)
		case "env":
			for _, e := range taskPairs(value) {
				text, ok := taskScalar(e.value)
				name, okName := Quote(e.key.Value)
				lit, okValue := Quote(text)
				if !ok || !okName || !okValue || strings.Contains(text, "{{") {
					src.items = append(src.items, Item{
						Line: e.key.Line, Name: "env " + e.key.Value, Global: true,
						Note: "its value is not literal text, so no task has it",
					})
					continue
				}
				tf.env = append(tf.env, "env("+name+", "+lit+")")
			}
		case "silent":
			tf.silent = taskTrue(value)
		default:
			note := "the key is not converted"
			switch {
			case key.Value == "includes":
				note = "included Taskfiles are not read, so their tasks are not imported"
			case taskGlobal[key.Value]:
				note = "the key is not converted, and it changes how every task runs"
			}
			src.items = append(src.items, Item{Line: key.Line, Name: key.Value, Note: note, Global: taskGlobal[key.Value]})
		}
	}
	if !version {
		return nil, &ParseError{1, "the Taskfile has no version"}
	}
	tf.good, tf.bad = src.addVars(vars)
	seen := map[string]bool{}
	for _, e := range taskPairs(tasks) {
		if seen[e.key.Value] {
			return nil, &ParseError{e.key.Line, fmt.Sprintf("task %q is defined twice", e.key.Value)}
		}
		seen[e.key.Value] = true
		src.tasks = append(src.tasks, taskTask(e.key, e.value, tf))
	}
	// Task gives the arguments after -- to every task of the run.
	called := map[string]bool{}
	for _, t := range src.tasks {
		for _, c := range slices.Concat(t.Deps, t.Post) {
			called[c.Name] = true
		}
	}
	for _, t := range src.tasks {
		if called[t.Source] && len(t.Params) > 0 && t.Status != Unsupported {
			t.review("Task gives the arguments after -- to every task; here it gets them only when run by name")
		}
	}
	return src, nil
}

// taskVersion reports whether a Taskfile version is 3 or 3.x.
func taskVersion(v string) bool {
	return v == "3" || strings.HasPrefix(v, "3.")
}

// taskPair is one entry of a YAML mapping, in file order.
type taskPair struct{ key, value *yaml.Node }

// taskPairs lists the entries of a mapping. Anything else has none.
func taskPairs(n *yaml.Node) []taskPair {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	var pairs []taskPair
	for i := 0; i+1 < len(n.Content); i += 2 {
		pairs = append(pairs, taskPair{n.Content[i], n.Content[i+1]})
	}
	return pairs
}

// taskScalar returns the text of a node that is text, a number or a
// boolean.
func taskScalar(n *yaml.Node) (string, bool) {
	if n.Kind != yaml.ScalarNode || n.Tag == "!!null" {
		return "", false
	}
	return n.Value, true
}

// taskTrue reports whether a node is true as Task reads it: the key is a
// boolean, so YAML takes "True" and "yes" as well as "true".
func taskTrue(n *yaml.Node) bool {
	var b bool
	return n.Decode(&b) == nil && b
}

// taskStrings returns the items of a list of scalars.
func taskStrings(n *yaml.Node) ([]string, bool) {
	if n.Kind != yaml.SequenceNode {
		return nil, false
	}
	var out []string
	for _, item := range n.Content {
		s, ok := taskScalar(item)
		if !ok {
			return nil, false
		}
		out = append(out, s)
	}
	return out, true
}

// taskVars reads the top-level vars: a value that is text converts, one
// that runs a command or holds a structure does not.
func taskVars(n *yaml.Node) []*srcVar {
	var vars []*srcVar
	for _, e := range taskPairs(n) {
		v := &srcVar{name: e.key.Value, line: e.key.Line}
		text, ok := taskScalar(e.value)
		switch {
		case !validName(v.name):
			v.reason = "its name is not a valid Rune name"
		case ok:
			v.expr, v.refs, v.reason = taskValue(text)
		case slices.ContainsFunc(taskPairs(e.value), func(p taskPair) bool { return p.key.Value == "sh" }):
			v.reason = "its value runs a command (sh), which import never runs"
		default:
			v.reason = "its value is not text, a number or a boolean"
		}
		vars = append(vars, v)
	}
	return vars
}

// taskValue converts the value of a variable to a Runefile expression:
// text and {{.NAME}} references. refs are the names it uses.
func taskValue(value string) (expr string, refs []string, reason string) {
	var parts []string
	literal := func(s string) bool {
		if s == "" {
			return true
		}
		q, ok := Quote(s)
		parts = append(parts, q)
		return ok
	}
	rest := value
	for {
		loc := taskAction.FindStringIndex(rest)
		if loc == nil {
			break
		}
		m := taskVarRef.FindStringSubmatch(rest[loc[0]:loc[1]])
		if m == nil || m[1] == "CLI_ARGS" {
			return "", nil, "its value is not built from text and plain variables"
		}
		if !literal(rest[:loc[0]]) {
			return "", nil, "its value has a control character"
		}
		parts = append(parts, m[1])
		refs = append(refs, m[1])
		rest = rest[loc[1]:]
	}
	if strings.Contains(rest, "{{") {
		return "", nil, "its value is not built from text and plain variables"
	}
	if !literal(rest) {
		return "", nil, "its value has a control character"
	}
	if len(parts) == 0 {
		return `""`, nil, ""
	}
	return strings.Join(parts, " + "), refs, ""
}

// taskTemplate converts the templates of one command: {{.NAME}} of a
// converted variable becomes a Rune interpolation and {{.CLI_ARGS}} the
// task's own arguments, each quoted as one word, as Task passes them. args
// reports that the task needs a parameter for them.
func taskTemplate(text string, good map[string]bool, bad map[string]int) (out string, args bool, reason string) {
	var b strings.Builder
	rest := text
	for {
		loc := taskAction.FindStringIndex(rest)
		if loc == nil {
			break
		}
		action := rest[loc[0]:loc[1]]
		m := taskVarRef.FindStringSubmatch(action)
		// The text between the templates has no "{{"; Rune would still read
		// its "}}}}" as two braces.
		b.WriteString(EscapeBraces(rest[:loc[0]]))
		switch {
		case m == nil:
			return "", false, "the template " + action + " is not a plain variable"
		case m[1] == "CLI_ARGS":
			b.WriteString("{{quote_each(args)}}")
			args = true
		case good[m[1]]:
			b.WriteString("{{" + m[1] + "}}")
		case bad[m[1]] != 0:
			return "", false, fmt.Sprintf("it uses the variable %q of line %d, which was not converted", m[1], bad[m[1]])
		default:
			return "", false, "it uses {{." + m[1] + "}}, which the Taskfile does not set"
		}
		rest = rest[loc[1]:]
	}
	if strings.Contains(rest, "{{") {
		return "", false, "a template is not closed: " + text
	}
	b.WriteString(EscapeBraces(rest))
	return b.String(), args, ""
}

// taskLiteral returns a value that must be plain text as a Runefile string.
func taskLiteral(n *yaml.Node) (string, bool) {
	text, ok := taskScalar(n)
	if !ok || strings.Contains(text, "{{") {
		return "", false
	}
	return Quote(text)
}

// taskTask converts one task. A key outside the supported subset makes the
// whole task unsupported.
func taskTask(key, value *yaml.Node, tf *taskfile) *Task {
	t := &Task{Source: key.Value, Line: key.Line}
	if strings.Contains(t.Source, "*") {
		t.unsupported("a wildcard task name")
		t.NoWrap = true
		return t
	}
	var (
		cmds           *yaml.Node
		deps           []Call
		confirm, dir   string
		env            []string
		cache          string
		silent, ignore bool
	)
	switch value.Kind {
	case yaml.ScalarNode, yaml.SequenceNode:
		cmds = value
	case yaml.MappingNode:
	default:
		t.unsupported("the task is not a command, a list or a mapping")
		return t
	}
	var sources, generates []string
	for _, e := range taskPairs(value) {
		var ok bool
		switch e.key.Value {
		case "desc":
			t.Doc = strings.Join(strings.Fields(e.value.Value), " ")
		case "cmds", "cmd":
			cmds = e.value
		case "deps":
			if deps, ok = taskCalls(e.value); !ok {
				t.unsupported("the deps are not a list of task names")
			}
		case "internal":
			if taskTrue(e.value) {
				// Task refuses to run an internal task by name.
				t.Attrs, t.NoWrap = append(t.Attrs, "private"), true
			}
		case "prompt":
			if lit, isText := taskLiteral(e.value); isText {
				confirm = "confirm(" + lit + ")"
			} else {
				t.unsupported("the prompt is not one line of plain text")
			}
		case "silent":
			silent = taskTrue(e.value)
		case "ignore_error":
			ignore = taskTrue(e.value)
		case "env":
			for _, p := range taskPairs(e.value) {
				name, okName := Quote(p.key.Value)
				lit, okValue := taskLiteral(p.value)
				if !okName || !okValue {
					t.unsupported(fmt.Sprintf("the value of env %s is not literal text", p.key.Value))
					continue
				}
				env = append(env, "env("+name+", "+lit+")")
			}
		case "dir":
			if lit, isText := taskLiteral(e.value); isText {
				dir = "working-directory(" + lit + ")"
			} else {
				t.unsupported("the dir is not a literal path")
			}
		case "sources":
			if sources, ok = taskGlobs(e.value); !ok {
				t.unsupported("the sources are not a list of literal patterns")
			}
		case "generates":
			if generates, ok = taskGlobs(e.value); !ok {
				t.unsupported("the generates are not a list of literal patterns")
			}
		case "summary", "aliases", "label":
			t.review(fmt.Sprintf("the key %q is not carried over", e.key.Value))
		default:
			t.unsupported(fmt.Sprintf("the key %q is not converted", e.key.Value))
		}
	}
	switch {
	case len(sources) > 0:
		cache = "cache(inputs = [" + strings.Join(sources, ", ") + "]"
		if len(generates) > 0 {
			cache += ", outputs = [" + strings.Join(generates, ", ") + "]"
		}
		cache += ")"
		t.review("Rune caches by the content of the inputs, the task body and the variables, and logs a hit")
	case len(generates) > 0:
		t.unsupported("generates without sources")
	}

	lines, before, after, why := taskCmds(cmds)
	if why != "" {
		t.unsupported(why)
	}
	if len(deps) > 0 && len(before) > 0 {
		t.unsupported("task calls at the start of cmds together with deps: Task runs the deps concurrently and the calls one at a time")
	}
	t.Deps, t.Post = append(deps, before...), after
	called := map[string]bool{}
	for _, c := range slices.Concat(t.Deps, t.Post) {
		if called[c.Name] {
			t.unsupported(fmt.Sprintf("it runs %q more than once, and Rune runs a task once per invocation", c.Name))
			break
		}
		called[c.Name] = true
	}

	// Attributes in one fixed order, so that the output does not depend on
	// the order of the keys.
	if confirm != "" {
		t.Attrs = append(t.Attrs, confirm)
	}
	if len(deps) > 1 {
		t.Attrs = append(t.Attrs, "parallel")
	}
	if dir != "" {
		t.Attrs = append(t.Attrs, dir)
		t.review("Task creates a missing directory; Rune does not")
	}
	t.Attrs = append(t.Attrs, taskEnv(tf.env, env)...)
	if len(tf.env)+len(env) > 0 {
		t.review(taskEnvNote)
	}
	if cache != "" {
		t.Attrs = append(t.Attrs, cache)
	}

	for i := range lines {
		text, args, why := taskTemplate(lines[i].text, tf.good, tf.bad)
		if why != "" {
			t.unsupported(why)
			return t
		}
		if args {
			t.Params = []string{"*args"}
		}
		lines[i].text = text
		lines[i].noEcho = lines[i].noEcho || silent || tf.silent
		lines[i].ignore = lines[i].ignore || ignore
	}
	// The parameter hides a variable of its name in the body.
	if len(t.Params) > 0 && slices.ContainsFunc(lines, func(l cmdLine) bool { return strings.Contains(l.text, "{{args}}") }) {
		t.unsupported(`it uses {{.CLI_ARGS}} and the variable "args", which has the name of the parameter that takes the arguments`)
	}
	body, why := shellBody(lines, true)
	if why != "" {
		t.unsupported(why)
	}
	t.Body = body
	return t
}

// taskEnv joins the [env] attributes of the file and of the task. The task
// wins for a name both set.
func taskEnv(file, task []string) []string {
	var out []string
	for _, attr := range file {
		name, _, _ := strings.Cut(attr, ", ")
		if !slices.ContainsFunc(task, func(a string) bool { return strings.HasPrefix(a, name+", ") }) {
			out = append(out, attr)
		}
	}
	return append(out, task...)
}

// taskGlobs returns a list of patterns as Runefile strings.
func taskGlobs(n *yaml.Node) ([]string, bool) {
	items, ok := taskStrings(n)
	if !ok {
		return nil, false
	}
	for i, s := range items {
		lit, printable := Quote(s)
		if !printable || strings.Contains(s, "{{") {
			return nil, false
		}
		items[i] = lit
	}
	return items, true
}

// taskCalls reads a deps list: task names, or mappings with only a task
// key.
func taskCalls(n *yaml.Node) ([]Call, bool) {
	if n.Kind != yaml.SequenceNode {
		return nil, false
	}
	var calls []Call
	for _, item := range n.Content {
		name, ok := taskScalar(item)
		if pairs := taskPairs(item); len(pairs) == 1 && pairs[0].key.Value == "task" {
			name, ok = taskScalar(pairs[0].value)
		}
		if !ok || name == "" || strings.Contains(name, "{{") {
			return nil, false
		}
		calls = append(calls, Call{Name: name})
	}
	return calls, true
}

// taskCmds splits the commands of a task into the calls to other tasks
// before the shell commands, the shell commands, and the calls after them.
func taskCmds(n *yaml.Node) (lines []cmdLine, before, after []Call, reason string) {
	if n == nil {
		return nil, nil, nil, ""
	}
	items := []*yaml.Node{n}
	if n.Kind == yaml.SequenceNode {
		items = n.Content
	}
	for _, item := range items {
		if text, ok := taskScalar(item); ok {
			if len(after) > 0 {
				return nil, nil, nil, "a task call between shell commands"
			}
			lines = append(lines, cmdLine{text: text})
			continue
		}
		pairs := taskPairs(item)
		if len(pairs) == 0 {
			return nil, nil, nil, "a command that is not text or a mapping"
		}
		var c cmdLine
		call, isCmd := "", false
		for _, p := range pairs {
			text, ok := taskScalar(p.value)
			switch {
			case p.key.Value == "cmd" && ok:
				c.text, isCmd = text, true
			case p.key.Value == "task" && ok && !strings.Contains(text, "{{"):
				call = text
			case p.key.Value == "silent" && ok:
				c.noEcho = taskTrue(p.value)
			case p.key.Value == "ignore_error" && ok:
				c.ignore = taskTrue(p.value)
			case p.key.Value == "vars":
				return nil, nil, nil, "a task call with vars"
			default:
				return nil, nil, nil, fmt.Sprintf("a command with the key %q", p.key.Value)
			}
		}
		switch {
		case isCmd == (call != ""):
			return nil, nil, nil, "a command that is not one shell command or one task call"
		case isCmd && len(after) > 0:
			return nil, nil, nil, "a task call between shell commands"
		case isCmd:
			lines = append(lines, c)
		case len(lines) == 0:
			before = append(before, Call{Name: call})
		default:
			after = append(after, Call{Name: call})
		}
	}
	return lines, before, after, ""
}
