package ci

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// KeyInput is what a node's cache key depends on besides its slug.
type KeyInput struct {
	Fingerprint string // Rune's cache fingerprint of the node: inputs, body, variables, executor
	Node        string // the node's run-once key: task and arguments
	Version     string // the Rune release that runs the node
	GOOS        string
	GOARCH      string
	Image       string // the runner image, from ImageOS; "" when unknown
}

// Key is the exact cache key of a node's bundle: rune-SLUG-HEX, with HEX the
// SHA-256 of every field of in.
func Key(slug string, in KeyInput) string {
	h := sha256.New()
	for _, f := range [][2]string{
		{"fingerprint", in.Fingerprint},
		{"node", in.Node},
		{"rune", in.Version},
		{"goos", in.GOOS},
		{"goarch", in.GOARCH},
		{"image", in.Image},
	} {
		fmt.Fprintf(h, "%s\x00%s\n", f[0], f[1])
	}
	return "rune-" + cut(slug, maxJobID) + "-" + hex.EncodeToString(h.Sum(nil))
}
