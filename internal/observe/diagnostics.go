package observe

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/rune-task-runner/rune/internal/audit"
)

const diagnosticsFile = "diagnostics.json"

// Count is how often a reason stopped an event from being recorded, and
// when it last did.
type Count struct {
	N    int64     `json:"n"`
	Last time.Time `json:"last"`
}

// Diagnostics counts the events that were not recorded, per reason. It holds
// reason names, counts and times only, never text from an event.
type Diagnostics struct {
	Reasons map[string]Count `json:"reasons"`
}

// Merge returns the counts of d and o together.
func (d Diagnostics) Merge(o Diagnostics) Diagnostics {
	out := Diagnostics{Reasons: map[string]Count{}}
	for _, src := range []Diagnostics{d, o} {
		for reason, c := range src.Reasons {
			sum := out.Reasons[reason]
			sum.N += c.N
			if c.Last.After(sum.Last) {
				sum.Last = c.Last
			}
			out.Reasons[reason] = sum
		}
	}
	return out
}

// CountDrop counts one event of this project that was not recorded.
func (s *Store) CountDrop(reason string) error {
	unlock, err := s.lock()
	if err != nil {
		return err
	}
	defer unlock()
	return addCount(s.dir, reason, s.now())
}

// CountShared counts one event that could not be attributed to a project,
// in the diagnostics shared by all projects, in base.
func CountShared(base, reason string, now time.Time) error {
	if err := audit.PrivateDir(filepath.Dir(base), base, stateHint); err != nil {
		return err
	}
	unlock, err := audit.LockDir(base, lockWaitDefault)
	if err != nil {
		return err
	}
	defer unlock()
	return addCount(base, reason, now)
}

// ReadDiagnostics reads the diagnostics in dir: a project's directory, or
// the base for the shared ones. A missing file reads as no counts.
func ReadDiagnostics(dir string) (Diagnostics, error) {
	d := Diagnostics{Reasons: map[string]Count{}}
	data, err := os.ReadFile(filepath.Join(dir, diagnosticsFile))
	if errors.Is(err, fs.ErrNotExist) {
		return d, nil
	}
	if err != nil {
		return d, err
	}
	if err := json.Unmarshal(data, &d); err != nil {
		return Diagnostics{Reasons: map[string]Count{}}, err
	}
	if d.Reasons == nil {
		d.Reasons = map[string]Count{}
	}
	return d, nil
}

// addCount adds one to reason in dir's diagnostics. The lock is held.
func addCount(dir, reason string, now time.Time) error {
	d, err := ReadDiagnostics(dir)
	if err != nil {
		// A damaged file is started again: counts are a hint, and a file
		// that cannot be read must not stop counting for good.
		d = Diagnostics{Reasons: map[string]Count{}}
	}
	c := d.Reasons[reason]
	c.N++
	c.Last = now.UTC().Truncate(time.Second)
	d.Reasons[reason] = c
	data, err := json.Marshal(d)
	if err != nil {
		return err
	}
	return replaceFile(dir, filepath.Join(dir, diagnosticsFile), append(data, '\n'))
}
