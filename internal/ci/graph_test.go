package ci

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/rune-task-runner/rune/internal/diag"
	"github.com/rune-task-runner/rune/internal/runtime/scheduler"
)

const diamond = `generate:
    @echo generate

build: generate
    @echo build

lint: build
    @echo lint

test: build
    @echo test
`

func TestBuildDiamond(t *testing.T) {
	g := mustBuild(t, diamond, "lint", "test")
	want := []string{"generate", "build<-generate", "lint<-build", "test<-build"}
	if got := shape(g); !slices.Equal(got, want) {
		t.Errorf("shape = %v, want %v", got, want)
	}
	if len(g.Roots) != 2 || g.Roots[0].Task.Name != "lint" || g.Roots[1].Task.Name != "test" {
		t.Errorf("roots = %v", g.Roots)
	}
	for i, n := range g.Nodes {
		if n.Index != i {
			t.Errorf("%s has index %d, want %d", n.Task.Name, n.Index, i)
		}
	}
}

func TestBuildParameterizedDependencies(t *testing.T) {
	src := `build mode:
    @echo {{mode}}

lint: (build "release")
    @echo lint

test: (build "debug")
    @echo test
`
	g := mustBuild(t, src, "lint", "test")
	want := []string{"build release", "lint<-build release", "build debug", "test<-build debug"}
	if got := shape(g); !slices.Equal(got, want) {
		t.Errorf("shape = %v, want %v", got, want)
	}
}

func TestBuildRunOnceAcrossRoots(t *testing.T) {
	g := mustBuild(t, diamond, "lint", "build", "lint")
	if got := shape(g); !slices.Equal(got, []string{"generate", "build<-generate", "lint<-build"}) {
		t.Errorf("shape = %v", got)
	}
	if len(g.Roots) != 2 {
		t.Errorf("roots = %d, want 2 (lint once, build)", len(g.Roots))
	}
}

func TestBuildSequentialDependenciesKeepTheirOrder(t *testing.T) {
	src := "a:\n    @echo a\n\nb:\n    @echo b\n\nc:\n    @echo c\n\ntop: a b c\n    @echo top\n"
	g := mustBuild(t, src, "top")
	want := []string{"a", "b<-a", "c<-b", "top<-c"}
	if got := shape(g); !slices.Equal(got, want) {
		t.Errorf("shape = %v, want %v", got, want)
	}
}

func TestBuildParallelDependenciesDoNotWait(t *testing.T) {
	src := "a:\n    @echo a\n\nb:\n    @echo b\n\nc:\n    @echo c\n\n[parallel]\ntop: a b c\n    @echo top\n"
	g := mustBuild(t, src, "top")
	want := []string{"a", "b", "c", "top<-a,b,c"}
	if got := shape(g); !slices.Equal(got, want) {
		t.Errorf("shape = %v, want %v", got, want)
	}
}

func TestBuildPostHooks(t *testing.T) {
	src := `notify:
    @echo notify

task: && notify
    @echo task

after: task
    @echo after
`
	g := mustBuild(t, src, "after")
	want := []string{"task", "notify<-task", "after<-notify"}
	if got := shape(g); !slices.Equal(got, want) {
		t.Errorf("shape = %v, want %v", got, want)
	}
	if done := g.Nodes[0].Done; len(done) != 2 {
		t.Errorf("task done = %d nodes, want task and notify", len(done))
	}
}

func TestBuildPostHookAlreadyAPrerequisite(t *testing.T) {
	src := "setup:\n    @echo s\n\ntask: setup && setup\n    @echo t\n"
	g := mustBuild(t, src, "task")
	if got := shape(g); !slices.Equal(got, []string{"setup", "task<-setup"}) {
		t.Errorf("shape = %v", got)
	}
}

func TestBuildSkipsUnavailableDependency(t *testing.T) {
	src := "nix:\n    @echo n\n\nwin:\n    @echo w\n\nsetup: nix win\n    @echo s\n"
	g, r, err := build(t, src, "linux", []string{"win"}, "setup")
	if err != nil {
		t.Fatal(err)
	}
	if got := shape(g); !slices.Equal(got, []string{"nix", "setup<-nix"}) {
		t.Errorf("shape = %v", got)
	}
	if slices.Contains(r.evaluated, "win") {
		t.Error("the arguments of an unavailable dependency were evaluated")
	}
}

func TestBuildRootsDoNotWaitForEachOther(t *testing.T) {
	src := "a:\n    @echo a\n\nb:\n    @echo b\n"
	g := mustBuild(t, src, "a", "b")
	if got := shape(g); !slices.Equal(got, []string{"a", "b"}) {
		t.Errorf("shape = %v", got)
	}
}

func TestBuildCycle(t *testing.T) {
	src := "a: b\n    @echo a\n\nb: && a\n    @echo b\n"
	_, _, err := build(t, src, "linux", nil, "a")
	var cyc *scheduler.CycleError
	if !errors.As(err, &cyc) {
		t.Fatalf("err = %v, want a cycle", err)
	}
	if got := strings.Join(cyc.Path, " "); got != "a b a" {
		t.Errorf("path = %q, want a b a", got)
	}
}

func TestBuildResolverError(t *testing.T) {
	src := "a: missing\n    @echo a\n"
	if _, _, err := build(t, src, "linux", nil, "a"); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Errorf("err = %v, want the resolver's error", err)
	}
}

func TestBuildRootBindError(t *testing.T) {
	src := "needs x:\n    @echo {{x}}\n"
	if _, _, err := build(t, src, "linux", nil, "needs"); err == nil {
		t.Error("a root without its required argument was accepted")
	}
}

func TestBuildUnavailableRootIsRefused(t *testing.T) {
	src := "[windows]\nwin:\n    @echo w\n"
	_, _, err := build(t, src, "linux", []string{"win"}, "win")
	var r *Refusal
	if !errors.As(err, &r) {
		t.Fatalf("err = %v, want a refusal", err)
	}
}

func TestBuildCachedAndOutputs(t *testing.T) {
	src := `[cache(inputs=["a"], outputs=["dist"])]
out:
    @echo o

[cache(inputs=["a"])]
noout:
    @echo n
`
	g := mustBuild(t, src, "out", "noout")
	if n := g.Nodes[0]; !n.Cached || !n.Outputs {
		t.Errorf("out: cached %v outputs %v", n.Cached, n.Outputs)
	}
	if n := g.Nodes[1]; !n.Cached || n.Outputs {
		t.Errorf("noout: cached %v outputs %v", n.Cached, n.Outputs)
	}
}

func TestWords(t *testing.T) {
	src := "t a b=\"x\" *rest:\n    @echo\n\ntop: (t \"1\")\n    @echo\n\ntop2: (t \"1\" \"2\" \"3\" \"4\")\n    @echo\n"
	g := mustBuild(t, src, "top", "top2")
	if got := g.Nodes[0].Argv; !slices.Equal(got, []string{"1", "x"}) {
		t.Errorf("argv = %q", got)
	}
	if got := g.Nodes[2].Argv; !slices.Equal(got, []string{"1", "2", "3", "4"}) {
		t.Errorf("argv = %q", got)
	}
}

func TestReduceNeedsAndAncestors(t *testing.T) {
	src := "a:\n    @echo\n\nb: a\n    @echo\n\nc: a b\n    @echo\n"
	g := mustBuild(t, src, "c")
	if got := shape(g); !slices.Equal(got, []string{"a", "b<-a", "c<-b"}) {
		t.Errorf("shape = %v (a is implied by b)", got)
	}
	anc := g.Nodes[2].Ancestors()
	if len(anc) != 2 || anc[0].Task.Name != "a" || anc[1].Task.Name != "b" {
		t.Errorf("ancestors = %v", anc)
	}
}

func TestRefusalError(t *testing.T) {
	one := &Refusal{Diags: diag.List{{Message: "x"}}}
	if one.Error() != "x" {
		t.Errorf("one = %q", one.Error())
	}
	two := &Refusal{Diags: diag.List{{Message: "x"}, {Message: "y"}}}
	if !strings.Contains(two.Error(), "cannot run") {
		t.Errorf("two = %q", two.Error())
	}
}
