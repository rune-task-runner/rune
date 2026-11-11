package selfupgrade

import (
	"fmt"
	"strings"

	"github.com/rune-task-runner/rune/internal/semver"
)

// Origin is how the running binary was installed. Only a standalone install
// is upgraded in place; every other origin has its own upgrade path, and
// overwriting it would leave that tool's records wrong.
type Origin string

const (
	OriginDevelopment Origin = "development"
	OriginGoInstall   Origin = "go-install"
	OriginContainer   Origin = "container"
	OriginHomebrew    Origin = "homebrew"
	OriginScoop       Origin = "scoop"
	OriginStandalone  Origin = "standalone"
)

// detectOrigin classifies the install; the first match wins. resolvedPath is
// the executable after symbolic links are followed, so a link into a package
// manager's directory is recognised as that package manager's install.
func detectOrigin(version string, stamped bool, resolvedPath, goos string, getenv func(string) string) Origin {
	if _, err := semver.Parse(version); err != nil {
		return OriginDevelopment
	}
	// A release build stamps its version; `go install` reports one it read
	// from module build info.
	if !stamped {
		return OriginGoInstall
	}
	// The images declare themselves: no path signal holds across runtimes.
	if getenv("RUNE_CONTAINER") == "1" {
		return OriginContainer
	}
	// Each package manager is looked for only on the systems it runs on, so
	// its directory names mean nothing elsewhere.
	segments := pathSegments(resolvedPath)
	if goos == "windows" {
		if underScoop(segments, getenv) {
			return OriginScoop
		}
		return OriginStandalone
	}
	for _, s := range segments {
		if s == "Cellar" || s == "Caskroom" {
			return OriginHomebrew
		}
	}
	return OriginStandalone
}

// pathSegments splits on both separators, so a Windows path is understood on
// any host.
func pathSegments(p string) []string {
	return strings.FieldsFunc(p, func(r rune) bool { return r == '/' || r == '\\' })
}

// underScoop reports whether the path lies in a Scoop apps directory: the
// default "scoop" root or one named by SCOOP or SCOOP_GLOBAL.
func underScoop(segments []string, getenv func(string) string) bool {
	for i := 0; i+1 < len(segments); i++ {
		if strings.EqualFold(segments[i], "scoop") && strings.EqualFold(segments[i+1], "apps") {
			return true
		}
	}
	for _, key := range []string{"SCOOP", "SCOOP_GLOBAL"} {
		root := pathSegments(getenv(key))
		if len(root) == 0 || len(segments) <= len(root) {
			continue
		}
		match := true
		for i, s := range root {
			if !strings.EqualFold(segments[i], s) {
				match = false
				break
			}
		}
		if match && strings.EqualFold(segments[len(root)], "apps") {
			return true
		}
	}
	return false
}

// Refused is returned when the install is not Rune's to upgrade. The message
// names the command that upgrades it the way it was installed.
type Refused struct {
	Origin  Origin
	Path    string
	Version string
}

func (r *Refused) Error() string {
	switch r.Origin {
	case OriginHomebrew:
		return fmt.Sprintf("this rune was installed with Homebrew (%s); run: brew upgrade rune", r.Path)
	case OriginScoop:
		return fmt.Sprintf("this rune was installed with Scoop (%s); run: scoop update rune", r.Path)
	case OriginGoInstall:
		return "this rune was built with go install; run: go install github.com/rune-task-runner/rune/cmd/rune@latest"
	case OriginContainer:
		return "this rune runs from the container image; pull a newer one: docker pull ghcr.io/rune-task-runner/rune:latest"
	case OriginDevelopment:
		return fmt.Sprintf("this is a development build (version %q) and is not upgraded; install a release: %s", r.Version, releasesURL)
	}
	return fmt.Sprintf("this rune install (%s) is not upgraded in place", r.Origin)
}
