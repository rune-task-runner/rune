package language

import "strings"

// BuiltinKind classifies an entry in the language registry.
type BuiltinKind int

const (
	BuiltinFunction  BuiltinKind = iota // an expression built-in, e.g. env(...)
	BuiltinSetting                      // a `set NAME` setting
	BuiltinAttribute                    // a `[name]` task attribute
	BuiltinExecutor                     // a task executor, e.g. (python)
)

// Builtin is one entry of Rune language metadata. This registry is the SINGLE
// source consumed by hover, completion, and (incrementally) analyzer validation
// and CLI reference generation (spec FR-027): duplicate hard-coded lists of this
// metadata must not exist.
type Builtin struct {
	Name          string
	Kind          BuiltinKind
	Signature     string
	Documentation string
	// IntroducedIn is the semver in which the symbol first appeared, when known;
	// empty means "present since before versions were tracked". It feeds the
	// incompatible-version story and future minimum-version checks.
	IntroducedIn string
}

// builtinFunctions are the expression built-ins (mirrors internal/eval).
var builtinFunctions = []Builtin{
	{Name: "env", Kind: BuiltinFunction, Signature: "env(name, default?) -> string", Documentation: "Read an environment variable. When it is unset, the optional default (or an empty string) is returned."},
	{Name: "os", Kind: BuiltinFunction, Signature: "os() -> string", Documentation: "The host operating system (Go's GOOS), e.g. \"linux\", \"darwin\", \"windows\"."},
	{Name: "arch", Kind: BuiltinFunction, Signature: "arch() -> string", Documentation: "The host architecture (Go's GOARCH), e.g. \"amd64\", \"arm64\"."},
	{Name: "os_family", Kind: BuiltinFunction, Signature: "os_family() -> string", Documentation: "The OS family: \"unix\" or \"windows\"."},
	{Name: "num_cpus", Kind: BuiltinFunction, Signature: "num_cpus() -> string", Documentation: "The number of logical CPUs available."},
	{Name: "path_exists", Kind: BuiltinFunction, Signature: "path_exists(path) -> string", Documentation: "\"true\" if path exists on disk, otherwise \"false\"."},
	{Name: "read", Kind: BuiltinFunction, Signature: "read(path) -> string", Documentation: "The contents of the file at path."},
	{Name: "absolute_path", Kind: BuiltinFunction, Signature: "absolute_path(path) -> string", Documentation: "The absolute form of path."},
	{Name: "clean", Kind: BuiltinFunction, Signature: "clean(path) -> string", Documentation: "The shortest lexically-equivalent path (removes ./ and ../)."},
	{Name: "parent_dir", Kind: BuiltinFunction, Signature: "parent_dir(path) -> string", Documentation: "The parent directory of path."},
	{Name: "file_name", Kind: BuiltinFunction, Signature: "file_name(path) -> string", Documentation: "The final element of path."},
	{Name: "file_stem", Kind: BuiltinFunction, Signature: "file_stem(path) -> string", Documentation: "The file name without its extension."},
	{Name: "extension", Kind: BuiltinFunction, Signature: "extension(path) -> string", Documentation: "The file extension of path (including the dot)."},
	{Name: "join", Kind: BuiltinFunction, Signature: "join(elem, ...) -> string", Documentation: "Join path elements with the OS separator."},
	{Name: "uppercase", Kind: BuiltinFunction, Signature: "uppercase(s) -> string", Documentation: "s in upper case."},
	{Name: "lowercase", Kind: BuiltinFunction, Signature: "lowercase(s) -> string", Documentation: "s in lower case."},
	{Name: "capitalize", Kind: BuiltinFunction, Signature: "capitalize(s) -> string", Documentation: "s with its first letter upper-cased."},
	{Name: "trim", Kind: BuiltinFunction, Signature: "trim(s) -> string", Documentation: "s with leading and trailing whitespace removed."},
	{Name: "trim_start", Kind: BuiltinFunction, Signature: "trim_start(s) -> string", Documentation: "s with leading whitespace removed."},
	{Name: "trim_end", Kind: BuiltinFunction, Signature: "trim_end(s) -> string", Documentation: "s with trailing whitespace removed."},
	{Name: "replace", Kind: BuiltinFunction, Signature: "replace(s, from, to) -> string", Documentation: "s with each occurrence of from replaced by to."},
	{Name: "replace_regex", Kind: BuiltinFunction, Signature: "replace_regex(s, pattern, repl) -> string", Documentation: "s with regex pattern matches replaced by repl."},
	{Name: "quote", Kind: BuiltinFunction, Signature: "quote(s) -> string", Documentation: "s quoted for safe use as a single shell argument."},
	{Name: "require", Kind: BuiltinFunction, Signature: "require(name) -> string", Documentation: "The PATH location of executable name; errors if it is not found."},
	{Name: "which", Kind: BuiltinFunction, Signature: "which(name) -> string", Documentation: "The PATH location of executable name, or an empty string when not found."},
	{Name: "datetime", Kind: BuiltinFunction, Signature: "datetime(format?) -> string", Documentation: "The current date/time, optionally formatted by the given layout."},
	{Name: "uuid", Kind: BuiltinFunction, Signature: "uuid() -> string", Documentation: "A newly generated UUID."},
	{Name: "error", Kind: BuiltinFunction, Signature: "error(message) -> string", Documentation: "Abort evaluation with the given error message."},
	{Name: "sha256", Kind: BuiltinFunction, Signature: "sha256(s) -> string", Documentation: "The hex-encoded SHA-256 hash of s."},
	{Name: "sha256_file", Kind: BuiltinFunction, Signature: "sha256_file(path) -> string", Documentation: "The hex-encoded SHA-256 hash of the file at path."},
}

// builtinSettings are the valid `set NAME` settings (mirrors config/settings.go).
var builtinSettings = []Builtin{
	{Name: "working-directory", Kind: BuiltinSetting, Signature: "set working-directory := \"path\"", Documentation: "Base directory tasks run in, relative to the Runefile."},
	{Name: "quiet", Kind: BuiltinSetting, Signature: "set quiet", Documentation: "Suppress command echo for all tasks."},
	{Name: "export", Kind: BuiltinSetting, Signature: "set export", Documentation: "Export all module variables into the task environment."},
	{Name: "fallback", Kind: BuiltinSetting, Signature: "set fallback", Documentation: "Search parent directories for a Runefile when none is found locally."},
	{Name: "dotenv", Kind: BuiltinSetting, Signature: "set dotenv := \"path\"", Documentation: "Load environment variables from the given .env file."},
	{Name: "shell", Kind: BuiltinSetting, Signature: "set shell := [\"prog\", \"args\"...]", Documentation: "Command used to run shell task bodies."},
	{Name: "python", Kind: BuiltinSetting, Signature: "set python := [\"python3\"]", Documentation: "Command used for (python) tasks."},
	{Name: "node", Kind: BuiltinSetting, Signature: "set node := [\"node\"]", Documentation: "Command used for (node) tasks."},
	{Name: "agent_cmd", Kind: BuiltinSetting, Signature: "set agent_cmd := [\"...\"]", Documentation: "Command used to drive (agent) tasks."},
	{Name: "agent_provider", Kind: BuiltinSetting, Signature: "set agent_provider := \"name\"", Documentation: "Named agent provider for (agent) tasks."},
	{Name: "secrets", Kind: BuiltinSetting, Signature: "set secrets := [\"NAME\"]", Documentation: "Extra environment variable names whose values are masked as *** in all output."},
	{Name: "unmasked", Kind: BuiltinSetting, Signature: "set unmasked := [\"NAME\"]", Documentation: "Variable names exempted from the built-in secret-name masking patterns."},
	{Name: "minimum_version", Kind: BuiltinSetting, Signature: "set minimum_version := \"0.8.0\"", Documentation: "Pin the minimum Rune binary version required to run this Runefile."},
	{Name: "rune_version", Kind: BuiltinSetting, Signature: "set rune_version := \"0.8.0\"", Documentation: "Declare the Rune language version this Runefile targets."},
}

// builtinAttributes are the valid task attributes (mirrors ast Attr* constants).
var builtinAttributes = []Builtin{
	{Name: "private", Kind: BuiltinAttribute, Signature: "[private]", Documentation: "Hide the task from listings and disallow direct invocation."},
	{Name: "confirm", Kind: BuiltinAttribute, Signature: "[confirm(\"Prompt?\")]", Documentation: "Prompt for confirmation before running (maps to the MCP destructive hint)."},
	{Name: "group", Kind: BuiltinAttribute, Signature: "[group(\"name\")]", Documentation: "Group the task under a heading in listings."},
	{Name: "parallel", Kind: BuiltinAttribute, Signature: "[parallel]", Documentation: "Run this task's dependencies concurrently; the body starts only after all succeed."},
	{Name: "cache", Kind: BuiltinAttribute, Signature: "[cache(inputs = [], outputs = [])]", Documentation: "Skip the task when inputs and outputs are unchanged since the last run."},
	{Name: "env", Kind: BuiltinAttribute, Signature: "[env(\"KEY\", \"value\")]", Documentation: "Set an environment variable for the task."},
	{Name: "working-directory", Kind: BuiltinAttribute, Signature: "[working-directory(\"./path\")]", Documentation: "Run the task in the given directory, relative to the Runefile."},
	{Name: "network", Kind: BuiltinAttribute, Signature: "[network]", Documentation: "Declare the task uses the network (sets the MCP openWorldHint)."},
	{Name: "no-cd", Kind: BuiltinAttribute, Signature: "[no-cd]", Documentation: "Run the task in the invocation directory instead of the working directory."},
	{Name: "no-exit-message", Kind: BuiltinAttribute, Signature: "[no-exit-message]", Documentation: "Suppress the trailing error banner on failure (exit code is unaffected)."},
	{Name: "linux", Kind: BuiltinAttribute, Signature: "[linux]", Documentation: "Only available on Linux."},
	{Name: "macos", Kind: BuiltinAttribute, Signature: "[macos]", Documentation: "Only available on macOS."},
	{Name: "windows", Kind: BuiltinAttribute, Signature: "[windows]", Documentation: "Only available on Windows."},
	{Name: "unix", Kind: BuiltinAttribute, Signature: "[unix]", Documentation: "Only available on Unix-like systems (not Windows)."},
	{Name: "doc", Kind: BuiltinAttribute, Signature: "[doc(\"One-line doc\")]", Documentation: "Override the task's doc comment."},
	{Name: "script", Kind: BuiltinAttribute, Signature: "[script(\"/usr/bin/env python3\")]", Documentation: "Run the whole body as a script under this interpreter."},
	{Name: "context", Kind: BuiltinAttribute, Signature: "[context]", Documentation: "Project-health hook: output is injected into agent context at session start."},
	{Name: "param-doc", Kind: BuiltinAttribute, Signature: "[param-doc(\"name\", \"description\")]", Documentation: "Describe one parameter; surfaced to agents in the tool schema (spec 023)."},
	{Name: "returns", Kind: BuiltinAttribute, Signature: "[returns(\"expected output\")]", Documentation: "Describe the task's successful output; surfaced to agents and listings (spec 023)."},
}

// builtinExecutors are the recognized task executors (mirrors runtime.Select).
var builtinExecutors = []Builtin{
	{Name: "sh", Kind: BuiltinExecutor, Signature: "task (sh):", Documentation: "Run the body with Rune's built-in POSIX shell (mvdan.cc/sh). The default."},
	{Name: "python", Kind: BuiltinExecutor, Signature: "task (python):", Documentation: "Run the body with the configured Python interpreter."},
	{Name: "node", Kind: BuiltinExecutor, Signature: "task (node):", Documentation: "Run the body with the configured Node interpreter."},
	{Name: "agent", Kind: BuiltinExecutor, Signature: "task (agent):", Documentation: "Drive the body through an installed agent CLI."},
}

// Registry returns every language-metadata entry, across all kinds.
func Registry() []Builtin {
	out := make([]Builtin, 0, len(builtinFunctions)+len(builtinSettings)+len(builtinAttributes)+len(builtinExecutors))
	out = append(out, builtinFunctions...)
	out = append(out, builtinSettings...)
	out = append(out, builtinAttributes...)
	out = append(out, builtinExecutors...)
	return out
}

// OfKind returns the registry entries of a single kind.
func OfKind(kind BuiltinKind) []Builtin {
	switch kind {
	case BuiltinFunction:
		return builtinFunctions
	case BuiltinSetting:
		return builtinSettings
	case BuiltinAttribute:
		return builtinAttributes
	case BuiltinExecutor:
		return builtinExecutors
	default:
		return nil
	}
}

// Lookup returns the registry entry of the given kind and name, if present.
func Lookup(kind BuiltinKind, name string) (Builtin, bool) {
	for _, b := range OfKind(kind) {
		if b.Name == name {
			return b, true
		}
	}
	return Builtin{}, false
}

// IsValid reports whether name is a recognized entry of the given kind. This is
// what analyzer validation consults so completion and validation agree (R9).
func IsValid(kind BuiltinKind, name string) bool {
	_, ok := Lookup(kind, name)
	return ok
}

// MatchKind returns the entries of a kind whose name starts with prefix,
// ordered as declared (used by completion).
func MatchKind(kind BuiltinKind, prefix string) []Builtin {
	var out []Builtin
	for _, b := range OfKind(kind) {
		if strings.HasPrefix(b.Name, prefix) {
			out = append(out, b)
		}
	}
	return out
}
