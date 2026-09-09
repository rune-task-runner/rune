// Command docsite generates the Astro + Starlight content tree in website/ from
// the canonical documentation in docs/. It is a development tool, not part of
// the released rune binary: .goreleaser.yml builds only ./cmd/rune.
//
//	go run ./cmd/docsite            # generate website/src/content/docs
//	go run ./cmd/docsite -check     # verify the tree is current, writing nothing
//
// Run it through the Runefile task instead: `rune docs-site-gen`.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/rune-task-runner/rune/internal/docsite"
)

func main() {
	opts := docsite.DefaultOptions()
	check := flag.Bool("check", false, "verify the generated tree matches docs/ without writing")
	flag.StringVar(&opts.DocsDir, "docs", opts.DocsDir, "directory holding the canonical Markdown")
	flag.StringVar(&opts.NavPath, "nav", opts.NavPath, "navigation manifest")
	flag.StringVar(&opts.OutDir, "out", opts.OutDir, "generated content tree")
	flag.StringVar(&opts.AssetsDir, "assets", opts.AssetsDir, "copied assets root")
	flag.Parse()

	if err := run(opts, *check); err != nil {
		fmt.Fprintln(os.Stderr, "docsite:", err)
		os.Exit(1)
	}
}

func run(opts docsite.Options, check bool) error {
	plan, err := docsite.Build(opts)
	if err != nil {
		return err
	}

	if check {
		diff, err := plan.Check(".")
		if err != nil {
			return err
		}
		if len(diff) > 0 {
			for _, d := range diff {
				fmt.Fprintln(os.Stderr, "  "+d)
			}
			return fmt.Errorf("%d generated file(s) out of date — run `rune docs-site-gen`", len(diff))
		}
		fmt.Printf("docsite: %d pages up to date\n", len(plan.Pages))
		return nil
	}

	if err := plan.Write("."); err != nil {
		return err
	}
	fmt.Printf("docsite: generated %d pages into %s\n", len(plan.Pages), opts.OutDir)
	return nil
}
