package bench

import (
	"math"
	"strings"
	"testing"
	"time"
)

func TestShardSplitsRoundRobin(t *testing.T) {
	ps := []Profile{{Name: "a"}, {Name: "b"}, {Name: "c"}, {Name: "d"}, {Name: "e"}}
	seen := map[string]int{}
	for i := 1; i <= 3; i++ {
		got, err := Shard(ps, itoaShard(i, 3))
		if err != nil {
			t.Fatalf("Shard(%d/3) = %v", i, err)
		}
		for _, p := range got {
			seen[p.Name]++
		}
	}
	// Every profile measured exactly once across the shards. A profile that
	// appeared twice would be wasted machine time; one that appeared zero times
	// would be a hole in the table nobody notices.
	if len(seen) != len(ps) {
		t.Fatalf("shards covered %d profiles, want %d: %v", len(seen), len(ps), seen)
	}
	for name, n := range seen {
		if n != 1 {
			t.Errorf("%s measured %d times, want exactly 1", name, n)
		}
	}
	// Round-robin, not contiguous: shard 1 takes a and d, so the slow profiles
	// spread out instead of landing on one unlucky box.
	first, _ := Shard(ps, "1/3")
	if len(first) != 2 || first[0].Name != "a" || first[1].Name != "d" {
		t.Errorf("shard 1/3 = %v, want a and d", first)
	}
}

func TestShardEmptySpecKeepsEverything(t *testing.T) {
	ps := []Profile{{Name: "a"}, {Name: "b"}}
	got, err := Shard(ps, "")
	if err != nil || len(got) != 2 {
		t.Errorf("Shard(ps, \"\") = (%v, %v), want both", got, err)
	}
}

func TestBadShardErrorSaysWhatIsAllowed(t *testing.T) {
	// The message is the whole value of a typed error here: "bad shard 4/3" on
	// its own leaves you guessing whether the problem is the 4 or the 3.
	_, err := Shard([]Profile{{Name: "a"}}, "4/3")
	if err == nil {
		t.Fatal("4/3 should be rejected")
	}
	if !strings.Contains(err.Error(), "4/3") || !strings.Contains(err.Error(), "1 <= i <= n") {
		t.Errorf("error should name the bad spec and the rule, got %q", err)
	}
}

func TestMarshalResultsRejectsWhatCannotBeEncoded(t *testing.T) {
	// NaN has no JSON representation, and a run whose numbers went wrong should
	// fail loudly rather than write a shard file the merge step cannot read.
	_, err := MarshalResults([]Result{{
		Profile: "x", Runs: []Run{{PeakCPU: math.NaN()}},
	}})
	if err == nil {
		t.Error("a value with no JSON encoding should be an error")
	}
}

func TestShardRejectsNonsense(t *testing.T) {
	for _, spec := range []string{"1", "0/3", "4/3", "a/3", "1/0", "1/2/3", ""} {
		if spec == "" {
			continue
		}
		if _, err := Shard([]Profile{{Name: "a"}}, spec); err == nil {
			t.Errorf("Shard accepted %q", spec)
		}
	}
}

func TestMergeResultsCombinesAndOrders(t *testing.T) {
	// Two shards finishing in either order must produce the same table.
	a, err := MarshalResults([]Result{
		{Profile: "z", Workload: WorkloadPack, Runs: []Run{{Chunks: 1, Elapsed: time.Second}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	b, err := MarshalResults([]Result{
		{Profile: "a", Workload: WorkloadPack, Runs: []Run{{Chunks: 2, Elapsed: time.Second}}},
		{Profile: "m", Workload: WorkloadVanilla, Runs: []Run{{Chunks: 3, Elapsed: time.Second}}},
	})
	if err != nil {
		t.Fatal(err)
	}

	merged, err := MergeResults([][]byte{a, b})
	if err != nil {
		t.Fatalf("MergeResults() = %v", err)
	}
	if len(merged) != 3 {
		t.Fatalf("merged %d results, want 3", len(merged))
	}
	reversed, err := MergeResults([][]byte{b, a})
	if err != nil {
		t.Fatal(err)
	}
	for i := range merged {
		if merged[i].Profile != reversed[i].Profile || merged[i].Workload != reversed[i].Workload {
			t.Fatalf("order depends on which shard was read first: %v vs %v", merged, reversed)
		}
	}
	// The measurements survive the round trip, not just the names.
	if merged[0].Runs[0].Chunks == 0 {
		t.Error("runs were lost in the round trip")
	}
}

func TestMergeResultsRejectsRubbish(t *testing.T) {
	if _, err := MergeResults([][]byte{[]byte("not json")}); err == nil {
		t.Error("a corrupt shard file should be an error, not a silently short table")
	}
}

func itoaShard(i, n int) string {
	return string(rune('0'+i)) + "/" + string(rune('0'+n))
}
