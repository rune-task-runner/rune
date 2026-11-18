package taskimport

import (
	"slices"
	"testing"
)

func TestParseFormat(t *testing.T) {
	for _, name := range []string{"justfile", "make", "task", "npm"} {
		f, err := ParseFormat(name)
		if err != nil || string(f) != name {
			t.Errorf("ParseFormat(%q) = %q, %v", name, f, err)
		}
	}
	_, err := ParseFormat("gradle")
	if want := `unknown format "gradle" (want justfile, make, task or npm)`; err == nil || err.Error() != want {
		t.Errorf("ParseFormat(gradle) error = %v, want %q", err, want)
	}
}

func TestFormatOf(t *testing.T) {
	want := map[string]Format{
		"justfile": FormatJust, "Justfile": FormatJust, ".justfile": FormatJust,
		"GNUmakefile": FormatMake, "makefile": FormatMake, "Makefile": FormatMake,
		"Taskfile.yml": FormatTask, "Taskfile.yaml": FormatTask, "taskfile.yml": FormatTask, "taskfile.yaml": FormatTask,
		"package.json": FormatNPM,
	}
	for name, format := range want {
		if got, ok := FormatOf(name); !ok || got != format {
			t.Errorf("FormatOf(%q) = %q, %v, want %q", name, got, ok, format)
		}
	}
	for _, name := range []string{"Runefile", "build.gradle", "tasks.yml", "other.json", "ci.just", "rules.mk", ""} {
		if got, ok := FormatOf(name); ok {
			t.Errorf("FormatOf(%q) = %q, want no format", name, got)
		}
	}
}

func TestCandidates(t *testing.T) {
	want := []string{
		"justfile", "Justfile", ".justfile",
		"GNUmakefile", "makefile", "Makefile",
		"Taskfile.yml", "Taskfile.yaml", "taskfile.yml", "taskfile.yaml",
		"package.json",
	}
	if got := Candidates(""); !slices.Equal(got, want) {
		t.Errorf("Candidates() = %q, want %q", got, want)
	}
	if got := Candidates(FormatMake); !slices.Equal(got, want[3:6]) {
		t.Errorf("Candidates(make) = %q, want %q", got, want[3:6])
	}
}

func TestStatusString(t *testing.T) {
	for s, want := range map[Status]string{
		Converted: "converted", Review: "review", Unsupported: "unsupported", Wrapper: "wrapper",
	} {
		if s.String() != want {
			t.Errorf("Status(%d).String() = %q, want %q", s, s.String(), want)
		}
	}
}

func TestRunner(t *testing.T) {
	for f, want := range map[Format]string{FormatJust: "just", FormatMake: "make", FormatTask: "task", FormatNPM: "npm"} {
		if got := f.Runner(); got != want {
			t.Errorf("%q.Runner() = %q, want %q", f, got, want)
		}
	}
}

func TestParseErrorMessage(t *testing.T) {
	err := &ParseError{Line: 7, Msg: "unexpected text"}
	if got := err.Error(); got != "line 7: unexpected text" {
		t.Errorf("Error() = %q", got)
	}
}

func TestOneLine(t *testing.T) {
	for in, want := range map[string]string{
		"": "", "one": "one", "a template is not closed: echo {{\nevil:\n    rm x": "a template is not closed: echo {{",
		"\nrest": "",
	} {
		if got := oneLine(in); got != want {
			t.Errorf("oneLine(%q) = %q, want %q", in, got, want)
		}
	}
}

// A note that quotes source text never brings a line break into the report
// or into the Runefile.
func TestNotesAreOneLine(t *testing.T) {
	task := &Task{}
	task.review("a\nb")
	task.unsupported("c\nd")
	if want := []string{"a", "c"}; !slices.Equal(task.Notes, want) || task.Status != Unsupported {
		t.Errorf("Notes = %q, Status = %v, want %q, unsupported", task.Notes, task.Status, want)
	}
}

func TestConvertSkipsByteOrderMark(t *testing.T) {
	tests := []struct {
		format Format
		text   string
	}{
		{FormatNPM, "\ufeff{\"scripts\": {\"build\": \"echo a\"}}"},
		{FormatJust, "\ufeffbuild:\n    echo a\n"},
		{FormatMake, "\ufeff.PHONY: build\nbuild:\n\techo a\n"},
		{FormatTask, "\ufeffversion: '3'\ntasks:\n  build: echo a\n"},
	}
	for _, tt := range tests {
		res, err := Convert([]byte(tt.text), Options{Format: tt.format, SourceName: "source"})
		if err != nil {
			t.Errorf("%s: %v", tt.format, err)
			continue
		}
		if len(res.Tasks) != 1 || res.Tasks[0].Source != "build" || res.Tasks[0].Status != Converted {
			t.Errorf("%s: tasks = %+v, want build converted", tt.format, res.Tasks)
		}
	}
}
