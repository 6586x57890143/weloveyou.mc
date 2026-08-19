package bench

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// rejects builds a Prober that refuses any flag in the given set, the way a
// JVM refuses one it no longer recognises.
func rejects(bad ...string) Prober {
	set := map[string]bool{}
	for _, b := range bad {
		set[b] = true
	}
	return func(_ string, flags []string) error {
		for _, f := range flags {
			if set[f] {
				return errors.New("Unrecognized VM option")
			}
		}
		return nil
	}
}

func TestPreflightKeepsTheGoodFlags(t *testing.T) {
	in := []string{"-XX:+AlwaysPreTouch", "-XX:NmethodSweepActivity=1", "-XX:+UseCompactObjectHeaders"}
	ok, dropped := Preflight("img", in, rejects("-XX:NmethodSweepActivity=1"))

	want := []string{"-XX:+AlwaysPreTouch", "-XX:+UseCompactObjectHeaders"}
	if !reflect.DeepEqual(ok, want) {
		t.Errorf("kept %v, want %v", ok, want)
	}
	if !reflect.DeepEqual(dropped, []string{"-XX:NmethodSweepActivity=1"}) {
		t.Errorf("dropped %v", dropped)
	}
}

func TestPreflightProbesIndividuallyOnceTheSetIsRefused(t *testing.T) {
	// Probing the set only reveals that something in it is bad. The whole point
	// is to keep the rest, so once the set is refused each flag is asked about
	// on its own: one probe for the set, then one per flag.
	var calls int
	probe := func(_ string, flags []string) error {
		calls++
		for _, f := range flags {
			if f == "-XX:b" {
				return errors.New("Unrecognized VM option")
			}
		}
		return nil
	}
	Preflight("img", []string{"-XX:a", "-XX:b", "-XX:c"}, probe)
	if calls != 4 {
		t.Errorf("probed %d times; want 1 for the set plus 3 individual", calls)
	}
}

func TestPreflightSkipsIndividualProbesWhenTheSetIsClean(t *testing.T) {
	// The common case, and the expensive one: every probe is a JVM start, and
	// a profile carries ~29 flags. Nothing needs dropping when the set is
	// accepted whole, so it costs one start rather than twenty-nine.
	var calls int
	probe := func(_ string, _ []string) error { calls++; return nil }
	ok, dropped := Preflight("img", []string{"-XX:a", "-XX:b", "-XX:c"}, probe)
	if calls != 1 {
		t.Errorf("probed %d times for an acceptable set; want 1", calls)
	}
	if len(ok) != 3 || len(dropped) != 0 {
		t.Errorf("Preflight = (%v, %v), want all kept", ok, dropped)
	}
}

func TestPreflightEdgeCases(t *testing.T) {
	ok, dropped := Preflight("img", nil, rejects())
	if ok != nil || dropped != nil {
		t.Errorf("no flags in should mean nothing out, got (%v, %v)", ok, dropped)
	}
	// Everything refused is a legitimate outcome, not an error: the profile
	// simply degrades to the container defaults rather than failing to boot.
	ok, dropped = Preflight("img", []string{"-XX:x"}, rejects("-XX:x"))
	if len(ok) != 0 || len(dropped) != 1 {
		t.Errorf("all-refused should yield no kept flags, got (%v, %v)", ok, dropped)
	}
}

func TestProbeArgsUnlocksFirst(t *testing.T) {
	// Experimental and diagnostic flags are rejected for being locked unless the
	// unlockers precede them, which would make preflight drop good flags.
	args := ProbeArgs([]string{"-XX:+UseCompactObjectHeaders"})
	if args[0] != Unlockers[0] || args[1] != Unlockers[1] {
		t.Fatalf("unlockers must come first, got %v", args)
	}
	if args[len(args)-1] != "-version" {
		t.Errorf("probe must end with -version, got %v", args)
	}
	if !strings.Contains(strings.Join(args, " "), "-XX:+UseCompactObjectHeaders") {
		t.Errorf("the candidate flag is missing from %v", args)
	}
}

func TestProbeArgsDoesNotRepeatAnUnlocker(t *testing.T) {
	for _, u := range Unlockers {
		args := ProbeArgs([]string{u})
		count := 0
		for _, a := range args {
			if a == u {
				count++
			}
		}
		if count != 1 {
			t.Errorf("ProbeArgs(%q) repeated it %d times: %v", u, count, args)
		}
	}
}

func TestPreflightKeepsFlagsThatAreOnlyLegalTogether(t *testing.T) {
	// -XX:NodeLimitFudgeFactor must be 2-40% of -XX:MaxNodeLimit, so raising
	// MaxNodeLimit alone is refused against the default fudge factor while the
	// pair together is accepted. Dropping half the pair would leave the sweep
	// measuring a configuration nobody chose, and the row would still look fine.
	pair := []string{"-XX:MaxNodeLimit=240000", "-XX:NodeLimitFudgeFactor=8000"}
	probe := func(_ string, flags []string) error {
		if len(flags) == 1 && flags[0] == pair[0] {
			return errors.New("Unrecognized VM option")
		}
		return nil
	}
	ok, dropped := Preflight("img", pair, probe)
	if len(dropped) != 0 {
		t.Errorf("dropped %v, but the set as a whole is accepted", dropped)
	}
	if len(ok) != 2 {
		t.Errorf("kept %v, want both flags", ok)
	}
}

func TestPreflightStillDropsIndividuallyWhenTheSetIsBad(t *testing.T) {
	// One fatal flag must not rescue the others by making the set-probe fail.
	probe := func(_ string, flags []string) error {
		for _, f := range flags {
			if f == "-XX:Fatal" {
				return errors.New("Unrecognized VM option")
			}
		}
		return nil
	}
	ok, dropped := Preflight("img", []string{"-XX:Good", "-XX:Fatal"}, probe)
	if len(ok) != 1 || ok[0] != "-XX:Good" {
		t.Errorf("kept %v, want just the good flag", ok)
	}
	if len(dropped) != 1 || dropped[0] != "-XX:Fatal" {
		t.Errorf("dropped %v, want just the fatal flag", dropped)
	}
}

func TestPreflightHandlesNoFlags(t *testing.T) {
	ok, dropped := Preflight("img", nil, rejects("-XX:anything"))
	if len(ok) != 0 || len(dropped) != 0 {
		t.Errorf("Preflight(nil) = (%v, %v), want both empty", ok, dropped)
	}
}
