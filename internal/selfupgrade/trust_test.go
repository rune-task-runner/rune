package selfupgrade

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/theupdateframework/go-tuf/v2/metadata"
)

func TestTUFFetcher(t *testing.T) {
	srv := tlsServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/1.root.json":
			_, _ = w.Write([]byte("root metadata"))
		default:
			http.NotFound(w, r)
		}
	})
	f := &tufFetcher{ctx: context.Background(), dl: newDownloader(srv.Client())}

	got, err := f.DownloadFile(srv.URL+"/1.root.json", 100, 0)
	if err != nil || string(got) != "root metadata" {
		t.Fatalf("DownloadFile = %q, %v", got, err)
	}

	// The TUF client walks N+1.root.json until it gets this exact error.
	_, err = f.DownloadFile(srv.URL+"/2.root.json", 100, 0)
	var httpErr *metadata.ErrDownloadHTTP
	if !errors.As(err, &httpErr) || httpErr.StatusCode != 404 {
		t.Errorf("404: err = %#v, want *metadata.ErrDownloadHTTP with status 404", err)
	}

	if _, err := f.DownloadFile(srv.URL+"/1.root.json", 4, 0); err == nil {
		t.Error("DownloadFile returned a body longer than maxLength")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cancelled := &tufFetcher{ctx: ctx, dl: newDownloader(srv.Client())}
	if _, err := cancelled.DownloadFile(srv.URL+"/1.root.json", 100, 0); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled: err = %v, want context.Canceled", err)
	}
}

func TestPublicTrustFailsClosed(t *testing.T) {
	// A host that is not a TUF repository must yield an error, never a trust root.
	srv := tlsServer(t, func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })
	trust := publicTrust(srv.Client(), srv.URL)
	if tm, err := trust(context.Background()); err == nil {
		t.Errorf("publicTrust returned %v from a host with no metadata", tm)
	}
}

// The trust-root host is bounded like every other: no hang, no retry.
func TestTUFFetcherIsBounded(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	var requests atomic.Int64
	srv := tlsServer(t, func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path == "/stall" {
			w.Header().Set("Content-Length", "1000")
			_, _ = w.Write([]byte("x"))
			w.(http.Flusher).Flush()
		}
		select {
		case <-release:
		case <-r.Context().Done():
		}
	})
	f := &tufFetcher{ctx: context.Background(), dl: shortLimits(newDownloader(srv.Client()))}
	for _, path := range []string{"/silent", "/stall"} {
		start := time.Now()
		_, err := f.DownloadFile(srv.URL+path, 2000, 0)
		wantReason(t, path, err, ReasonTimeout)
		if d := time.Since(start); d > 5*time.Second {
			t.Errorf("%s: took %v", path, d)
		}
	}
	if requests.Load() != 2 {
		t.Errorf("%d requests for 2 downloads: the fetcher retried", requests.Load())
	}
}

func TestPublicTrustBuildsASource(t *testing.T) {
	// Only construction is checked: calling it would contact the public service.
	if PublicTrust(&http.Client{}) == nil {
		t.Error("PublicTrust returned no trust source")
	}
}
