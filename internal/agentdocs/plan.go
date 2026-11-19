package agentdocs

// Format is one of the two exports.
type Format string

// The formats of `rune export`.
const (
	AgentsMD    Format = "agents-md"
	ClaudeSkill Format = "claude-skill"
)

// Formats lists the format names in the order help shows them.
func Formats() []string {
	return []string{string(AgentsMD), string(ClaudeSkill)}
}

// ParseFormat reads a format name, and reports whether it is one.
func ParseFormat(name string) (Format, bool) {
	switch f := Format(name); f {
	case AgentsMD, ClaudeSkill:
		return f, true
	}
	return "", false
}

// File is one file of a plan: what it holds now and what it is to hold.
type File struct {
	Path           string // "" for the Markdown file of agents-md, else the skill file
	Old, New       []byte // New is nil for a file to remove
	Existed        bool
	Owned          bool   // Old held Rune's content: a section, or a generated line
	OldFingerprint string // the fingerprint Old carried, or ""
	Remove         bool   // a generated tasks.md that is no longer part of the skill
	Changed        bool   // New differs from Old by more than line endings
	Tasks          int    // the entries the file holds
	Size           int    // the bytes Rune owns in New
}

// Status is what a write does with the file.
func (f File) Status() string {
	switch {
	case f.Remove:
		return "removed"
	case !f.Existed:
		return "created"
	case f.Changed:
		return "updated"
	}
	return "unchanged"
}

// The states a check reports.
const (
	StateCurrent = "current"
	StateMissing = "missing"
)

// CheckState is what a check says about the file, given the fingerprint of
// the catalog now. A matching fingerprint with another text means that the
// text was edited, or written by a Rune that words it differently.
func (f File) CheckState(fingerprint string) string {
	switch {
	case f.Remove:
		return "stale: no longer part of the skill"
	case !f.Owned:
		return StateMissing
	case !f.Changed:
		return StateCurrent
	case f.OldFingerprint != fingerprint:
		return "stale: the Runefile changed since the export"
	}
	return "stale: the text differs from what this rune generates (edited by hand, or written by another rune version)"
}

// PlanSection plans the agents-md export into a Markdown file. old is nil
// for a file that does not exist.
func PlanSection(c Catalog, old []byte, existed bool) (File, error) {
	section := Section(c)
	m, err := mergeSection(string(old), section)
	if err != nil {
		return File{}, err
	}
	return File{
		Old:            old,
		New:            []byte(m.text),
		Existed:        existed,
		Owned:          m.owned,
		OldFingerprint: m.fingerprint,
		Changed:        m.changed || !existed,
		Tasks:          len(c.Entries),
		Size:           m.size,
	}, nil
}

// PlanSkill plans the claude-skill export into a skill directory named
// name. existing holds the bytes of SKILL.md and tasks.md where they exist.
// A file that Rune did not generate is never replaced: when the skill needs
// its place the plan is refused, before any file is planned.
func PlanSkill(c Catalog, name string, existing map[string][]byte) ([]File, error) {
	skill, tasks := SkillFiles(c, name)
	var files []File
	for _, want := range []struct{ path, text string }{{SkillFile, skill}, {TasksFile, tasks}} {
		old, existed := existing[want.path]
		fingerprint, owned := generatedFingerprint(old)
		switch {
		case want.text == "" && !owned:
			// Not part of the skill, and not Rune's to remove.
			continue
		case want.text == "":
			files = append(files, File{Path: want.path, Old: old, Existed: true, Owned: true, OldFingerprint: fingerprint, Remove: true, Changed: true})
			continue
		case existed && !owned:
			return nil, &Refusal{Path: want.path}
		}
		files = append(files, File{
			Path:           want.path,
			Old:            old,
			New:            []byte(want.text),
			Existed:        existed,
			Owned:          owned,
			OldFingerprint: fingerprint,
			Changed:        !existed || !sameText(string(old), want.text),
			Tasks:          len(c.Entries),
			Size:           len(want.text),
		})
	}
	return files, nil
}
