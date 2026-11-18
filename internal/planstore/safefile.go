package planstore

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

const (
	runeDirName  = ".rune"
	plansDirName = "plans"
	ignoreName   = ".gitignore"
)

// fileProblem says why a path cannot be read as a regular file. Its text is
// the Detail of the exported errors.
type fileProblem string

const (
	problemMissing    fileProblem = "is missing"
	problemNotRegular fileProblem = "is not a regular file"
	problemSymlink    fileProblem = "is a symbolic link"
)

func (p fileProblem) Error() string { return string(p) }

// openRegular opens path for reading and accepts only a regular file reached
// without a final symbolic link. The handle is compared with what Lstat saw,
// so a file swapped in between the two calls is refused as well.
func openRegular(path string) (*os.File, os.FileInfo, error) {
	before, err := os.Lstat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, nil, problemMissing
	case err != nil:
		return nil, nil, fmt.Errorf("inspect file: %w", err)
	case before.Mode()&fs.ModeSymlink != 0:
		return nil, nil, problemSymlink
	case !before.Mode().IsRegular():
		return nil, nil, problemNotRegular
	}
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, problemMissing
	}
	if err != nil {
		return nil, nil, fmt.Errorf("open file: %w", err)
	}
	after, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, nil, fmt.Errorf("inspect file: %w", err)
	}
	if !after.Mode().IsRegular() || !os.SameFile(before, after) {
		_ = f.Close()
		return nil, nil, problemNotRegular
	}
	return f, after, nil
}

// realDir inspects one directory of the store's path without following a
// link. A missing path is reported with an error that wraps fs.ErrNotExist.
func realDir(path string) (os.FileInfo, error) {
	info, err := os.Lstat(path)
	switch {
	case err != nil:
		return nil, fmt.Errorf("inspect plan store: %w", err)
	case info.Mode()&fs.ModeSymlink != 0:
		return nil, &UnusableError{Path: path, Reason: "is a symbolic link"}
	case !info.IsDir():
		return nil, &UnusableError{Path: path, Reason: "is not a directory"}
	}
	return info, nil
}

// ensureDir creates the store under root when it is missing and returns its
// directory. It refuses a store it would not have created itself: a link, a
// file, or a directory of another user.
func ensureDir(root string) (string, error) {
	dir := root
	var info os.FileInfo
	for _, name := range [...]string{runeDirName, plansDirName} {
		dir = filepath.Join(dir, name)
		if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
			return "", fmt.Errorf("create plan store: %w", err)
		}
		var err error
		if info, err = realDir(dir); err != nil {
			return "", err
		}
	}
	if !ownedByCurrentUser(info) {
		return "", &UnusableError{Path: dir, Reason: "belongs to another user"}
	}
	if info.Mode().Perm() != 0o700 {
		if err := os.Chmod(dir, 0o700); err != nil {
			return "", fmt.Errorf("restrict plan store: %w", err)
		}
	}
	ignore := filepath.Join(dir, ignoreName)
	if _, err := os.Lstat(ignore); errors.Is(err, fs.ErrNotExist) {
		if err := os.WriteFile(ignore, []byte("*\n"), 0o600); err != nil {
			return "", fmt.Errorf("create plan store: %w", err)
		}
	}
	return dir, nil
}

// checkDir applies the checks of ensureDir without creating anything. It
// reports false, with no error, for a store that does not exist yet.
func checkDir(root string) (bool, error) {
	dir := root
	var info os.FileInfo
	for _, name := range [...]string{runeDirName, plansDirName} {
		dir = filepath.Join(dir, name)
		var err error
		info, err = realDir(dir)
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
	}
	if !ownedByCurrentUser(info) {
		return false, &UnusableError{Path: dir, Reason: "belongs to another user"}
	}
	return true, nil
}
