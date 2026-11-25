package ci

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// BundleFile is the name of a node's bundle in the job's state directory,
// and of the file in each downloaded artifact.
const BundleFile = "bundle.tar"

// Pack writes a bundle of the given paths, relative to root with forward
// slashes: directories and regular files, as cache.OutputFiles lists them.
// A path outside root, a link, or any other kind of file is refused: a
// bundle carries project files only.
func Pack(root string, paths []string, w io.Writer) error {
	zw := gzip.NewWriter(w)
	tw := tar.NewWriter(zw)
	for _, p := range paths {
		if err := packOne(tw, root, p); err != nil {
			return err
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return zw.Close()
}

func packOne(tw *tar.Writer, root, p string) error {
	if !filepath.IsLocal(filepath.FromSlash(p)) {
		return fmt.Errorf("output %s is outside the project, and a bundle carries project files only", p)
	}
	full := filepath.Join(root, filepath.FromSlash(p))
	info, err := os.Lstat(full)
	if err != nil {
		return err
	}
	switch {
	case info.Mode()&fs.ModeSymlink != 0:
		return fmt.Errorf("output %s is a link, and a bundle carries regular files and directories only", p)
	case !info.IsDir() && !info.Mode().IsRegular():
		return fmt.Errorf("output %s is not a regular file or a directory", p)
	}
	h, err := tar.FileInfoHeader(info, "")
	if err != nil {
		return err
	}
	h.Name = p
	if info.IsDir() {
		h.Name += "/"
	}
	h.Uid, h.Gid, h.Uname, h.Gname = 0, 0, "", ""
	if err := tw.WriteHeader(h); err != nil {
		return err
	}
	if info.IsDir() {
		return nil
	}
	f, err := os.Open(full)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	_, err = io.Copy(tw, f)
	return err
}

// Unpack writes the entries of a bundle under root. It accepts only
// directories and regular files with relative, clean paths, and writes
// through an os.Root, so no entry lands outside root, not even through a
// link already there.
func Unpack(root string, r io.Reader) error {
	zr, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("not a bundle: %w", err)
	}
	dir, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer func() { _ = dir.Close() }()
	tr := tar.NewReader(zr)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("not a bundle: %w", err)
		}
		if err := unpackOne(dir, tr, h); err != nil {
			return err
		}
	}
}

func unpackOne(dir *os.Root, tr *tar.Reader, h *tar.Header) error {
	name := strings.TrimSuffix(h.Name, "/")
	if strings.Contains(name, `\`) || path.Clean(name) != name || !filepath.IsLocal(filepath.FromSlash(name)) {
		return fmt.Errorf("bundle entry %q is not a path inside the project", h.Name)
	}
	local := filepath.FromSlash(name)
	mode := h.FileInfo().Mode().Perm()
	switch h.Typeflag {
	case tar.TypeDir:
		return dir.MkdirAll(local, 0o755)
	case tar.TypeReg:
		if parent := filepath.Dir(local); parent != "." {
			if err := dir.MkdirAll(parent, 0o755); err != nil {
				return err
			}
		}
		// A job unpacks the same bundles in its key step and again before
		// the body, so a read-only file of an earlier unpack must be
		// replaced, not opened for writing.
		if info, err := dir.Lstat(local); err == nil && !info.IsDir() {
			if err := dir.Remove(local); err != nil {
				return err
			}
		}
		f, err := dir.OpenFile(local, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
		if err != nil {
			return err
		}
		_, err = io.Copy(f, tr)
		if closeErr := f.Close(); err == nil {
			err = closeErr
		}
		if err == nil {
			err = dir.Chmod(local, mode)
		}
		if err == nil {
			err = dir.Chtimes(local, h.ModTime, h.ModTime)
		}
		return err
	default:
		return fmt.Errorf("bundle entry %q is not a regular file or a directory", h.Name)
	}
}

// UnpackDir unpacks, under root, the bundles the job's state directory dir
// holds: those of the jobs before it, in dir/in/*/ by name, and with
// restored the node's own restored bundle dir/bundle.tar last.
func UnpackDir(root, dir string, restored bool) error {
	var files []string
	ins, err := os.ReadDir(filepath.Join(dir, "in"))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	for _, in := range ins {
		if !in.IsDir() {
			continue
		}
		// ReadDir, not Glob: the state directory's path may hold glob
		// characters.
		entries, err := os.ReadDir(filepath.Join(dir, "in", in.Name()))
		if err != nil {
			return err
		}
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".tar") {
				files = append(files, filepath.Join(dir, "in", in.Name(), e.Name()))
			}
		}
	}
	if own := filepath.Join(dir, BundleFile); restored {
		if _, err := os.Stat(own); err == nil {
			files = append(files, own)
		}
	}
	for _, file := range files {
		if err := unpackFile(root, file); err != nil {
			return fmt.Errorf("%s: %w", file, err)
		}
	}
	return nil
}

func unpackFile(root, file string) error {
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	return Unpack(root, f)
}
