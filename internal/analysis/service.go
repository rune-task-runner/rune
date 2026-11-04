package analysis

import (
	"context"
	"path/filepath"

	"github.com/rune-task-runner/rune/internal/analyzer"
	"github.com/rune-task-runner/rune/internal/ast"
	"github.com/rune-task-runner/rune/internal/config"
	"github.com/rune-task-runner/rune/internal/diag"
	"github.com/rune-task-runner/rune/internal/language"
	"github.com/rune-task-runner/rune/internal/parser"
)

// Service is the reusable analysis layer shared by the CLI, `rune analyze`, the
// language server, and MCP task discovery (spec FR-002). It executes nothing.
type Service struct {
	store SourceStore
}

// NewService returns a Service backed by the given source store (a
// DiskSourceStore for one-shot CLI use, or an OverlaySourceStore for the LSP).
// A nil store defaults to disk.
func NewService(store SourceStore) *Service {
	if store == nil {
		store = DiskSourceStore{}
	}
	return &Service{store: store}
}

// Store exposes the service's source store (the LSP manages overlays through it).
func (s *Service) Store() SourceStore { return s.store }

// Analyze runs parse → compose → analyze → build index → build import graph for
// one entry document and returns an immutable Snapshot. Unlike the execution
// path, it aggregates ALL diagnostics (it does not stop at the first failing
// stage) so editors and `rune analyze` see everything at once. It never
// executes tasks, shells, or network requests (spec FR-028).
func (s *Service) Analyze(ctx context.Context, req AnalyzeRequest) (*Snapshot, error) {
	src := req.Content
	if src == "" {
		b, err := s.store.Read(ctx, req.URI)
		if err != nil {
			return nil, err
		}
		src = string(b)
	}

	file, diags := parser.Parse(req.URI, src)

	// Imported/mod files resolve through the overlay-aware provider; the entry's
	// (possibly unsaved) content is served for its own path so rendering and any
	// self-reference use exactly what we analyzed.
	provider := entryProvider(ctx, s.store, req.URI, src)

	diags = append(diags, config.Compose(file, provider)...)
	diags = append(diags, analyzer.Analyze(file)...)
	diags = append(diags, analyzer.CheckDocumentation(file)...)
	diags = append(diags, analyzer.CheckQuoting(file)...)
	diags = append(diags, analyzer.CheckSettings(file)...)

	idx := language.BuildIndex(file)
	graph := buildImportGraph(req.URI, file, provider)

	return &Snapshot{
		URI:         req.URI,
		Version:     req.Version,
		File:        file,
		Sources:     provider,
		Diagnostics: diags,
		Symbols:     idx,
		Imports:     graph,
	}, nil
}

// entryProvider serves the entry document's analyzed content for its own path
// and delegates every other path to the store (overlay-then-disk).
func entryProvider(ctx context.Context, store SourceStore, entryURI DocumentURI, entrySrc string) diag.SourceProvider {
	base := Provider(ctx, store)
	return func(path string) ([]byte, bool) {
		if path == entryURI {
			return []byte(entrySrc), true
		}
		return base(path)
	}
}

// buildImportGraph records the entry file's direct import/mod edges. Deeper
// transitive edges are added as those files are themselves analyzed; the LSP
// composes per-entry graphs into a workspace graph.
func buildImportGraph(entry DocumentURI, f *ast.File, src diag.SourceProvider) ImportGraph {
	g := NewImportGraph()
	if f == nil {
		return g
	}
	base := filepath.Dir(entry)
	for _, im := range f.Imports {
		g.AddEdge(entry, filepath.Join(base, filepath.FromSlash(im.Path)))
	}
	for _, m := range f.Mods {
		// Resolve mods through the same rules as Compose so directory-resolved
		// mods (`mod foo` -> foo/Runefile) also get an edge — otherwise saving
		// such a mod file would not invalidate the open document that imports it.
		if target := config.ResolveModPath(base, m, src); target != "" {
			g.AddEdge(entry, target)
		}
	}
	return g
}
