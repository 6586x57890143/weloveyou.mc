package rcon

import (
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

// A stand-in Minecraft server on loopback.
//
// A real listener rather than net.Pipe, and that is not incidental: Exec sends
// the command and its end-of-reply sentinel back to back, which deadlocks
// instantly against a synchronous pipe because the server cannot write its
// answer until the client reads, and the client will not read until it has
// finished writing. TCP has buffers and Minecraft is TCP, so the fake has to be
// too or the test is exercising a protocol nobody speaks. Loopback only; no test
// touches the network.
func fakeServer(t *testing.T, password string, reply func(cmd string) string) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go serveOne(conn, password, reply)
		}
	}()
	return ln.Addr().String()
}

func serveOne(conn net.Conn, password string, reply func(cmd string) string) {
	defer func() { _ = conn.Close() }()
	authed := false
	for {
		id, typ, body, err := readFrame(conn)
		if err != nil {
			return
		}
		switch typ {
		case typeAuth:
			if string(body) != password {
				// The only signal a wrong password gives: the id comes back as
				// -1 and the connection stays open.
				_ = writeFrame(conn, authFailedID, typeCommand, "")
				continue
			}
			authed = true
			_ = writeFrame(conn, id, typeCommand, "")
		case typeCommand:
			if !authed {
				return
			}
			out := ""
			if reply != nil && string(body) != "" {
				out = reply(string(body))
			}
			_ = writeFrame(conn, id, typeResponse, out)
		}
	}
}

func readFrame(c net.Conn) (id, typ int32, body []byte, err error) {
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	var size int32
	if err := binary.Read(c, binary.LittleEndian, &size); err != nil {
		return 0, 0, nil, err
	}
	buf := make([]byte, size)
	if _, err := io.ReadFull(c, buf); err != nil {
		return 0, 0, nil, err
	}
	id = int32(binary.LittleEndian.Uint32(buf[0:4]))
	typ = int32(binary.LittleEndian.Uint32(buf[4:8]))
	return id, typ, []byte(strings.TrimRight(string(buf[8:]), "\x00")), nil
}

func writeFrame(c net.Conn, id, typ int32, body string) error {
	_ = c.SetWriteDeadline(time.Now().Add(2 * time.Second))
	out := make([]byte, 0, len(body)+14)
	out = binary.LittleEndian.AppendUint32(out, uint32(len(body)+10))
	out = binary.LittleEndian.AppendUint32(out, uint32(id))
	out = binary.LittleEndian.AppendUint32(out, uint32(typ))
	out = append(out, body...)
	out = append(out, 0, 0)
	_, err := c.Write(out)
	return err
}

func dialFake(t *testing.T, reply func(string) string) *Conn {
	t.Helper()
	c, err := Dial(fakeServer(t, "secret", reply), "secret", 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// A wrong password does not close the connection and returns no error text. A
// client that does not check for the -1 id reads a failed login as a successful
// one and then silently does nothing forever.
func TestWrongPasswordIsAnError(t *testing.T) {
	_, err := Dial(fakeServer(t, "secret", nil), "wrong", 2*time.Second)
	if !errors.Is(err, ErrAuth) {
		t.Fatalf("err = %v, want ErrAuth", err)
	}
	// And it must not carry the password, because errors reach Discord.
	if err != nil && strings.Contains(err.Error(), "wrong") {
		t.Errorf("the error quotes the password: %v", err)
	}
}

// These are the formats read off the live server on 2026-08-25, trailing space
// and all. The empty list is the one that bites: splitting on "," without
// trimming reports one player named "".
func TestPlayersMatchesTheLiveFormat(t *testing.T) {
	empty := dialFake(t, func(string) string {
		return "There are 0 of a max of 20 players online: "
	})
	o, err := Players(empty)
	if err != nil {
		t.Fatal(err)
	}
	if o.Count != 0 || o.Max != 20 || len(o.Players) != 0 {
		t.Errorf("empty server = %+v, want nobody and no phantom name", o)
	}

	busy := dialFake(t, func(string) string {
		return "There are 3 of a max of 20 players online: kon, ellis, m"
	})
	o, err = Players(busy)
	if err != nil {
		t.Fatal(err)
	}
	if o.Count != 3 || len(o.Players) != 3 || o.Players[2] != "m" {
		t.Errorf("busy server = %+v", o)
	}
}

func TestWorldDay(t *testing.T) {
	c := dialFake(t, func(string) string { return "The time is 484" })
	day, err := WorldDay(c)
	if err != nil || day != 484 {
		t.Errorf("day = %d, %v", day, err)
	}
}

func TestWorldDayRejectsNonsense(t *testing.T) {
	c := dialFake(t, func(string) string { return "Unknown or incomplete command" })
	if _, err := WorldDay(c); err == nil {
		t.Error("a refused command was read as a day number")
	}
}

func TestPosition(t *testing.T) {
	c := dialFake(t, func(string) string {
		return "kon has the following entity data: [214.5d, 64.0d, -88.3d]"
	})
	x, z, ok := Position(c, "kon")
	if !ok || x != 214 || z != -88 {
		t.Errorf("position = %d,%d,%v want 214,-88,true", x, z, ok)
	}

	// A dead or absent player is not an error worth losing a feed post over.
	gone := dialFake(t, func(string) string {
		return "No entity was found"
	})
	if _, _, ok := Position(gone, "ghost"); ok {
		t.Error("invented a position for a player who is not there")
	}
}

// A long reply arrives as several packets. Minecraft fills one to 4096 bytes
// before starting another, so a short packet is the end. A client that reads a
// single packet gets the first 4096 bytes with no hint it was cut.
func TestLongReplyIsReassembled(t *testing.T) {
	long := strings.Repeat("x", bodySplit) + strings.Repeat("y", 900)
	addr := listenOnce(t, func(conn net.Conn) {
		id, _, _, err := readFrame(conn) // auth
		if err != nil {
			return
		}
		_ = writeFrame(conn, id, typeCommand, "")
		cmdID, _, _, err := readFrame(conn)
		if err != nil {
			return
		}
		_ = writeFrame(conn, cmdID, typeResponse, long[:bodySplit])
		_ = writeFrame(conn, cmdID, typeResponse, long[bodySplit:])
	})

	c, err := Dial(addr, "secret", 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	got, err := c.Exec("spark profiler")
	if err != nil {
		t.Fatal(err)
	}
	if got != long {
		t.Errorf("got %d bytes, want %d: a multi-packet reply was truncated",
			len(got), len(long))
	}
}

// A reply landing exactly on the split boundary has no short packet to end it.
// Waiting the full timeout for a continuation that never comes would stall the
// status board for five seconds per query, so the short wait ends it instead.
func TestReplyExactlyOnTheBoundaryDoesNotHang(t *testing.T) {
	exact := strings.Repeat("x", bodySplit)
	addr := listenOnce(t, func(conn net.Conn) {
		id, _, _, err := readFrame(conn)
		if err != nil {
			return
		}
		_ = writeFrame(conn, id, typeCommand, "")
		cmdID, _, _, err := readFrame(conn)
		if err != nil {
			return
		}
		_ = writeFrame(conn, cmdID, typeResponse, exact)
		select {} // say nothing more, ever
	})

	c, err := Dial(addr, "secret", 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })

	start := time.Now()
	got, err := c.Exec("list")
	if err != nil {
		t.Fatal(err)
	}
	if got != exact {
		t.Errorf("got %d bytes, want %d", len(got), len(exact))
	}
	if time.Since(start) > 3*time.Second {
		t.Errorf("took %v; it waited the full timeout rather than the short one",
			time.Since(start))
	}
}

// Minecraft CLOSES THE CONNECTION on an empty command, which is why Exec must
// not send one as an end-of-reply sentinel. Found against the live server:
// every query came back "read size: EOF" while dial and auth had both
// succeeded, so the board reported the server up with day 0 and 0ms.
func TestNoEmptyCommandIsEverSent(t *testing.T) {
	seen := make(chan string, 4)
	addr := listenOnce(t, func(conn net.Conn) {
		id, _, _, err := readFrame(conn)
		if err != nil {
			return
		}
		_ = writeFrame(conn, id, typeCommand, "")
		for {
			cmdID, _, body, err := readFrame(conn)
			if err != nil {
				return
			}
			seen <- string(body)
			_ = writeFrame(conn, cmdID, typeResponse, "The time is 484")
		}
	})

	c, err := Dial(addr, "secret", 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	if _, err := c.Exec("time query day"); err != nil {
		t.Fatal(err)
	}
	close(seen)
	for cmd := range seen {
		if cmd == "" {
			t.Fatal("sent an empty command; Minecraft drops the connection on one")
		}
	}
}

// A frame claiming an absurd size must be refused rather than allocated. wly
// runs under a 512m limit next to a Minecraft server.
func TestAbsurdFrameIsRefused(t *testing.T) {
	addr := listenOnce(t, func(conn net.Conn) {
		_, _, _, _ = readFrame(conn)
		out := make([]byte, 0, 4)
		out = binary.LittleEndian.AppendUint32(out, 1<<30)
		_, _ = conn.Write(out)
	})
	if _, err := Dial(addr, "secret", time.Second); err == nil ||
		!strings.Contains(err.Error(), "valid frame") {
		t.Errorf("err = %v, want a refusal to allocate a gigabyte", err)
	}
}

// listenOnce serves exactly one connection with a hand-written script, for the
// cases where the reply is not a simple command-to-string mapping.
func listenOnce(t *testing.T, handle func(net.Conn)) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		handle(conn)
	}()
	return ln.Addr().String()
}

// Anything arriving from Discord is attacker-controlled in the ordinary case of
// a stranger in a public channel. A newline ends a console command, so one
// embedded in a message would run a second command with operator rights.
func TestSanitiseChat(t *testing.T) {
	for _, tc := range []struct{ in, wantNot string }{
		{"hello\nsay I am the server", "\n"},
		{"hello\r\nkick everyone", "\r"},
		{"§4IMPOSTOR", "§"},
		{"say \"x\"", `"`},
		{`back\slash`, `\`},
	} {
		if got := SanitiseChat(tc.in); strings.Contains(got, tc.wantNot) {
			t.Errorf("SanitiseChat(%q) = %q, still contains %q", tc.in, got, tc.wantNot)
		}
	}
	// And it is length-capped, so one Discord message cannot flood game chat.
	if got := SanitiseChat(strings.Repeat("a", 1000)); len(got) > 256 {
		t.Errorf("length = %d, want it capped", len(got))
	}
	// Ordinary text survives intact, or the bridge is useless.
	if got := SanitiseChat("hey, anyone near spawn?"); got != "hey, anyone near spawn?" {
		t.Errorf("mangled ordinary text: %q", got)
	}
}
