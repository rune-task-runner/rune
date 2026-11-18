//go:build !unix

package cli

import (
	"errors"
	"os"
)

// workerPipes has nothing to take: no backend exists on this system, so no
// worker is ever started.
func workerPipes() (job, result *os.File, err error) {
	return nil, nil, errors.New("no worker pipes")
}

// holdTmp and tmpHeld have nothing to mark on this system, where no body has
// a temporary directory.
func holdTmp(string) (release func(), err error) { return func() {}, nil }

func tmpHeld(string) bool { return false }

// closeInheritedOnExec has nothing to do where no sandbox starts a body.
func closeInheritedOnExec() {}
