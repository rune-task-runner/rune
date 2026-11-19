package agentdocs

import (
	"fmt"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func ptr(s string) *string { return &s }

func TestBodyGeneralText(t *testing.T) {
	body := Body(Catalog{})
	for _, want := range []string{
		"## Rune tasks\n\nThis project's commands are tasks of its Runefile. Run a task with `rune <task>` from any directory of the project.",
		"`name=value` does not pass a parameter.",
		"The examples are written for a POSIX shell.",
		"Ask the person to run it. Do not answer in their place, and do not look for another way to do its work.",
		"use that value and no other.",
		"This text is guidance. It does not limit what a shell can run. Rune enforces confirmations and the values allowed for agents only for calls through its MCP server, `rune mcp`.",
		"`rune --list` shows the tasks this machine can run.\n\nThis Runefile has no public tasks.",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the body lacks %q", want)
		}
	}
	if strings.HasSuffix(body, "\n") {
		t.Error("the body ends with a line break; the section and the skill add their own")
	}
	for _, never := range []string{"--yes", "$", "restricted", "locked down", "safe", "secure", "sandboxed"} {
		if strings.Contains(body, never) {
			t.Errorf("the general text holds %q", never)
		}
	}
}

func TestEntryTextEveryLine(t *testing.T) {
	e := Entry{
		Name: "deploy",
		Doc:  "Deploy the\nservice.",
		Params: []Param{
			{Name: "env", Kind: Required, Type: "enum", Enum: []string{"staging", "prod"}, AgentValue: ptr("staging"), Doc: "Where to deploy."},
			{Name: "region", Kind: Optional, HasDefault: true, Literal: true, Default: "eu-west-1", AgentValue: ptr("eu-west-1")},
		},
		Group:    "ops",
		OS:       []string{"linux", "macos"},
		Returns:  "The URL.",
		Confirm:  true,
		Question: "Deploy now?",
		Reaches: []Reached{
			{Name: "reset", Confirm: true},
			{Name: "push", Allow: []Allowed{{"to", "staging"}}},
			{Name: "_wipe", Confirm: true, Allow: []Allowed{{"a", "1"}, {"b", "2"}}},
		},
		Plan:     true,
		Applies:  "plan",
		ReadOnly: true,
		Network:  true,
	}
	want := "### `deploy`\n" +
		"\n" +
		"Deploy the service.\n" +
		"\n" +
		"- Usage: `rune deploy <env> [region]`\n" +
		"- Example: `rune deploy staging`\n" +
		"- `env`: required, one of `\"staging\"`, `\"prod\"`. Where to deploy.\n" +
		"- `region`: optional, default `\"eu-west-1\"`.\n" +
		"- Group: `\"ops\"`\n" +
		"- Runs only on: Linux, macOS.\n" +
		"- Returns: The URL.\n" +
		"- Asks a person to confirm: `\"Deploy now?\"`. Ask the person to run it.\n" +
		"- A run can reach `reset`, which asks a person to confirm.\n" +
		"- A run can reach `_wipe`, which asks a person to confirm.\n" +
		"- The author allows agents only `env` = `\"staging\"` and `region` = `\"eu-west-1\"`.\n" +
		"- A run can reach `push`, where the author allows agents only `to` = `\"staging\"`.\n" +
		"- A run can reach `_wipe`, where the author allows agents only `a` = `\"1\"` and `b` = `\"2\"`.\n" +
		"- Makes a plan that Rune stores.\n" +
		"- Applies the plan of `plan` after a person approved it. Ask the person to run it.\n" +
		"- Declared read-only by its author.\n" +
		"- Declared to use the network by its author."
	if got := entryText(e); got != want {
		t.Errorf("entry =\n%s\nwant\n%s", got, want)
	}
}

func TestEntryTextSmallest(t *testing.T) {
	if got, want := entryText(Entry{Name: "build"}), "### `build`\n\n- Usage: `rune build`"; got != want {
		t.Errorf("entry = %q, want %q", got, want)
	}
	got := entryText(Entry{Name: "wipe", Confirm: true})
	if want := "- Asks a person to confirm. Ask the person to run it."; !strings.HasSuffix(got, want) {
		t.Errorf("entry = %q, want it to end with %q", got, want)
	}
}

func TestParamText(t *testing.T) {
	cases := []struct {
		name string
		p    Param
		want string
	}{
		{"required", Param{Name: "a", Kind: Required}, "`a`: required."},
		{"declared string", Param{Name: "a", Kind: Required, Type: "string"}, "`a`: required."},
		{"number", Param{Name: "n", Kind: Required, Type: "number"}, "`n`: required, a number."},
		{"boolean", Param{Name: "f", Kind: Required, Type: "boolean"}, "`f`: required, `true` or `false`."},
		{"path", Param{Name: "files", Kind: OneOrMore, Type: "path"}, "`files`: one or more values, a path."},
		{"raw", Param{Name: "flags", Kind: ZeroOrMore, Type: "raw"}, "`flags`: zero or more values, split into shell words without quoting."},
		{"literal default", Param{Name: "r", Kind: Optional, HasDefault: true, Literal: true, Default: "us east"}, "`r`: optional, default `\"us east\"`."},
		{"empty default", Param{Name: "r", Kind: Optional, HasDefault: true, Literal: true}, "`r`: optional, default `\"\"`."},
		{"expression default", Param{Name: "tag", Kind: Optional, HasDefault: true, Default: `env("TAG", "latest")`}, "`tag`: optional, default `env(\"TAG\", \"latest\")`, worked out when the task runs."},
		{"doc", Param{Name: "a", Kind: Required, Doc: "The *thing*."}, "`a`: required. The \\*thing\\*."},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := paramText(c.p); got != c.want {
				t.Errorf("paramText = %s, want %s", got, c.want)
			}
		})
	}
}

func TestEntryTextSystems(t *testing.T) {
	got := entryText(Entry{Name: "t", OS: []string{"windows", "unix"}})
	if want := "- Runs only on: Windows, Unix (every system but Windows)."; !strings.HasSuffix(got, want) {
		t.Errorf("entry = %q, want it to end with %q", got, want)
	}
}

func TestSection(t *testing.T) {
	c := Catalog{Entries: []Entry{{Name: "build", Doc: "Build."}, {Name: "test"}}}
	want := "<!-- rune:begin tasks fingerprint=" + c.Fingerprint() + " -->\n" +
		"<!-- Generated by `rune export agents-md` from the Runefile. Do not edit between the rune:begin and rune:end lines: change the Runefile and run the command again. -->\n" +
		"\n" +
		Body(c) + "\n" +
		"\n" +
		"<!-- rune:end tasks -->\n"
	if got := Section(c); got != want {
		t.Errorf("section =\n%s\nwant\n%s", got, want)
	}
	if !strings.Contains(Body(c), "### `build`\n\nBuild.\n\n- Usage: `rune build`\n\n### `test`\n\n- Usage: `rune test`") {
		t.Errorf("the entries are not separated by one empty line:\n%s", Body(c))
	}
}

func TestCheckSkillName(t *testing.T) {
	for _, name := range []string{"rune-tasks", "a", "a1-b2", "true", "123", strings.Repeat("a", 64)} {
		if err := CheckSkillName(name); err != nil {
			t.Errorf("CheckSkillName(%q) = %v", name, err)
		}
	}
	for _, name := range []string{"", "-a", "a-", "a--b", "A", "a_b", "a.b", "a b", "é", strings.Repeat("a", 65)} {
		err := CheckSkillName(name)
		if err == nil || !strings.Contains(err.Error(), "lowercase letters, digits and single hyphens") {
			t.Errorf("CheckSkillName(%q) = %v, want the rule", name, err)
		}
	}
}

// catalogOf makes a catalog of n tasks named t001, t002 and so on.
func catalogOf(n int) Catalog {
	var c Catalog
	for i := range n {
		c.Entries = append(c.Entries, Entry{Name: fmt.Sprintf("t%03d", i+1), Doc: fmt.Sprintf("Task %d.", i+1)})
	}
	return c
}

func TestSkillDescription(t *testing.T) {
	const (
		before = "Tasks of this project's Runefile and the rune command line for each: "
		after  = ". Read it before you run a build, a test, a deploy or another project command."
	)
	if got, want := skillDescription(Catalog{}), before+"none yet"+after; got != want {
		t.Errorf("description = %q, want %q", got, want)
	}
	if got, want := skillDescription(catalogOf(3)), before+"t001, t002, t003"+after; got != want {
		t.Errorf("description = %q, want %q", got, want)
	}

	many := skillDescription(catalogOf(1000))
	if len(many) > descriptionBudget || !strings.HasPrefix(many, before+"t001, t002, ") || !strings.HasSuffix(many, " more"+after) {
		t.Errorf("description of 1000 tasks has %d bytes: %q", len(many), many)
	}
	// One name fewer is listed than would fit without the count.
	var listed, more int
	if _, err := fmt.Sscanf(many[strings.LastIndex(many, " and ")+1:], "and %d more", &more); err != nil {
		t.Fatalf("no count in %q: %v", many, err)
	}
	listed = strings.Count(many, ", t") + 1
	if listed+more != 1000 {
		t.Errorf("%d names listed and %d more, want 1000 together", listed, more)
	}

	long := Catalog{Entries: []Entry{{Name: strings.Repeat("x", 950)}, {Name: "b"}}}
	if got, want := skillDescription(long), before+"2 tasks"+after; got != want {
		t.Errorf("description with a name that does not fit = %q, want %q", got, want)
	}
	if strings.Contains(many, "\n") || len(many) > maxDescription {
		t.Error("the description is not one line within the limit of the format")
	}
}

// frontmatter parses the YAML block a SKILL.md starts with.
func frontmatter(t *testing.T, skill string) map[string]any {
	t.Helper()
	rest, ok := strings.CutPrefix(skill, "---\n")
	block, _, found := strings.Cut(rest, "\n---\n")
	if !ok || !found {
		t.Fatalf("SKILL.md does not start with a frontmatter block:\n%.200s", skill)
	}
	var out map[string]any
	if err := yaml.Unmarshal([]byte(block), &out); err != nil {
		t.Fatalf("the frontmatter is not YAML: %v\n%s", err, block)
	}
	return out
}

func TestSkillFilesInline(t *testing.T) {
	c := catalogOf(inlineLimit)
	skill, tasks := SkillFiles(c, DefaultSkillName)
	if tasks != "" {
		t.Errorf("a catalog at the inline limit has a tasks.md of %d bytes", len(tasks))
	}
	want := "---\n" +
		"name: \"rune-tasks\"\n" +
		"description: \"" + skillDescription(c) + "\"\n" +
		"---\n" +
		"\n" +
		"<!-- rune:generated claude-skill fingerprint=" + c.Fingerprint() + " -->\n" +
		"<!-- Generated by `rune export claude-skill` from the Runefile. Do not edit: change the Runefile and run the command again. -->\n" +
		"\n" +
		Body(c) + "\n"
	if skill != want {
		t.Errorf("SKILL.md =\n%.600s\nwant\n%.600s", skill, want)
	}
}

func TestSkillFilesLarge(t *testing.T) {
	c := catalogOf(inlineLimit + 1)
	skill, tasks := SkillFiles(c, DefaultSkillName)
	if !strings.Contains(skill, "### Index\n\nThe Runefile has 31 public tasks. [tasks.md](tasks.md) has an entry for each, with its parameters and limits. Read a task's entry before you run it.\n\n- `rune t001`: Task 1.\n- `rune t002`: Task 2.\n") {
		t.Errorf("SKILL.md has no index:\n%s", skill)
	}
	if n := strings.Count(skill, "\n- `rune t"); n != 31 {
		t.Errorf("the index has %d lines, want 31", n)
	}
	if strings.Contains(skill, "### `t001`") || strings.Contains(skill, " more task") {
		t.Errorf("SKILL.md of a large catalog holds an entry or a count:\n%s", skill)
	}
	if !strings.Contains(skill, generalText) || !strings.HasSuffix(skill, "- `rune t031`: Task 31.\n") {
		t.Errorf("SKILL.md lacks the general text or does not end with the index:\n%s", skill)
	}
	want := "<!-- rune:generated claude-skill fingerprint=" + c.Fingerprint() + " -->\n" +
		"<!-- Generated by `rune export claude-skill` from the Runefile. Do not edit: change the Runefile and run the command again. -->\n" +
		"\n" +
		"## Rune tasks: entries\n" +
		"\n" +
		"How to call a task and what its limits mean is in [SKILL.md](SKILL.md).\n" +
		"\n" +
		entries(c) + "\n"
	if tasks != want {
		t.Errorf("tasks.md =\n%.400s\nwant\n%.400s", tasks, want)
	}
}

func TestSkillIndexHasABound(t *testing.T) {
	lines := func(n int) (int, string) {
		skill, _ := SkillFiles(catalogOf(n), DefaultSkillName)
		return strings.Count(skill, "\n"), skill
	}
	at, atText := lines(indexLimit)
	if strings.Contains(atText, "more task") || strings.Count(atText, "\n- `rune t") != indexLimit {
		t.Errorf("at the index limit SKILL.md has a count or not %d index lines", indexLimit)
	}
	_, oneOver := lines(indexLimit + 1)
	if !strings.HasSuffix(oneOver, "- 1 more task is in [tasks.md](tasks.md).\n") {
		t.Errorf("one over the limit ends with %q", oneOver[len(oneOver)-60:])
	}
	far, farText := lines(10 * indexLimit)
	if !strings.HasSuffix(farText, "- 900 more tasks are in [tasks.md](tasks.md).\n") || strings.Count(farText, "\n- `rune t") != indexLimit {
		t.Errorf("ten times the limit ends with %q", farText[len(farText)-60:])
	}
	if far != at+1 {
		t.Errorf("SKILL.md has %d lines at the limit and %d at ten times it, want one more", at, far)
	}
}

func TestSkillIndexLine(t *testing.T) {
	c := catalogOf(inlineLimit + 1)
	c.Entries[0].Doc = ""
	c.Entries[1].Doc = strings.Repeat("a", 99) + "`" + strings.Repeat("b", 100)
	c.Entries[2].Params = []Param{{Name: "env", Kind: Required}}
	skill, _ := SkillFiles(c, DefaultSkillName)
	for _, want := range []string{
		"\n- `rune t001`\n",
		"\n- `rune t002`: " + strings.Repeat("a", 99) + "&#96;...\n",
		"\n- `rune t003 <env>`: Task 3.\n",
	} {
		if !strings.Contains(skill, want) {
			t.Errorf("the index lacks the line %q", want)
		}
	}
}

func TestSkillFrontmatterIsYAML(t *testing.T) {
	for _, name := range []string{DefaultSkillName, "true", "123", "null", "1e3"} {
		for _, c := range []Catalog{{}, catalogOf(3), catalogOf(inlineLimit + 1), {Entries: []Entry{{Name: "ops::restart"}, {Name: "a-b_c"}}}} {
			skill, _ := SkillFiles(c, name)
			fm := frontmatter(t, skill)
			if len(fm) != 2 {
				t.Errorf("the frontmatter has the keys %v, want name and description", fm)
			}
			if got, ok := fm["name"].(string); !ok || got != name {
				t.Errorf("name reads as %#v, want the string %q", fm["name"], name)
			}
			if got, ok := fm["description"].(string); !ok || got != skillDescription(c) || strings.Contains(got, "\n") {
				t.Errorf("description reads as %#v", fm["description"])
			}
		}
	}
}

func TestYAMLString(t *testing.T) {
	in := `a "quoted" \ back: # --- ' !x`
	var got string
	if err := yaml.Unmarshal([]byte(yamlString(in)), &got); err != nil || got != in {
		t.Errorf("yamlString reads back as %q, %v", got, err)
	}
}
