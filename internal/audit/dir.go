package audit

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// StateDir is the directory Rune keeps the user's state in: rune under the
// user's state directory. A relative XDG_STATE_HOME is ignored, as the XDG
// specification says: two programs with working directories of their own
// would each read it as a different directory.
func StateDir(getenv func(string) string, goos string, home func() (string, error)) (string, error) {
	if dir := getenv("XDG_STATE_HOME"); filepath.IsAbs(dir) {
		return filepath.Join(dir, "rune"), nil
	}
	if goos == "windows" {
		if dir := getenv("LocalAppData"); dir != "" {
			return filepath.Join(dir, "rune"), nil
		}
	}
	dir, err := home()
	if err != nil || dir == "" {
		return "", errors.New("no state directory: set XDG_STATE_HOME")
	}
	return filepath.Join(dir, ".local", "state", "rune"), nil
}

// DefaultBase is the directory the logs of every project live under when
// the operator names none: audit under StateDir.
func DefaultBase(getenv func(string) string, goos string, home func() (string, error)) (string, error) {
	dir, err := StateDir(getenv, goos, home)
	if err != nil {
		return "", errors.New("no state directory: set RUNE_AUDIT_DIR or XDG_STATE_HOME")
	}
	return filepath.Join(dir, "audit"), nil
}

// Inside reports whether dir, with the symbolic links of its existing part
// followed, is project or lies under it.
func Inside(dir, project string) bool {
	return within(resolve(dir), resolve(project))
}

// ProjectID names a project's directory: the first 16 hex digits of the
// SHA-256 of its Runefile's resolved path.
func ProjectID(runefile string) string {
	sum := sha256.Sum256([]byte(runefile))
	return hex.EncodeToString(sum[:8])
}

// ProjectDir is the directory that holds the log of one Runefile.
func ProjectDir(base, runefile string) string {
	return filepath.Join(base, ProjectID(runefile))
}

// resolve returns path with the symbolic links of its existing part
// followed. The part that does not exist yet is kept as written, so a
// directory can be judged before it is created.
func resolve(path string) string {
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

// within reports whether dir is parent or lies under it. Both are resolved
// paths.
func within(dir, parent string) bool {
	rel, err := filepath.Rel(parent, dir)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// LocationError reports a log directory that lies inside the project, where
// a task's own files are.
type LocationError struct {
	Dir     string
	Project string
}

func (e *LocationError) Error() string {
	return fmt.Sprintf("%s is inside the project (%s); set RUNE_AUDIT_DIR to a directory outside it", e.Dir, e.Project)
}

// UnusableError reports a log directory that is a link, is not a directory
// or belongs to another user.
type UnusableError struct {
	Path   string
	Reason string
}

func (e *UnusableError) Error() string { return e.Path + " " + e.Reason }

// realDir inspects one directory of the log's path without following a link.
func realDir(path string) (os.FileInfo, error) {
	info, err := os.Lstat(path)
	switch {
	case err != nil:
		return nil, err
	case info.Mode()&os.ModeSymlink != 0:
		return nil, &UnusableError{Path: path, Reason: "is a symbolic link"}
	case !info.IsDir():
		return nil, &UnusableError{Path: path, Reason: "is not a directory"}
	}
	return info, nil
}
