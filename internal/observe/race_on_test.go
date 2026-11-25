//go:build race

package observe

// raceEnabled says the race detector is on. It slows the store's many small
// writes several times over.
const raceEnabled = true
