package selfupgrade

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sync/atomic"
	"time"
)

// limits bounds every network wait. Nothing is retried, so the worst case of
// a run is the sum of these, capped by runLimit.
type limits struct {
	dial         time.Duration
	tlsHandshake time.Duration
	header       time.Duration // request sent, no response headers yet
	idle         time.Duration // no body bytes received
	redirects    int
}

var defaultLimits = limits{
	dial:         10 * time.Second,
	tlsHandshake: 10 * time.Second,
	header:       15 * time.Second,
	idle:         30 * time.Second,
	redirects:    5,
}

// runLimit caps a whole upgrade.
const runLimit = 10 * time.Minute

// downloader performs every HTTP request of an upgrade. It stands apart from
// Upgrader because the trust-root fetch is built before an Upgrader exists
// and must go through the same bounded code.
type downloader struct {
	client    *http.Client
	userAgent string
	lim       limits
}

// newDownloader wraps a copy of client, so the caller's client is untouched.
// A client without a transport gets one that honors the proxy environment
// and bounds connection setup.
func newDownloader(client *http.Client) *downloader {
	lim := defaultLimits
	c := *client
	if c.Transport == nil {
		c.Transport = &http.Transport{
			Proxy:               http.ProxyFromEnvironment,
			DialContext:         (&net.Dialer{Timeout: lim.dial}).DialContext,
			TLSHandshakeTimeout: lim.tlsHandshake,
		}
	}
	return &downloader{client: &c, userAgent: "rune", lim: lim}
}

func checkRedirect(lim limits, req *http.Request, via []*http.Request) error {
	if len(via) > lim.redirects {
		return fmt.Errorf("stopped after %d redirects", lim.redirects)
	}
	if req.URL.Scheme != "https" {
		return errors.New("refused a redirect to a non-HTTPS address")
	}
	return nil
}

// fetchBytes downloads url into memory, refusing more than limit bytes.
func (d *downloader) fetchBytes(ctx context.Context, rawURL string, limit int64) ([]byte, error) {
	return d.fetchBytesAccept(ctx, rawURL, "", limit)
}

func (d *downloader) fetchBytesAccept(ctx context.Context, rawURL, accept string, limit int64) ([]byte, error) {
	var buf bytes.Buffer
	if err := d.get(ctx, rawURL, accept, limit, &buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// fetchFile downloads url into dst and returns the SHA-256 of what it wrote.
func (d *downloader) fetchFile(ctx context.Context, rawURL string, dst *os.File, limit int64) ([32]byte, error) {
	h := sha256.New()
	file := &failingWriter{w: dst}
	if err := d.get(ctx, rawURL, "", limit, io.MultiWriter(file, h)); err != nil {
		if file.err != nil {
			// The disk failed (full, over quota), not the download.
			return [32]byte{}, &Error{Reason: ReasonNotWritable, Subject: filepath.Dir(dst.Name()), Err: file.err}
		}
		return [32]byte{}, err
	}
	return [32]byte(h.Sum(nil)), nil
}

// A stalled request is cancelled by one of two timers; which one fired
// decides the message.
const (
	stallNone int32 = iota
	stallHeader
	stallIdle
)

func (d *downloader) get(parent context.Context, rawURL, accept string, limit int64, w io.Writer) error {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return fmt.Errorf("invalid download URL %q", rawURL)
	}
	name := path.Base(u.Path)

	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	var stalled atomic.Int32
	stall := func(kind int32) func() {
		return func() {
			stalled.CompareAndSwap(stallNone, kind)
			cancel()
		}
	}
	// host is the server being waited on; a redirect moves it.
	host := u.Host
	fail := func(err error) error { return d.classify(parent, err, host, name, stalled.Load()) }

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", d.userAgent)
	if accept != "" {
		req.Header.Set("Accept", accept)
	}

	headerTimer := time.AfterFunc(d.lim.header, stall(stallHeader))
	// Each redirect hop is a new server with its own header limit. The copy
	// shares the transport; CheckRedirect runs on this goroutine, inside Do.
	client := *d.client
	client.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		if err := checkRedirect(d.lim, next, via); err != nil {
			return err
		}
		host = next.URL.Host
		headerTimer.Reset(d.lim.header)
		return nil
	}
	resp, err := client.Do(req)
	headerTimer.Stop()
	if err != nil {
		return fail(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		if e := rateLimited(resp); e != nil {
			return e
		}
		return &Error{Reason: ReasonDownloadFailed, Subject: name, Status: resp.StatusCode}
	}
	tooLarge := &Error{Reason: ReasonTooLarge, Subject: name, Limit: formatSize(limit)}
	if resp.ContentLength > limit {
		return tooLarge
	}

	idleTimer := time.AfterFunc(d.lim.idle, stall(stallIdle))
	defer idleTimer.Stop()
	body := &progressReader{r: resp.Body, onProgress: func() { idleTimer.Reset(d.lim.idle) }}
	n, err := io.Copy(w, io.LimitReader(body, limit+1))
	if err != nil {
		return fail(err)
	}
	if n > limit {
		return tooLarge
	}
	if resp.ContentLength >= 0 && n < resp.ContentLength {
		return &Error{Reason: ReasonDownloadFailed, Subject: name, Err: io.ErrUnexpectedEOF}
	}
	return nil
}

// progressReader reports each read that delivered bytes.
type progressReader struct {
	r          io.Reader
	onProgress func()
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	if n > 0 {
		p.onProgress()
	}
	return n, err
}

// classify names why a request failed. The caller's own cancellation is
// passed through untouched so it is reported as an interrupt, not a failure.
func (d *downloader) classify(parent context.Context, err error, host, name string, stalled int32) error {
	switch stalled {
	case stallHeader:
		return &Error{Reason: ReasonTimeout, Subject: host, Limit: d.lim.header.String()}
	case stallIdle:
		return &Error{Reason: ReasonTimeout, Subject: host, Limit: d.lim.idle.String()}
	}
	if errors.Is(parent.Err(), context.Canceled) {
		return context.Canceled
	}
	if errors.Is(parent.Err(), context.DeadlineExceeded) {
		return &Error{Reason: ReasonTimeout, Subject: host, Limit: fmt.Sprintf("the %s allowed for the whole upgrade", runLimit)}
	}
	// The URL in a *url.Error only repeats what the message already names.
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return &Error{Reason: ReasonTimeout, Subject: host, Limit: d.lim.dial.String()}
	}
	var dnsErr *net.DNSError
	var opErr *net.OpError
	if errors.As(err, &opErr) && opErr.Op == "proxyconnect" {
		// The address is on the dial error inside, when there is one.
		proxy, cause := "the proxy", opErr.Err
		var dial *net.OpError
		if errors.As(opErr.Err, &dial) && dial.Addr != nil {
			proxy, cause = "the proxy "+dial.Addr.String(), dial.Err
		}
		return &Error{Reason: ReasonOffline, Subject: proxy, Err: cause}
	}
	if errors.As(err, &dnsErr) || (errors.As(err, &opErr) && opErr.Op == "dial") {
		return &Error{Reason: ReasonOffline, Subject: host, Err: err}
	}
	return &Error{Reason: ReasonDownloadFailed, Subject: name, Err: err}
}
