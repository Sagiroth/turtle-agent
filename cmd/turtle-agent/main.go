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
	// exitLogoutRefused: the server still refused the logout (in combat) when the retries
	// ran out; the agent closed the socket and the character may stay in world.
	exitLogoutRefused = 8
	// exitFleet (fleet.go) is 9: a fleet child failed or the fleet never got into world.
)

func main() {
	code := run(signalContext(), os.Args[1:], os.Stdout, os.Stderr)
	os.Exit(code)
}

// signalContext ends on the first Ctrl-C or SIGTERM and then hands both signals back to
// the default action, so a second Ctrl-C ends the process at once, even while a logout
// is being retried (review I1). The handler is stopped before the context ends.
func signalContext() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		signal.Stop(c)
		cancel()
	}()
	return ctx
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return exitUsage
	}
	switch args[0] {
	case "login":
		return loginCmd(ctx, args[1:], stdout, stderr)
	case "fleet":
		return fleetCmd(ctx, args[1:], stdout, stderr)
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
  turtle-agent login [--config FILE] [--capture FILE] [--stay DURATION] [--rpc ADDR] <account> <character>
  turtle-agent fleet --config FILE [--settings FILE] [--stagger DURATION] [--wait DURATION]
  turtle-agent scrub-check [--config FILE] <capture>...
  turtle-agent capture-stats <capture>
  turtle-agent version`)
}
