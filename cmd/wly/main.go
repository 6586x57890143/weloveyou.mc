// Command wly is the server-side daemon: Discord bot, log-to-event bridge,
// RCON control, R2 sync and the JVM benchmark harness.
//
// It runs beside the Minecraft container and shares its data volume read-only.
package main

import (
	"fmt"
	"io"
	"os"

	"weloveyou-mc/internal/buildinfo"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "wly:", err)
		os.Exit(1)
	}
}

func run(args []string, out io.Writer) error {
	cmd := ""
	if len(args) > 0 {
		cmd = args[0]
	}
	switch cmd {
	case "version":
		fmt.Fprintln(out, buildinfo.String("wly"))
		return nil
	case "guild":
		return runGuild(args[1:], out)
	case "surfaces":
		return runSurfaces(args[1:], out)
	case "bench":
		return benchCmd(args[1:], out)
	case "serve":
		return runServe(args[1:], out)
	case "":
		return fmt.Errorf("no command given; try: version | serve | bench | guild | surfaces")
	default:
		return fmt.Errorf("unknown command %q; try: version | serve | bench | guild | surfaces", cmd)
	}
}
