package mcpserver

import (
	"context"
	"crypto/subtle"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ServeStdio runs the MCP server over stdio (always available, auth inherited
// from the local process/session).
func (s *Server) ServeStdio(ctx context.Context) error {
	return s.mcp.Run(ctx, &mcp.StdioTransport{})
}

// HTTPConfig configures the opt-in Streamable HTTP transport.
type HTTPConfig struct {
	Addr  string // e.g. 127.0.0.1:7777
	Token string // required bearer token
}

// ServeHTTP starts the Streamable HTTP transport. It binds the configured
// address (localhost by default), requires a bearer token before any list/call
// (SC-010), and refuses a non-localhost bind unless the address is explicit.
func (s *Server) ServeHTTP(ctx context.Context, cfg HTTPConfig) error {
	if cfg.Token == "" {
		return fmt.Errorf("a bearer token is required for the HTTP transport (use --token-file)")
	}
	if cfg.Addr == "" {
		cfg.Addr = "127.0.0.1:0"
	}
	if !isLoopback(cfg.Addr) {
		fmt.Fprintln(os.Stderr, "warning: serving MCP on a non-loopback address without TLS; the bearer token is sent in cleartext")
	}

	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s.mcp }, nil)
	authed := s.tokenMiddleware(cfg.Token, handler)

	srv := &http.Server{Addr: cfg.Addr, Handler: authed}
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", cfg.Addr)
	if err != nil {
		return err
	}
	// The watcher closes the server on cancellation; `done` guarantees it also
	// exits if Serve returns first (e.g. a listener error) — no leaked goroutine.
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = srv.Close()
		case <-done:
		}
	}()
	return srv.Serve(ln)
}

// StartHTTP binds and serves the HTTP transport in a background goroutine,
// returning the bound address and a stop function. Used by the (agent) executor
// to expose project tasks to the agent it drives.
func (s *Server) StartHTTP(cfg HTTPConfig) (addr string, stop func(), err error) {
	if cfg.Token == "" {
		return "", nil, fmt.Errorf("a bearer token is required for the HTTP transport")
	}
	if cfg.Addr == "" {
		cfg.Addr = "127.0.0.1:0"
	}
	var lc net.ListenConfig
	ln, err := lc.Listen(context.Background(), "tcp", cfg.Addr)
	if err != nil {
		return "", nil, err
	}
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s.mcp }, nil)
	srv := &http.Server{Handler: s.tokenMiddleware(cfg.Token, handler)}
	go func() { _ = srv.Serve(ln) }()
	return ln.Addr().String(), func() { _ = srv.Close() }, nil
}

// tokenMiddleware rejects any request lacking a valid bearer token.
func (s *Server) tokenMiddleware(token string, next http.Handler) http.Handler {
	want := []byte("Bearer " + token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := r.Header.Get("Authorization")
		if subtle.ConstantTimeCompare([]byte(got), want) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// isLoopback reports whether addr binds a loopback host.
func isLoopback(addr string) bool {
	host := addr
	if h, _, err := net.SplitHostPort(addr); err == nil {
		host = h
	}
	if host == "" || host == "localhost" {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return strings.HasPrefix(host, "127.")
}
