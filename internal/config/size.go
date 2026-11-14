package config

import (
	"fmt"
	"strconv"
	"strings"
)

// Bounds of an output limit in bytes. They repeat MinOutputLimit
// and MaxOutputLimit so that this package, which the analyzer and
// the language server load, does not pull in the MCP server; a test in
// internal/cli holds the two pairs equal.
const (
	MinOutputLimit = 1 << 10
	MaxOutputLimit = 1 << 30
)

// ParseOutputLimit reads the size of `set mcp_output_limit` and of
// `rune mcp --output-limit`: decimal digits with an optional B, KiB, or MiB
// unit, from MinOutputLimit to MaxOutputLimit bytes. One
// parser serves both so a limit is spelled the same way in either place.
func ParseOutputLimit(s string) (int, error) {
	invalid := fmt.Errorf("invalid size %q: use a whole number of bytes or a KiB or MiB count, such as 65536 or 256KiB, from 1KiB to 1024MiB", s)
	digits := strings.TrimRight(s, "BKMi")
	unit := uint64(1)
	switch s[len(digits):] {
	case "", "B":
	case "KiB":
		unit = 1 << 10
	case "MiB":
		unit = 1 << 20
	default:
		return 0, invalid
	}
	// ParseUint accepts a sign and underscores in no base-10 form, but an
	// explicit digit check keeps "+5" out whatever strconv allows later.
	if digits == "" || strings.Trim(digits, "0123456789") != "" {
		return 0, invalid
	}
	n, err := strconv.ParseUint(digits, 10, 64)
	// The bound on n comes first so the product cannot overflow.
	if err != nil || n > MaxOutputLimit || n*unit < MinOutputLimit || n*unit > MaxOutputLimit {
		return 0, invalid
	}
	return int(n * unit), nil
}
