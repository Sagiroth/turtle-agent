// Command turtle-agent is a headless WoW 1.12 / Tortoise 1.18.1 client for bot tests.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
)

const version = "0.1.0"

// Exit codes; the Python wrapper and shell scripts branch on them.
const (
	exitOK           = 0
	exitUsage        = 2 // bad flags, arguments or settings file
	exitAuth         = 3 // realmd rejected the login
	exitRealm        = 4 // realm missing or offline
	exitWorld        = 5 // world server rejected the session or the character
	exitDisconnected = 6 // the server closed the connection
	exitScrub        = 7 // scrub-check found a secret in a capture
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return exitUsage
	}
	switch args[0] {
	case "login":
		return loginCmd(ctx, args[1:], stdout, stderr)
	case "scrub-check":
		return scrubCmd(args[1:], stdout, stderr)
	case "capture-stats":
		return statsCmd(args[1:], stdout, stderr)
	case "version":
		fmt.Fprintln(stdout, "turtle-agent", version)
		return exitOK
	default:
		usage(stderr)
		return exitUsage
	}
}

func usage(w io.Writer) {
	fmt.Fprintln(w, `usage:
  turtle-agent login [--config FILE] [--capture FILE] [--stay DURATION] <account> <character>
  turtle-agent scrub-check [--config FILE] <capture>...
  turtle-agent capture-stats <capture>
  turtle-agent version`)
}
