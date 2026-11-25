package main

import (
	"github.com/spf13/cobra"

	"github.com/rune-task-runner/rune/internal/cli"
)

// newVerifyCmd builds `verify` (spec 060). The name hides a Runefile task
// named verify, which is run with `rune -- verify`.
func newVerifyCmd(opts *cli.Options) *cobra.Command {
	var flags cli.VerifyFlags
	cmd := &cobra.Command{
		Use:   "verify [flags] REVISION TASK...",
		Short: "Check that each task has a trusted, passing receipt for a revision",
		Long: `Check, for each task you name, that a signed receipt shows it passed on
exactly the files of REVISION. Nothing runs and nothing is written.

The receipts are those in .rune/receipts/ and in the note of the revision's
commit under refs/notes/rune-receipts. A receipt counts when a trusted key
signed it, its task passed, and the snapshot it ran on, before and after the
body, is the revision's tree. A failed, interrupted, refused or cached run
never counts, nor one whose files changed during the run.

Trusted keys: the key this machine signs with (RUNE_RECEIPT_KEY, else the
local key, if it exists) and the public keys in each --key file.

The report says which task is verified and why another is not, and whether
the working tree still matches the revision. --json prints it as one JSON
document.

A Runefile task named 'verify' is run with 'rune -- verify'.

Exit status: 0 when every task is verified, 1 when one is not, 2 for a usage
error (no task, an unknown revision, no repository, an unreadable key file).`,
		Example: `  rune verify HEAD test lint
  rune verify --key ci.pub origin/main test
  rune verify --json HEAD test`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := fileFlag(cmd, opts); err != nil {
				return err
			}
			return cli.Verify(*opts, flags, args)
		},
	}
	f := cmd.Flags()
	f.StringVarP(&opts.File, "file", "f", "", "use this Runefile instead of the one found from the working directory")
	f.StringArrayVar(&flags.Keys, "key", nil, "trust the public keys (PEM) in this file; repeatable")
	f.BoolVar(&flags.JSON, "json", false, "print the report as one JSON document")
	return cmd
}
