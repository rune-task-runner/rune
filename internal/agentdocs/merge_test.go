package agentdocs

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

const testSection = "<!-- rune:begin tasks fingerprint=sha256:new -->\nline one\nline two\n<!-- rune:end tasks -->\n"

func TestIsMarkerAndFingerprint(t *testing.T) {
	begins := map[string]string{
		"<!-- rune:begin tasks fingerprint=sha256:abc -->": "sha256:abc",
		"<!-- rune:begin tasks -->":                        "",
		"<!-- rune:begin tasks-->":                         "",
	}
	for text, fp := range begins {
		if !isMarker(text, beginPrefix) {
			t.Errorf("%q is not read as a begin line", text)
		}
		if got := fingerprintOf(text); got != fp {
			t.Errorf("fingerprint of %q = %q, want %q", text, got, fp)
		}
	}
	for _, text := range []string{
		"<!-- rune:begin tasksX -->",
		"<!-- rune:begin tasks",
		"see <!-- rune:begin tasks --> in the docs",
		"<!-- rune:begin -->",
	} {
		if isMarker(text, beginPrefix) {
			t.Errorf("%q is read as a begin line", text)
		}
	}
}

func merge(t *testing.T, old string) merged {
	t.Helper()
	m, err := mergeSection(old, testSection)
	if err != nil {
		t.Fatalf("mergeSection(%q): %v", old, err)
	}
	return m
}

func TestMergeSectionNewAndAppended(t *testing.T) {
	cases := []struct{ name, old, want string }{
		{"no file", "", testSection},
		{"a byte order mark alone", bom, bom + testSection},
		{"text with a final line break", "# Notes\n\nBy hand.\n", "# Notes\n\nBy hand.\n\n" + testSection},
		{"text without one", "# Notes", "# Notes\n\n" + testSection},
		{"a mark and text", bom + "# Notes\n", bom + "# Notes\n\n" + testSection},
		{"a marker inside a sentence is text", "see <!-- rune:end tasks --> here\n", "see <!-- rune:end tasks --> here\n\n" + testSection},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := merge(t, c.old)
			if m.text != c.want || !m.changed || m.owned || m.fingerprint != "" {
				t.Errorf("merged = %+v, want the text %q, changed, not owned", m, c.want)
			}
			// What was written is found again, and is then unchanged.
			again := merge(t, m.text)
			if again.text != m.text || again.changed || !again.owned || again.fingerprint != "sha256:new" {
				t.Errorf("a second merge = %+v, want the same text, unchanged, owned", again)
			}
		})
	}
}

func TestMergeSectionReplaces(t *testing.T) {
	before := "# Notes\n\nBy hand.\n\n"
	after := "\n## More\n\nAlso by hand.\n"
	old := before + "<!-- rune:begin tasks fingerprint=sha256:old --> \nstale\n<!-- rune:end tasks -->\t\n" + after
	m := merge(t, old)
	// The marker lines are Rune's whole, with the white space at their ends.
	want := before + testSection + after
	if m.text != want {
		t.Errorf("merged text = %q, want %q", m.text, want)
	}
	if !m.changed || !m.owned || m.fingerprint != "sha256:old" {
		t.Errorf("merged = %+v, want changed, owned, the old fingerprint", m)
	}
	if !strings.HasPrefix(m.text, before) || !strings.HasSuffix(m.text, after) {
		t.Error("a byte outside the section changed")
	}

	// An end line that is the last line of the file, with no line break.
	bare := before + "<!-- rune:begin tasks -->\nstale\n<!-- rune:end tasks -->"
	if got, want := merge(t, bare).text, before+strings.TrimSuffix(testSection, "\n"); got != want {
		t.Errorf("merged text = %q, want %q", got, want)
	}
	// A marker inside a code fence counts as a marker.
	fenced := "```\n<!-- rune:begin tasks -->\n```\n<!-- rune:end tasks -->\n"
	if got := merge(t, fenced); !got.owned {
		t.Error("a begin line inside a code fence was not read as one")
	}
}

func TestMergeSectionLineEndings(t *testing.T) {
	crlf := strings.ReplaceAll(testSection, "\n", "\r\n")
	if got, want := merge(t, "# Notes\r\n").text, "# Notes\r\n\r\n"+crlf; got != want {
		t.Errorf("appended to a CR LF file: %q, want %q", got, want)
	}
	old := "# Notes\r\n<!-- rune:begin tasks -->\r\nstale\r\n<!-- rune:end tasks -->\r\nafter\r\n"
	if got, want := merge(t, old).text, "# Notes\r\n"+crlf+"after\r\n"; got != want {
		t.Errorf("replaced in a CR LF file: %q, want %q", got, want)
	}
	// The same section with the other line ending is not a change.
	same := "# Notes\r\n" + crlf + "after\r\n"
	if m := merge(t, same); m.changed || m.text != same {
		t.Errorf("a CR LF copy of the section counts as changed: %+v", m)
	}
	// A file that mixes the two keeps its own section as it is, too.
	mixed := "# Notes\r\n" + testSection
	if m := merge(t, mixed); m.changed || m.text != mixed {
		t.Errorf("an LF section in a CR LF file counts as changed: %+v", m)
	}
}

func TestMergeSectionRefuses(t *testing.T) {
	const begin, end = "<!-- rune:begin tasks -->\n", "<!-- rune:end tasks -->\n"
	cases := []struct {
		name, old string
		want      []Marker
	}{
		{"two begin lines", begin + "x\n" + begin + end, []Marker{{1, true}, {3, true}, {4, false}}},
		{"a begin line with no end line", "a\n" + begin, []Marker{{2, true}}},
		{"an end line with no begin line", end, []Marker{{1, false}}},
		{"an end line before its begin line", end + begin, []Marker{{1, false}, {2, true}}},
		{"two end lines", begin + end + end, []Marker{{1, true}, {2, false}, {3, false}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := mergeSection(c.old, testSection)
			var refusal *Refusal
			if !errors.As(err, &refusal) {
				t.Fatalf("err = %v, want a refusal", err)
			}
			if refusal.Path != "" || !reflect.DeepEqual(refusal.Markers, c.want) {
				t.Errorf("refusal = %+v, want the markers %+v", refusal, c.want)
			}
			if !strings.Contains(refusal.Error(), "not one") {
				t.Errorf("refusal text = %q", refusal.Error())
			}
		})
	}
}

func TestGeneratedFingerprint(t *testing.T) {
	skill, _ := SkillFiles(catalogOf(2), DefaultSkillName)
	fp, owned := generatedFingerprint([]byte(skill))
	if !owned || fp != catalogOf(2).Fingerprint() {
		t.Errorf("generatedFingerprint = %q, %v", fp, owned)
	}
	if _, owned := generatedFingerprint([]byte(bom + "<!-- rune:generated claude-skill -->\r\n")); !owned {
		t.Error("a generated line after a byte order mark, with CR LF, is not found")
	}
	for _, text := range []string{"", "---\nname: mine\n---\n\nBy hand.\n", "<!-- rune:generated other -->\n"} {
		if fp, owned := generatedFingerprint([]byte(text)); owned || fp != "" {
			t.Errorf("generatedFingerprint(%q) = %q, %v", text, fp, owned)
		}
	}
	if got := (&Refusal{Path: SkillFile}).Error(); got != "SKILL.md was not generated by rune export" {
		t.Errorf("refusal text = %q", got)
	}
}
