package selfupgrade

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rune-task-runner/rune/internal/selfupgrade/upgradetest"
)

func upgraderFor(t *testing.T, rel *upgradetest.Release) *Upgrader {
	t.Helper()
	cfg := validConfig()
	cfg.HTTP, cfg.APIBase, cfg.DownloadBase = rel.Client, rel.URL, rel.URL
	u, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestLatest(t *testing.T) {
	rel := upgradetest.New(t, upgradetest.Options{})
	tag, version, err := upgraderFor(t, rel).latest(context.Background())
	if err != nil || tag != "v9.9.9" || version.String() != "9.9.9" {
		t.Fatalf("latest = %q, %q, %v", tag, version, err)
	}
}

func TestLatestHeaders(t *testing.T) {
	var got http.Header
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		_, _ = w.Write([]byte(`{"tag_name":"v1.2.3"}`))
	}))
	defer srv.Close()
	cfg := validConfig()
	cfg.HTTP, cfg.APIBase, cfg.DownloadBase = srv.Client(), srv.URL, srv.URL
	u, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := u.latest(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got.Get("Accept") != "application/vnd.github+json" {
		t.Errorf("Accept = %q", got.Get("Accept"))
	}
	if !strings.HasPrefix(got.Get("User-Agent"), "rune/") {
		t.Errorf("User-Agent = %q, want a rune/ prefix", got.Get("User-Agent"))
	}
}

func TestLatestRefuses(t *testing.T) {
	for name, o := range map[string]upgradetest.Options{
		"prerelease flag":  {Prerelease: true},
		"draft flag":       {Draft: true},
		"prerelease tag":   {Version: "9.9.9-rc.1"},
		"server error":     {Status: 500},
		"not found":        {Status: 404},
		"forbidden, quota": {Status: 403},
	} {
		rel := upgradetest.New(t, o)
		_, _, err := upgraderFor(t, rel).latest(context.Background())
		wantReason(t, name, err, ReasonReleaseLookup)
	}

	for name, body := range map[string]string{
		"not JSON":       "<html>",
		"no tag":         `{"draft":false}`,
		"tag without v":  `{"tag_name":"1.2.3"}`,
		"tag not semver": `{"tag_name":"vnext"}`,
		"empty":          "",
	} {
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }))
		cfg := validConfig()
		cfg.HTTP, cfg.APIBase, cfg.DownloadBase = srv.Client(), srv.URL, srv.URL
		u, err := New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		_, _, err = u.latest(context.Background())
		wantReason(t, name, err, ReasonReleaseLookup)
		srv.Close()
	}
}

func TestLatestKeepsStatus(t *testing.T) {
	rel := upgradetest.New(t, upgradetest.Options{Status: 500})
	_, _, err := upgraderFor(t, rel).latest(context.Background())
	if err == nil || !strings.Contains(err.Error(), "HTTP 500") {
		t.Errorf("err = %v, want it to name HTTP 500", err)
	}
}

func TestAssetURL(t *testing.T) {
	cfg := validConfig()
	cfg.DownloadBase = "https://github.com"
	u, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	want := "https://github.com/rune-task-runner/rune/releases/download/v0.8.0/checksums.txt"
	if got := u.assetURL("v0.8.0", "checksums.txt"); got != want {
		t.Errorf("assetURL = %q, want %q", got, want)
	}
}
