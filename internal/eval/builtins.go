package eval

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/rune-task-runner/rune/internal/ast"
	"github.com/rune-task-runner/rune/internal/token"
)

// compare evaluates a comparison operator used in a conditional condition.
func compare(op token.Kind, l, r string, span token.Span) (bool, *Error) {
	switch op {
	case token.EQ:
		return l == r, nil
	case token.NEQ:
		return l != r, nil
	case token.MATCH:
		re, err := regexp.Compile(r)
		if err != nil {
			return false, &Error{Span: span, Msg: "invalid regular expression: " + err.Error()}
		}
		return re.MatchString(l), nil
	default:
		return false, &Error{Span: span, Msg: "internal: unknown comparison operator"}
	}
}

// callBuiltin evaluates a function call's arguments and dispatches to the
// builtin registry. The expression sublanguage has no user-defined functions.
func (e *Evaluator) callBuiltin(fc *ast.FuncCall) (string, *Error) {
	if out, ok := e.quoteEachList(fc); ok {
		return out, nil
	}
	args := make([]string, len(fc.Args))
	for i, a := range fc.Args {
		v, err := e.Eval(a)
		if err != nil {
			return "", err
		}
		args[i] = v
	}
	fn, ok := builtins()[fc.Name]
	if !ok {
		return "", &Error{Span: fc.Sp, Msg: "unknown function: " + fc.Name}
	}
	return fn(e, args, fc.Sp)
}

// quoteEachList handles quote_each of a bare variadic reference. It must run
// before arguments are evaluated: the string value is already joined, so the
// element boundaries exist only in Scope.Lists (spec 030 R2). Map membership,
// not length, marks a variadic, so an empty list is not mistaken for a scalar.
func (e *Evaluator) quoteEachList(fc *ast.FuncCall) (string, bool) {
	if fc.Name != "quote_each" || len(fc.Args) != 1 {
		return "", false
	}
	ref, isRef := fc.Args[0].(*ast.VarRef)
	if !isRef {
		return "", false
	}
	elems, ok := e.scope.Lists[ref.Name]
	if !ok {
		return "", false
	}
	quoted := make([]string, len(elems))
	for i, el := range elems {
		quoted[i] = shellQuote(el)
	}
	return strings.Join(quoted, " "), true
}

// IsBuiltin reports whether name is a known built-in function. The analyzer
// uses this to flag unknown function calls statically.
func IsBuiltin(name string) bool {
	_, ok := builtins()[name]
	return ok
}

// BuiltinNames returns the sorted names of every built-in function. It is the
// authoritative set the language registry (internal/language) verifies itself
// against so completion/hover/validation never drift from evaluation (FR-027).
func BuiltinNames() []string {
	m := builtins()
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

type builtinFn func(e *Evaluator, args []string, span token.Span) (string, *Error)

func arityErr(span token.Span, name string, want string, got int) *Error {
	return &Error{Span: span, Msg: fmt.Sprintf("%s() expects %s, got %d", name, want, got)}
}

// builtins is the immutable registry of built-in functions, constructed once on
// first use. Lazy construction via sync.OnceValue avoids an init() and a mutable
// package global (idiomatic Go discipline); the set never changes at runtime.
var builtins = sync.OnceValue(func() map[string]builtinFn {
	return map[string]builtinFn{
		// Environment & host.
		"env": func(e *Evaluator, a []string, s token.Span) (string, *Error) {
			if len(a) != 1 && len(a) != 2 {
				return "", arityErr(s, "env", "1 or 2 arguments", len(a))
			}
			v, ok := e.lookupEnv(a[0])
			if ok {
				return v, nil
			}
			if len(a) == 2 {
				return a[1], nil
			}
			return "", &Error{Span: s, Msg: "environment variable not set: " + a[0]}
		},
		"os":        hostFn(func(e *Evaluator) string { return e.goos() }),
		"arch":      hostFn(func(e *Evaluator) string { return e.arch() }),
		"os_family": hostFn(func(e *Evaluator) string { return osFamily(e.goos()) }),
		"num_cpus":  hostFn(func(e *Evaluator) string { return fmt.Sprintf("%d", runtime.NumCPU()) }),

		// Path operations (forward-slash, Principle V).
		"join": func(e *Evaluator, a []string, s token.Span) (string, *Error) {
			if len(a) == 0 {
				return "", arityErr(s, "join", "at least 1 argument", 0)
			}
			out := a[0]
			for _, p := range a[1:] {
				out = joinPath(out, p)
			}
			return out, nil
		},
		"clean":         unary("clean", func(p string) string { return path.Clean(p) }),
		"extension":     unary("extension", func(p string) string { return strings.TrimPrefix(path.Ext(p), ".") }),
		"file_name":     unary("file_name", func(p string) string { return path.Base(p) }),
		"file_stem":     unary("file_stem", func(p string) string { return strings.TrimSuffix(path.Base(p), path.Ext(p)) }),
		"parent_dir":    unary("parent_dir", func(p string) string { return path.Dir(p) }),
		"absolute_path": absolutePath,

		// String operations.
		"uppercase":     unary("uppercase", strings.ToUpper),
		"lowercase":     unary("lowercase", strings.ToLower),
		"trim":          unary("trim", strings.TrimSpace),
		"trim_start":    unary("trim_start", func(s string) string { return strings.TrimLeft(s, " \t\n\r") }),
		"trim_end":      unary("trim_end", func(s string) string { return strings.TrimRight(s, " \t\n\r") }),
		"capitalize":    unary("capitalize", capitalize),
		"replace":       replaceFn,
		"replace_regex": replaceRegexFn,

		// Filesystem.
		"path_exists": func(e *Evaluator, a []string, s token.Span) (string, *Error) {
			if len(a) != 1 {
				return "", arityErr(s, "path_exists", "1 argument", len(a))
			}
			if _, err := os.Stat(a[0]); err == nil {
				return "true", nil
			}
			return "false", nil
		},
		"read": func(e *Evaluator, a []string, s token.Span) (string, *Error) {
			if len(a) != 1 {
				return "", arityErr(s, "read", "1 argument", len(a))
			}
			data, err := os.ReadFile(a[0])
			if err != nil {
				return "", &Error{Span: s, Msg: "read: " + err.Error()}
			}
			return string(data), nil
		},

		// Hashing.
		"sha256": func(e *Evaluator, a []string, s token.Span) (string, *Error) {
			if len(a) != 1 {
				return "", arityErr(s, "sha256", "1 argument", len(a))
			}
			sum := sha256.Sum256([]byte(a[0]))
			return hex.EncodeToString(sum[:]), nil
		},
		"sha256_file": func(e *Evaluator, a []string, s token.Span) (string, *Error) {
			if len(a) != 1 {
				return "", arityErr(s, "sha256_file", "1 argument", len(a))
			}
			data, err := os.ReadFile(a[0])
			if err != nil {
				return "", &Error{Span: s, Msg: "sha256_file: " + err.Error()}
			}
			sum := sha256.Sum256(data)
			return hex.EncodeToString(sum[:]), nil
		},

		// Misc.
		"uuid": func(e *Evaluator, a []string, s token.Span) (string, *Error) {
			if len(a) != 0 {
				return "", arityErr(s, "uuid", "0 arguments", len(a))
			}
			return newUUID(), nil
		},
		"datetime": func(e *Evaluator, a []string, s token.Span) (string, *Error) {
			now := time.Now()
			if len(a) == 0 {
				return now.Format(time.RFC3339), nil
			}
			if len(a) == 1 {
				return now.Format(a[0]), nil
			}
			return "", arityErr(s, "datetime", "0 or 1 arguments", len(a))
		},
		"error": func(e *Evaluator, a []string, s token.Span) (string, *Error) {
			msg := "error()"
			if len(a) == 1 {
				msg = a[0]
			}
			return "", &Error{Span: s, Msg: msg}
		},
		"quote":      unary("quote", shellQuote),
		"quote_each": unary("quote_each", shellQuote),
		"require":    requireFn,
		"which":      whichFn,
	}
})

// --- helpers ---

func (e *Evaluator) lookupEnv(name string) (string, bool) {
	if e.scope.Env != nil {
		return e.scope.Env(name)
	}
	return os.LookupEnv(name)
}

func (e *Evaluator) goos() string {
	if e.scope.GOOS != "" {
		return e.scope.GOOS
	}
	return runtime.GOOS
}

func (e *Evaluator) arch() string {
	if e.scope.Arch != "" {
		return e.scope.Arch
	}
	return runtime.GOARCH
}

func osFamily(goos string) string {
	if goos == "windows" {
		return "windows"
	}
	return "unix"
}

func hostFn(f func(e *Evaluator) string) builtinFn {
	return func(e *Evaluator, a []string, s token.Span) (string, *Error) {
		if len(a) != 0 {
			return "", arityErr(s, "host function", "0 arguments", len(a))
		}
		return f(e), nil
	}
}

func unary(name string, f func(string) string) builtinFn {
	return func(e *Evaluator, a []string, s token.Span) (string, *Error) {
		if len(a) != 1 {
			return "", arityErr(s, name, "1 argument", len(a))
		}
		return f(a[0]), nil
	}
}

func absolutePath(e *Evaluator, a []string, s token.Span) (string, *Error) {
	if len(a) != 1 {
		return "", arityErr(s, "absolute_path", "1 argument", len(a))
	}
	abs, err := filepath.Abs(a[0])
	if err != nil {
		return "", &Error{Span: s, Msg: "absolute_path: " + err.Error()}
	}
	return filepath.ToSlash(abs), nil
}

func replaceFn(e *Evaluator, a []string, s token.Span) (string, *Error) {
	if len(a) != 3 {
		return "", arityErr(s, "replace", "3 arguments (s, from, to)", len(a))
	}
	return strings.ReplaceAll(a[0], a[1], a[2]), nil
}

func replaceRegexFn(e *Evaluator, a []string, s token.Span) (string, *Error) {
	if len(a) != 3 {
		return "", arityErr(s, "replace_regex", "3 arguments (s, pattern, repl)", len(a))
	}
	re, err := regexp.Compile(a[1])
	if err != nil {
		return "", &Error{Span: s, Msg: "replace_regex: invalid pattern: " + err.Error()}
	}
	return re.ReplaceAllString(a[0], a[2]), nil
}

func requireFn(e *Evaluator, a []string, s token.Span) (string, *Error) {
	if len(a) != 1 {
		return "", arityErr(s, "require", "1 argument", len(a))
	}
	p, err := exec.LookPath(a[0])
	if err != nil {
		return "", &Error{Span: s, Msg: "required executable not found on PATH: " + a[0]}
	}
	return p, nil
}

func whichFn(e *Evaluator, a []string, s token.Span) (string, *Error) {
	if len(a) != 1 {
		return "", arityErr(s, "which", "1 argument", len(a))
	}
	p, err := exec.LookPath(a[0])
	if err != nil {
		return "", nil // empty string when not found
	}
	return p, nil
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + strings.ToLower(s[1:])
}

// shellQuote single-quotes a string for safe shell interpolation.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
