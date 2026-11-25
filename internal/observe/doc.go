// Package observe records the shell commands an agent client runs, for
// `rune suggest` (spec 058). A hook handler of the client passes each event to
// `rune agent record`, which decodes it, removes secrets, and appends one
// record to a private store outside the project.
//
// A recorded command is read as text and parsed as shell syntax, never run,
// expanded or evaluated. Its output is never kept.
package observe
