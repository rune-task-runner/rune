package config

import "testing"

func TestParseOutputLimit(t *testing.T) {
	accepted := map[string]int{
		"65536":   65536,
		"065536":  65536,
		"65536B":  65536,
		"64KiB":   65536,
		"064KiB":  65536,
		"256KiB":  262144,
		"1MiB":    1048576,
		"1024KiB": 1048576,
		"1024":    1024,
		"1KiB":    1024,
		"1024MiB": 1073741824,
	}
	for in, want := range accepted {
		got, err := ParseOutputLimit(in)
		if err != nil || got != want {
			t.Errorf("ParseOutputLimit(%q) = %d, %v; want %d", in, got, err, want)
		}
	}

	rejected := []string{
		"", "lots", "-1", "+5KiB", "0", "0KiB", "10", "1023", "1.5MiB", "64 KiB", " 64KiB", "64KiB ",
		"64kib", "64KB", "64K", "64Ki", "64iB", "64BB", "64KiBB", "1GiB", "1025MiB", "1073741825",
		"KiB", "B", "1_024", "0x400", "99999999999999999999", "9999999999999MiB", "18446744073709551615KiB",
	}
	for _, in := range rejected {
		got, err := ParseOutputLimit(in)
		if err == nil {
			t.Errorf("ParseOutputLimit(%q) = %d, want an error", in, got)
			continue
		}
		want := `invalid size "` + in + `": use a whole number of bytes or a KiB or MiB count, such as 65536 or 256KiB, from 1KiB to 1024MiB`
		if err.Error() != want {
			t.Errorf("ParseOutputLimit(%q) error = %q\nwant %q", in, err, want)
		}
	}
}
