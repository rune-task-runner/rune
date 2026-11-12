// Package release_test keeps the two files that describe Rune's container
// image in step: .goreleaser.yaml builds it for releases, .ko.yaml for local
// builds. ko's own file cannot import GoReleaser's settings, so the values are
// written twice and compared here.
package release_test

import (
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

type build struct {
	ID      string   `yaml:"id"`
	Main    string   `yaml:"main"`
	Env     []string `yaml:"env"`
	Flags   []string `yaml:"flags"`
	Ldflags []string `yaml:"ldflags"`
}

type koConfig struct {
	DefaultBaseImage string   `yaml:"defaultBaseImage"`
	DefaultPlatforms []string `yaml:"defaultPlatforms"`
	Builds           []build  `yaml:"builds"`
}

type koImage struct {
	ID           string            `yaml:"id"`
	Build        string            `yaml:"build"`
	BaseImage    string            `yaml:"base_image"`
	Repositories []string          `yaml:"repositories"`
	Platforms    []string          `yaml:"platforms"`
	Tags         []string          `yaml:"tags"`
	Bare         bool              `yaml:"bare"`
	SBOM         string            `yaml:"sbom"`
	Labels       map[string]string `yaml:"labels"`
}

type releaseConfig struct {
	Builds          []build   `yaml:"builds"`
	Kos             []koImage `yaml:"kos"`
	Dockers         any       `yaml:"dockers"`
	DockerManifests any       `yaml:"docker_manifests"`
	DockerSigns     []struct {
		Artifacts string   `yaml:"artifacts"`
		Args      []string `yaml:"args"`
	} `yaml:"docker_signs"`
}

func load(t *testing.T, path string, into any) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if err := yaml.Unmarshal(b, into); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
}

func configs(t *testing.T) (koConfig, releaseConfig) {
	t.Helper()
	var ko koConfig
	var rel releaseConfig
	load(t, "../../.ko.yaml", &ko)
	load(t, "../../.goreleaser.yaml", &rel)
	return ko, rel
}

// image returns the one ko image a release builds.
func image(t *testing.T, rel releaseConfig) koImage {
	t.Helper()
	if len(rel.Kos) != 1 {
		t.Fatalf(".goreleaser.yaml has %d kos entries, want 1", len(rel.Kos))
	}
	return rel.Kos[0]
}

func runeBuild(t *testing.T, file string, builds []build) build {
	t.Helper()
	for _, b := range builds {
		if b.ID == "rune" {
			return b
		}
	}
	t.Fatalf("%s has no build with id rune", file)
	return build{}
}

var pinnedBase = regexp.MustCompile(`^ghcr\.io/rune-task-runner/rune-base@sha256:[0-9a-f]{64}$`)

func TestBaseImagePinned(t *testing.T) {
	ko, rel := configs(t)
	img := image(t, rel)
	if !pinnedBase.MatchString(ko.DefaultBaseImage) {
		t.Errorf(".ko.yaml defaultBaseImage = %q, want rune-base pinned by digest", ko.DefaultBaseImage)
	}
	if ko.DefaultBaseImage != img.BaseImage {
		t.Errorf("base images differ:\n .ko.yaml         %q\n .goreleaser.yaml %q", ko.DefaultBaseImage, img.BaseImage)
	}
}

func TestPlatformsAgree(t *testing.T) {
	ko, rel := configs(t)
	want := []string{"linux/amd64", "linux/arm64"}
	if !slices.Equal(ko.DefaultPlatforms, want) {
		t.Errorf(".ko.yaml defaultPlatforms = %v, want %v", ko.DefaultPlatforms, want)
	}
	if got := image(t, rel).Platforms; !slices.Equal(got, want) {
		t.Errorf(".goreleaser.yaml kos platforms = %v, want %v", got, want)
	}
}

// withoutStamps drops every "-X name=value" pair: a release stamps version
// and commit, a local build does not.
func withoutStamps(ldflags []string) []string {
	words := strings.Fields(strings.Join(ldflags, " "))
	var out []string
	for i := 0; i < len(words); i++ {
		if words[i] == "-X" {
			i++
			continue
		}
		out = append(out, words[i])
	}
	return out
}

func TestWithoutStamps(t *testing.T) {
	got := withoutStamps([]string{"-s -w -X main.version={{.Version}} -X main.commit={{.ShortCommit}}", "-extldflags=-static"})
	if want := []string{"-s", "-w", "-extldflags=-static"}; !slices.Equal(got, want) {
		t.Errorf("withoutStamps = %v, want %v", got, want)
	}
}

func TestBuildSettingsAgree(t *testing.T) {
	ko, rel := configs(t)
	local := runeBuild(t, ".ko.yaml", ko.Builds)
	release := runeBuild(t, ".goreleaser.yaml", rel.Builds)
	if local.Main != release.Main {
		t.Errorf("main: .ko.yaml %q, .goreleaser.yaml %q", local.Main, release.Main)
	}
	if !slices.Equal(local.Flags, release.Flags) {
		t.Errorf("flags: .ko.yaml %v, .goreleaser.yaml %v", local.Flags, release.Flags)
	}
	if !slices.Equal(local.Env, release.Env) {
		t.Errorf("env: .ko.yaml %v, .goreleaser.yaml %v", local.Env, release.Env)
	}
	if l, r := withoutStamps(local.Ldflags), withoutStamps(release.Ldflags); !slices.Equal(l, r) {
		t.Errorf("ldflags without -X stamps: .ko.yaml %v, .goreleaser.yaml %v", l, r)
	}
}

func TestKoImageSettings(t *testing.T) {
	_, rel := configs(t)
	img := image(t, rel)
	if img.Build != "rune" {
		t.Errorf("build = %q, want rune (the image must reuse the archive build's settings)", img.Build)
	}
	if !img.Bare {
		t.Error("bare is false: the image would get a suffix after the repository path")
	}
	if img.SBOM != "spdx" {
		t.Errorf("sbom = %q, want spdx", img.SBOM)
	}
	if want := []string{"ghcr.io/rune-task-runner/rune"}; !slices.Equal(img.Repositories, want) {
		t.Errorf("repositories = %v, want %v", img.Repositories, want)
	}
	// The second tag is empty for a prerelease, and ko skips an empty tag,
	// so only a stable release moves latest.
	if want := []string{"{{ .Version }}", "{{ if not .Prerelease }}latest{{ end }}"}; !slices.Equal(img.Tags, want) {
		t.Errorf("tags = %q, want %q", img.Tags, want)
	}
	wantLabels := map[string]string{
		"org.opencontainers.image.title":       "{{ .ProjectName }}",
		"org.opencontainers.image.description": "A shared task runner for humans and AI agents",
		"org.opencontainers.image.source":      "https://github.com/rune-task-runner/rune",
		"org.opencontainers.image.licenses":    "MIT",
		"org.opencontainers.image.version":     "{{ .Version }}",
		"org.opencontainers.image.revision":    "{{ .FullCommit }}",
	}
	for k, want := range wantLabels {
		if got := img.Labels[k]; got != want {
			t.Errorf("label %s = %q, want %q", k, got, want)
		}
	}
	if len(img.Labels) != len(wantLabels) {
		t.Errorf("%d labels, want %d", len(img.Labels), len(wantLabels))
	}
}

func TestNoBuildxSections(t *testing.T) {
	_, rel := configs(t)
	if rel.Dockers != nil || rel.DockerManifests != nil {
		t.Error(".goreleaser.yaml still has dockers or docker_manifests: the image is built by ko")
	}
	if len(rel.DockerSigns) != 1 {
		t.Fatalf("%d docker_signs entries, want 1", len(rel.DockerSigns))
	}
	sign := rel.DockerSigns[0]
	if sign.Artifacts != "manifests" {
		t.Errorf("docker_signs artifacts = %q, want manifests (what the ko step publishes)", sign.Artifacts)
	}
	if !slices.Contains(sign.Args, "--recursive") {
		t.Errorf("docker_signs args %v lack --recursive: the per-architecture images would be unsigned", sign.Args)
	}
}
