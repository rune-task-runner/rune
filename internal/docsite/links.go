package docsite

import (
	"path"
	"regexp"
	"strings"
)

// Config carries the values every rewritten link depends on. One Config is
// threaded through a whole generation run so the base path and blob root are
// each defined exactly once.
type Config struct {
	// BasePath is the site's base path, e.g. "/rune". A custom domain later
	// makes this "/" and nothing else changes.
	BasePath string
	// BlobBase is the GitHub blob root for files the site does not host, e.g.
	// "https://github.com/rune-task-runner/rune/blob/main".
	BlobBase string
	// EditBase is the GitHub edit root for docs/, e.g.
	// "https://github.com/rune-task-runner/rune/edit/main/docs". Each generated
	// page carries its own editUrl built from this plus its true source path,
	// which is why output paths need not mirror docs/'s layout.
	EditBase string
	// Excluded reports whether a docs/-relative page is unpublished. Links to
	// such pages become blob URLs, since a site path would 404.
	Excluded func(relPath string) bool
}

var (
	// inlineLinkRe matches the target of a Markdown inline link or image. The
	// text group is permissive about nested brackets so labels like
	// "Editor setup (LSP)" survive.
	inlineLinkRe = regexp.MustCompile(`(!?\[[^\]]*\]\()([^)\s]+)(\s*(?:"[^"]*")?\))`)
	// refDefRe matches a reference-style link definition at the start of a line.
	refDefRe = regexp.MustCompile(`(?m)^(\s{0,3}\[[^\]]+\]:\s+)(\S+)(.*)$`)
)

// RewriteLinks rewrites every Markdown link and image target in src. fromPath is
// the docs/-relative path of the page being transformed, used to resolve
// relative targets. Content inside fenced code blocks is left alone, so examples
// that show Markdown links keep showing them.
func RewriteLinks(src, fromPath string, cfg Config) string {
	var out strings.Builder
	inFence := false
	lines := strings.Split(src, "\n")
	for i, line := range lines {
		if isFenceDelimiter(line) {
			inFence = !inFence
		} else if !inFence {
			line = inlineLinkRe.ReplaceAllStringFunc(line, func(m string) string {
				g := inlineLinkRe.FindStringSubmatch(m)
				return g[1] + rewriteTarget(g[2], fromPath, cfg) + g[3]
			})
			line = refDefRe.ReplaceAllStringFunc(line, func(m string) string {
				g := refDefRe.FindStringSubmatch(m)
				return g[1] + rewriteTarget(g[2], fromPath, cfg) + g[3]
			})
		}
		out.WriteString(line)
		if i < len(lines)-1 {
			out.WriteString("\n")
		}
	}
	return out.String()
}

// rewriteTarget maps one link target per the contract in
// specs/024-docs-site/spec.md §4.2.
func rewriteTarget(target, fromPath string, cfg Config) string {
	if target == "" || strings.HasPrefix(target, "#") || strings.HasPrefix(target, "/") ||
		strings.Contains(target, "://") || strings.HasPrefix(target, "mailto:") {
		return target
	}

	body, suffix := splitLinkSuffix(target)
	if body == "" {
		return target
	}

	// Resolve against the page's directory, in repository-root terms.
	repoRel := path.Join("docs", path.Dir(fromPath), body)
	if strings.HasPrefix(repoRel, "../") {
		return target // escapes the repository; leave it for a human to fix
	}

	docsRel, insideDocs := strings.CutPrefix(repoRel, "docs/")
	if !insideDocs {
		return cfg.BlobBase + "/" + repoRel + suffix
	}

	if !strings.EqualFold(path.Ext(docsRel), ".md") {
		if strings.HasPrefix(docsRel, "img/") {
			return cfg.BasePath + "/" + docsRel + suffix
		}
		return cfg.BlobBase + "/docs/" + docsRel + suffix
	}

	if cfg.Excluded != nil && cfg.Excluded(docsRel) {
		return cfg.BlobBase + "/docs/" + docsRel + suffix
	}

	slug := Slug(docsRel)
	if slug == "" {
		return cfg.BasePath + "/" + suffix
	}
	return cfg.BasePath + "/" + slug + "/" + suffix
}

// splitLinkSuffix separates a target from its #anchor or ?query suffix, which is
// carried through rewriting verbatim.
func splitLinkSuffix(target string) (body, suffix string) {
	if i := strings.IndexAny(target, "#?"); i >= 0 {
		return target[:i], target[i:]
	}
	return target, ""
}

// markdownLinkTargets returns every inline-link target in src. It exists for the
// generator's own assertions and for tests.
func markdownLinkTargets(src string) []string {
	var out []string
	for _, m := range inlineLinkRe.FindAllStringSubmatch(src, -1) {
		out = append(out, m[2])
	}
	for _, m := range refDefRe.FindAllStringSubmatch(src, -1) {
		out = append(out, m[2])
	}
	return out
}
