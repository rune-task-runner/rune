package integration

import (
	"net"
	"sync/atomic"
	"testing"
)

// Only --self-upgrade may touch the network. Every other invocation runs with
// all proxy variables pointing at a listener that counts connections; any
// update check added later would show up here as a connection.
func TestNoNetworkOutsideSelfUpgrade(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	var connections atomic.Int64
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			connections.Add(1)
			_ = conn.Close()
		}
	}()
	proxy := "http://" + ln.Addr().String()
	env := []string{
		"HTTPS_PROXY=" + proxy, "HTTP_PROXY=" + proxy, "ALL_PROXY=" + proxy,
		"https_proxy=" + proxy, "http_proxy=" + proxy, "all_proxy=" + proxy,
		"NO_PROXY=", "no_proxy=",
		"RUNE_TEST_VERSION=0.0.1",
	}

	in := newUpgradeInstall(t, nil, "bin")
	dir := writeRunefile(t, "build:\n    @echo BUILT\n")
	for _, args := range [][]string{
		{"--list"},
		{"--version"},
		{"--help"},
		{"version"},
		{"version", "--check"},
		{"--dry-run", "build"},
		{"--summary", "build"},
		{"build"},
		{},
	} {
		r := in.exec(t, dir, env, args...)
		if r.code != 0 {
			t.Errorf("%v: exit %d; stderr:\n%s", args, r.code, r.stderr)
		}
		if n := connections.Load(); n != 0 {
			t.Fatalf("%v opened %d network connection(s)", args, n)
		}
	}

	// Control: the same environment does catch a command that uses the network.
	r := in.exec(t, dir, env, "--self-upgrade")
	if r.code != 1 {
		t.Errorf("--self-upgrade through a dead proxy: exit %d, want 1; stderr:\n%s", r.code, r.stderr)
	}
	if connections.Load() == 0 {
		t.Error("the probe saw no connection from --self-upgrade, so it proves nothing about the other commands")
	}
	in.unchanged(t)
}
