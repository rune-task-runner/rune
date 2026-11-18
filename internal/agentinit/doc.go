// Package agentinit plans the project files that `rune agent init` writes
// for an agent client (spec 055): the registration of `rune mcp`, the
// settings that remove the client's command-running tools, and the rules
// that let Rune's tools run.
//
// It works from bytes to bytes. It is given the content of the client files
// that exist and returns the content they should have, the conflicts that
// stop a run, and the outcome the setup reaches. It reads no file, writes no
// file and starts no process; internal/cli does that.
//
// An existing file is edited in place: every change is an insertion or a
// replacement at a position a parser gave, so comments, key order and layout
// outside the entries Rune owns stay as they were.
package agentinit
