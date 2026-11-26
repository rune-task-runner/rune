// Package receipt is Rune's evidence that a task ran (spec 060). It defines
// receipt format version 1, signs and opens DSSE envelopes with Ed25519
// keys, stores receipts in a project, takes git snapshots of the working
// tree, reads and writes the receipts notes of commits, and evaluates
// receipts against a revision.
//
// It knows nothing about tasks, schedulers or the command line: callers hand
// it values and get values back.
//
// A receipt attests to what Rune observed when it ran a task. It is evidence
// for a revision only when the files the task ran on are that revision's
// files, and only as far as the key that signed it is trusted.
package receipt
