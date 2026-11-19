package agentdocs

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestParseFormat(t *testing.T) {
	for _, name := range []string{"agents-md", "claude-skill"} {
		if f, ok := ParseFormat(name); !ok || string(f) != name {
			t.Errorf("ParseFormat(%q) = %q, %v", name, f, ok)
		}
	}
	for _, name := range []string{"", "markdown", "AGENTS-MD"} {
		if f, ok := ParseFormat(name); ok || f != "" {
			t.Errorf("ParseFormat(%q) = %q, %v, want no format", name, f, ok)
		}
	}
	if got := Formats(); !slices.Equal(got, []string{"agents-md", "claude-skill"}) {
		t.Errorf("Formats() = %v", got)
	}
}

func TestPlanSection(t *testing.T) {
	c := catalogOf(2)
	section := Section(c)

	created, err := PlanSection(c, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if created.Status() != "created" || string(created.New) != section || created.Owned || created.Tasks != 2 || created.Size != len(section) {
		t.Errorf("a new file: %+v", created)
	}
	if got := created.CheckState(c.Fingerprint()); got != StateMissing {
		t.Errorf("check of a file that does not exist = %q", got)
	}

	appended, _ := PlanSection(c, []byte("# Notes\n"), true)
	if appended.Status() != "updated" || appended.Owned || appended.CheckState(c.Fingerprint()) != StateMissing {
		t.Errorf("a file with no section: status %q, %+v", appended.Status(), appended)
	}
	if appended.Size != len(section) {
		t.Errorf("Size = %d, want the %d bytes of the section", appended.Size, len(section))
	}

	// In a file with CR LF the section is written with CR LF, and its size
	// is the size of those bytes.
	crlf, _ := PlanSection(c, []byte("# Notes\r\n"), true)
	if want := len(strings.ReplaceAll(section, "\n", "\r\n")); crlf.Size != want || !strings.HasSuffix(string(crlf.New), "<!-- rune:end tasks -->\r\n") {
		t.Errorf("Size in a CR LF file = %d, want the %d bytes written", crlf.Size, want)
	}

	same, _ := PlanSection(c, appended.New, true)
	if same.Status() != "unchanged" || !same.Owned || same.OldFingerprint != c.Fingerprint() || same.CheckState(c.Fingerprint()) != StateCurrent {
		t.Errorf("a current file: status %q, %+v", same.Status(), same)
	}
	if string(same.New) != string(appended.New) {
		t.Error("an unchanged plan has other bytes than the file")
	}

	grown := catalogOf(3)
	stale, _ := PlanSection(grown, appended.New, true)
	if stale.Status() != "updated" || stale.CheckState(grown.Fingerprint()) != "stale: the Runefile changed since the export" {
		t.Errorf("after the catalog changed: status %q, check %q", stale.Status(), stale.CheckState(grown.Fingerprint()))
	}

	edited := strings.Replace(string(appended.New), "Task 1.", "Task one.", 1)
	hand, _ := PlanSection(c, []byte(edited), true)
	if got, want := hand.CheckState(c.Fingerprint()), "stale: the text differs from what this rune generates (edited by hand, or written by another rune version)"; got != want {
		t.Errorf("after a hand edit: check %q, want %q", got, want)
	}

	_, err = PlanSection(c, []byte("<!-- rune:begin tasks -->\n"), true)
	var refusal *Refusal
	if !errors.As(err, &refusal) {
		t.Errorf("broken markers: err = %v, want a refusal", err)
	}
}

func planSkill(t *testing.T, c Catalog, existing map[string][]byte) map[string]File {
	t.Helper()
	files, err := PlanSkill(c, DefaultSkillName, existing)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]File{}
	for _, f := range files {
		out[f.Path] = f
	}
	return out
}

func TestPlanSkill(t *testing.T) {
	small, large := catalogOf(3), catalogOf(inlineLimit+1)
	smallSkill, _ := SkillFiles(small, DefaultSkillName)
	largeSkill, largeTasks := SkillFiles(large, DefaultSkillName)
	byHand := []byte("---\nname: mine\n---\n\nBy hand.\n")

	t.Run("a new small skill is one file", func(t *testing.T) {
		files := planSkill(t, small, nil)
		f := files[SkillFile]
		if len(files) != 1 || f.Status() != "created" || string(f.New) != smallSkill || f.Tasks != 3 || f.Size != len(smallSkill) {
			t.Errorf("plan = %+v", files)
		}
	})

	t.Run("a new large skill is two files, SKILL.md first", func(t *testing.T) {
		files, err := PlanSkill(large, DefaultSkillName, nil)
		if err != nil || len(files) != 2 || files[0].Path != SkillFile || files[1].Path != TasksFile {
			t.Fatalf("plan = %+v, %v", files, err)
		}
		if files[1].Status() != "created" || string(files[1].New) != largeTasks {
			t.Errorf("tasks.md = %+v", files[1])
		}
	})

	t.Run("a current skill is unchanged, also with CR LF", func(t *testing.T) {
		crlf := strings.ReplaceAll(largeSkill, "\n", "\r\n")
		files := planSkill(t, large, map[string][]byte{SkillFile: []byte(crlf), TasksFile: []byte(largeTasks)})
		for _, f := range files {
			if f.Status() != "unchanged" || f.CheckState(large.Fingerprint()) != StateCurrent {
				t.Errorf("%s: status %q, check %q", f.Path, f.Status(), f.CheckState(large.Fingerprint()))
			}
		}
	})

	t.Run("a skill of an older catalog is updated", func(t *testing.T) {
		f := planSkill(t, catalogOf(4), map[string][]byte{SkillFile: []byte(smallSkill)})[SkillFile]
		if f.Status() != "updated" || f.OldFingerprint != small.Fingerprint() || f.CheckState(catalogOf(4).Fingerprint()) != "stale: the Runefile changed since the export" {
			t.Errorf("SKILL.md = status %q, %+v", f.Status(), f)
		}
	})

	t.Run("a generated tasks.md that is no longer needed is removed", func(t *testing.T) {
		files := planSkill(t, small, map[string][]byte{SkillFile: []byte(largeSkill), TasksFile: []byte(largeTasks)})
		f := files[TasksFile]
		if !f.Remove || f.Status() != "removed" || f.New != nil || f.CheckState(small.Fingerprint()) != "stale: no longer part of the skill" {
			t.Errorf("tasks.md = %+v", f)
		}
		if files[SkillFile].Status() != "updated" {
			t.Errorf("SKILL.md = %q", files[SkillFile].Status())
		}
	})

	t.Run("a missing tasks.md of a large skill is created", func(t *testing.T) {
		f := planSkill(t, large, map[string][]byte{SkillFile: []byte(largeSkill)})[TasksFile]
		if f.Status() != "created" || f.CheckState(large.Fingerprint()) != StateMissing {
			t.Errorf("tasks.md = status %q, check %q", f.Status(), f.CheckState(large.Fingerprint()))
		}
	})

	t.Run("a SKILL.md written by hand is refused", func(t *testing.T) {
		_, err := PlanSkill(small, DefaultSkillName, map[string][]byte{SkillFile: byHand})
		var refusal *Refusal
		if !errors.As(err, &refusal) || refusal.Path != SkillFile {
			t.Errorf("err = %v, want a refusal that names SKILL.md", err)
		}
	})

	t.Run("a tasks.md written by hand is refused when the skill needs one", func(t *testing.T) {
		files, err := PlanSkill(large, DefaultSkillName, map[string][]byte{SkillFile: []byte(smallSkill), TasksFile: byHand})
		var refusal *Refusal
		if !errors.As(err, &refusal) || refusal.Path != TasksFile || files != nil {
			t.Errorf("files = %+v, err = %v, want no file and a refusal that names tasks.md", files, err)
		}
	})

	t.Run("a tasks.md written by hand is left alone when the skill needs none", func(t *testing.T) {
		files := planSkill(t, small, map[string][]byte{TasksFile: byHand})
		if _, planned := files[TasksFile]; planned || len(files) != 1 {
			t.Errorf("plan = %+v, want SKILL.md alone", files)
		}
	})
}
