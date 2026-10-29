package cli

import (
	"errors"
	"strings"
	"testing"
)

// A non-interactive decline explains itself through the standard failure
// banner (FR-004, FR-011, contracts/confirm-cli.md §2).
func TestConfirmNonTerminalMessage(t *testing.T) {
	const defaultWant = `task "clean" was not confirmed (Run "clean"?): stdin is not a terminal; pass --yes to approve`
	tests := []struct {
		name string
		src  string
		task string
		want string
	}{
		{
			name: "author prompt",
			src:  "[confirm(\"Apply to live infrastructure?\")]\napply:\n    @echo body\n",
			task: "apply",
			want: `task "apply" was not confirmed (Apply to live infrastructure?): stdin is not a terminal; pass --yes to approve`,
		},
		{
			name: "default prompt",
			src:  "[confirm]\nclean:\n    @echo body\n",
			task: "clean",
			want: defaultWant,
		},
		{
			// [no-exit-message] governs body failures; this body never started.
			name: "no-exit-message does not hide it",
			src:  "[confirm]\n[no-exit-message]\nclean:\n    @echo body\n",
			task: "clean",
			want: defaultWant,
		},
		{
			name: "control characters stay on one line",
			src:  "[confirm(\"Really?\\nThis drops prod\")]\nx:\n    @echo body\n",
			task: "x",
			want: `task "x" was not confirmed (Really?\nThis drops prod): stdin is not a terminal; pass --yes to approve`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			eng, _, _ := confirmEngine(t, tt.src, strings.NewReader("y\n"), false, false)

			err := eng.Execute(eng.tasks[tt.task], nil)

			var tf *TaskFailure
			if !errors.As(err, &tf) {
				t.Fatalf("Execute error = %v, want *TaskFailure", err)
			}
			if tf.Silent {
				t.Error("non-interactive decline must not be silent: its message names the fix")
			}
			if got := BannerMessage(err); got != tt.want {
				t.Errorf("banner = %q\n want %q", got, tt.want)
			}
		})
	}
}
