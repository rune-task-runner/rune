package receipt

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// The ways git can answer that are not failures of git itself.
var (
	ErrNotRepository   = errors.New("not a git repository")
	ErrUnknownRevision = errors.New("unknown revision")
)

// gitTimeout bounds one git call.
const gitTimeout = time.Minute

// Repo is the git working tree a project lies in.
type Repo struct {
	Top    string // the top of the working tree
	GitDir string // the git directory of this working tree

	// afterWrite runs after attach wrote a note. Nil outside tests, which
	// use it to stand in for another writer.
	afterWrite func()
}

// OpenRepo finds the repository dir lies in.
func OpenRepo(ctx context.Context, dir string) (*Repo, error) {
	out, code, err := runGit(ctx, dir, nil, nil, "rev-parse", "--show-toplevel", "--absolute-git-dir")
	if err != nil {
		if code > 0 {
			return nil, fmt.Errorf("%w: %s", ErrNotRepository, dir)
		}
		return nil, err
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) != 2 {
		return nil, fmt.Errorf("%w: %s", ErrNotRepository, dir)
	}
	return &Repo{Top: filepath.FromSlash(lines[0]), GitDir: filepath.FromSlash(lines[1])}, nil
}

// Snapshot returns the tree git would write for the working tree staged with
// `git add -A`, leaving out the directory exclude (relative to the top, with
// forward slashes). It stages into a private copy of the index, so the
// repository's index, HEAD, refs and working tree stay as they were; the
// blobs and trees it stores are unreachable.
func (r *Repo) Snapshot(ctx context.Context, exclude string) (string, error) {
	tmp, err := os.CreateTemp("", "rune-receipt-index-*")
	if err != nil {
		return "", fmt.Errorf("snapshot: %w", err)
	}
	index := tmp.Name()
	defer func() {
		_ = os.Remove(index)
		_ = os.Remove(index + ".lock")
	}()
	// The copy keeps git's stat cache, so only changed files are hashed.
	src, err := os.Open(filepath.Join(r.GitDir, "index"))
	switch {
	case err == nil:
		var info os.FileInfo
		if info, err = src.Stat(); err == nil {
			_, err = io.Copy(tmp, src)
		}
		_ = src.Close()
		if cerr := tmp.Close(); err == nil {
			err = cerr
		}
		// git trusts the stat cache only for entries older than the index
		// file itself: a copy dated now would pass a file changed in the
		// same second as the last index write as unchanged.
		if err == nil {
			err = os.Chtimes(index, info.ModTime(), info.ModTime())
		}
		if err != nil {
			return "", fmt.Errorf("snapshot: %w", err)
		}
	case errors.Is(err, os.ErrNotExist):
		// git reads a missing index as an empty one, and an empty file as
		// a broken one.
		_ = tmp.Close()
		_ = os.Remove(index)
	default:
		_ = tmp.Close()
		return "", fmt.Errorf("snapshot: %w", err)
	}
	env := []string{"GIT_INDEX_FILE=" + index}
	// git add skips the files an entry marks assume-unchanged or
	// skip-worktree, so a copy with such an entry would miss their edits.
	hidden, err := r.hidesChanges(ctx, env)
	if err != nil {
		return "", fmt.Errorf("snapshot: %w", err)
	}
	if hidden {
		// An index read fresh from HEAD keeps the tracked files that git
		// add -A, starting from nothing, would leave out as ignored.
		_ = os.Remove(index)
		if err := r.readHead(ctx, env); err != nil {
			return "", fmt.Errorf("snapshot: %w", err)
		}
		// A sparse checkout marks entries again: then start from nothing.
		if hidden, err = r.hidesChanges(ctx, env); err != nil {
			return "", fmt.Errorf("snapshot: %w", err)
		}
		if hidden {
			_ = os.Remove(index)
		}
	}
	if _, _, err := runGit(ctx, r.Top, env, nil, "add", "-A", "--", ":/", ":(top,exclude)"+exclude); err != nil {
		return "", fmt.Errorf("snapshot: %w", err)
	}
	out, _, err := runGit(ctx, r.Top, env, nil, "write-tree")
	if err != nil {
		return "", fmt.Errorf("snapshot: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// readHead fills the index env names with HEAD's tree. Before the first
// commit it leaves the index as it is.
func (r *Repo) readHead(ctx context.Context, env []string) error {
	commit, _, err := r.Head(ctx)
	if err != nil || commit == "" {
		return err
	}
	_, _, err = runGit(ctx, r.Top, env, nil, "read-tree", commit)
	return err
}

// Head returns HEAD's commit and tree, or empty values before the first
// commit.
func (r *Repo) Head(ctx context.Context) (commit, tree string, err error) {
	commit, tree, err = r.Resolve(ctx, "HEAD")
	if errors.Is(err, ErrUnknownRevision) {
		return "", "", nil
	}
	return commit, tree, err
}

// Resolve returns the commit a revision names and its tree.
func (r *Repo) Resolve(ctx context.Context, rev string) (commit, tree string, err error) {
	if rev == "" {
		return "", "", fmt.Errorf("%w: empty", ErrUnknownRevision)
	}
	verify := func(spec string) (string, error) {
		out, code, err := runGit(ctx, r.Top, nil, nil, "rev-parse", "-q", "--verify", "--end-of-options", spec)
		if err != nil {
			if code > 0 {
				return "", fmt.Errorf("%w: %s", ErrUnknownRevision, rev)
			}
			return "", err
		}
		return strings.TrimSpace(string(out)), nil
	}
	if commit, err = verify(rev + "^{commit}"); err != nil {
		return "", "", err
	}
	if tree, err = verify(commit + "^{tree}"); err != nil {
		return "", "", err
	}
	return commit, tree, nil
}

// WorktreeMatches reports whether the working tree holds exactly the files of
// commit, leaving out ignored files and the directory exclude. It only reads.
func (r *Repo) WorktreeMatches(ctx context.Context, commit, exclude string) (bool, error) {
	// git diff does not look at a file marked assume-unchanged or
	// skip-worktree, so with one the match cannot be shown.
	if hidden, err := r.hidesChanges(ctx, nil); err != nil || hidden {
		return false, err
	}
	paths := []string{"--", ":/", ":(top,exclude)" + exclude}
	_, code, err := runGit(ctx, r.Top, nil, nil, append([]string{"diff", "--quiet", commit}, paths...)...)
	switch {
	case code == 1:
		return false, nil
	case err != nil:
		return false, err
	}
	out, _, err := runGit(ctx, r.Top, nil, nil, append([]string{"ls-files", "--others", "--exclude-standard", "-z"}, paths...)...)
	if err != nil {
		return false, err
	}
	return len(out) == 0, nil
}

// hidesChanges reports whether the index env names has an entry marked
// assume-unchanged or skip-worktree: ls-files -v tags the first in lower
// case and the second with S.
func (r *Repo) hidesChanges(ctx context.Context, env []string) (bool, error) {
	out, _, err := runGit(ctx, r.Top, env, nil, "ls-files", "-v", "-z")
	if err != nil {
		return false, err
	}
	for entry := range bytes.SplitSeq(out, []byte{0}) {
		if len(entry) > 0 && (entry[0] == 'S' || entry[0] >= 'a' && entry[0] <= 'z') {
			return true, nil
		}
	}
	return false, nil
}

// CheckIgnore returns those of paths (relative to the top, forward slashes)
// that git ignores and does not track: a snapshot cannot hold them.
func (r *Repo) CheckIgnore(ctx context.Context, paths []string) ([]string, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	in := []byte(strings.Join(paths, "\x00") + "\x00")
	out, code, err := runGit(ctx, r.Top, nil, in, "check-ignore", "--stdin", "-z")
	if code == 1 {
		return []string{}, nil
	}
	if err != nil {
		return nil, err
	}
	var ignored []string
	for p := range strings.SplitSeq(string(out), "\x00") {
		if p != "" {
			ignored = append(ignored, p)
		}
	}
	return ignored, nil
}

// runGit runs git in dir and returns its standard output and exit code. A
// failure carries git's standard error. The exit code is -1 when git did not
// run to an exit.
func runGit(ctx context.Context, dir string, env []string, stdin []byte, args ...string) ([]byte, int, error) {
	ctx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()
	// core.fsmonitor can name a program, which a status-like call would start.
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir, "-c", "core.fsmonitor=false"}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0", "LC_ALL=C")
	cmd.Env = append(cmd.Env, env...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		return stdout.Bytes(), 0, nil
	}
	code := -1
	var exit *exec.ExitError
	if errors.As(err, &exit) && ctx.Err() == nil {
		code = exit.ExitCode()
	}
	msg := strings.TrimSpace(stderr.String())
	if msg == "" {
		return stdout.Bytes(), code, fmt.Errorf("git %s: %w", args[0], err)
	}
	return stdout.Bytes(), code, fmt.Errorf("git %s: %w: %s", args[0], err, msg)
}
