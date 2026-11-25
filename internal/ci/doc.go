// Package ci turns the tasks a Runefile's CI roots reach into a GitHub
// Actions workflow (spec 059). It builds the graph of nodes, one per task and
// bound arguments, in the order the scheduler would run them, refuses what a
// job cannot run as a local run would, and renders the workflow text. It also
// holds what `rune ci run` needs inside a job: the bundles that carry
// declared outputs between jobs and the cache key of a node.
//
// Nothing here runs a task body. Building the graph evaluates dependency
// arguments and parameter defaults, and only after checking that they read
// nothing from the machine.
package ci
