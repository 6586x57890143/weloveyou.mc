// Package rcon speaks Valve's Source RCON protocol, which is what Minecraft
// listens with.
//
// Written rather than pulled in for the same reason wly speaks the Docker Engine
// API with stdlib net/http: the protocol is four little-endian fields and a
// null-terminated string, and a dependency for that is a dependency to keep
// patched forever. It is about a hundred lines and it is exercised against a
// fake server in tests.
//
// RCON IS AN ADMIN CHANNEL WITH A SHARED PASSWORD AND NO TRANSPORT SECURITY.
// Every byte, the password included, crosses the wire in clear. That is
// tolerable here only because it never leaves the compose network: mc publishes
// 25575 on 127.0.0.1 alone and wly reaches it as `mc:25575`. Nothing in this
// package should ever be pointed at an address that is not on that network, and
// the password is never written to a log or an error, because an error string
// ends up in Discord.
package rcon

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"
)

// Packet types, from the protocol.
const (
	typeResponse = 0 // SERVERDATA_RESPONSE_VALUE
	typeCommand  = 2 // SERVERDATA_EXECCOMMAND, and the auth response reuses it
	typeAuth     = 3 // SERVERDATA_AUTH
)

// A single packet's body is capped at 4096 bytes by the protocol, and the
// header is 10 bytes on top of the length field. Anything claiming more than
// this is either a broken server or something that is not one, and reading it
// would let a hostile listener balloon a 512m container.
const maxPacketSize = 4096 + 16

// authFailedID is what a server returns instead of the request id when the
// password is wrong. It is the ONLY signal: the connection is not closed and no
// error text arrives, so a client that does not check this reads a failed login
// as a successful one and then silently does nothing forever.
const authFailedID = -1

// ErrAuth is a wrong password. It is deliberately a sentinel with no detail:
// the password must never reach a log line or a Discord message.
var ErrAuth = errors.New("rcon: authentication failed")

// Conn is an authenticated RCON connection.
//
// One command at a time, guarded by a mutex. RCON multiplexes on a request id,
// but Minecraft answers in order on one socket and the daemon has exactly one
// caller loop, so a lock is the honest amount of machinery. Two goroutines
// interleaving reads on this socket would produce answers attributed to the
// wrong question, which on a status board is worse than an error.
type Conn struct {
	mu   sync.Mutex
	conn net.Conn
	r    *bufio.Reader
	id   int32

	// Timeout bounds every read and write. A Minecraft server pausing for a
	// long GC must time out rather than wedge the daemon that reports on it.
	Timeout time.Duration
}

// Dial connects and authenticates.
func Dial(addr, password string, timeout time.Duration) (*Conn, error) {
	c, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return nil, fmt.Errorf("rcon: dial %s: %w", addr, err)
	}
	conn := &Conn{conn: c, r: bufio.NewReader(c), Timeout: timeout}
	if err := conn.auth(password); err != nil {
		_ = c.Close()
		return nil, err
	}
	return conn, nil
}

// New wraps an already-open connection and authenticates on it. Tests use this
// with a net.Pipe; nothing else should need it.
func New(c net.Conn, password string, timeout time.Duration) (*Conn, error) {
	conn := &Conn{conn: c, r: bufio.NewReader(c), Timeout: timeout}
	if err := conn.auth(password); err != nil {
		return nil, err
	}
	return conn, nil
}

func (c *Conn) auth(password string) error {
	id, err := c.send(typeAuth, password)
	if err != nil {
		return err
	}
	// Some servers send an empty RESPONSE_VALUE before the auth result. Read
	// until the auth reply arrives rather than assuming which came first.
	for {
		got, typ, _, err := c.readPacket(c.Timeout)
		if err != nil {
			return err
		}
		if typ == typeResponse {
			continue
		}
		if got == authFailedID {
			return ErrAuth
		}
		if got != id {
			return fmt.Errorf("rcon: auth reply id %d, expected %d", got, id)
		}
		return nil
	}
}

// bodySplit is where Minecraft splits a long reply. A packet holding exactly
// this much is a strong hint that more is coming; anything shorter is the end.
const bodySplit = 4096

// continuationWait is how long to wait for a follow-up packet once a full-sized
// one has arrived. Short, because the server has already composed the whole
// answer and is writing it back to back.
const continuationWait = 400 * time.Millisecond

// Exec runs one command and returns everything the server said.
//
// A long reply arrives as SEVERAL packets with no count and no terminator
// anywhere, so the end has to be inferred.
//
// THE USUAL TRICK DOES NOT WORK HERE, and it cost a live debugging session to
// find out. Most RCON clients send a second, empty command straight after the
// real one and treat its reply as the end marker. Minecraft CLOSES THE
// CONNECTION on an empty command: probed against the live server on 2026-08-25,
// every `list` came back `read size: EOF` and the socket was gone. The status
// board showed day 0 and 0ms because of exactly this, while still reporting the
// server as up, since the dial and the auth had both succeeded.
//
// So the end is inferred from the split instead: Minecraft fills a packet to
// 4096 bytes before starting another, so a short packet is the last one. A reply
// that lands exactly on the boundary would otherwise hang until the full
// timeout, so continuation reads get a short deadline and a timeout there means
// "that was all of it" rather than an error.
func (c *Conn) Exec(cmd string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	id, err := c.send(typeCommand, cmd)
	if err != nil {
		return "", err
	}

	var b strings.Builder
	for first := true; ; first = false {
		wait := c.Timeout
		if !first {
			wait = continuationWait
		}
		got, _, body, err := c.readPacket(wait)
		if err != nil {
			if !first && isTimeout(err) {
				return b.String(), nil // the boundary case: that was all of it
			}
			return "", err
		}
		if got != id {
			return "", fmt.Errorf("rcon: reply id %d, expected %d", got, id)
		}
		b.Write(body)
		if len(body) < bodySplit {
			return b.String(), nil
		}
	}
}

func isTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

// Close ends the session.
func (c *Conn) Close() error { return c.conn.Close() }

func (c *Conn) send(typ int32, body string) (int32, error) {
	c.id++
	if c.id <= 0 { // never reuse -1, which means "auth failed" on the way back
		c.id = 1
	}
	id := c.id

	// size counts everything after the size field: id, type, body, two nulls.
	payload := make([]byte, 0, len(body)+14)
	payload = binary.LittleEndian.AppendUint32(payload, uint32(len(body)+10))
	payload = binary.LittleEndian.AppendUint32(payload, uint32(id))
	payload = binary.LittleEndian.AppendUint32(payload, uint32(typ))
	payload = append(payload, body...)
	payload = append(payload, 0, 0)

	if err := c.conn.SetWriteDeadline(time.Now().Add(c.Timeout)); err != nil {
		return 0, err
	}
	if _, err := c.conn.Write(payload); err != nil {
		return 0, fmt.Errorf("rcon: write: %w", err)
	}
	return id, nil
}

func (c *Conn) readPacket(wait time.Duration) (id, typ int32, body []byte, err error) {
	if err := c.conn.SetReadDeadline(time.Now().Add(wait)); err != nil {
		return 0, 0, nil, err
	}
	var size int32
	if err := binary.Read(c.r, binary.LittleEndian, &size); err != nil {
		return 0, 0, nil, fmt.Errorf("rcon: read size: %w", err)
	}
	// Bounded on both ends. A size below the 10-byte header is malformed, and
	// one above the protocol maximum is either a broken server or not a server,
	// and allocating what it asks for is how a hostile listener exhausts memory.
	if size < 10 || size > maxPacketSize {
		return 0, 0, nil, fmt.Errorf("rcon: packet claims %d bytes, which is not a "+
			"valid frame; refusing to read it", size)
	}
	buf := make([]byte, size)
	if _, err := io.ReadFull(c.r, buf); err != nil {
		return 0, 0, nil, fmt.Errorf("rcon: read body: %w", err)
	}
	id = int32(binary.LittleEndian.Uint32(buf[0:4]))
	typ = int32(binary.LittleEndian.Uint32(buf[4:8]))
	// The body is null-terminated and the packet ends with a second null.
	body = bytes.TrimRight(buf[8:], "\x00")
	return id, typ, body, nil
}
