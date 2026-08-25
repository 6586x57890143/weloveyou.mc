// Package logtail follows a file the way `tail -F` does: across truncation,
// across rotation, and without replaying what it already read.
//
// It is separate from internal/mcevents because that package promises to do no
// I/O beyond reading a handle it is given, and that promise is what keeps every
// log format testable against a fixed string. This is the part that touches the
// filesystem, so it lives on its own and mcevents stays pure.
//
// Written rather than pulled in because it is ninety lines of stdlib and the
// alternative is a dependency in the hot path of the one component that must
// keep running unattended for weeks.
package logtail

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"
)

// DefaultPoll is how often the file is checked when it has nothing new.
//
// 200ms because the feed is meant to feel live: a death showing up in Discord
// two seconds later reads as a bot, and a quarter of a second reads as the
// server talking. The cost is one read syscall on an idle file, which is
// nothing next to a Minecraft server.
const DefaultPoll = 200 * time.Millisecond

// MaxLine bounds a single line.
//
// A crash dump, a mod printing a whole NBT tree, or a hostile name can produce
// a line with no newline in sight, and an unbounded reader would hold all of it
// in a 512m container. Past this the line is truncated and delivered, because
// dropping it silently would lose the very event most worth seeing.
const MaxLine = 64 << 10

// Tailer follows one file.
type Tailer struct {
	Path string
	Poll time.Duration

	// FromStart replays the file from byte zero instead of following from the
	// end. Off by default and it must stay off for the live feed: the daemon
	// restarting would otherwise repost every death of the last week into
	// Discord, which is the kind of mistake that is only funny once.
	FromStart bool
}

// Follow calls fn for every complete line until ctx is cancelled.
//
// fn runs on this goroutine, so a slow handler slows the tail. That is
// deliberate back-pressure: the alternative is an unbounded queue that grows
// while Discord is rate limiting, and a feed that is behind is better than a
// daemon that is out of memory.
func (t *Tailer) Follow(ctx context.Context, fn func(line string)) error {
	poll := t.Poll
	if poll <= 0 {
		poll = DefaultPoll
	}

	f, err := t.open()
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	if !t.FromStart {
		if _, err := f.Seek(0, io.SeekEnd); err != nil {
			return err
		}
	}

	var pending []byte
	buf := make([]byte, 32<<10)

	for {
		n, readErr := f.Read(buf)
		if n > 0 {
			pending = append(pending, buf[:n]...)
			for {
				i := bytes.IndexByte(pending, '\n')
				if i < 0 {
					break
				}
				line := pending[:i]
				pending = pending[i+1:]
				fn(string(bytes.TrimRight(line, "\r")))
			}
			// A line with no newline in sight is truncated rather than
			// buffered forever. Delivering the first 64k of a crash dump beats
			// holding all of it and beats dropping it.
			if len(pending) > MaxLine {
				fn(string(pending[:MaxLine]))
				pending = pending[:0]
			}
			continue // there may be more waiting; do not sleep on a busy file
		}
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return fmt.Errorf("logtail: read %s: %w", t.Path, readErr)
		}

		// Nothing new. Wait, then check whether the file under us is still the
		// file we opened.
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(poll):
		}

		rotated, err := t.rotated(f)
		if err != nil || !rotated {
			continue // a stat failure is transient; keep following what we have
		}
		next, err := t.open()
		if err != nil {
			continue // the new file may not exist yet; try again next tick
		}
		_ = f.Close()
		f = next
		pending = pending[:0]
	}
}

func (t *Tailer) open() (*os.File, error) {
	f, err := os.Open(t.Path)
	if err != nil {
		return nil, fmt.Errorf("logtail: open %s: %w", t.Path, err)
	}
	return f, nil
}

// rotated reports whether the path now names a different file, or the same one
// that got shorter.
//
// Minecraft does BOTH of these. On restart it renames latest.log into
// logs/<date>-N.log.gz and creates a fresh latest.log, so the open handle is
// still valid and still points at a file nobody writes to any more: following it
// forever means the feed goes quiet after the first restart and nothing looks
// broken. Truncation in place is the other shape, and it is caught by comparing
// the size against where we are rather than by identity.
func (t *Tailer) rotated(f *os.File) (bool, error) {
	onDisk, err := os.Stat(t.Path)
	if err != nil {
		return false, err
	}
	open, err := f.Stat()
	if err != nil {
		return false, err
	}
	if !os.SameFile(onDisk, open) {
		return true, nil
	}
	pos, err := f.Seek(0, io.SeekCurrent)
	if err != nil {
		return false, err
	}
	return open.Size() < pos, nil
}
