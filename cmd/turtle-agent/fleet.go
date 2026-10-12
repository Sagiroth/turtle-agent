package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"text/tabwriter"
	"time"

	"github.com/pjerra/turtle-agent/internal/config"
	"github.com/pjerra/turtle-agent/internal/netutil"
	"github.com/pjerra/turtle-agent/internal/session"
)

// exitFleet: a child failed or the fleet did not reach the world in time.
const exitFleet = 9

// fleetMember is one entry of the fleet file. The account is a key into the settings
// file, which holds the password; the fleet file never carries a secret.
type fleetMember struct {
	Account   string `json:"account"`
	Character string `json:"character"`
	RPC       string `json:"rpc"`
}

type fleetFile struct {
	Members []fleetMember `json:"members"`
}

// loadFleet reads and validates the fleet file: every entry needs an account, a
// character and a loopback RPC address with a fixed port; characters
// and RPC ports must be distinct. If settings is non-nil each account must exist
// in it (the error does not name the account).
func loadFleet(path string, settings *config.Settings) ([]fleetMember, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("fleet %s: %w", path, err)
	}
	var f fleetFile
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		detail := ""
		if msg := err.Error(); strings.HasPrefix(msg, "json: unknown field ") {
			detail = ": " + strings.TrimPrefix(msg, "json: ")
		}
		return nil, fmt.Errorf("fleet %s is not valid fleet JSON%s", path, detail)
	}
	if len(f.Members) == 0 {
		return nil, errors.New(`fleet: "members" must list at least one entry`)
	}
	chars := map[string]bool{}
	ports := map[int]bool{}
	for i := range f.Members {
		m := &f.Members[i]
		n := i + 1
		if m.Account == "" || m.Character == "" || m.RPC == "" {
			return nil, fmt.Errorf("fleet: member %d needs account, character and rpc", n)
		}
		host, port, err := netutil.CheckLoopbackAddr(m.RPC)
		if err != nil {
			return nil, fmt.Errorf("fleet: member %d rpc %w", n, err)
		}
		if port == 0 {
			return nil, fmt.Errorf("fleet: member %d rpc needs a fixed port (the supervisor polls it)", n)
		}
		// Ports must differ outright: 127.0.0.1:N and ::1:N are different sockets, but a
		// fleet that mixes them is almost surely a typo.
		if ports[port] {
			return nil, fmt.Errorf("fleet: member %d rpc port %d is used twice", n, port)
		}
		ports[port] = true
		m.RPC = net.JoinHostPort(host, strconv.Itoa(port))
		key := strings.ToLower(m.Character)
		if chars[key] {
			return nil, fmt.Errorf("fleet: character %q is listed twice", m.Character)
		}
		chars[key] = true
		if settings != nil {
			if _, err := settings.Password(m.Account); err != nil {
				return nil, fmt.Errorf("fleet: member %d (%s): %w", n, m.Character, err)
			}
		}
	}
	return f.Members, nil
}

// fleetChild is a running login process (or a fake in tests).
type fleetChild interface {
	// Stop asks the child to log out cleanly (an interrupt).
	Stop()
	// Kill ends it at once.
	Kill()
	// Done closes when the child has exited; Code is valid after that.
	Done() <-chan struct{}
	Code() int
}

type fleetDeps struct {
	start func(m fleetMember, stderr io.Writer) (fleetChild, error)
	// status returns the session state ("in_world", ...) of the agent on addr.
	status func(ctx context.Context, addr string) (string, error)
}

type fleetOpts struct {
	stagger, poll, wait, stopGrace time.Duration
}

func fleetCmd(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("fleet", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fleetPath := fs.String("config", "", "fleet file: {\"members\":[{\"account\",\"character\",\"rpc\"}]} (required)")
	settingsPath := fs.String("settings", config.DefaultPath(), "settings file (realmd address, accounts)")
	stagger := fs.Duration("stagger", 2*time.Second, "pause between starting children (realmd throttles logins)")
	wait := fs.Duration("wait", 2*time.Minute, "give up if not every character is in world after this long")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if *fleetPath == "" || fs.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: turtle-agent fleet --config FILE [--settings FILE] [--stagger D] [--wait D]")
		return exitUsage
	}
	st, err := config.Load(*settingsPath)
	if err != nil {
		fmt.Fprintln(stderr, "config:", err)
		return exitUsage
	}
	members, err := loadFleet(*fleetPath, &st)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitUsage
	}
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintln(stderr, "fleet:", err)
		return exitUsage
	}
	deps := fleetDeps{
		start: func(m fleetMember, w io.Writer) (fleetChild, error) {
			return startProcess(exe, *settingsPath, m, w)
		},
		status: rpcStatus,
	}
	return runFleet(ctx, members, deps, fleetOpts{stagger: *stagger, poll: 500 * time.Millisecond, wait: *wait, stopGrace: 60 * time.Second}, stdout, stderr)
}

// runFleet starts every member, waits until all are in world, prints the table, then
// supervises until ctx ends (Ctrl-C: every child is stopped and logs out) or a child
// fails (the rest are stopped too). It returns 0 only if every child exited 0.
func runFleet(ctx context.Context, members []fleetMember, deps fleetDeps, o fleetOpts, stdout, stderr io.Writer) int {
	var mu sync.Mutex // serialises writes to stderr from the children's prefixers
	children := make([]fleetChild, len(members))
	states := make([]string, len(members))
	for i := range states {
		states[i] = "starting"
	}

	stopAll := func() {
		for _, c := range children {
			if c != nil {
				c.Stop()
			}
		}
	}
	// finish stops every child, waits for them (killing after stopGrace) and returns the exit code.
	finish := func(failed bool) int {
		stopAll()
		timer := time.NewTimer(o.stopGrace)
		defer timer.Stop()
		for i, c := range children {
			if c == nil {
				continue
			}
			select {
			case <-c.Done():
			case <-timer.C:
				fmt.Fprintf(stderr, "fleet: %s did not log out in %s; killing it\n", members[i].Character, o.stopGrace)
				c.Kill()
				<-c.Done()
				failed = true
			}
		}
		for i, c := range children {
			if c != nil && c.Code() != exitOK {
				fmt.Fprintf(stderr, "fleet: %s exited with code %d\n", members[i].Character, c.Code())
				failed = true
			}
		}
		if failed {
			return exitFleet
		}
		return exitOK
	}

	for i, m := range members {
		if i > 0 && o.stagger > 0 {
			select {
			case <-time.After(o.stagger):
			case <-ctx.Done():
			}
		}
		if ctx.Err() != nil {
			return finish(false)
		}
		c, err := deps.start(m, &prefixWriter{w: stderr, mu: &mu, prefix: "[" + m.Character + "] "})
		if err != nil {
			fmt.Fprintf(stderr, "fleet: starting %s: %v\n", m.Character, err)
			return finish(true)
		}
		children[i] = c
	}

	// anyExit reports the first child that has exited and whether it failed.
	anyExit := func() (int, bool) {
		for i, c := range children {
			select {
			case <-c.Done():
				return i, c.Code() != exitOK
			default:
			}
		}
		return -1, false
	}

	deadline := time.NewTimer(o.wait)
	defer deadline.Stop()
	tick := time.NewTicker(o.poll)
	defer tick.Stop()
	for ready := false; !ready; {
		ready = true
		for i, m := range members {
			select {
			case <-children[i].Done():
				states[i] = "exited"
			default:
				s, err := deps.status(ctx, m.RPC)
				if err != nil {
					s = "starting"
				}
				states[i] = s
			}
			if states[i] != string(session.StatusInWorld) {
				ready = false
			}
		}
		if i, failed := anyExit(); i >= 0 {
			printFleetTable(stdout, members, states)
			if failed {
				fmt.Fprintf(stderr, "fleet: %s failed before the fleet was in world\n", members[i].Character)
			} else {
				fmt.Fprintf(stderr, "fleet: %s logged out before the fleet was in world\n", members[i].Character)
			}
			return finish(true)
		}
		if ready {
			break
		}
		select {
		case <-tick.C:
		case <-deadline.C:
			printFleetTable(stdout, members, states)
			fmt.Fprintf(stderr, "fleet: not every character was in world after %s\n", o.wait)
			return finish(true)
		case <-ctx.Done():
			return finish(false)
		}
	}
	printFleetTable(stdout, members, states)
	fmt.Fprintf(stderr, "fleet: %d character(s) in world; Ctrl-C logs them all out\n", len(members))

	// Supervise: fail fast when a child dies, otherwise run until Ctrl-C or until every
	// child has logged out through its own RPC.
	exited := make(chan struct{}, len(children))
	for _, c := range children {
		go func(c fleetChild) { <-c.Done(); exited <- struct{}{} }(c)
	}
	for left := len(children); left > 0; left-- {
		select {
		case <-ctx.Done():
			fmt.Fprintln(stderr, "fleet: interrupt, logging every character out")
			return finish(false)
		case <-exited:
			if i, failed := anyExit(); failed {
				fmt.Fprintf(stderr, "fleet: %s failed; stopping the rest\n", members[i].Character)
				return finish(true)
			}
		}
	}
	return finish(false)
}

func printFleetTable(w io.Writer, members []fleetMember, states []string) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "CHARACTER\tRPC\tSTATE")
	for i, m := range members {
		fmt.Fprintf(tw, "%s\t%s\t%s\n", m.Character, m.RPC, states[i])
	}
	tw.Flush()
}

// prefixWriter labels each line of a child's stderr with its character.
type prefixWriter struct {
	w      io.Writer
	mu     *sync.Mutex
	prefix string
	buf    []byte
}

func (p *prefixWriter) Write(b []byte) (int, error) {
	p.buf = append(p.buf, b...)
	for {
		i := bytes.IndexByte(p.buf, '\n')
		if i < 0 {
			break
		}
		p.mu.Lock()
		fmt.Fprintf(p.w, "%s%s\n", p.prefix, p.buf[:i])
		p.mu.Unlock()
		p.buf = p.buf[i+1:]
	}
	return len(b), nil
}

// procChild is a real `turtle-agent login` process. The password is never passed on its
// command line or environment: the child reads it from the settings file by account key.
type procChild struct {
	cmd  *exec.Cmd
	done chan struct{}
	code int
}

func startProcess(exe, settings string, m fleetMember, stderr io.Writer) (fleetChild, error) {
	cmd := exec.Command(exe, "login", "--config", settings, "--rpc", m.RPC, m.Account, m.Character)
	// Own process group (unix): a terminal Ctrl-C reaches only the supervisor, which
	// forwards exactly one interrupt; a second one would end the child's logout retry.
	detachProcessGroup(cmd)
	pr, pw := io.Pipe()
	cmd.Stderr = pw
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	c := &procChild{cmd: cmd, done: make(chan struct{})}
	copied := make(chan struct{})
	go func() {
		sc := bufio.NewScanner(pr)
		sc.Buffer(make([]byte, 64*1024), 1<<20)
		for sc.Scan() {
			fmt.Fprintln(stderr, sc.Text())
		}
		close(copied)
	}()
	go func() {
		err := cmd.Wait()
		pw.Close()
		<-copied
		var ee *exec.ExitError
		switch {
		case err == nil:
		case errors.As(err, &ee):
			c.code = ee.ExitCode()
			if c.code < 0 {
				c.code = 1 // killed by a signal
			}
		default:
			c.code = 1
		}
		close(c.done)
	}()
	return c, nil
}

func (c *procChild) Stop()                 { _ = c.cmd.Process.Signal(os.Interrupt) }
func (c *procChild) Kill()                 { _ = c.cmd.Process.Kill() }
func (c *procChild) Done() <-chan struct{} { return c.done }
func (c *procChild) Code() int             { return c.code }

// rpcStatus calls the agent's JSON-RPC status method and returns the session state.
func rpcStatus(ctx context.Context, addr string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+addr+"/rpc",
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"status"}`))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var out struct {
		Result struct {
			State string `json:"state"`
		} `json:"result"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil {
		return "", err
	}
	if out.Error != nil {
		return "", errors.New("rpc error")
	}
	return out.Result.State, nil
}
