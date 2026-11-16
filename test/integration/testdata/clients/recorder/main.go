// Command recorder runs an MCP stdio server and records the session a client
// has with it. A client is configured to start the recorder in place of the
// server:
//
//	recorder -out session.jsonl rune mcp
//
// The recorder passes stdin, stdout and stderr through unchanged and appends
// one JSON object per line to the -out file: every JSON-RPC message with its
// sender, every stderr line of the server, the moment the client closed stdin
// or sent a signal, and the server's exit code or the signal that killed it.
// "ms" is the time since the recorder started, so a recording shows how long a
// client waited before it cancelled a call.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

// entry is one line of a recording. Exactly one of Msg, Raw, Stderr and Event
// is set.
type entry struct {
	MS     int64           `json:"ms"`
	From   string          `json:"from,omitempty"`
	Msg    json.RawMessage `json:"msg,omitempty"`
	Raw    string          `json:"raw,omitempty"`
	Stderr string          `json:"stderr,omitempty"`
	Event  string          `json:"event,omitempty"`
}

// log writes entries to the recording, one per line, in the order they happen.
type log struct {
	mu    sync.Mutex
	w     io.Writer
	start time.Time
	ended bool
}

func (l *log) add(e entry) { l.write(e, false) }

// end writes the last entry of the recording. The stdin relay can still be
// running when the server exits, and what it adds after that is dropped, so
// the server's end is always the last line.
func (l *log) end(e entry) { l.write(e, true) }

func (l *log) write(e entry, last bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.ended {
		return
	}
	l.ended = last
	e.MS = time.Since(l.start).Milliseconds()
	// json.Marshal would rewrite a message: it drops the spaces and escapes
	// <, > and &. The message is added as the last field, byte for byte.
	msg := e.Msg
	e.Msg = nil
	b, err := json.Marshal(e)
	if err != nil {
		return
	}
	if len(msg) > 0 {
		b = append(b[:len(b)-1], `,"msg":`...)
		b = append(append(b, msg...), '}')
	}
	_, _ = l.w.Write(append(b, '\n'))
}

// message records one line a peer sent: as msg when it is a JSON object, as
// raw text when it is not. A number or a string is valid JSON too, but no
// reader of a recording takes it for a message.
func (l *log) message(from string, line []byte) {
	if trimmed := bytes.TrimSpace(line); len(trimmed) > 0 && trimmed[0] == '{' && json.Valid(line) {
		l.add(entry{From: from, Msg: json.RawMessage(line)})
		return
	}
	l.add(entry{From: from, Raw: string(line)})
}

// relay copies src to dst line by line and hands each line, without its line
// break, to record. A last line without a line break is copied too. A line is
// recorded before it is copied: the peer cannot answer a line it has not been
// given, so a recording never shows a reply above the message it answers.
func relay(dst io.Writer, src io.Reader, record func(line []byte)) error {
	r := bufio.NewReader(src)
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 {
			if trimmed := trimEOL(line); len(trimmed) > 0 {
				record(trimmed)
			}
			if _, werr := dst.Write(line); werr != nil {
				return werr
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

func trimEOL(line []byte) []byte {
	for len(line) > 0 && (line[len(line)-1] == '\n' || line[len(line)-1] == '\r') {
		line = line[:len(line)-1]
	}
	return line
}

func main() {
	out := flag.String("out", "", "file the recording is appended to")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: recorder -out FILE COMMAND [ARG...]")
	}
	flag.Parse()
	if *out == "" || flag.NArg() == 0 {
		flag.Usage()
		os.Exit(2)
	}
	os.Exit(record(*out, flag.Args()))
}

// record runs the command and returns the exit code the recorder ends with:
// the server's own, or 1 when the server could not be run or was killed by a
// signal.
func record(out string, argv []string) int {
	f, err := os.OpenFile(out, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		fmt.Fprintln(os.Stderr, "recorder:", err)
		return 1
	}
	defer func() { _ = f.Close() }()
	l := &log{w: f, start: time.Now()}

	// The session ends when the client ends it, so the command has no deadline.
	cmd := exec.CommandContext(context.Background(), argv[0], argv[1:]...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		fmt.Fprintln(os.Stderr, "recorder:", err)
		return 1
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		fmt.Fprintln(os.Stderr, "recorder:", err)
		return 1
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		fmt.Fprintln(os.Stderr, "recorder:", err)
		return 1
	}
	if err := cmd.Start(); err != nil {
		fmt.Fprintln(os.Stderr, "recorder:", err)
		return 1
	}

	// A client that stops a server with a signal sends it to the recorder,
	// which records it and passes it on.
	signals := make(chan os.Signal, 4)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	go func() {
		for s := range signals {
			l.add(entry{Event: "client sent signal: " + s.String()})
			_ = cmd.Process.Signal(s)
		}
	}()

	go func() {
		// A relay that fails stopped because the server no longer reads, not
		// because the client closed stdin.
		if err := relay(stdin, os.Stdin, func(line []byte) { l.message("client", line) }); err == nil {
			l.add(entry{Event: "client closed stdin"})
		}
		_ = stdin.Close()
	}()

	var outputs sync.WaitGroup
	outputs.Add(2)
	go func() {
		defer outputs.Done()
		_ = relay(os.Stdout, stdout, func(line []byte) { l.message("server", line) })
	}()
	go func() {
		defer outputs.Done()
		_ = relay(os.Stderr, stderr, func(line []byte) { l.add(entry{From: "server", Stderr: string(line)}) })
	}()
	outputs.Wait()

	code := 0
	if err := cmd.Wait(); err != nil {
		code = 1
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			code = exit.ExitCode()
		}
		if code < 0 {
			// Killed by a signal, so there is no exit code to record or return.
			l.end(entry{Event: "server ended without an exit code: " + exit.String()})
			return 1
		}
	}
	l.end(entry{Event: fmt.Sprintf("server exited with code %d", code)})
	return code
}
