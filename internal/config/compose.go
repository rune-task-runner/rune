package config

import (
	"os"
	"path/filepath"

	"github.com/rune-task-runner/rune/internal/ast"
	"github.com/rune-task-runner/rune/internal/diag"
	"github.com/rune-task-runner/rune/internal/parser"
)

// Compose resolves a file's imports (spliced into the same namespace) and mods
// (loaded as child namespaces addressable as name::task). It mutates f in place
// and returns any diagnostics (including reported name collisions, FR-011).
// Imported and mod files are read through src (spec FR-003): when a document is
// open in an editor overlay, its unsaved content is used; otherwise src falls
// back to disk. A nil src (or one that does not resolve a path) falls back to
// the filesystem, preserving the original disk-only behavior.
func Compose(f *ast.File, src diag.SourceProvider) diag.List {
	var diags diag.List
	seen := map[string]bool{f.Path: true}
	compose(f, src, seen, &diags)
	return diags
}

func compose(f *ast.File, src diag.SourceProvider, seen map[string]bool, diags *diag.List) {
	spliceImports(f, src, seen, diags)
	loadMods(f, src, seen, diags)
}

// readSource reads path through the overlay-aware provider first, falling back
// to disk when the provider does not resolve it. This implements the spec's
// resolution rule (overlay when open, else disk) for imported/mod files.
func readSource(src diag.SourceProvider, path string) ([]byte, error) {
	if src != nil {
		if b, ok := src(path); ok {
			return b, nil
		}
	}
	return os.ReadFile(path)
}

func spliceImports(f *ast.File, src diag.SourceProvider, seen map[string]bool, diags *diag.List) {
	base := filepath.Dir(f.Path)
	for _, im := range f.Imports {
		path := filepath.Join(base, filepath.FromSlash(im.Path))
		if seen[path] {
			continue // already spliced; avoid cycles
		}
		data, err := readSource(src, path)
		if err != nil {
			if im.Optional {
				continue
			}
			diags.Codef(diag.CodeUnresolvedImport, im.Sp, "cannot import %q: %v", im.Path, err)
			continue
		}
		seen[path] = true
		sub, sdiags := parser.Parse(path, string(data))
		*diags = append(*diags, sdiags...)
		compose(sub, src, seen, diags)
		spliceInto(f, sub, diags)
	}
}

// spliceInto merges sub's definitions into f, reporting collisions.
func spliceInto(f, sub *ast.File, diags *diag.List) {
	existingTasks := map[string]bool{}
	for _, t := range f.Tasks {
		existingTasks[t.Name] = true
	}
	existingVars := map[string]bool{}
	for _, a := range f.Assignments {
		existingVars[a.Name] = true
	}

	for _, t := range sub.Tasks {
		if existingTasks[t.Name] {
			diags.Codef(diag.CodeDuplicateNamespace, t.Sp, "import collision: task %q is already defined", t.Name)
			continue
		}
		existingTasks[t.Name] = true
		f.Tasks = append(f.Tasks, t)
	}
	for _, a := range sub.Assignments {
		if existingVars[a.Name] {
			diags.Codef(diag.CodeDuplicateNamespace, a.Sp, "import collision: variable %q is already defined", a.Name)
			continue
		}
		existingVars[a.Name] = true
		f.Assignments = append(f.Assignments, a)
	}
	// Imported settings fill gaps but never override the importer's settings.
	have := map[string]bool{}
	for _, s := range f.Settings {
		have[s.Name] = true
	}
	for _, s := range sub.Settings {
		if !have[s.Name] {
			f.Settings = append(f.Settings, s)
			have[s.Name] = true
		}
	}
}

func loadMods(f *ast.File, src diag.SourceProvider, seen map[string]bool, diags *diag.List) {
	base := filepath.Dir(f.Path)
	for _, m := range f.Mods {
		path := resolveModPath(base, m, src)
		if path == "" {
			diags.Codef(diag.CodeUnresolvedImport, m.Sp, "cannot find module %q", m.Name)
			continue
		}
		data, err := readSource(src, path)
		if err != nil {
			diags.Codef(diag.CodeUnresolvedImport, m.Sp, "cannot load module %q: %v", m.Name, err)
			continue
		}
		sub, sdiags := parser.Parse(path, string(data))
		*diags = append(*diags, sdiags...)
		// Compose the module's own imports/mods only once per path; a mod cycle
		// (a mods b, b mods a) would otherwise recurse without bound. The module
		// is still namespaced into f either way so its tasks stay addressable.
		if !seen[path] {
			seen[path] = true
			compose(sub, src, seen, diags)
		}
		namespaceInto(f, sub, m.Name, diags)
	}
}

// namespaceInto adds sub's tasks under the mod's namespace (name::task),
// rewriting intra-module dependency references, and merges its variables.
func namespaceInto(f, sub *ast.File, ns string, diags *diag.List) {
	local := map[string]bool{}
	for _, t := range sub.Tasks {
		local[t.Name] = true
	}
	prefix := ns + "::"
	rewrite := func(deps []*ast.DepCall) {
		for _, d := range deps {
			if local[d.Name] {
				d.Name = prefix + d.Name
			}
		}
	}
	existing := map[string]bool{}
	for _, t := range f.Tasks {
		existing[t.Name] = true
	}
	for _, t := range sub.Tasks {
		rewrite(t.Deps)
		rewrite(t.PostHooks)
		rewrite(t.FailHooks)
		t.Name = prefix + t.Name
		if existing[t.Name] {
			diags.Codef(diag.CodeDuplicateNamespace, t.Sp, "module collision: task %q already defined", t.Name)
			continue
		}
		existing[t.Name] = true
		f.Tasks = append(f.Tasks, t)
	}
	// v1 simplification: module variables share the parent scope.
	have := map[string]bool{}
	for _, a := range f.Assignments {
		have[a.Name] = true
	}
	for _, a := range sub.Assignments {
		if !have[a.Name] {
			f.Assignments = append(f.Assignments, a)
			have[a.Name] = true
		}
	}
}

// ResolveModPath resolves a mod declaration to its target file path using the
// same rules as Compose (explicit path, else <name>.rune / <name>/Runefile /
// <name>/.runefile), consulting src for open overlays. It returns "" when the
// mod cannot be resolved. Exposed so the analysis layer can build a complete
// import graph that includes directory-resolved mods.
func ResolveModPath(base string, m *ast.Mod, src diag.SourceProvider) string {
	return resolveModPath(base, m, src)
}

func resolveModPath(base string, m *ast.Mod, src diag.SourceProvider) string {
	if m.Path != "" {
		return filepath.Join(base, filepath.FromSlash(m.Path))
	}
	for _, cand := range []string{m.Name + ".rune", filepath.Join(m.Name, "Runefile"), filepath.Join(m.Name, ".runefile")} {
		p := filepath.Join(base, cand)
		if modExists(src, p) {
			return p
		}
	}
	return ""
}

// modExists reports whether a mod candidate resolves, preferring an open
// overlay document over disk (spec FR-003).
func modExists(src diag.SourceProvider, path string) bool {
	if src != nil {
		if _, ok := src(path); ok {
			return true
		}
	}
	_, err := os.Stat(path)
	return err == nil
}
