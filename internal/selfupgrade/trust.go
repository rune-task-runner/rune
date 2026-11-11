package selfupgrade

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/tuf"
	"github.com/theupdateframework/go-tuf/v2/metadata"
)

// tufFetcher lets the TUF client download through the bounded downloader.
// The library's default fetcher retries and has no timeout, and its interface
// carries no context, so the context is held here.
type tufFetcher struct {
	ctx context.Context //nolint:containedctx // the fetcher interface has no context parameter
	dl  *downloader
}

func (f *tufFetcher) DownloadFile(urlPath string, maxLength int64, _ time.Duration) ([]byte, error) {
	b, err := f.dl.fetchBytes(f.ctx, urlPath, maxLength)
	var e *Error
	if errors.As(err, &e) && e.Reason == ReasonDownloadFailed && e.Status != 0 {
		// The TUF client relies on this exact error, for a 404, to learn that
		// no newer root metadata exists.
		return nil, &metadata.ErrDownloadHTTP{StatusCode: e.Status, URL: urlPath}
	}
	return b, err
}

// PublicTrust returns a source of the public Sigstore trust root. It is
// fetched through TUF on every call and nothing is cached on disk, so a run
// writes nothing outside the install directory.
func PublicTrust(client *http.Client) func(context.Context) (root.TrustedMaterial, error) {
	return publicTrust(client, "")
}

// publicTrust is PublicTrust against another TUF repository; an empty
// repoURL means the public one.
func publicTrust(client *http.Client, repoURL string) func(context.Context) (root.TrustedMaterial, error) {
	return func(ctx context.Context) (root.TrustedMaterial, error) {
		opts := tuf.DefaultOptions()
		opts.DisableLocalCache = true
		opts.Fetcher = &tufFetcher{ctx: ctx, dl: newDownloader(client)}
		if repoURL != "" {
			opts.RepositoryBaseURL = repoURL
		}
		tufClient, err := tuf.New(opts)
		if err != nil {
			return nil, err
		}
		return root.GetTrustedRoot(tufClient)
	}
}
