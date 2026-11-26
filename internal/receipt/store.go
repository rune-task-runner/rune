package receipt

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const manifestSuffix = ".outputs.json"

// Dir is where the receipts of the project in root are kept.
func Dir(root string) string { return filepath.Join(root, ".rune", "receipts") }

// Stored is one receipt file as read from a directory.
type Stored struct {
	Name string
	Data []byte
}

// Write stores one receipt line as ID.json in dir. It never replaces a
// file: a receipt that exists already is fs.ErrExist.
func Write(dir, id string, line []byte) error {
	if err := ensureDir(dir); err != nil {
		return err
	}
	return writeNew(dir, id+".json", line, 0o644)
}

// WriteManifest stores the output manifest of receipt id and returns its
// file name.
func WriteManifest(dir, id string, data []byte) (string, error) {
	if err := ensureDir(dir); err != nil {
		return "", err
	}
	name := id + manifestSuffix
	return name, writeNew(dir, name, data, 0o644)
}

// ReadManifest reads a manifest a receipt names. The name comes from a
// receipt, so it must be a manifest file name in dir and nothing else.
func ReadManifest(dir, name string) ([]byte, error) {
	if name == "" || filepath.Base(name) != name || strings.ContainsAny(name, `/\`) || !strings.HasSuffix(name, manifestSuffix) {
		return nil, fmt.Errorf("%q is not a manifest name", name)
	}
	return os.ReadFile(filepath.Join(dir, name))
}

// List reads every receipt file in dir, sorted by name. A missing directory
// holds none.
func List(dir string) ([]Stored, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read receipts: %w", err)
	}
	var out []Stored
	for _, e := range entries {
		name := e.Name()
		if !e.Type().IsRegular() || !strings.HasSuffix(name, ".json") || strings.HasSuffix(name, manifestSuffix) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, fmt.Errorf("read receipts: %w", err)
		}
		out = append(out, Stored{Name: name, Data: data})
	}
	return out, nil
}

// ensureDir creates the receipts directory with a .gitignore that keeps git
// from seeing anything in it.
func ensureDir(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create receipts directory: %w", err)
	}
	if err := writeNew(dir, ".gitignore", []byte("*\n"), 0o644); err != nil && !errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("create receipts directory: %w", err)
	}
	return nil
}

// writeNew writes data to dir/name all at once or not at all, and never
// over an existing file: it writes a temporary file and links it into place.
func writeNew(dir, name string, data []byte, perm os.FileMode) (err error) {
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	defer func() {
		_ = os.Remove(tmp.Name())
	}()
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Link(tmp.Name(), filepath.Join(dir, name))
}
