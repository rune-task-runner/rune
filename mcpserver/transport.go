package mcpserver

import (
	"context"
	"crypto/subtle"
	"fmt"
	"net"
	"net/http"

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
	// Warn, when set, is called once with a one-line message when the
	// transport has bound an address that is not loopback: without TLS the
	// bearer token then crosses the network in cleartext. It is called before
	// any request is served, on the goroutine that started the transport.
	Warn func(message string)
}

// cleartextWarning is what HTTPConfig.Warn receives.
const cleartextWarning = "serving MCP on a non-loopback address without TLS; the bearer token is sent in cleartext"

// warnCleartext reports a bound address that other machines can reach.
func (cfg HTTPConfig) warnCleartext(bound net.Addr) {
	if cfg.Warn != nil && !isLoopback(bound) {
		cfg.Warn(cleartextWarning)
	}
}

// ServeHTTP starts the Streamable HTTP transport. It binds the configured
// address (localhost by default) and requires a bearer token before any
// list/call (SC-010). An address that is not loopback is bound as asked, not
// refused: the transport has no TLS, so the token then crosses the network in
// cleartext, and cfg.Warn is told.
func (s *Server) ServeHTTP(ctx context.Context, cfg HTTPConfig) error {
	if cfg.Token == "" {
		return fmt.Errorf("a bearer token is required for the HTTP transport (use --token-file)")
	}
	if cfg.Addr == "" {
		cfg.Addr = "127.0.0.1:0"
	}

	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s.mcp }, nil)
	authed := s.tokenMiddleware(cfg.Token, handler)

	srv := &http.Server{Addr: cfg.Addr, Handler: authed}
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", cfg.Addr)
	if err != nil {
		return err
	}
	cfg.warnCleartext(ln.Addr())
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
	cfg.warnCleartext(ln.Addr())
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
			s.auditAuthFailure(r)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// isLoopback reports whether a listener bound to a can be reached only from
// this machine. The bound address is judged, not the text it came from: an
// empty host binds every interface, and a host name is whatever it resolved
// to. Anything that is not a TCP address counts as reachable.
func isLoopback(a net.Addr) bool {
	tcp, ok := a.(*net.TCPAddr)
	return ok && tcp.IP.IsLoopback()
}

// auditAuthFailure reports a request refused for its bearer token. Only the
// address it came from is taken from the request: what an unauthenticated
// client says about itself is not evidence.
func (s *Server) auditAuthFailure(r *http.Request) {
	if s.opts.Audit == nil {
		return
	}
	now := s.now()
	if ok, suppressed := s.authFailures.allow(now); ok {
		s.opts.Audit.AuthFailed(r.Context(), AuditAuthFailure{Time: now, Remote: r.RemoteAddr, Suppressed: suppressed})
	}
}
