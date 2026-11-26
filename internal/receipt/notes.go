package receipt

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
)

// NotesRef is the notes ref receipts travel in. No other tool writes it.
const NotesRef = "refs/notes/rune-receipts"

// ReadNote returns the non-blank lines of commit's receipts note, or none
// when it has no note.
func (r *Repo) ReadNote(ctx context.Context, commit string) ([][]byte, error) {
	return r.readNote(ctx, NotesRef, commit)
}

// readNote is ReadNote in the notes ref ref.
func (r *Repo) readNote(ctx context.Context, ref, commit string) ([][]byte, error) {
	// The note's blob is looked up in the list of every note, so a missing
	// ref and a commit without a note need no reading of git's messages.
	out, code, err := runGit(ctx, r.Top, nil, nil, "notes", "--ref="+ref, "list")
	if err != nil {
		if code > 0 {
			return nil, nil
		}
		return nil, err
	}
	blob := ""
	for line := range strings.SplitSeq(string(out), "\n") {
		if fields := strings.Fields(line); len(fields) == 2 && fields[1] == commit {
			blob = fields[0]
			break
		}
	}
	if blob == "" {
		return nil, nil
	}
	data, _, err := runGit(ctx, r.Top, nil, nil, "cat-file", "blob", blob)
	if err != nil {
		return nil, err
	}
	var lines [][]byte
	for line := range bytes.SplitSeq(data, []byte("\n")) {
		if line = bytes.TrimSpace(line); len(line) > 0 {
			lines = append(lines, line)
		}
	}
	return lines, nil
}

// attachTries bounds how often attach merges again with a note that another
// writer replaced in between.
const attachTries = 3

// AttachNote adds lines to commit's receipts note: the note becomes the
// sorted union of its lines and these, each once, so git's cat_sort_uniq
// strategy merges two such notes without loss. It returns how many lines it
// added and how many were there already. Only the receipts notes ref
// changes.
func (r *Repo) AttachNote(ctx context.Context, commit string, lines [][]byte) (added, present int, err error) {
	want := make([][]byte, 0, len(lines))
	for _, l := range lines {
		if l = bytes.TrimSpace(l); len(l) > 0 {
			want = append(want, l)
		}
	}
	for range attachTries {
		var b [8]byte
		_, _ = rand.Read(b[:])
		scratch := NotesRef + "-attach-" + hex.EncodeToString(b[:])
		var done bool
		added, present, done, err = r.attachOnce(ctx, commit, want, scratch)
		_, _, _ = runGit(context.WithoutCancel(ctx), r.Top, nil, nil, "update-ref", "-d", scratch)
		if err != nil || done {
			return added, present, err
		}
	}
	return 0, 0, errors.New("the receipts note kept changing while it was written; try again")
}

// attachOnce writes the merged note on the scratch ref, which starts where
// the receipts ref is, and moves the receipts ref to it only if no other
// writer moved it in between: a writer that wins the race never loses its
// lines. done is false when another writer did move it.
func (r *Repo) attachOnce(ctx context.Context, commit string, want [][]byte, scratch string) (added, present int, done bool, err error) {
	old, err := r.refValue(ctx, NotesRef)
	if err != nil {
		return 0, 0, false, err
	}
	var existing [][]byte
	if old != "" {
		if _, _, err := runGit(ctx, r.Top, nil, nil, "update-ref", scratch, old); err != nil {
			return 0, 0, false, err
		}
		if existing, err = r.readNote(ctx, scratch, commit); err != nil {
			return 0, 0, false, err
		}
	}
	merged := slices.Clone(existing)
	for _, l := range want {
		if slices.ContainsFunc(merged, func(m []byte) bool { return bytes.Equal(m, l) }) {
			present++
			continue
		}
		merged = append(merged, l)
		added++
	}
	if added == 0 {
		return 0, present, true, nil
	}
	slices.SortFunc(merged, bytes.Compare)
	merged = slices.CompactFunc(merged, bytes.Equal)
	if err := r.writeNote(ctx, scratch, commit, append(bytes.Join(merged, []byte("\n")), '\n')); err != nil {
		return 0, 0, false, err
	}
	next, err := r.refValue(ctx, scratch)
	if err != nil {
		return 0, 0, false, err
	}
	if r.afterWrite != nil {
		r.afterWrite()
	}
	// An empty old value makes git require that the ref does not exist.
	_, _, err = runGit(ctx, r.Top, nil, nil, "update-ref", "-m", "rune receipt attach", NotesRef, next, old)
	if err == nil {
		return added, present, true, nil
	}
	now, verr := r.refValue(ctx, NotesRef)
	if verr != nil {
		return 0, 0, false, verr
	}
	if now != old {
		return 0, 0, false, nil
	}
	return 0, 0, false, err
}

// refValue is the object a ref names, or "" when it does not exist.
func (r *Repo) refValue(ctx context.Context, ref string) (string, error) {
	out, code, err := runGit(ctx, r.Top, nil, nil, "rev-parse", "-q", "--verify", "--end-of-options", ref)
	if code == 1 {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func (r *Repo) writeNote(ctx context.Context, ref, commit string, content []byte) error {
	tmp, err := os.CreateTemp("", "rune-receipt-note-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(content); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	_, _, err = runGit(ctx, r.Top, nil, nil, "notes", "--ref="+ref, "add", "-f", "-F", tmp.Name(), commit)
	if err != nil && strings.Contains(err.Error(), "tell me who you are") {
		return fmt.Errorf("%w (a note is a commit on %s: set user.name and user.email)", err, NotesRef)
	}
	return err
}
