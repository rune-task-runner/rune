// Package cache implements opt-in, per-task content-hash caching for tasks
// carrying [cache(inputs=[...], outputs=[...])]. There is NO timestamp-based
// skipping (Principle I): a task is skipped iff its fingerprint matches the
// stored one AND every declared output exists. Every decision is logged
// (cached/running) by the caller; corruption is treated as a miss, never an
// error.
package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Spec describes a cacheable task invocation.
type Spec struct {
	Key       string            // task name (display)
	Namespace string            // mod namespace ("" at top level)
	Root      string            // directory containing the Runefile (cache + glob base)
	Inputs    []string          // input glob patterns
	Outputs   []string          // output path patterns
	Body      string            // raw body text (pre-interpolation)
	Vars      map[string]string // resolved variables/params referenced by the task
	Executor  string            // executor identity (e.g. "sh" or "python:[python3]")
}

// Fingerprint is the on-disk cache record (contracts/cache-fingerprint.md).
type Fingerprint struct {
	Key       string   `json:"key"`
	Namespace string   `json:"namespace"`
	Hash      string   `json:"hash"`
	Inputs    []string `json:"inputs"`
	Outputs   []string `json:"outputs"`
	Executor  string   `json:"executor"`
	CreatedAt string   `json:"createdAt"`
}

// Decision is the result of evaluating the cache for a task.
type Decision struct {
	Skip       bool
	Hash       string
	InputFiles []string
}

// Decide computes the current fingerprint and reports whether the task may be
// skipped (hash matches the stored record AND all outputs exist).
func Decide(spec Spec) (Decision, error) {
	files, err := expandGlobs(spec.Root, spec.Inputs)
	if err != nil {
		return Decision{}, err
	}
	hash, err := computeHash(spec, files)
	if err != nil {
		return Decision{}, err
	}
	d := Decision{Hash: hash, InputFiles: files}

	stored, ok := readRecord(RecordPath(spec))
	if !ok || stored.Hash != hash {
		return d, nil
	}
	if !outputsExist(spec.Root, spec.Outputs) {
		return d, nil
	}
	d.Skip = true
	return d, nil
}

// Store writes the fingerprint record after a successful run. createdAt is
// informational only and never part of the hash.
func Store(spec Spec, hash, createdAt string) error {
	dir := cacheDir(spec.Root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	rec := Fingerprint{
		Key:       spec.Key,
		Namespace: spec.Namespace,
		Hash:      hash,
		Inputs:    spec.Inputs,
		Outputs:   spec.Outputs,
		Executor:  spec.Executor,
		CreatedAt: createdAt,
	}
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(RecordPath(spec), data, 0o644)
}

// OutputFiles lists, relative to root and with forward slashes, what the
// output patterns name: the files a pattern matches by the glob rules of
// Decide, else the path itself when it exists, a directory with everything
// below it. A CI bundle carries these between jobs (spec 059).
func OutputFiles(root string, outputs []string) ([]string, error) {
	set := map[string]bool{}
	for _, pat := range outputs {
		matches, err := expandGlobs(root, []string{pat})
		if err != nil {
			return nil, err
		}
		if len(matches) > 0 {
			for _, m := range matches {
				set[m] = true
			}
			continue
		}
		base := filepath.Join(root, pat)
		info, err := os.Lstat(base)
		if err != nil {
			continue
		}
		if !info.IsDir() {
			set[filepath.ToSlash(filepath.Clean(pat))] = true
			continue
		}
		err = filepath.WalkDir(base, func(path string, _ fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			set[filepath.ToSlash(rel)] = true
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	out := make([]string, 0, len(set))
	for f := range set {
		out = append(out, f)
	}
	sort.Strings(out)
	return out, nil
}

// Clear removes the project-local cache directory.
func Clear(root string) error {
	err := os.RemoveAll(cacheDir(root))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func cacheDir(root string) string { return filepath.Join(root, ".rune", "cache") }

// RecordPath is the file Store writes the fingerprint record of spec to.
func RecordPath(spec Spec) string {
	key := spec.Key
	if spec.Namespace != "" {
		key = spec.Namespace + "::" + spec.Key
	}
	return filepath.Join(cacheDir(spec.Root), sanitize(key)+".json")
}

var unsafeKeyChars = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func sanitize(key string) string { return unsafeKeyChars.ReplaceAllString(key, "__") }

// computeHash hashes the canonical serialization of inputs, body, vars, executor.
func computeHash(spec Spec, files []string) (string, error) {
	h := sha256.New()
	for _, rel := range files {
		sum, err := fileSHA(filepath.Join(spec.Root, rel))
		if err != nil {
			return "", err
		}
		fmt.Fprintf(h, "input\x00%s\x00%s\n", rel, sum)
	}
	fmt.Fprintf(h, "body\x00%s\n", spec.Body)
	names := make([]string, 0, len(spec.Vars))
	for k := range spec.Vars {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		fmt.Fprintf(h, "var\x00%s\x00%s\n", k, spec.Vars[k])
	}
	fmt.Fprintf(h, "exec\x00%s\n", spec.Executor)
	return hex.EncodeToString(h.Sum(nil)), nil
}

func fileSHA(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func readRecord(path string) (Fingerprint, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Fingerprint{}, false
	}
	var rec Fingerprint
	if err := json.Unmarshal(data, &rec); err != nil {
		return Fingerprint{}, false // corruption => miss
	}
	return rec, true
}

func outputsExist(root string, outputs []string) bool {
	for _, pat := range outputs {
		matches, err := expandGlobs(root, []string{pat})
		if err != nil || len(matches) == 0 {
			// Fall back to a literal path check (the output may not exist yet,
			// in which case the glob yields nothing => not satisfied).
			if _, statErr := os.Stat(filepath.Join(root, pat)); statErr != nil {
				return false
			}
		}
	}
	return true
}

// expandGlobs expands patterns (supporting **) into a sorted, deduped list of
// file paths relative to root.
func expandGlobs(root string, patterns []string) ([]string, error) {
	set := map[string]bool{}
	var res []*regexp.Regexp
	for _, p := range patterns {
		res = append(res, globToRegexp(p))
	}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			base := d.Name()
			if base == ".rune" || base == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		for _, re := range res {
			if re.MatchString(rel) {
				set[rel] = true
				break
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(set))
	for f := range set {
		out = append(out, f)
	}
	sort.Strings(out)
	return out, nil
}

// globToRegexp converts a glob pattern (with ** and *) to an anchored regexp.
func globToRegexp(pattern string) *regexp.Regexp {
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(pattern); i++ {
		c := pattern[i]
		switch c {
		case '*':
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				i++
				if i+1 < len(pattern) && pattern[i+1] == '/' {
					i++
					b.WriteString("(?:.*/)?")
				} else {
					b.WriteString(".*")
				}
			} else {
				b.WriteString("[^/]*")
			}
		case '?':
			b.WriteString("[^/]")
		case '.', '+', '(', ')', '|', '^', '$', '{', '}', '[', ']', '\\':
			b.WriteByte('\\')
			b.WriteByte(c)
		default:
			b.WriteByte(c)
		}
	}
	b.WriteString("$")
	re, err := regexp.Compile(b.String())
	if err != nil {
		return regexp.MustCompile(`^\x00$`) // never matches
	}
	return re
}
