package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/rune-task-runner/rune/internal/cli"
)

// newReceiptCmd builds `receipt`, the parent of `receipt key` and `receipt
// attach` (spec 060). The name hides a Runefile task named receipt, which is
// run with `rune -- receipt`.
func newReceiptCmd(opts *cli.Options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "receipt",
		Short: "Print the receipt signing key, and attach receipts to a commit",
		Long: `Commands for the signed receipts a Runefile with 'set receipts' writes.

  key      print the public key receipts are signed with
  attach   add the receipts of a revision to its note in refs/notes/rune-receipts

Check receipts with 'rune verify'. A Runefile task named 'receipt' is run
with 'rune -- receipt'.`,
		Example: `  rune receipt key > me.pub
  rune receipt attach HEAD && git push origin refs/notes/rune-receipts`,
		// Without this Cobra prints the help and exits 0 for any argument.
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return &cli.UsageError{Err: fmt.Errorf(`unknown command %q for "rune receipt"; to run a task named receipt, use: rune -- receipt`, args[0])}
			}
			cli.ReceiptTaskNote(*opts)
			return cmd.Help()
		},
	}
	cmd.AddCommand(newReceiptKeyCmd(opts), newReceiptAttachCmd(opts))
	return cmd
}

func newReceiptKeyCmd(opts *cli.Options) *cobra.Command {
	return &cobra.Command{
		Use:   "key",
		Short: "Print the public key receipts are signed with",
		Long: `Print, in PEM, the public key of the key receipts are signed with:
RUNE_RECEIPT_KEY when it is set, else this machine's local key, which is
created when there is none. Its key ID and source go to standard error.

Give the public key to whoever verifies your receipts: rune verify --key FILE.`,
		Example: `  rune receipt key > me.pub
  RUNE_RECEIPT_KEY="$(cat ci.pem)" rune receipt key > ci.pub`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return cli.ReceiptKey(*opts)
		},
	}
}

func newReceiptAttachCmd(opts *cli.Options) *cobra.Command {
	var flags cli.AttachFlags
	cmd := &cobra.Command{
		Use:   "attach [flags] [REVISION]",
		Short: "Add the receipts of a revision to its note in refs/notes/rune-receipts",
		Long: `Add to the note of REVISION's commit (default HEAD), under
refs/notes/rune-receipts, every receipt in .rune/receipts/ that ran on exactly
that revision's files and that a trusted key signed. The note keeps one
receipt per line, sorted, each once. The commit and other notes do not change.

Share the notes with: git push origin refs/notes/rune-receipts

Exit status: 0 when receipts are attached or already there, 1 when no receipt
ran on the revision's files or git could not write the note, 2 for a usage
error.`,
		Example: `  rune receipt attach
  rune receipt attach --key ci.pub v1.2.0`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := fileFlag(cmd, opts); err != nil {
				return err
			}
			return cli.ReceiptAttach(*opts, flags, args)
		},
	}
	f := cmd.Flags()
	f.StringVarP(&opts.File, "file", "f", "", "use this Runefile instead of the one found from the working directory")
	f.StringArrayVar(&flags.Keys, "key", nil, "also accept receipts signed by the public keys (PEM) in this file; repeatable")
	return cmd
}
