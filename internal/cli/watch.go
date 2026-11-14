package cli

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
)

// watch re-runs the pipeline whenever a Runefile (or any file in its directory)
// changes, until interrupted. The first run happens immediately.
func watch(opts Options, runefile string, args []string) error {
	dir := filepath.Dir(runefile)
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return &UsageError{Err: err}
	}
	defer func() { _ = w.Close() }()
	if err := w.Add(dir); err != nil {
		return &UsageError{Err: err}
	}

	// Watch chrome is meta output, dimmed like dry-run/cache notices (014 C3);
	// the theme is built once and reused for every re-run and error banner.
	th := opts.themeStderr()

	runOnce := func() {
		// BannerMessage suppresses banners the pipeline already rendered (a
		// ValidationError's diagnostics/cycle banner) or intentionally silences
		// ([no-exit-message]), matching the non-watch path in cmd/rune so a
		// re-run never double-prints the banner.
		if err := execute(opts, runefile, args); err != nil {
			if msg := BannerMessage(err); msg != "" {
				printErrorBanner(opts, th, msg)
			}
		}
	}
	fmt.Fprintln(opts.Stderr, th.Muted.Render(fmt.Sprintf("watching %s (Ctrl-C to stop)", dir)))
	runOnce()

	var debounce <-chan time.Time
	for {
		select {
		case <-opts.ctx().Done():
			// SIGINT/cancellation: stop watching cleanly (exit 130).
			return &Interrupted{Err: opts.ctx().Err()}
		case ev, ok := <-w.Events:
			if !ok {
				return nil
			}
			if relevant(ev) {
				debounce = time.After(reloadDebounce)
			}
		case <-debounce:
			debounce = nil
			fmt.Fprintln(opts.Stderr, th.Muted.Render("change detected, re-running…"))
			runOnce()
		case err, ok := <-w.Errors:
			if !ok {
				return nil
			}
			printErrorBanner(opts, th, "watch error: "+err.Error())
		}
	}
}

// relevant reports whether a filesystem event should trigger a re-run, ignoring
// the cache directory and editor temp churn.
func relevant(ev fsnotify.Event) bool {
	if ev.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename|fsnotify.Remove) == 0 {
		return false
	}
	base := filepath.Base(ev.Name)
	if strings.HasPrefix(base, ".") && base != ".runefile" {
		return false
	}
	if strings.Contains(ev.Name, ".rune/") {
		return false
	}
	return true
}
