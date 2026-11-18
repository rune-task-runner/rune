//go:build unix

// Command sandboxprobe is what a task of the sandbox tests runs to try
// something from inside its body: open a connection, write a file, list its
// open descriptors. Each subcommand prints "ok" or the error and exits 0 or
// 1, so a Runefile can chain attempts and a test can read what happened.
//
// It is a program of its own because the test container has no Python and
// the built-in shell has no network commands.
package main

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const dialTimeout = 2 * time.Second

func main() {
	if len(os.Args) < 2 {
		fail(errors.New("usage: probe COMMAND [ARGS]"))
	}
	if err := run(os.Args[1], os.Args[2:]); err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Println("error:", err)
	os.Exit(1)
}

// arg returns the i-th argument, or fails with the usage of the command.
func arg(args []string, i int) string {
	if i >= len(args) {
		fail(errors.New("missing argument"))
	}
	return args[i]
}

func run(command string, args []string) error {
	switch command {
	case "connect":
		conn, err := net.DialTimeout("tcp", arg(args, 0), dialTimeout)
		if err != nil {
			return err
		}
		_, _ = conn.Write([]byte("probe\n"))
		return finish(conn.Close())
	case "udp":
		return udp(arg(args, 0))
	case "resolve":
		addrs, err := net.LookupHost(arg(args, 0))
		if err != nil {
			return err
		}
		if len(addrs) == 0 {
			return errors.New("no address")
		}
		return finish(nil)
	case "unix":
		conn, err := net.DialTimeout("unix", arg(args, 0), dialTimeout)
		if err != nil {
			return err
		}
		return finish(conn.Close())
	case "abstract":
		conn, err := net.DialTimeout("unix", "@"+arg(args, 0), dialTimeout)
		if err != nil {
			return err
		}
		return finish(conn.Close())
	case "read":
		data, err := os.ReadFile(arg(args, 0))
		if err != nil {
			return err
		}
		fmt.Printf("ok %d bytes\n", len(data))
		return nil
	case "write":
		return finish(os.WriteFile(arg(args, 0), []byte("written by the probe\n"), 0o644))
	case "link":
		return finish(os.Link(arg(args, 0), arg(args, 1)))
	case "rename":
		return finish(os.Rename(arg(args, 0), arg(args, 1)))
	case "fds":
		return fds()
	case "ls":
		entries, err := os.ReadDir(arg(args, 0))
		if err != nil {
			return err
		}
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		fmt.Println("ok", strings.Join(names, " "))
		return nil
	case "tty":
		f, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
		if err != nil {
			return err
		}
		return finish(f.Close())
	case "status":
		return status()
	case "selfserve":
		return selfServe()
	case "daemon":
		return daemon(arg(args, 0))
	case "heartbeat":
		heartbeat(arg(args, 0))
		return nil
	case "spawn":
		return spawn(arg(args, 0))
	case "sleep":
		seconds, err := strconv.ParseFloat(arg(args, 0), 64)
		if err != nil {
			return err
		}
		time.Sleep(time.Duration(seconds * float64(time.Second)))
		return finish(nil)
	case "agent":
		return agent(arg(args, 0))
	}
	return fmt.Errorf("unknown command %q", command)
}

// finish prints "ok" for a nil error and passes any other on.
func finish(err error) error {
	if err == nil {
		fmt.Println("ok")
	}
	return err
}

// udp sends one datagram and waits for its echo.
func udp(addr string) error {
	conn, err := net.DialTimeout("udp", addr, dialTimeout)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.Write([]byte("probe")); err != nil {
		return err
	}
	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		return err
	}
	buf := make([]byte, 16)
	if _, err := conn.Read(buf); err != nil {
		return err
	}
	return finish(nil)
}

// fds prints the descriptors this process was started with: the open ones
// that are not close-on-exec, which is also what a program it starts would
// get.
func fds() error {
	dir, err := syscall.Open("/proc/self/fd", syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer func() { _ = syscall.Close(dir) }()
	var names []string
	buf := make([]byte, 8192)
	for {
		n, err := syscall.ReadDirent(dir, buf)
		if err != nil {
			return err
		}
		if n == 0 {
			break
		}
		_, _, names = syscall.ParseDirent(buf[:n], -1, names)
	}
	var open []int
	for _, name := range names {
		fd, err := strconv.Atoi(name)
		if err != nil || fd == dir {
			continue
		}
		// A descriptor that came through exec is not close-on-exec. One
		// the Go runtime opened for itself is, and is not counted.
		flags, _, errno := syscall.Syscall(syscall.SYS_FCNTL, uintptr(fd), syscall.F_GETFD, 0)
		if errno == 0 && flags&syscall.FD_CLOEXEC == 0 {
			open = append(open, fd)
		}
	}
	slices.Sort(open)
	parts := make([]string, len(open))
	for i, fd := range open {
		parts[i] = strconv.Itoa(fd)
	}
	fmt.Println("ok", strings.Join(parts, " "))
	return nil
}

// status prints what this process may do: its capabilities, whether it can
// gain privileges, and its user.
func status() error {
	data, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return err
	}
	var kept []string
	for line := range strings.Lines(string(data)) {
		for _, key := range []string{"CapEff:", "CapBnd:", "NoNewPrivs:"} {
			if value, ok := strings.CutPrefix(line, key); ok {
				kept = append(kept, key+strings.TrimSpace(value))
			}
		}
	}
	fmt.Println("ok", strings.Join(kept, " "), "uid:"+strconv.Itoa(os.Getuid()))
	return nil
}

// selfServe listens on the loopback of this process and connects to itself.
func selfServe() error {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	defer func() { _ = listener.Close() }()
	accepted := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			_ = conn.Close()
		}
		accepted <- err
	}()
	conn, err := net.DialTimeout("tcp", listener.Addr().String(), dialTimeout)
	if err != nil {
		return err
	}
	_ = conn.Close()
	return finish(<-accepted)
}

// daemon starts a process in a session of its own that keeps writing the
// time to file, and returns at once: a child that has left its parent's
// process group, as a helper that means to stay does.
func daemon(file string) error {
	cmd := exec.Command(os.Args[0], "heartbeat", file)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	return finish(cmd.Process.Release())
}

func heartbeat(file string) {
	for {
		_ = os.WriteFile(file, []byte(strconv.FormatInt(time.Now().UnixNano(), 10)), 0o644)
		time.Sleep(100 * time.Millisecond)
	}
}

// spawn starts n children that sleep for a long time, and returns.
func spawn(count string) error {
	n, err := strconv.Atoi(count)
	if err != nil {
		return err
	}
	for range n {
		cmd := exec.Command(os.Args[0], "sleep", "600")
		cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
		if err := cmd.Start(); err != nil {
			return err
		}
		_ = cmd.Process.Release()
	}
	return finish(nil)
}

// agent is a stand-in for an agent program: it tries to reach addr, says
// whether Rune gave it an endpoint, and ignores the prompt, its last
// argument.
func agent(addr string) error {
	reach := "connected"
	if conn, err := net.DialTimeout("tcp", addr, dialTimeout); err != nil {
		reach = "no connection"
	} else {
		_ = conn.Close()
	}
	endpoint := "no endpoint"
	if os.Getenv("RUNE_MCP_URL") != "" {
		endpoint = "an endpoint"
	}
	fmt.Printf("agent: %s, %s\n", reach, endpoint)
	return nil
}
