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
	case "", "serve", "bench":
		return fmt.Errorf("%q is not implemented yet (phase 0 skeleton)", cmd)
	default:
		return fmt.Errorf("unknown command %q; try: version | serve | bench", cmd)
	}
}
