package cli

import (
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/rune-task-runner/rune/internal/analyzer"
	"github.com/rune-task-runner/rune/internal/ast"
	"github.com/rune-task-runner/rune/internal/config"
	"github.com/rune-task-runner/rune/internal/eval"
	"github.com/rune-task-runner/rune/internal/parser"
	"github.com/rune-task-runner/rune/internal/style"
	"github.com/rune-task-runner/rune/mcpserver"
)

// loadedModule is the parsed, composed, analyzed module plus its resolved
// scope/settings — shared by the MCP server and agent paths.
type loadedModule struct {
	file     *ast.File
	tasks    map[string]*ast.Task
	assigns  map[string]*ast.Assignment
	settings config.Settings
	root     string
	workDir  string
	baseEnv  []string
	scope    *eval.Scope
}

// loadModule resolves a Runefile statically (parse → compose → analyze) and
// builds its scope, settings, and environment. Diagnostics are rendered and a
// ValidationError returned on failure (exit 3). allowIgnoreVersion downgrades an
// unmet minimum_version to a warning: callers pass the CLI --ignore-version flag
// on interactive paths and the operator-only opt on the MCP/agent path.
func loadModule(opts Options, runefile string, allowIgnoreVersion bool) (*loadedModule, error) {
	root := filepath.Dir(runefile)
	source, err := os.ReadFile(runefile)
	if err != nil {
		return nil, &UsageError{Err: err}
	}
	file, diags := parser.Parse(runefile, string(source))
	src := newSourceProvider(runefile, source)
	// Enforce minimum_version before imports/analysis on the MCP/agent path too.
	if err := enforceMinimumVersion(opts, file, src, allowIgnoreVersion); err != nil {
		return nil, err
	}
	diags = append(diags, config.Compose(file, src)...)
	if diags.HasErrors() {
		renderDiags(opts, diags, src)
		return nil, &ValidationError{Err: errorf("%d static error(s)", countErrors(diags))}
	}
	if adiags := analyzer.Analyze(file); adiags.HasErrors() {
		renderDiags(opts, adiags, src)
		return nil, &ValidationError{Err: errorf("%d static error(s)", countErrors(adiags))}
	}

	scope := eval.NewScope(indexAssignments(file), map[string]string{})
	scope.GOOS = runtime.GOOS
	scope.Arch = runtime.GOARCH
	settings, sdiags := config.ResolveSettings(file, eval.New(scope))
	if sdiags.HasErrors() {
		renderDiags(opts, sdiags, src)
		return nil, &ValidationError{Err: errorf("invalid settings")}
	}

	return &loadedModule{
		file:     file,
		tasks:    indexTasks(file),
		assigns:  indexAssignments(file),
		settings: settings,
		root:     root,
		workDir:  resolveWorkDir(runefile, settings.WorkingDir),
		baseEnv:  buildEnv(settings, scope, root),
		scope:    scope,
	}, nil
}

// newMCPAdapter builds an mcpserver.Engine from a resolved Runefile.
func newMCPAdapter(opts Options, runefile string) (*mcpAdapter, error) {
	// On the MCP/agent path, ignoring an unmet minimum_version is an operator-only
	// opt (disabled by default) — never the CLI --ignore-version flag.
	mod, err := loadModule(opts, runefile, opts.MCPAllowIgnoreVersion)
	if err != nil {
		return nil, err
	}
	return &mcpAdapter{
		file:      mod.file,
		tasks:     mod.tasks,
		assigns:   mod.assigns,
		settings:  mod.settings,
		root:      mod.root,
		workDir:   mod.workDir,
		baseEnv:   mod.baseEnv,
		overrides: map[string]string{},
		now:       func() string { return "" },
		maskSet:   deriveMaskSet(mod.baseEnv, mod.tasks, mod.settings.Secrets, mod.settings.Unmasked),
		goos:      runtime.GOOS,
	}, nil
}

// ServeMCP starts the MCP server over stdio (default) or Streamable HTTP.
func ServeMCP(opts Options, useHTTP bool, addr, tokenFile string) error {
	// Every check before the [context] hook below runs no Runefile code, so a
	// bad --allow-destructive value has no side effects (spec 031 FR-010).
	granted, err := parseAllowDestructive(opts.MCPAllowDestructive)
	if err != nil {
		return err
	}
	runefile, err := config.Resolve(opts.File, opts.Cwd)
	if err != nil {
		return &UsageError{Err: err}
	}
	adapter, err := newMCPAdapter(opts, runefile)
	if err != nil {
		return err
	}
	if err := checkGrant(granted, adapter.tasks); err != nil {
		return err
	}
	ctx := opts.ctx()
	// Validate the transport before anything executes: a usage error must
	// never run Runefile code (the [context] hook below runs task commands).
	var httpToken string
	if useHTTP {
		httpToken, err = readToken(tokenFile)
		if err != nil {
			return &UsageError{Err: err}
		}
		if addr == "" {
			addr = "127.0.0.1:7777"
		}
	}
	warnNonDestructive(opts.Stderr, opts.themeStderr(), granted, adapter.tasks)
	// The analyzer rejects [confirm] anywhere in the [context] hook's closure,
	// so setting the grant before the hook runs never approves anything there.
	adapter.grants = make(map[string]bool, len(granted))
	for _, name := range granted {
		adapter.grants[name] = true
	}
	// Run the [context] hook once at server start; its masked output becomes
	// the instructions of every session's initialize result (spec 021 FR-002).
	instructions := adapter.gatherContext(ctx, opts.Stderr)
	srv := mcpserver.New(adapter, mcpserver.Options{
		AllowDestructive:     granted,
		AllowDestructiveFlag: "--allow-destructive",
		Version:              opts.Version,
		Instructions:         instructions,
	})
	// Server chrome is dimmed like the rest of Rune's meta output (014 C3).
	fmt.Fprintln(opts.Stderr, opts.themeStderr().Muted.Render(startupLine(useHTTP, addr, destructiveOnly(granted, adapter.tasks, adapter.goos))))
	if useHTTP {
		return srv.ServeHTTP(ctx, mcpserver.HTTPConfig{Addr: addr, Token: httpToken})
	}
	return srv.ServeStdio(ctx)
}

// parseAllowDestructive turns the raw --allow-destructive values into task
// names: each value is split on commas, each item trimmed, and a repeated
// name kept once at its first position (spec 031 R1).
func parseAllowDestructive(values []string) ([]string, error) {
	var names []string
	seen := map[string]bool{}
	for _, v := range values {
		for _, item := range strings.Split(v, ",") {
			name := strings.TrimSpace(item)
			if name == "" {
				return nil, usagef("--allow-destructive: empty task name")
			}
			if seen[name] {
				continue
			}
			seen[name] = true
			names = append(names, name)
		}
	}
	return names, nil
}

// checkGrant rejects the first name that is not a task of the loaded
// Runefile, suggesting the closest one. Every task the CLI accepts by name
// counts, including private, OS-restricted, and imported (ns::task) ones.
func checkGrant(names []string, tasks map[string]*ast.Task) error {
	for _, name := range names {
		if _, ok := tasks[name]; ok {
			continue
		}
		// Sorted, so a tie between equally close names resolves the same way
		// on every run.
		candidates := slices.Sorted(maps.Keys(tasks))
		if s, ok := nearest(name, candidates); ok {
			return usagef("unknown task in --allow-destructive: %s (did you mean %q?)", name, s)
		}
		return usagef("unknown task in --allow-destructive: %s", name)
	}
	return nil
}

// warnNonDestructive prints one warning per granted task without [confirm]:
// such a grant has no effect, but it must not stop the server, so a shared
// client configuration keeps working when [confirm] is removed (FR-011).
func warnNonDestructive(w io.Writer, theme style.Theme, names []string, tasks map[string]*ast.Task) {
	for _, name := range names {
		if t, ok := tasks[name]; ok && t.Attr(ast.AttrConfirm) == nil {
			fmt.Fprintf(w, "%s: --allow-destructive %s has no effect: task %q is not destructive\n",
				theme.Warning.Render("warning"), name, name)
		}
	}
}

// destructiveOnly keeps, in order, the granted names whose task has [confirm]
// and can run on goos: the startup line lists only grants that take effect on
// this host (spec 031 FR-012). A task restricted to another OS is accepted
// silently, because one client configuration may serve several hosts.
func destructiveOnly(names []string, tasks map[string]*ast.Task, goos string) []string {
	var out []string
	for _, name := range names {
		if t, ok := tasks[name]; ok && t.Attr(ast.AttrConfirm) != nil && t.AvailableOn(goos) {
			out = append(out, name)
		}
	}
	return out
}

// startupLine is the unstyled line the server prints on stderr when it
// starts. With no destructive grant it is byte-identical to before spec 031.
func startupLine(useHTTP bool, addr string, granted []string) string {
	list := strings.Join(granted, ", ")
	switch {
	case useHTTP && len(granted) > 0:
		return fmt.Sprintf("rune MCP server on http://%s (token required; destructive tasks allowed: %s)", addr, list)
	case useHTTP:
		return fmt.Sprintf("rune MCP server on http://%s (token required)", addr)
	case len(granted) > 0:
		return fmt.Sprintf("rune MCP server on stdio (destructive tasks allowed: %s)", list)
	default:
		return "rune MCP server on stdio"
	}
}

func readToken(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("the HTTP transport requires --token-file PATH")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	tok := strings.TrimSpace(string(data))
	if tok == "" {
		return "", fmt.Errorf("token file %q is empty", path)
	}
	return tok, nil
}
