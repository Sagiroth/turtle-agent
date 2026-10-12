package main

import (
	"context"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/pjerra/turtle-agent/internal/auth"
	"github.com/pjerra/turtle-agent/internal/capture"
	"github.com/pjerra/turtle-agent/internal/config"
	"github.com/pjerra/turtle-agent/internal/events"
	"github.com/pjerra/turtle-agent/internal/session"
	"github.com/pjerra/turtle-agent/internal/worldconn"
)

// Seams for tests: dial shows which key the login path hands to the world server,
// authLogin lets an end-to-end test skip realmd's SRP and pick the ticket.
var (
	dial      = worldconn.Dial
	authLogin = auth.Login
)

// withPrefix labels err with where it came from, unless it already says so (the auth
// and worldconn errors carry their own "realmd:" / "world:" prefix).
func withPrefix(prefix string, err error) string {
	if strings.HasPrefix(err.Error(), prefix) {
		return err.Error()
	}
	return prefix + " " + err.Error()
}

// checkCapture runs before the throttled realmd login so a bad --capture never costs an
// attempt: the file must not exist and the secrets known up front must be long enough.
// The key is 40 bytes and checked later by capture.Create.
func checkCapture(path, account, password string) error {
	if _, err := os.Lstat(path); err == nil {
		return fmt.Errorf("capture: %s already exists (captures are never overwritten)", path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("capture: %w", err)
	}
	if len(account) < 3 || len(password) < 3 {
		return fmt.Errorf("capture: %w", capture.ErrSecretTooShort)
	}
	return nil
}

// dialWorld keys the world connection with Ticket.KeyBytes(), the unpadded form the
// server hashes (39 bytes when the high byte of K is zero), never Ticket.Key[:].
func dialWorld(ctx context.Context, addr string, tk *auth.Ticket, tap worldconn.Tap) (*worldconn.Conn, error) {
	return dial(ctx, addr, tk.Account, tk.KeyBytes(), tap)
}

func loginCmd(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := fs.String("config", config.DefaultPath(), "settings file (realmd address, accounts)")
	capPath := fs.String("capture", "", "record decrypted world packets to this new file")
	stay := fs.Duration("stay", 0, "log out after this long in world (0: until interrupted or the logout RPC)")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if fs.NArg() != 2 {
		fmt.Fprintln(stderr, "usage: turtle-agent login [flags] <account> <character>")
		return exitUsage
	}
	account, character := fs.Arg(0), fs.Arg(1)
	st, err := config.Load(*cfgPath)
	if err != nil {
		fmt.Fprintln(stderr, "config:", err)
		return exitUsage
	}
	password, err := st.Password(account)
	if err != nil {
		fmt.Fprintf(stderr, "config: account %q: %v\n", account, err)
		return exitUsage
	}

	if *capPath != "" {
		if err := checkCapture(*capPath, account, password); err != nil {
			fmt.Fprintln(stderr, err)
			return exitUsage
		}
	}

	actx, cancel := context.WithTimeout(ctx, 30*time.Second)
	ticket, err := authLogin(actx, st.Realmd, account, password)
	cancel()
	if err != nil {
		fmt.Fprintln(stderr, withPrefix("realmd:", err))
		return exitAuth
	}
	// The account name is sensitive (ruling P10): the progress line does not repeat it.
	fmt.Fprintf(stderr, "realmd: authenticated, %d realm(s)\n", len(ticket.Realms))
	realm, err := ticket.Pick(st.Realm)
	if err != nil {
		fmt.Fprintln(stderr, withPrefix("realmd:", err))
		return exitRealm
	}
	addr := realm.Address
	if st.WorldAddr != "" {
		addr = st.WorldAddr
	}

	var tap worldconn.Tap
	if *capPath != "" {
		key := ticket.KeyBytes()
		hexKey := hex.EncodeToString(key)
		// Create expands each secret into its variants (raw, reversed, hex, case).
		w, err := capture.Create(*capPath, []byte(ticket.Account), []byte(password), []byte(strings.ToUpper(password)),
			ticket.Key[:], key, []byte(hexKey), []byte(strings.ToUpper(hexKey)))
		if err != nil {
			fmt.Fprintln(stderr, err)
			return exitUsage
		}
		defer func() {
			n, redacted := w.Counts()
			if err := w.Close(); err != nil {
				fmt.Fprintln(stderr, "capture:", err)
			}
			fmt.Fprintf(stderr, "capture: %d records, %d redacted, written to %s\n", n, redacted, *capPath)
		}()
		tap = w
	}

	wctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	conn, err := dialWorld(wctx, addr, ticket, tap)
	cancel()
	if err != nil {
		fmt.Fprintln(stderr, withPrefix("world:", err))
		return exitWorld
	}
	fmt.Fprintf(stderr, "world: authenticated on %s (%s)\n", realm.Name, addr)

	log := events.New(100000)
	s := session.New(conn, session.Options{Character: character, Log: log, Logger: stderr})
	runErr := make(chan error, 1)
	go func() { runErr <- s.Run(context.Background()) }()
	select {
	case <-s.Ready():
		info := s.Status()
		fmt.Fprintf(stderr, "in world: %s guid=%s map=%d x=%.1f y=%.1f z=%.1f\n", info.Character, info.GUID, info.Map, info.X, info.Y, info.Z)
	case err := <-runErr:
		// Ready never closes when login fails; Run's result says why.
		return sessionExit(err, stderr)
	case <-ctx.Done():
		conn.Close()
		<-runErr
		return exitWorld
	}

	var stayC <-chan time.Time
	if *stay > 0 {
		stayC = time.After(*stay)
	}
	select {
	case <-stayC:
		return logoutAndWait(s, runErr, stderr)
	case <-ctx.Done():
		return logoutAndWait(s, runErr, stderr)
	case err := <-runErr:
		return sessionExit(err, stderr)
	}
}

func logoutAndWait(s *session.Session, runErr <-chan error, stderr io.Writer) int {
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	if err := s.Logout(ctx); err != nil {
		fmt.Fprintln(stderr, "logout:", err)
	}
	select {
	case err := <-runErr:
		return sessionExit(err, stderr)
	case <-ctx.Done():
		fmt.Fprintln(stderr, "logout: timed out")
		return exitDisconnected
	}
}

func sessionExit(err error, stderr io.Writer) int {
	if err == nil {
		fmt.Fprintln(stderr, "logout: complete")
		return exitOK
	}
	fmt.Fprintln(stderr, "session:", err)
	if errors.Is(err, session.ErrDisconnected) {
		return exitDisconnected
	}
	return exitWorld
}
