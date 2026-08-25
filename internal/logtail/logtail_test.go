package logtail

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// collector gathers lines from a Follow running in the background.
type collector struct {
	mu    sync.Mutex
	lines []string
}

func (c *collector) add(s string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lines = append(c.lines, s)
}

func (c *collector) all() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.lines...)
}

// waitFor polls until the collector holds want, or gives up. Polling rather than
// sleeping a fixed time keeps the test fast when it passes and honest when it
// fails.
func (c *collector) waitFor(t *testing.T, want string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if slices.Contains(c.all(), want) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("never saw %q; got %q", want, c.all())
}

func startTail(t *testing.T, path string, fromStart bool) *collector {
	t.Helper()
	c := &collector{}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	tl := &Tailer{Path: path, Poll: 10 * time.Millisecond, FromStart: fromStart}
	go func() { _ = tl.Follow(ctx, c.add) }()
	return c
}

func appendLine(t *testing.T, path, line string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.WriteString(line + "\n"); err != nil {
		t.Fatal(err)
	}
}

// The default is to follow from the end. It has to be: the daemon restarting
// would otherwise repost every death of the last week into Discord.
func TestFollowsFromTheEndByDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "latest.log")
	if err := os.WriteFile(path, []byte("old line\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := startTail(t, path, false)
	time.Sleep(50 * time.Millisecond) // let it reach the end before writing
	appendLine(t, path, "new line")
	c.waitFor(t, "new line")

	for _, l := range c.all() {
		if l == "old line" {
			t.Fatal("replayed history; a restart would repost a week of deaths")
		}
	}
}

// Minecraft renames latest.log on restart and creates a fresh one. An open
// handle stays valid and stays pointed at a file nobody writes to again, so a
// tailer that does not notice goes quiet forever and nothing looks broken.
func TestFollowsAcrossRotation(t *testing.T) {
	// Windows refuses to rename a file that is open, so this exact sequence
	// cannot be staged here. The deployment target is Linux, where renaming an
	// open file is precisely what Minecraft does on restart, and the code under
	// test (os.SameFile against the path) is platform independent. Skipped
	// rather than weakened into something that passes everywhere and proves
	// less. CI runs on Linux, so this does get exercised.
	if runtime.GOOS == "windows" {
		t.Skip("renaming an open file is not permitted on Windows; this is Linux behaviour")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "latest.log")
	if err := os.WriteFile(path, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	c := startTail(t, path, false)
	time.Sleep(50 * time.Millisecond)
	appendLine(t, path, "before restart")
	c.waitFor(t, "before restart")

	// What the server does: rename the old one aside, write a new one.
	if err := os.Rename(path, filepath.Join(dir, "2026-08-25-1.log")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("after restart\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c.waitFor(t, "after restart")
}

// Truncation in place is the other shape, and identity does not catch it: the
// file is the same file, it just got shorter.
func TestFollowsAcrossTruncation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "latest.log")
	if err := os.WriteFile(path, []byte("first\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := startTail(t, path, false)
	time.Sleep(50 * time.Millisecond)
	appendLine(t, path, "second")
	c.waitFor(t, "second")

	// Truncate and append as two steps, because that is what copytruncate
	// actually does and it is what a tailer can observe. A single WriteFile
	// does both between two polls: the file is already back above the old
	// offset by the time anything looks, so the reader takes the tail of the
	// new content as the end of the old line and emits a fragment. That race is
	// real rather than a test artifact, and it is recorded in the package doc,
	// but it is not a shape Minecraft produces. Minecraft rotates by renaming,
	// which is the test above.
	if err := os.Truncate(path, 0); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	appendLine(t, path, "after truncate")
	c.waitFor(t, "after truncate")
}

// A line arriving in pieces must be delivered once, whole. Reading a partial
// write as a line would hand the parser half a death message.
func TestPartialWritesAreJoined(t *testing.T) {
	path := filepath.Join(t.TempDir(), "latest.log")
	if err := os.WriteFile(path, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	c := startTail(t, path, false)
	time.Sleep(50 * time.Millisecond)

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString("kon fell from ")
	time.Sleep(50 * time.Millisecond)
	_, _ = f.WriteString("a high place\n")
	_ = f.Close()

	c.waitFor(t, "kon fell from a high place")
}

// A crash dump or a mod printing an NBT tree can produce a line with no newline
// in sight. Unbounded buffering of that is how a 512m container dies.
func TestAbsurdlyLongLineIsTruncatedNotBuffered(t *testing.T) {
	path := filepath.Join(t.TempDir(), "latest.log")
	if err := os.WriteFile(path, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	c := startTail(t, path, false)
	time.Sleep(50 * time.Millisecond)

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString(strings.Repeat("x", MaxLine+1000))
	_ = f.Close()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		for _, l := range c.all() {
			if len(l) == MaxLine {
				return // delivered, capped, and not held forever
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("a line with no newline was never delivered or never capped")
}

// Windows line endings survive a docker log round trip in places. A trailing
// carriage return would end up inside a player's name.
func TestCarriageReturnsAreStripped(t *testing.T) {
	path := filepath.Join(t.TempDir(), "latest.log")
	if err := os.WriteFile(path, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	c := startTail(t, path, false)
	time.Sleep(50 * time.Millisecond)

	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	_, _ = f.WriteString("kon joined the game\r\n")
	_ = f.Close()

	c.waitFor(t, "kon joined the game")
}

func TestMissingFileIsAnError(t *testing.T) {
	tl := &Tailer{Path: filepath.Join(t.TempDir(), "nope.log")}
	if err := tl.Follow(context.Background(), func(string) {}); err == nil {
		t.Error("followed a file that does not exist")
	}
}

func TestFromStartReplays(t *testing.T) {
	path := filepath.Join(t.TempDir(), "latest.log")
	if err := os.WriteFile(path, []byte("historic\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := startTail(t, path, true)
	c.waitFor(t, "historic")
}

// Cancelling stops it, which is what makes a clean shutdown possible.
func TestContextCancelStops(t *testing.T) {
	path := filepath.Join(t.TempDir(), "latest.log")
	if err := os.WriteFile(path, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	tl := &Tailer{Path: path, Poll: 10 * time.Millisecond}
	go func() { done <- tl.Follow(ctx, func(string) {}) }()

	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("err = %v, want a clean stop", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Follow did not stop when cancelled")
	}
}
