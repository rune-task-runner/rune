package agentinit

import (
	"fmt"
	"strings"
	"testing"
)

func numbered(n int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	return b.String()
}

func TestUnifiedDiff(t *testing.T) {
	twenty := numbered(20)
	tests := []struct {
		name     string
		old, new string
		existed  bool
		want     string
	}{
		{"equal", "a\n", "a\n", true, ""},
		{"new file", "", "a\nb\n", false, "--- /dev/null\n+++ b/dir/f.json\n@@ -0,0 +1,2 @@\n+a\n+b\n"},
		{"empty file that existed", "", "a\n", true, "--- a/dir/f.json\n+++ b/dir/f.json\n@@ -0,0 +1,1 @@\n+a\n"},
		{
			"one change with three lines of context", twenty, strings.Replace(twenty, "line 10\n", "line ten\n", 1), true,
			"--- a/dir/f.json\n+++ b/dir/f.json\n@@ -7,7 +7,7 @@\n line 7\n line 8\n line 9\n-line 10\n+line ten\n line 11\n line 12\n line 13\n",
		},
		{
			"two changes far apart give two hunks", twenty,
			strings.Replace(strings.Replace(twenty, "line 2\n", "line 2\nadded\n", 1), "line 18\n", "", 1), true,
			"--- a/dir/f.json\n+++ b/dir/f.json\n@@ -1,5 +1,6 @@\n line 1\n line 2\n+added\n line 3\n line 4\n line 5\n" +
				"@@ -15,6 +16,5 @@\n line 15\n line 16\n line 17\n-line 18\n line 19\n line 20\n",
		},
		{
			"two changes close together share a hunk", numbered(9),
			strings.Replace(strings.Replace(numbered(9), "line 2\n", "", 1), "line 7\n", "", 1), true,
			"--- a/dir/f.json\n+++ b/dir/f.json\n@@ -1,9 +1,7 @@\n line 1\n-line 2\n line 3\n line 4\n line 5\n line 6\n-line 7\n line 8\n line 9\n",
		},
		{
			"no final line break before", "a\nb", "a\nb\nc\n", true,
			"--- a/dir/f.json\n+++ b/dir/f.json\n@@ -1,2 +1,3 @@\n a\n-b\n\\ No newline at end of file\n+b\n+c\n",
		},
		{
			"no final line break after", "a\n", "a\nb", true,
			"--- a/dir/f.json\n+++ b/dir/f.json\n@@ -1,1 +1,2 @@\n a\n+b\n\\ No newline at end of file\n",
		},
		{"everything removed", "a\n", "", true, "--- a/dir/f.json\n+++ b/dir/f.json\n@@ -1,1 +0,0 @@\n-a\n"},
		{
			"repeated lines: the common head is kept first", "a\nb\na\nb\na\n", "a\nb\na\n", true,
			"--- a/dir/f.json\n+++ b/dir/f.json\n@@ -1,5 +1,3 @@\n a\n b\n a\n-b\n-a\n",
		},
		{
			"the new text is the start of the old", "a\nb\nc\n", "a\n", true,
			"--- a/dir/f.json\n+++ b/dir/f.json\n@@ -1,3 +1,1 @@\n a\n-b\n-c\n",
		},
		{
			"a line added in the middle of many", numbered(40), strings.Replace(numbered(40), "line 20\n", "line 20\nnew\n", 1), true,
			"--- a/dir/f.json\n+++ b/dir/f.json\n@@ -18,6 +18,7 @@\n line 18\n line 19\n line 20\n+new\n line 21\n line 22\n line 23\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := UnifiedDiff("dir/f.json", []byte(tt.old), []byte(tt.new), tt.existed); got != tt.want {
				t.Errorf("got\n%s\nwant\n%s", got, tt.want)
			}
		})
	}
}

func TestSplitLines(t *testing.T) {
	if got := splitLines([]byte("a\n\nb")); len(got) != 3 || got[0] != "a\n" || got[1] != "\n" || got[2] != "b" {
		t.Errorf("got %q", got)
	}
	if got := splitLines(nil); len(got) != 0 {
		t.Errorf("an empty text has %d lines", len(got))
	}
}
