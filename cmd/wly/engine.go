package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

// The Docker Engine API, read-only, for exactly one fact: when the Minecraft
// container started.
//
// Why it exists at all: the status board says "up <relative time>", and nothing
// else on the box can answer it. The log's own "Done (12.345s)!" line carries no
// DATE, only a wall clock, so anything derived from it is wrong across midnight,
// and the daemon usually starts long after the server did and never sees that
// line at all.
//
// WHY IT IS NOT THE HOST SOCKET. CLAUDE.md records the 2026-08-24 security
// review that removed `/var/run/docker.sock` from the compose file: the socket
// is host-root-equivalent, which makes the read_only, cap_drop: ALL and
// no-new-privileges beside it decorative, because anything reaching that API can
// start a privileged container and mount the host. The review said the code,
// when it landed, would point at tecnativa/docker-socket-proxy with
// CONTAINERS=1, POST=1.
//
// THIS GOES FURTHER: POST=0. Nothing here needs to create, start, stop or kill
// anything, so the proxy is configured to refuse all of it. Lifecycle control is
// a later phase and it can raise the ceiling then, deliberately, rather than
// inheriting a permission nothing asked for. The proxy denies by default, so the
// blast radius of a bug in the daemon parsing Discord input off the internet is
// "it can read a container list".
//
// DOCKER_HOST unset means this is simply unavailable, which is the normal case
// in a checkout. Uptime is then absent from the board rather than invented.

// engineClient talks to the Engine API over TCP or a unix socket.
type engineClient struct {
	host string
	http *http.Client
}

func newEngineClient() *engineClient {
	host := os.Getenv("DOCKER_HOST")
	if host == "" {
		return nil
	}
	c := &engineClient{host: host, http: &http.Client{Timeout: 5 * time.Second}}
	if strings.HasPrefix(host, "unix://") {
		path := strings.TrimPrefix(host, "unix://")
		c.http.Transport = &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", path)
			},
		}
		c.host = "http://docker"
	} else {
		c.host = "http://" + strings.TrimPrefix(strings.TrimPrefix(host, "tcp://"), "http://")
	}
	return c
}

// containerStarted reads State.StartedAt for the Minecraft container.
func (d *daemon) containerStarted() (time.Time, error) {
	c := newEngineClient()
	if c == nil {
		return time.Time{}, fmt.Errorf("DOCKER_HOST is not set, so container uptime " +
			"is unavailable and the board shows none rather than guessing")
	}
	name := d.cfg.Server.Container
	if name == "" {
		name = "wly-mc"
	}

	resp, err := c.http.Get(c.host + "/containers/" + name + "/json")
	if err != nil {
		return time.Time{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	// Bounded, for the same reason the Discord client bounds its reads: an
	// unbounded one lets a broken or hostile responder balloon a 512m container.
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return time.Time{}, fmt.Errorf("docker inspect %s: %s", name, resp.Status)
	}
	var doc struct {
		State struct {
			StartedAt time.Time `json:"StartedAt"`
			Running   bool      `json:"Running"`
		} `json:"State"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return time.Time{}, err
	}
	if !doc.State.Running {
		return time.Time{}, fmt.Errorf("container %s is not running", name)
	}
	return doc.State.StartedAt, nil
}
