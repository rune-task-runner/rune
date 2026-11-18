// Package taskimport converts the task file of another runner (a justfile, a
// Makefile, a Taskfile or the scripts of a package.json) into Runefile text
// for the `rune import` command.
//
// It only reads the bytes it is given: it opens no file, starts no process
// and fetches nothing, so importing a project never runs that project's code.
// A construct outside the documented subset is reported, never guessed.
//
// This is not the `import` statement of a Runefile, which splices another
// Runefile into the current one.
package taskimport
