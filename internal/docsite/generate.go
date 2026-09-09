package docsite

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// Options configures one generation run. Every path is relative to the
// repository root except DocsDir and NavPath, which are read directly.
type Options struct {
	DocsDir     string // where the canonical Markdown lives, e.g. "docs"
	NavPath     string // the navigation manifest, e.g. "docs/nav.yaml"
	OutDir      string // generated content tree, e.g. "website/src/content/docs"
	AssetsDir   string // copied assets root, e.g. "website/public"
	SidebarPath string // generated sidebar JSON
	LandingPath string // committed landing page copied to OutDir/index.mdx; "" to skip
	Config      Config
}

// DefaultOptions returns the configuration the repository actually uses.
func DefaultOptions() Options {
	return Options{
		DocsDir:     "docs",
		NavPath:     filepath.Join("docs", "nav.yaml"),
		OutDir:      filepath.Join("website", "src", "content", "docs"),
		AssetsDir:   filepath.Join("website", "public"),
		SidebarPath: filepath.Join("website", "src", "generated", "sidebar.json"),
		LandingPath: filepath.Join("website", "src", "landing", "index.mdx"),
		Config: Config{
			BasePath: "/rune",
			BlobBase: "https://github.com/rune-task-runner/rune/blob/main",
			EditBase: "https://github.com/rune-task-runner/rune/edit/main/docs",
		},
	}
}

// outputPath is where a docs/ page is written inside the content tree. It is
// derived from the page's slug, not its source path, so the file Starlight
// serves and the URL every rewritten link points at cannot disagree: docs/
// README.md becomes index.md, and how-to/README.md becomes how-to.md.
func outputPath(relPath string) string {
	slug := Slug(relPath)
	if slug == "" {
		return "index.md"
	}
	return slug + ".md"
}

// Plan is everything a generation run produces: file contents keyed by
// slash-separated path relative to the repository root, the docs/-relative pages
// published, and the sidebar. Building a Plan writes nothing, so tests and the
// -check flag can inspect a run without touching the working tree.
type Plan struct {
	Files   map[string][]byte
	Pages   []string
	Sidebar []SidebarGroup

	dirs []string // the output directories Write is allowed to clear
}

// Build reads the documentation and the manifest and returns the Plan for them.
func Build(opts Options) (*Plan, error) {
	navSrc, err := os.ReadFile(opts.NavPath)
	if err != nil {
		return nil, fmt.Errorf("read nav manifest: %w", err)
	}
	nav, err := ParseNav(navSrc, filepath.ToSlash(opts.NavPath))
	if err != nil {
		return nil, err
	}
	if opts.Config.BasePath == "" {
		opts.Config.BasePath = nav.Base
	}
	opts.Config.Excluded = func(relPath string) bool { return MatchExcluded(nav, relPath) }

	allPages, assets, err := scanDocs(opts.DocsDir, nav)
	if err != nil {
		return nil, err
	}

	navOrder, err := NavPages(nav, allPages)
	if err != nil {
		return nil, err
	}
	sidebar, err := Sidebar(nav, allPages)
	if err != nil {
		return nil, err
	}

	plan := &Plan{
		Files:   map[string][]byte{},
		Pages:   navOrder,
		Sidebar: sidebar,
		dirs:    []string{opts.OutDir, filepath.Dir(opts.SidebarPath)},
	}

	// Only pages the manifest references are published: docs/README.md is the
	// map for readers on GitHub, and the site's root is the landing page. The
	// drift test in test/docs guarantees nothing else is dropped silently.
	for _, relPath := range navOrder {
		src, err := os.ReadFile(filepath.Join(opts.DocsDir, filepath.FromSlash(relPath)))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", relPath, err)
		}
		out, _ := Transform(string(src), relPath, opts.Config)
		plan.Files[joinSlash(opts.OutDir, outputPath(relPath))] = []byte(out)
	}

	for _, relPath := range assets {
		data, err := os.ReadFile(filepath.Join(opts.DocsDir, filepath.FromSlash(relPath)))
		if err != nil {
			return nil, fmt.Errorf("read asset %s: %w", relPath, err)
		}
		plan.Files[joinSlash(opts.AssetsDir, relPath)] = data
		plan.dirs = append(plan.dirs, joinSlash(opts.AssetsDir, path.Dir(relPath)))
	}

	// The landing page is committed, hand-written, and copied in. It is optional
	// so the generator is usable before Task 10 adds it.
	if opts.LandingPath != "" {
		landing, err := os.ReadFile(opts.LandingPath)
		switch {
		case err == nil:
			plan.Files[joinSlash(opts.OutDir, "index.mdx")] = landing
		case os.IsNotExist(err):
			// no landing page yet: the site simply has no root page
		default:
			return nil, fmt.Errorf("read landing page: %w", err)
		}
	}

	sidebarJSON, err := json.MarshalIndent(sidebar, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal sidebar: %w", err)
	}
	plan.Files[filepath.ToSlash(opts.SidebarPath)] = append(sidebarJSON, '\n')

	return plan, nil
}

// scanDocs returns the docs/-relative Markdown pages to publish and the asset
// files to copy, both excluding anything the manifest excludes.
func scanDocs(docsDir string, nav *Nav) (pages, assets []string, err error) {
	err = filepath.WalkDir(docsDir, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(docsDir, p)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		if MatchExcluded(nav, rel) {
			return nil
		}
		switch {
		case strings.EqualFold(path.Ext(rel), ".md"):
			pages = append(pages, rel)
		case strings.HasPrefix(rel, "img/"):
			assets = append(assets, rel)
		}
		return nil
	})
	if err != nil {
		return nil, nil, fmt.Errorf("walk %s: %w", docsDir, err)
	}
	sort.Strings(pages)
	sort.Strings(assets)
	return pages, assets, nil
}

// Write clears the plan's output directories and writes every file, so a page
// deleted from docs/ disappears from the site.
func (p *Plan) Write(root string) error {
	for _, dir := range p.managedDirs() {
		if err := os.RemoveAll(filepath.Join(root, filepath.FromSlash(dir))); err != nil {
			return fmt.Errorf("clear %s: %w", dir, err)
		}
	}
	for rel, content := range p.Files {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return fmt.Errorf("mkdir for %s: %w", rel, err)
		}
		if err := os.WriteFile(abs, content, 0o644); err != nil {
			return fmt.Errorf("write %s: %w", rel, err)
		}
	}
	return nil
}

// Check reports the paths where the plan and the files on disk disagree —
// missing, differing, or present on disk but not in the plan. It writes nothing.
func (p *Plan) Check(root string) ([]string, error) {
	var diff []string
	for rel, want := range p.Files {
		got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if os.IsNotExist(err) {
			diff = append(diff, rel+" (missing)")
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", rel, err)
		}
		if !bytes.Equal(got, want) {
			diff = append(diff, rel+" (differs)")
		}
	}

	planned := map[string]bool{}
	for rel := range p.Files {
		planned[rel] = true
	}
	for _, dir := range p.managedDirs() {
		abs := filepath.Join(root, filepath.FromSlash(dir))
		err := filepath.WalkDir(abs, func(fp string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				if os.IsNotExist(walkErr) {
					return nil
				}
				return walkErr
			}
			if d.IsDir() {
				return nil
			}
			rel, relErr := filepath.Rel(root, fp)
			if relErr != nil {
				return relErr
			}
			if slash := filepath.ToSlash(rel); !planned[slash] {
				diff = append(diff, slash+" (stale)")
			}
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("walk %s: %w", dir, err)
		}
	}

	sort.Strings(diff)
	return diff, nil
}

// managedDirs returns the deduplicated directories Write may clear.
func (p *Plan) managedDirs() []string {
	seen := map[string]bool{}
	var out []string
	for _, d := range p.dirs {
		s := filepath.ToSlash(d)
		if s == "" || s == "." || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func joinSlash(base, rel string) string {
	return path.Join(filepath.ToSlash(base), rel)
}
