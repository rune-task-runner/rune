package receipt

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// MaxInlineOutputs is the most output files a receipt lists itself. More
// go to a manifest file next to it.
const MaxInlineOutputs = 64

// The states of an output file compared with the working tree.
const (
	OutputMatch   = "match"
	OutputChanged = "changed"
	OutputMissing = "missing"
	OutputUnknown = "unknown"
)

// OutputState is one output file compared with the working tree.
type OutputState struct {
	Path  string `json:"path"`
	State string `json:"state"`
}

// OutputFiles digests the regular files among rel (paths relative to root,
// forward slashes), sorted by path. Directories, links and missing paths are
// left out.
func OutputFiles(root string, rel []string) ([]OutputFile, error) {
	var files []OutputFile
	for _, p := range rel {
		path := filepath.Join(root, filepath.FromSlash(p))
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		sum, err := fileDigest(path)
		if err != nil {
			return nil, fmt.Errorf("digest output %s: %w", p, err)
		}
		files = append(files, OutputFile{Path: p, Size: info.Size(), SHA256: sum})
	}
	slices.SortFunc(files, func(a, b OutputFile) int { return strings.Compare(a.Path, b.Path) })
	return files, nil
}

// BuildOutputs binds files into a receipt: inline up to MaxInlineOutputs,
// otherwise as a manifest whose bytes the caller stores next to the receipt
// and whose name it sets. The digest is the SHA-256 of the canonical list.
func BuildOutputs(files []OutputFile) (*Outputs, []byte, error) {
	if len(files) == 0 {
		return nil, nil, nil
	}
	data, err := json.Marshal(files)
	if err != nil {
		return nil, nil, fmt.Errorf("encode outputs: %w", err)
	}
	sum := sha256.Sum256(data)
	out := &Outputs{Count: len(files), Digest: hex.EncodeToString(sum[:])}
	if len(files) <= MaxInlineOutputs {
		out.Files = files
		return out, nil, nil
	}
	return out, data, nil
}

// CompareOutputs compares the outputs of a receipt with the files under root
// now. A manifest that is absent, or does not match the receipt's digest,
// gives one unknown entry named after it.
func CompareOutputs(root string, out *Outputs, manifest []byte) []OutputState {
	if out == nil {
		return nil
	}
	files := out.Files
	if out.Manifest != "" {
		sum := sha256.Sum256(manifest)
		if manifest == nil || hex.EncodeToString(sum[:]) != out.Digest || json.Unmarshal(manifest, &files) != nil {
			return []OutputState{{Path: out.Manifest, State: OutputUnknown}}
		}
	}
	states := make([]OutputState, 0, len(files))
	for _, f := range files {
		state := OutputMatch
		sum, err := fileDigest(filepath.Join(root, filepath.FromSlash(f.Path)))
		switch {
		case errors.Is(err, fs.ErrNotExist):
			state = OutputMissing
		case err != nil, sum != f.SHA256:
			state = OutputChanged
		}
		states = append(states, OutputState{Path: f.Path, State: state})
	}
	return states
}

func fileDigest(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
