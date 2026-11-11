package selfupgrade

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/rune-task-runner/rune/internal/semver"
)

const (
	repoPath      = "/rune-task-runner/rune"
	githubAccept  = "application/vnd.github+json"
	metadataLimit = 1 << 20
)

// latest asks the release service for the newest stable release. The service
// defines "latest" as neither draft nor prerelease; the answer is checked
// again here so a mislabelled release is refused, not installed.
func (u *Upgrader) latest(ctx context.Context) (tag string, version semver.Version, err error) {
	body, err := u.dl.fetchBytesAccept(ctx, u.cfg.APIBase+"/repos"+repoPath+"/releases/latest", githubAccept, metadataLimit)
	if err != nil {
		var e *Error
		if errors.As(err, &e) && e.Reason == ReasonDownloadFailed {
			// A bad answer from the release service is not a failed file download.
			return "", semver.Version{}, &Error{Reason: ReasonReleaseLookup, Status: e.Status, Err: e.Err}
		}
		return "", semver.Version{}, err
	}
	var rel struct {
		TagName    string `json:"tag_name"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
	}
	if err := json.Unmarshal(body, &rel); err != nil {
		return "", semver.Version{}, &Error{Reason: ReasonReleaseLookup, Err: errors.New("the answer is not JSON")}
	}
	bare, ok := strings.CutPrefix(rel.TagName, "v")
	if !ok {
		return "", semver.Version{}, &Error{Reason: ReasonReleaseLookup, Err: fmt.Errorf("unexpected release tag %q", rel.TagName)}
	}
	parsed, err := semver.Parse(bare)
	if err != nil {
		return "", semver.Version{}, &Error{Reason: ReasonReleaseLookup, Err: fmt.Errorf("unexpected release tag %q", rel.TagName)}
	}
	if rel.Draft || rel.Prerelease || len(parsed.Prerelease) > 0 {
		return "", semver.Version{}, &Error{Reason: ReasonReleaseLookup, Err: fmt.Errorf("release %s is not a stable release", rel.TagName)}
	}
	return rel.TagName, parsed, nil
}

// assetURL is the download address of one file of a release. It is built
// from the tag, so nothing but the version is taken from the service's answer.
func (u *Upgrader) assetURL(tag, name string) string {
	return u.cfg.DownloadBase + repoPath + "/releases/download/" + tag + "/" + name
}

// rateLimited recognises the release service refusing a request because the
// caller's quota is used up, and reports when it resets.
func rateLimited(resp *http.Response) *Error {
	if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusTooManyRequests {
		return nil
	}
	if resp.Header.Get("X-Ratelimit-Remaining") != "0" {
		return nil
	}
	until := "the limit resets"
	if reset, err := strconv.ParseInt(resp.Header.Get("X-Ratelimit-Reset"), 10, 64); err == nil {
		until = time.Unix(reset, 0).Local().Format("15:04:05 MST")
	}
	return &Error{Reason: ReasonRateLimited, Until: until, Status: resp.StatusCode}
}
