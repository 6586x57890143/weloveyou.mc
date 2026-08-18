package bench

import "testing"

func TestJavaMajorRejectsOversizedVersion(t *testing.T) {
	// A tag is untrusted text; an unparseable major must mean "assume nothing"
	// so compat flags get skipped rather than misapplied.
	if got := javaMajor("itzg/minecraft-server:java999999999999999999999999"); got != 0 {
		t.Errorf("javaMajor of an oversized tag = %d, want 0", got)
	}
}
