package ci

import "testing"

func TestPlanFile(t *testing.T) {
	planned := []byte(Marker + "\nname: rune\n")
	for _, c := range []struct {
		existing string
		existed  bool
		want     State
	}{
		{"", false, StateMissing},
		{"name: ci\n", true, StateNotGenerated},
		{"", true, StateNotGenerated},
		{string(planned), true, StateCurrent},
		{Marker + "\nname: old\n", true, StateStale},
		{Marker + "\r\nname: rune\r\n", true, StateStale},
	} {
		if got := PlanFile([]byte(c.existing), c.existed, planned); got != c.want {
			t.Errorf("PlanFile(%q, %v) = %s, want %s", c.existing, c.existed, got, c.want)
		}
	}
}

func TestGenerated(t *testing.T) {
	if !Generated([]byte(Marker)) || !Generated([]byte(Marker+"\r\nx")) {
		t.Error("a marker line was not recognized")
	}
	if Generated([]byte("# "+Marker)) || Generated(nil) {
		t.Error("a file without the marker line was taken as generated")
	}
}
