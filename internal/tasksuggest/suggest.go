package tasksuggest

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"mvdan.cc/sh/v3/syntax"

	"github.com/rune-task-runner/rune/internal/observe"
	"github.com/rune-task-runner/rune/internal/taskimport"
	"github.com/rune-task-runner/rune/internal/token"
)

// DefaultMinRuns is how many runs a command needs to be listed.
const DefaultMinRuns = 3

// Suggestion is a task Rune proposes: a group of runs, or a family of groups
// that differ in one word.
type Suggestion struct {
	ID          string
	Name        string
	Description string
	Dir         string
	// Body is the task's body line, braces escaped, with
	// {{quote(NAME)}} for a family's parameter.
	Body   string
	Param  *Param
	Groups []*Group

	Runs     int
	Outcomes map[Outcome]int
	Last     time.Time

	sessions map[string]bool
	descs    map[string]int
	descLast map[string]time.Time
	// key is the text the ID is made from; file is the tree the name is
	// made from, up to word namePos when it is not 0.
	key     string
	file    *syntax.File
	namePos int
	// defaultDir is Options.DefaultDir: a task in another directory names
	// its own.
	defaultDir string
}

// Param is the parameter of a family's task.
type Param struct {
	Name    string
	Default string
	// Values are the values run, with their runs, most first.
	Values []ValueCount
}

// ValueCount is one value of a family's parameter and the command it gave.
type ValueCount struct {
	Value string
	Text  string
	Runs  int
}

// Sessions is the number of sessions the suggestion's commands ran in.
func (s *Suggestion) Sessions() int { return len(s.sessions) }

// add counts a group's runs into the suggestion.
func (s *Suggestion) add(g *Group) {
	if s.Outcomes == nil {
		s.Outcomes, s.sessions, s.descs, s.descLast = map[Outcome]int{}, map[string]bool{}, map[string]int{}, map[string]time.Time{}
	}
	s.Groups = append(s.Groups, g)
	s.Runs += g.Runs
	s.Last = maxTime(s.Last, g.Last)
	for o, n := range g.Outcomes {
		s.Outcomes[o] += n
	}
	for id := range g.sessions {
		s.sessions[id] = true
	}
	for d, n := range g.Descriptions {
		s.descs[d] += n
		s.descLast[d] = maxTime(s.descLast[d], g.descLast[d])
	}
}

// Review is a listed group that Rune does not write as a task.
type Review struct {
	Group  *Group
	Reason string
}

// Covered is a task of the Runefile that already runs a recorded command.
type Covered struct {
	Task string
	Runs int
}

// Report is what `rune suggest` lists.
type Report struct {
	// Commands is the number of runs recorded; Attempts the calls with no
	// recorded outcome.
	Commands, Attempts, Sessions int
	First, Last                  time.Time
	Suggestions                  []*Suggestion
	Reviews                      []Review
	Covered                      []Covered
	// Damaged and Dropped are filled in by the caller from the store: lines
	// that were not records, and events not recorded per reason.
	Damaged int
	Dropped map[string]int64
}

// Options are the choices of a listing.
type Options struct {
	// MinRuns is how many runs a command needs; 0 means DefaultMinRuns.
	MinRuns int
	// DefaultDir is where a task without [working-directory] runs, as
	// TaskInfo.Dir says a directory: the Runefile's set working-directory.
	DefaultDir string
}

// Build makes the report of the records against the tasks of the Runefile
// (research R8 to R11). It reads only its arguments.
func Build(recs []observe.Record, tasks []TaskInfo, opts Options) *Report {
	minRuns := cmp.Or(opts.MinRuns, DefaultMinRuns)
	execs := Executions(recs)
	rep := &Report{}
	sessions := map[string]bool{}
	for _, e := range execs {
		if rep.First.IsZero() || e.Last.Before(rep.First) {
			rep.First = e.Last
		}
		rep.Last = maxTime(rep.Last, e.Last)
		if e.Outcome == Attempt {
			rep.Attempts++
			continue
		}
		rep.Commands++
		sessions[e.Session] = true
	}
	rep.Sessions = len(sessions)

	covered := map[string]int{}
	var cands []*Group
	for _, g := range groupExecutions(execs) {
		// A command of comments alone runs nothing: there is no task in it.
		if g.runsRune || g.Runs == 0 || (g.file != nil && len(g.file.Stmts) == 0) {
			continue
		}
		if i := slices.IndexFunc(tasks, func(t TaskInfo) bool { return t.matches(g) }); i >= 0 {
			if g.Runs >= minRuns {
				covered[tasks[i].Name] += g.Runs
			}
			continue
		}
		if g.ReviewReason == "" && !writable(g) {
			g.ReviewReason = reasonFeature
		}
		cands = append(cands, g)
	}
	for _, name := range slices.Sorted(maps.Keys(covered)) {
		rep.Covered = append(rep.Covered, Covered{Task: name, Runs: covered[name]})
	}

	inFamily := map[*Group]bool{}
	for _, f := range findFamilies(cands, minRuns) {
		s, ok := f.suggestion()
		if !ok {
			continue
		}
		for _, m := range f.members {
			inFamily[m.group] = true
		}
		rep.Suggestions = append(rep.Suggestions, s)
	}
	for _, g := range cands {
		switch {
		case inFamily[g] || g.Runs < minRuns:
		case g.ReviewReason != "":
			rep.Reviews = append(rep.Reviews, Review{Group: g, Reason: g.ReviewReason})
		default:
			s := &Suggestion{Dir: g.Dir, Body: taskimport.EscapeBraces(g.Text), key: g.Text, file: g.file}
			s.add(g)
			rep.Suggestions = append(rep.Suggestions, s)
		}
	}

	for _, s := range rep.Suggestions {
		s.ID = suggestionID(s.Dir, s.key, 8)
		s.defaultDir = opts.DefaultDir
	}
	slices.SortStableFunc(rep.Suggestions, func(a, b *Suggestion) int {
		return cmp.Or(cmp.Compare(b.Runs, a.Runs), b.Last.Compare(a.Last), strings.Compare(a.ID, b.ID))
	})
	slices.SortStableFunc(rep.Reviews, func(a, b Review) int {
		return cmp.Or(cmp.Compare(b.Group.Runs, a.Group.Runs), b.Group.Last.Compare(a.Group.Last), strings.Compare(a.Group.Text, b.Group.Text))
	})
	widenClashes(rep.Suggestions)
	nameSuggestions(rep.Suggestions, tasks)
	return rep
}

// writable reports whether the group's text can stand in a task: its
// directory can be written as a string, and its text does not start with
// what a body line reads as a prefix.
func writable(g *Group) bool {
	if _, ok := taskimport.Quote(g.Dir); !ok {
		return false
	}
	return !strings.HasPrefix(g.Text, "@") && !strings.HasPrefix(g.Text, "-")
}

// suggestionID names a suggestion by its content, so it stays the same
// while new records arrive or the Runefile changes.
func suggestionID(dir, text string, digits int) string {
	sum := sha256.Sum256([]byte("v1\x00" + dir + "\x00" + text))
	return hex.EncodeToString(sum[:])[:digits]
}

// widenClashes gives suggestions whose IDs clash the longer form.
func widenClashes(sugs []*Suggestion) {
	count := map[string]int{}
	for _, s := range sugs {
		count[s.ID]++
	}
	for _, s := range sugs {
		if count[s.ID] > 1 {
			s.ID = suggestionID(s.Dir, s.key, 16)
		}
	}
}

var nameWord = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)

// nameWords are the command's first words that can make a name, at most
// three, up to the first that cannot, and before word limit when it is not 0.
func nameWords(f *syntax.File, limit int) []string {
	var call *syntax.CallExpr
	syntax.Walk(f, func(n syntax.Node) bool {
		if c, ok := n.(*syntax.CallExpr); ok && call == nil && len(c.Args) > 0 {
			call = c
		}
		return call == nil
	})
	var words []string
	if call == nil {
		return nil
	}
	for i, w := range call.Args {
		lit := w.Lit()
		if len(words) == 3 || (limit > 0 && i == limit) || !nameWord.MatchString(lit) {
			break
		}
		words = append(words, lit)
	}
	return words
}

// nameSuggestions gives each suggestion, in listing order, a valid name no
// task, rune command or earlier suggestion has, and its description.
func nameSuggestions(sugs []*Suggestion, tasks []TaskInfo) {
	taken := map[string]bool{}
	for _, t := range tasks {
		taken[t.Name] = true
	}
	for _, s := range sugs {
		words := nameWords(s.file, s.namePos)
		base := strings.Join(words, "-")
		if base == "" {
			base = "task"
		}
		name := base
		for n := 2; taken[name] || taskimport.RuneCommands[name] || token.Lookup(name) != token.IDENT; n++ {
			name = base + "-" + strconv.Itoa(n)
		}
		taken[name] = true
		s.Name = name
		s.Description = describe(s, words)
	}
}

// describe is the agent's description used most, the latest on a tie, or
// one made from the words of the name.
func describe(s *Suggestion, words []string) string {
	best := ""
	for _, d := range slices.Sorted(maps.Keys(s.descs)) {
		if best == "" || s.descs[d] > s.descs[best] || (s.descs[d] == s.descs[best] && s.descLast[d].After(s.descLast[best])) {
			best = d
		}
	}
	if best != "" {
		return best
	}
	if len(words) == 0 {
		return "Run the recorded command."
	}
	return "Run " + strings.Join(words, " ") + "."
}

// Find returns the suggestion with the ID, or nil.
func (r *Report) Find(id string) *Suggestion {
	for _, s := range r.Suggestions {
		if s.ID == id {
			return s
		}
	}
	return nil
}
