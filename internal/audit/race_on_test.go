//go:build race

package audit

// raceEnabled says the race detector is on. It slows a test several times
// over, so the two tests that hold a time budget only report under it.
const raceEnabled = true
