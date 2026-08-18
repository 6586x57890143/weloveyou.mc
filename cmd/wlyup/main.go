// Command wlyup is the player-side pack updater: it reads the published
// packwiz index for a channel and brings a local install in line with it.
//
// Single static binary — no Java, no launcher plugin.
package main

import (
	"fmt"
	"io"
	"os"

	"weloveyou-mc/internal/buildinfo"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "wlyup:", err)
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
		fmt.Fprintln(out, buildinfo.String("wlyup"))
		return nil
	case "":
		return fmt.Errorf("syncing is not implemented yet (phase 0 skeleton)")
	default:
		return fmt.Errorf("unknown command %q; try: version", cmd)
	}
}
