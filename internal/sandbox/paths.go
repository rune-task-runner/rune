package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Resolve returns path with the symbolic links of its existing part followed.
// The part that does not exist yet is kept as written.
func Resolve(path string) string {
	path, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	rest := ""
	for dir := path; ; {
		if real, err := filepath.EvalSymlinks(dir); err == nil {
			return filepath.Join(real, rest)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return path
		}
		rest = filepath.Join(filepath.Base(dir), rest)
		dir = parent
	}
}

// Within reports whether path is parent or lies under it. Both are compared
// as written, so a caller resolves them first.
func Within(parent, path string) bool {
	rel, err := filepath.Rel(parent, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// Ancestors lists the directories between workspace and path, shallowest
// first, excluding both. A path outside the workspace has none.
func Ancestors(workspace, path string) []string {
	rel, err := filepath.Rel(workspace, path)
	if err != nil || !Within(workspace, path) {
		return nil
	}
	parts := strings.Split(rel, string(filepath.Separator))
	var dirs []string
	dir := workspace
	for _, part := range parts[:max(len(parts)-1, 0)] {
		dir = filepath.Join(dir, part)
		dirs = append(dirs, dir)
	}
	return dirs
}

// RepoRoot returns the nearest directory at or above dir that holds a .git
// entry, or dir when there is none. It runs no git: the answer decides what
// git itself may see.
func RepoRoot(dir string) string {
	for at := dir; ; {
		if _, err := os.Lstat(filepath.Join(at, ".git")); err == nil {
			return at
		}
		parent := filepath.Dir(at)
		if parent == at {
			return dir
		}
		at = parent
	}
}

// LinkedError reports a protected file that has more than one name. A mount
// protects a name, and the file could be written through the other one.
type LinkedError struct {
	Path  string
	Count uint64
}

func (e *LinkedError) Error() string {
	return fmt.Sprintf("%s: it has %d names (hard links), and a task could change it through another one. Remove the other names, or replace the file with a copy of itself.", e.Path, e.Count)
}

// SymlinkError reports a protected path that is reached through a symbolic
// link inside the workspace. A mount follows the link, so the link itself
// stays replaceable.
type SymlinkError struct {
	Path string
	Link string
}

func (e *SymlinkError) Error() string {
	return fmt.Sprintf("%s: it is reached through the symbolic link %s, which a task could replace. Use the real path.", e.Path, e.Link)
}

// CheckProtected reports why path, which lies inside workspace, cannot be
// protected by a mount: a part of it below the workspace is a symbolic link,
// or, when links is set, it is a file with more than one name. A path that
// does not exist has nothing to protect and passes.
func CheckProtected(workspace, path string, links bool) error {
	rel, err := filepath.Rel(workspace, path)
	if err != nil || !Within(workspace, path) || rel == "." {
		return nil
	}
	at := workspace
	var info os.FileInfo
	for part := range strings.SplitSeq(rel, string(filepath.Separator)) {
		at = filepath.Join(at, part)
		if info, err = os.Lstat(at); err != nil {
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return &SymlinkError{Path: path, Link: at}
		}
	}
	if links && info.Mode().IsRegular() {
		if n := linkCount(info); n > 1 {
			return &LinkedError{Path: path, Count: n}
		}
	}
	return nil
}
