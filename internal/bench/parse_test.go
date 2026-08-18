package bench

import (
	"strings"
	"testing"
	"time"
)

func TestParseChunkyChunks(t *testing.T) {
	tests := []struct {
		name string
		line string
		want int
		ok   bool
	}{
		{"plain", "[Chunky] Task running for minecraft:overworld. Processed: 5000 chunks (5.0%)", 5000, true},
		{"with colon and commas", "Processed: 1,234,567 chunks", 1234567, true},
		{"no colon", "Processed 42 chunks, ETA 00:01:00", 42, true},
		{"zero is a real reading", "Processed: 0 chunks", 0, true},
		// Negative cases matter more than positive ones here: a regex that
		// silently matches nothing is the failure mode, and so is one that
		// matches too much.
		{"unrelated line", "[Server thread/INFO]: Done (11.5s)!", 0, false},
		{"chunks without a count", "Processed: many chunks", 0, false},
		{"empty", "", 0, false},
		{"mentions chunks only", "Preparing spawn area: 12%", 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ParseChunkyChunks(tt.line)
			if ok != tt.ok || got != tt.want {
				t.Fatalf("ParseChunkyChunks(%q) = (%d, %v), want (%d, %v)", tt.line, got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestParseGCPause(t *testing.T) {
	tests := []struct {
		name string
		line string
		want float64
		ok   bool
	}{
		{"young", "[12.345s][info][gc] GC(7) Pause Young (Normal) 512M->128M(6144M) 12.345ms", 12.345, true},
		{"full", "[99.9s][info][gc] GC(12) Pause Full (System.gc()) 1G->200M(6G) 250.500ms", 250.5, true},
		{"sub-millisecond", "[1.0s][info][gc] GC(1) Pause Init Mark 0.123ms", 0.123, true},
		{"trailing space", "[1.0s][info][gc] GC(1) Pause Young 5.0ms  ", 5.0, true},
		{"a concurrent phase is not a pause", "[3.0s][info][gc] GC(2) Concurrent Mark 40.000ms", 0, false},
		{"not a gc line", "[Server thread/INFO]: Done (11.5s)!", 0, false},
		{"empty", "", 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ParseGCPause(tt.line)
			if ok != tt.ok || got != tt.want {
				t.Fatalf("ParseGCPause(%q) = (%v, %v), want (%v, %v)", tt.line, got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestParseMemUsage(t *testing.T) {
	tests := []struct {
		in   string
		want float64
		ok   bool
	}{
		{"1.234GiB / 11.6GiB", 1.234 * (1 << 30), true},
		{"512MiB / 11.6GiB", 512 * (1 << 20), true},
		{"  2GiB / 4GiB", 2 * (1 << 30), true},
		{"900KiB / 4GiB", 900 * (1 << 10), true},
		{"1.5GB / 4GB", 1.5e9, true},
		{"unavailable", 0, false},
		{"", 0, false},
		{"12 parsecs / 4GiB", 0, false},
	}
	for _, tt := range tests {
		got, ok := ParseMemUsage(tt.in)
		if ok != tt.ok || got != tt.want {
			t.Errorf("ParseMemUsage(%q) = (%v, %v), want (%v, %v)", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}

func TestParseServerReady(t *testing.T) {
	tests := []struct {
		line string
		want time.Duration
		ok   bool
	}{
		{`[15:08:13] [Server thread/INFO]: Done (11.572s)! For help, type "help"`, 11572 * time.Millisecond, true},
		{"Done (1.7s)!", 1700 * time.Millisecond, true},
		{"[Server thread/INFO]: Preparing level \"world\"", 0, false},
		{"", 0, false},
	}
	for _, tt := range tests {
		got, ok := ParseServerReady(tt.line)
		if ok != tt.ok || got != tt.want {
			t.Errorf("ParseServerReady(%q) = (%v, %v), want (%v, %v)", tt.line, got, ok, tt.want, tt.ok)
		}
	}
}

func FuzzParseLogLines(f *testing.F) {
	f.Add("[12.3s][info][gc] GC(1) Pause Young 5.0ms")
	f.Add("Processed: 100 chunks")
	f.Add("Done (1.0s)!")
	f.Add("1.2GiB / 4GiB")
	// These parse server output, which includes player chat. Nothing here may
	// panic on hostile input.
	f.Fuzz(func(t *testing.T, s string) {
		ParseChunkyChunks(s)
		ParseGCPause(s)
		ParseMemUsage(s)
		ParseServerReady(s)
	})
}

// A regex match guarantees digits but not that they fit. Server logs are not a
// trusted input, and a 400-digit chunk count must be refused rather than
// wrapped into something plausible.
func TestParsersRejectOversizedNumbers(t *testing.T) {
	huge := "9" + strings.Repeat("9", 400)
	if _, ok := ParseChunkyChunks("Processed: " + huge + " chunks"); ok {
		t.Error("an unrepresentable chunk count should be refused")
	}
	if _, ok := ParseGCPause("GC(1) Pause Young " + huge + ".0ms"); ok {
		t.Error("an unrepresentable pause should be refused")
	}
	if _, ok := ParseMemUsage(huge + ".0GiB / 4GiB"); ok {
		t.Error("an unrepresentable memory figure should be refused")
	}
	if _, ok := ParseServerReady("Done (" + huge + ".0s)!"); ok {
		t.Error("an unrepresentable startup time should be refused")
	}
}
