// Package config handles Runefile discovery and settings resolution.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ErrNotFound is returned when no Runefile can be located by upward discovery.
var ErrNotFound = errors.New("no Runefile found in this directory or any parent")

// candidateNames are the accepted Runefile base names, matched case-insensitively.
var candidateNames = []string{"runefile", ".runefile"}

// Resolve picks the Runefile to use. If fileFlag is non-empty it is used
// directly (and must exist); otherwise discovery walks up from startDir.
func Resolve(fileFlag, startDir string) (string, error) {
	if fileFlag != "" {
		abs, err := filepath.Abs(fileFlag)
		if err != nil {
			return "", err
		}
		info, err := os.Stat(abs)
		if err != nil {
			return "", err
		}
		if info.IsDir() {
			return "", errors.New("--file points at a directory, not a Runefile")
		}
		return abs, nil
	}
	return Discover(startDir)
}

// AmbiguousError reports a directory that holds more than one Runefile.
// Picking one of them would let a file that was added beside the Runefile
// take its place without anyone saying so: a task an agent ran can write
// such a file (spec 053).
type AmbiguousError struct {
	Dir   string
	Names []string
}

func (e *AmbiguousError) Error() string {
	return fmt.Sprintf("%s holds more than one Runefile (%s); remove all but one, or name one with --file", e.Dir, strings.Join(e.Names, ", "))
}

// Discover walks startDir and its ancestors, returning the absolute path of the
// nearest Runefile / .runefile (case-insensitive). It returns ErrNotFound if
// none exists up to the filesystem root, and an *AmbiguousError for a
// directory that holds more than one.
func Discover(startDir string) (string, error) {
	dir, err := filepath.Abs(startDir)
	if err != nil {
		return "", err
	}
	for {
		path, err := runefileIn(dir)
		if err != nil {
			return "", err
		}
		if path != "" {
			return path, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", ErrNotFound
		}
		dir = parent
	}
}

// runefileIn returns the path of the Runefile in dir, or "" when there is
// none.
func runefileIn(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", nil
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		lower := strings.ToLower(e.Name())
		for _, cand := range candidateNames {
			if lower == cand {
				names = append(names, e.Name())
			}
		}
	}
	switch len(names) {
	case 0:
		return "", nil
	case 1:
		return filepath.Join(dir, names[0]), nil
	}
	return "", &AmbiguousError{Dir: dir, Names: names}
}
