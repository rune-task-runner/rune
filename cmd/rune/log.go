package main

import (
	"github.com/spf13/cobra"

	"github.com/rune-task-runner/rune/internal/cli"
)

// newLogCmd builds the `log` command: it lists what agents asked Rune's MCP
// servers to do in this project, from the audit log (spec 052). It runs no
// task and writes nothing.
func newLogCmd(opts *cli.Options) *cobra.Command {
	var flags cli.LogFlags

	cmd := &cobra.Command{
		Use:   "log",
		Short: "Show what agents did through the MCP server",
		Long: `Show the audit log of this project: every tool call an agent made through
'rune mcp', 'rune serve' or the endpoint of an (agent) task, with its time,
outcome, task, arguments, exit code, duration and client. Nothing is executed.

With no flags the command lists today's actions. "Today" and every day you
name are read in the display time zone: the zone of this machine, or the one
--tz names. Stored times are UTC.

An action is a call that completed, failed, was refused, had invalid
arguments, named an unknown tool, was cancelled or timed out. A call with no
recorded completion is listed as incomplete: it is still running, or the
server stopped before it ended. A refused call has no exit code.

The log lives outside the project, in the state directory of your user:
$XDG_STATE_HOME/rune/audit, else %LocalAppData%\rune\audit on Windows, else
~/.local/state/rune/audit. Set RUNE_AUDIT_DIR to keep it elsewhere, and
RUNE_AUDIT_RETENTION_DAYS to change how long a day is kept (default 90, 0
keeps everything). Both are read from the environment of the server.

A Runefile task named 'log' is run with 'rune -- log'.`,
		Example: `  rune log                                   # today's actions
  rune log --date 2026-11-17                 # one day
  rune log --since 2h                        # the last two hours
  rune log --since 2026-11-01 --until 2026-11-07
  rune log --tz UTC                          # read days in UTC
  rune log --task deploy --outcome failed,refused
  rune log --client claude-code --json       # one JSON object per action
  rune log --all                             # also requests that are not tool calls
  rune log --dir                             # where the log files are`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return cli.Log(*opts, flags)
		},
	}

	f := cmd.Flags()
	// Declared here, not inherited from the root, as for serve: the command
	// takes none of the run flags.
	f.StringVarP(&opts.File, "file", "f", "", "read the log of this Runefile instead of the one found from the working directory")
	f.StringVar(&flags.Date, "date", "", "list one day (YYYY-MM-DD) in the display time zone")
	f.StringVar(&flags.Since, "since", "", "list actions that started at or after a day, an RFC 3339 time, or a duration ago (2h, 7d)")
	f.StringVar(&flags.Until, "until", "", "list actions that started before a time; a day includes that day")
	f.StringVar(&flags.TZ, "tz", "", "display time zone: an IANA name, UTC, or local (default local)")
	f.StringArrayVar(&flags.Tasks, "task", nil, "only these tasks or tools (comma-separated, repeatable)")
	f.StringArrayVar(&flags.Outcomes, "outcome", nil, "only these outcomes, such as failed or refused (comma-separated, repeatable)")
	f.StringArrayVar(&flags.Clients, "client", nil, "only these client names, ignoring case (comma-separated, repeatable)")
	f.StringArrayVar(&flags.Sessions, "session", nil, "only these session ids or id prefixes (comma-separated, repeatable)")
	f.BoolVar(&flags.All, "all", false, "also list requests that are not tool calls")
	f.BoolVar(&flags.JSON, "json", false, "print one JSON object per action")
	f.BoolVar(&flags.Dir, "dir", false, "print the directory that holds this project's log files")

	return cmd
}
