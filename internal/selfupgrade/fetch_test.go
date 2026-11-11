package selfupgrade

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rune-task-runner/rune/internal/selfupgrade/upgradetest"
)

func tlsServer(t *testing.T, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewTLSServer(h)
	t.Cleanup(srv.Close)
	return srv
}

func tempFile(t *testing.T) *os.File {
	t.Helper()
	f, err := os.Create(filepath.Join(t.TempDir(), "out"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

func TestFetch(t *testing.T) {
	payload := bytes.Repeat([]byte("rune"), 5000)
	srv := tlsServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/file.bin":
			_, _ = w.Write(payload)
		case "/gone":
			http.NotFound(w, r)
		default:
			http.Error(w, "boom", http.StatusInternalServerError)
		}
	})
	dl := newDownloader(srv.Client())
	ctx := context.Background()

	got, err := dl.fetchBytes(ctx, srv.URL+"/file.bin", 1<<20)
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("fetchBytes: %d bytes, err %v", len(got), err)
	}

	f := tempFile(t)
	sum, err := dl.fetchFile(ctx, srv.URL+"/file.bin", f, 1<<20)
	if err != nil {
		t.Fatalf("fetchFile: %v", err)
	}
	if sum != sha256.Sum256(payload) {
		t.Error("fetchFile returned the wrong digest")
	}
	onDisk, err := os.ReadFile(f.Name())
	if err != nil || !bytes.Equal(onDisk, payload) {
		t.Errorf("fetchFile wrote %d bytes, err %v", len(onDisk), err)
	}

	for path, status := range map[string]int{"/gone": 404, "/broken": 500} {
		_, err := dl.fetchBytes(ctx, srv.URL+path, 1<<20)
		var e *Error
		if !errors.As(err, &e) || e.Reason != ReasonDownloadFailed || e.Status != status || e.Subject != path[1:] {
			t.Errorf("%s: err = %#v, want download-failed with status %d and subject %q", path, err, status, path[1:])
		}
		if _, err := dl.fetchFile(ctx, srv.URL+path, tempFile(t), 1<<20); !errors.As(err, &e) || e.Status != status {
			t.Errorf("fetchFile %s: err = %v", path, err)
		}
	}

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := dl.fetchBytes(cancelled, srv.URL+"/file.bin", 1<<20); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled fetchBytes: err = %v, want context.Canceled", err)
	}
	if _, err := dl.fetchFile(cancelled, srv.URL+"/file.bin", tempFile(t), 1<<20); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled fetchFile: err = %v, want context.Canceled", err)
	}
}

func TestFetchBadURL(t *testing.T) {
	dl := newDownloader(&http.Client{})
	if _, err := dl.fetchBytes(context.Background(), "://nope", 10); err == nil {
		t.Error("fetchBytes accepted an unparseable URL")
	}
}

func shortLimits(dl *downloader) *downloader {
	dl.lim.header = 150 * time.Millisecond
	dl.lim.idle = 150 * time.Millisecond
	return dl
}

func TestLimitsMatchThePlan(t *testing.T) {
	want := limits{dial: 10 * time.Second, tlsHandshake: 10 * time.Second, header: 15 * time.Second, idle: 30 * time.Second, redirects: 5}
	if defaultLimits != want {
		t.Errorf("defaultLimits = %+v, want %+v", defaultLimits, want)
	}
	if runLimit != 10*time.Minute || metadataLimit != 1<<20 || archiveLimit != 100<<20 || binaryLimit != 200<<20 {
		t.Errorf("run %v, metadata %d, archive %d, binary %d", runLimit, metadataLimit, archiveLimit, binaryLimit)
	}
}

func TestFetchTimeouts(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	var requests atomic.Int64
	srv := tlsServer(t, func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		switch r.URL.Path {
		case "/silent":
			select {
			case <-release:
			case <-r.Context().Done():
			}
		case "/stall":
			w.Header().Set("Content-Length", "1000")
			_, _ = w.Write([]byte("first chunk"))
			w.(http.Flusher).Flush()
			select {
			case <-release:
			case <-r.Context().Done():
			}
		}
	})
	dl := shortLimits(newDownloader(srv.Client()))
	host := strings.TrimPrefix(srv.URL, "https://")

	for _, path := range []string{"/silent", "/stall"} {
		start := time.Now()
		_, err := dl.fetchBytes(context.Background(), srv.URL+path, 1<<20)
		var e *Error
		if !errors.As(err, &e) || e.Reason != ReasonTimeout || e.Subject != host || e.Limit != "150ms" {
			t.Errorf("%s: err = %#v, want a timeout naming %s and 150ms", path, err, host)
		}
		if d := time.Since(start); d > 5*time.Second {
			t.Errorf("%s: took %v, far beyond the shortened limit", path, d)
		}
	}
	if requests.Load() != 2 {
		t.Errorf("%d requests for 2 fetches: something retried", requests.Load())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := newDownloader(srv.Client()).fetchBytes(ctx, srv.URL+"/silent", 1<<20)
	var e *Error
	if !errors.As(err, &e) || e.Reason != ReasonTimeout || !strings.Contains(e.Limit, "whole upgrade") {
		t.Errorf("deadline: err = %#v, want a timeout naming the overall limit", err)
	}
}

func TestFetchOffline(t *testing.T) {
	dl := newDownloader(&http.Client{})
	_, err := dl.fetchBytes(context.Background(), "https://127.0.0.1:1/x", 10)
	var e *Error
	if !errors.As(err, &e) || e.Reason != ReasonOffline || e.Subject != "127.0.0.1:1" {
		t.Errorf("closed port: err = %#v, want offline naming 127.0.0.1:1", err)
	}

	noDNS := &http.Client{Transport: &http.Transport{
		DialContext: func(context.Context, string, string) (net.Conn, error) {
			return nil, &net.DNSError{Err: "no such host", Name: "releases.invalid", IsNotFound: true}
		},
	}}
	_, err = newDownloader(noDNS).fetchBytes(context.Background(), "https://releases.invalid/x", 10)
	if !errors.As(err, &e) || e.Reason != ReasonOffline || e.Subject != "releases.invalid" {
		t.Errorf("no DNS: err = %#v, want offline naming releases.invalid", err)
	}
}

func TestFetchUnreachableProxy(t *testing.T) {
	proxy, _ := url.Parse("http://127.0.0.1:1")
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxy)}}
	_, err := newDownloader(client).fetchBytes(context.Background(), "https://releases.invalid/x", 10)
	var e *Error
	if !errors.As(err, &e) || e.Reason != ReasonOffline || e.Subject != "the proxy 127.0.0.1:1" {
		t.Fatalf("err = %#v, want offline naming the proxy", err)
	}
	if msg := e.Error(); !strings.HasPrefix(msg, "cannot reach the proxy 127.0.0.1:1: ") || strings.Contains(msg, "proxyconnect") {
		t.Errorf("message = %q", msg)
	}
}

// A disk that fails while the archive streams in is a local fault.
func TestFetchFileWriteFailure(t *testing.T) {
	srv := tlsServer(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("payload")) })
	f := tempFile(t)
	_ = f.Close()
	_, err := newDownloader(srv.Client()).fetchFile(context.Background(), srv.URL+"/a.tar.gz", f, 1<<20)
	var e *Error
	if !errors.As(err, &e) || e.Reason != ReasonNotWritable || e.Subject != filepath.Dir(f.Name()) || e.Err == nil {
		t.Errorf("err = %#v, want not-writable naming the directory, with the cause", err)
	}
}

func TestFetchRateLimited(t *testing.T) {
	reset := time.Now().Add(30 * time.Minute).Truncate(time.Second)
	for _, status := range []int{403, 429} {
		srv := tlsServer(t, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("X-Ratelimit-Remaining", "0")
			w.Header().Set("X-Ratelimit-Reset", strconv.FormatInt(reset.Unix(), 10))
			w.WriteHeader(status)
		})
		_, err := newDownloader(srv.Client()).fetchBytes(context.Background(), srv.URL+"/x", 10)
		var e *Error
		if !errors.As(err, &e) || e.Reason != ReasonRateLimited {
			t.Fatalf("%d: err = %#v, want rate-limited", status, err)
		}
		if want := reset.Local().Format("15:04:05 MST"); e.Until != want || !strings.Contains(e.Error(), want) {
			t.Errorf("%d: Until = %q, message %q, want %q", status, e.Until, e.Error(), want)
		}
	}

	plain := tlsServer(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(403) })
	_, err := newDownloader(plain.Client()).fetchBytes(context.Background(), plain.URL+"/x", 10)
	wantReason(t, "403 with quota left", err, ReasonDownloadFailed)
}

func TestFetchTooLarge(t *testing.T) {
	var served atomic.Int64
	srv := tlsServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/declared" {
			w.Header().Set("Content-Length", "1000")
		}
		flusher := w.(http.Flusher)
		for range 1000 {
			if _, err := w.Write([]byte("x")); err != nil {
				return
			}
			if r.URL.Path == "/chunked" {
				flusher.Flush()
			}
			served.Add(1)
		}
	})
	dl := newDownloader(srv.Client())

	_, err := dl.fetchBytes(context.Background(), srv.URL+"/declared", 100)
	var e *Error
	if !errors.As(err, &e) || e.Reason != ReasonTooLarge || e.Subject != "declared" || e.Limit != "100 bytes" {
		t.Errorf("declared length: err = %#v, want too-large for declared, 100 bytes", err)
	}

	f := tempFile(t)
	_, err = dl.fetchFile(context.Background(), srv.URL+"/chunked", f, 100)
	wantReason(t, "undeclared length", err, ReasonTooLarge)
	info, statErr := f.Stat()
	if statErr != nil || info.Size() > 101 {
		t.Errorf("wrote %d bytes past a 100 byte limit (err %v)", info.Size(), statErr)
	}
}

func TestFetchRedirects(t *testing.T) {
	var srv *httptest.Server
	srv = tlsServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/final":
			_, _ = w.Write([]byte("ok"))
		case "/once":
			http.Redirect(w, r, srv.URL+"/final", http.StatusFound)
		case "/plain":
			http.Redirect(w, r, "http://127.0.0.1:1/final", http.StatusFound)
		default:
			n, _ := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/hop"))
			if n == 0 {
				http.Redirect(w, r, srv.URL+"/final", http.StatusFound)
				return
			}
			http.Redirect(w, r, srv.URL+"/hop"+strconv.Itoa(n-1), http.StatusFound)
		}
	})
	dl := newDownloader(srv.Client())
	ctx := context.Background()

	if b, err := dl.fetchBytes(ctx, srv.URL+"/once", 10); err != nil || string(b) != "ok" {
		t.Errorf("one HTTPS redirect: %q, %v", b, err)
	}
	if b, err := dl.fetchBytes(ctx, srv.URL+"/hop3", 10); err != nil || string(b) != "ok" {
		t.Errorf("four redirects: %q, %v", b, err)
	}
	_, err := dl.fetchBytes(ctx, srv.URL+"/plain", 10)
	wantReason(t, "redirect to http", err, ReasonDownloadFailed)
	_, err = dl.fetchBytes(ctx, srv.URL+"/hop5", 10)
	wantReason(t, "six redirects", err, ReasonDownloadFailed)
}

// Each redirect hop gets its own header limit, and a stall is blamed on the
// server that stalled, not the one first asked.
func TestFetchRedirectHeaderLimitIsPerHop(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	second := tlsServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/slow":
			time.Sleep(100 * time.Millisecond)
			_, _ = w.Write([]byte("ok"))
		default:
			select {
			case <-release:
			case <-r.Context().Done():
			}
		}
	})
	first := tlsServer(t, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		http.Redirect(w, r, second.URL+r.URL.Path, http.StatusFound)
	})
	pool := x509.NewCertPool()
	pool.AddCert(first.Certificate())
	pool.AddCert(second.Certificate())
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}}}
	dl := shortLimits(newDownloader(client))

	// 100ms on each hop is within the 150ms limit; the 200ms total is not.
	if b, err := dl.fetchBytes(context.Background(), first.URL+"/slow", 10); err != nil || string(b) != "ok" {
		t.Errorf("two hops inside their own limits: %q, %v", b, err)
	}
	_, err := dl.fetchBytes(context.Background(), first.URL+"/silent", 10)
	var e *Error
	if host := strings.TrimPrefix(second.URL, "https://"); !errors.As(err, &e) || e.Reason != ReasonTimeout || e.Subject != host {
		t.Errorf("stall after a redirect: err = %#v, want a timeout naming %s", err, host)
	}
}

func TestFetchCutShort(t *testing.T) {
	rel := upgradetest.New(t, upgradetest.Options{CutAfter: 1000, Binary: bigBinary()})
	dl := newDownloader(rel.Client)
	url := rel.URL + "/rune-task-runner/rune/releases/download/v9.9.9/" + upgradetest.ArchiveName("9.9.9", "linux", "amd64")
	_, err := dl.fetchFile(context.Background(), url, tempFile(t), 1<<20)
	var e *Error
	if !errors.As(err, &e) || e.Reason != ReasonDownloadFailed || e.Subject != "rune_9.9.9_linux_amd64.tar.gz" {
		t.Errorf("cut transfer: err = %#v, want download-failed naming the archive", err)
	}
	if rel.Requests() != 1 {
		t.Errorf("%d requests: a cut transfer was retried", rel.Requests())
	}
}

func TestNewDownloaderKeepsProxyAndLeavesClientAlone(t *testing.T) {
	client := &http.Client{}
	dl := newDownloader(client)
	if client.Transport != nil || client.CheckRedirect != nil {
		t.Error("newDownloader modified the caller's client")
	}
	tr, ok := dl.client.Transport.(*http.Transport)
	if !ok || tr.Proxy == nil {
		t.Fatalf("downloader transport = %#v, want an *http.Transport with a proxy function", dl.client.Transport)
	}
	if tr.TLSHandshakeTimeout != defaultLimits.tlsHandshake {
		t.Errorf("TLS handshake timeout = %v", tr.TLSHandshakeTimeout)
	}
}
