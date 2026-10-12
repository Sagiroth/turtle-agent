package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pjerra/turtle-agent/internal/config"
)

type fakeChild struct {
	mu      sync.Mutex
	done    chan struct{}
	code    int
	stopped bool
	killed  bool
	closed  bool
	onStop  int // exit code used when Stop is called; -1: ignore Stop
}

func newFake() *fakeChild { return &fakeChild{done: make(chan struct{})} }

func (f *fakeChild) exit(code int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.closed {
		f.code = code
		f.closed = true
		close(f.done)
	}
}
func (f *fakeChild) Stop() {
	f.mu.Lock()
	f.stopped = true
	ignore := f.onStop < 0
	f.mu.Unlock()
	if !ignore {
		f.exit(f.onStop)
	}
}
func (f *fakeChild) Kill()                 { f.mu.Lock(); f.killed = true; f.mu.Unlock(); f.exit(1) }
func (f *fakeChild) Done() <-chan struct{} { return f.done }
func (f *fakeChild) Code() int             { f.mu.Lock(); defer f.mu.Unlock(); return f.code }

type fakeFleet struct {
	mu       sync.Mutex
	children map[string]*fakeChild // by rpc address
	states   map[string]string
	started  []string
}

func newFakeFleet() *fakeFleet {
	return &fakeFleet{children: map[string]*fakeChild{}, states: map[string]string{}}
}

func (ff *fakeFleet) deps() fleetDeps {
	return fleetDeps{
		start: func(m fleetMember, w io.Writer) (fleetChild, error) {
			ff.mu.Lock()
			defer ff.mu.Unlock()
			c := newFake()
			ff.children[m.RPC] = c
			ff.started = append(ff.started, m.Character)
			io.WriteString(w, "world: authenticated\npartial")
			return c, nil
		},
		status: func(_ context.Context, addr string) (string, error) {
			ff.mu.Lock()
			defer ff.mu.Unlock()
			if s, ok := ff.states[addr]; ok {
				return s, nil
			}
			return "", io.EOF
		},
	}
}

func (ff *fakeFleet) setState(addr, s string) { ff.mu.Lock(); ff.states[addr] = s; ff.mu.Unlock() }
func (ff *fakeFleet) child(addr string) *fakeChild {
	ff.mu.Lock()
	defer ff.mu.Unlock()
	return ff.children[addr]
}

var party = []fleetMember{
	{Account: "ACC1", Character: "Tankone", RPC: "127.0.0.1:9001"},
	{Account: "ACC2", Character: "Healtwo", RPC: "127.0.0.1:9002"},
	{Account: "ACC3", Character: "Dpsthree", RPC: "127.0.0.1:9003"},
}

var fastOpts = fleetOpts{poll: 5 * time.Millisecond, wait: 2 * time.Second, stopGrace: time.Second}

// syncBuf lets the test read output while runFleet still writes.
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

func waitFor(t *testing.T, what string, f func() bool) {
	t.Helper()
	for i := 0; i < 400; i++ {
		if f() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestFleetAllInWorldThenCtrlCLogsOutEveryone(t *testing.T) {
	ff := newFakeFleet()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var out, errOut syncBuf
	code := make(chan int, 1)
	go func() { code <- runFleet(ctx, party, ff.deps(), fastOpts, &out, &errOut) }()

	// Two are in world, one is still logging in: no table yet.
	ff.setState("127.0.0.1:9001", "in_world")
	ff.setState("127.0.0.1:9002", "in_world")
	ff.setState("127.0.0.1:9003", "logging_in")
	time.Sleep(50 * time.Millisecond)
	if out.String() != "" {
		t.Fatalf("table printed early: %q", out.String())
	}
	ff.setState("127.0.0.1:9003", "in_world")
	waitFor(t, "table", func() bool { return strings.Contains(out.String(), "Dpsthree") })
	for _, want := range []string{"CHARACTER", "RPC", "STATE", "Tankone", "127.0.0.1:9002", "in_world"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("table missing %q:\n%s", want, out.String())
		}
	}
	cancel() // Ctrl-C
	if c := <-code; c != exitOK {
		t.Fatalf("exit %d, stderr:\n%s", c, errOut.String())
	}
	for _, m := range party {
		if !ff.child(m.RPC).stopped {
			t.Errorf("%s was not told to log out", m.Character)
		}
	}
	// Child output is prefixed by character, one line at a time.
	if !strings.Contains(errOut.String(), "[Tankone] world: authenticated") {
		t.Errorf("child log not prefixed:\n%s", errOut.String())
	}
}

func TestFleetChildFailureStopsTheRestAndFails(t *testing.T) {
	ff := newFakeFleet()
	for _, m := range party {
		ff.setState(m.RPC, "in_world")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var out, errOut syncBuf
	code := make(chan int, 1)
	go func() { code <- runFleet(ctx, party, ff.deps(), fastOpts, &out, &errOut) }()
	waitFor(t, "table", func() bool { return strings.Contains(out.String(), "Dpsthree") })
	ff.child("127.0.0.1:9002").exit(exitDisconnected)
	if c := <-code; c != exitFleet {
		t.Fatalf("exit %d, want %d", c, exitFleet)
	}
	if !ff.child("127.0.0.1:9001").stopped || !ff.child("127.0.0.1:9003").stopped {
		t.Error("survivors were not stopped")
	}
	if !strings.Contains(errOut.String(), "Healtwo exited with code 6") {
		t.Errorf("stderr:\n%s", errOut.String())
	}
}

func TestFleetChildFailsBeforeWorld(t *testing.T) {
	ff := newFakeFleet()
	var out, errOut syncBuf
	code := make(chan int, 1)
	go func() { code <- runFleet(context.Background(), party, ff.deps(), fastOpts, &out, &errOut) }()
	waitFor(t, "start", func() bool { return ff.child("127.0.0.1:9003") != nil })
	ff.child("127.0.0.1:9001").exit(exitAuth)
	if c := <-code; c != exitFleet {
		t.Fatalf("exit %d", c)
	}
	if !strings.Contains(out.String(), "exited") || !strings.Contains(errOut.String(), "Tankone failed before") {
		t.Errorf("out:\n%s\nerr:\n%s", out.String(), errOut.String())
	}
}

func TestFleetTimeoutFails(t *testing.T) {
	ff := newFakeFleet() // no status ever answers
	o := fastOpts
	o.wait = 30 * time.Millisecond
	var out, errOut bytes.Buffer
	if c := runFleet(context.Background(), party, ff.deps(), o, &out, &errOut); c != exitFleet {
		t.Fatalf("exit %d", c)
	}
	if !strings.Contains(out.String(), "starting") || !strings.Contains(errOut.String(), "not every character") {
		t.Errorf("out:\n%s\nerr:\n%s", out.String(), errOut.String())
	}
	for _, m := range party {
		if !ff.child(m.RPC).stopped {
			t.Errorf("%s left running", m.Character)
		}
	}
}

func TestFleetKillsAChildThatIgnoresStop(t *testing.T) {
	ff := newFakeFleet()
	for _, m := range party {
		ff.setState(m.RPC, "in_world")
	}
	d := ff.deps()
	inner := d.start
	d.start = func(m fleetMember, w io.Writer) (fleetChild, error) {
		c, err := inner(m, w)
		if m.Character == "Healtwo" {
			c.(*fakeChild).onStop = -1
		}
		return c, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	o := fastOpts
	o.stopGrace = 30 * time.Millisecond
	var out, errOut syncBuf
	code := make(chan int, 1)
	go func() { code <- runFleet(ctx, party, d, o, &out, &errOut) }()
	waitFor(t, "table", func() bool { return strings.Contains(out.String(), "Dpsthree") })
	cancel()
	if c := <-code; c != exitFleet {
		t.Fatalf("exit %d", c)
	}
	if !ff.child("127.0.0.1:9002").killed {
		t.Error("stuck child not killed")
	}
}

func TestFleetCleanLogoutOfEveryoneExitsZero(t *testing.T) {
	ff := newFakeFleet()
	for _, m := range party {
		ff.setState(m.RPC, "in_world")
	}
	var out, errOut syncBuf
	code := make(chan int, 1)
	go func() { code <- runFleet(context.Background(), party, ff.deps(), fastOpts, &out, &errOut) }()
	waitFor(t, "table", func() bool { return strings.Contains(out.String(), "Dpsthree") })
	for _, m := range party {
		ff.child(m.RPC).exit(0) // each logged out through its own RPC
	}
	if c := <-code; c != exitOK {
		t.Fatalf("exit %d, stderr:\n%s", c, errOut.String())
	}
}

func writeFile(t *testing.T, name, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadFleetValidation(t *testing.T) {
	st := config.Settings{Accounts: map[string]string{"alpha": "pw-one", "BETA": "pw-two"}}
	ok := `{"members":[{"account":"ALPHA","character":"A","rpc":"localhost:9001"},{"account":"beta","character":"B","rpc":"[::1]:9002"}]}`
	ms, err := loadFleet(writeFile(t, "f.json", ok), &st)
	if err != nil {
		t.Fatal(err)
	}
	if ms[0].RPC != "127.0.0.1:9001" || ms[1].RPC != "[::1]:9002" {
		t.Fatalf("addresses not normalised: %+v", ms)
	}
	cases := map[string]string{
		"empty":         `{"members":[]}`,
		"unknown field": `{"members":[{"account":"alpha","character":"A","rpc":"127.0.0.1:9001","password":"x"}]}`,
		"missing":       `{"members":[{"account":"alpha","rpc":"127.0.0.1:9001"}]}`,
		"non-loopback":  `{"members":[{"account":"alpha","character":"A","rpc":"0.0.0.0:9001"}]}`,
		"port zero":     `{"members":[{"account":"alpha","character":"A","rpc":"127.0.0.1:0"}]}`,
		"dup port":      `{"members":[{"account":"alpha","character":"A","rpc":"127.0.0.1:9001"},{"account":"beta","character":"B","rpc":"localhost:9001"}]}`,
		"dup character": `{"members":[{"account":"alpha","character":"A","rpc":"127.0.0.1:9001"},{"account":"beta","character":"a","rpc":"127.0.0.1:9002"}]}`,
		"no account":    `{"members":[{"account":"gamma","character":"A","rpc":"127.0.0.1:9001"}]}`,
	}
	for name, body := range cases {
		_, err := loadFleet(writeFile(t, "f.json", body), &st)
		if err == nil {
			t.Errorf("%s: accepted", name)
			continue
		}
		if strings.Contains(err.Error(), "pw-") || strings.Contains(err.Error(), "gamma") {
			t.Errorf("%s: error leaks a secret or account: %v", name, err)
		}
	}
}

func TestFleetCmdUsageAndSecrets(t *testing.T) {
	var out, errOut bytes.Buffer
	if c := run(context.Background(), []string{"fleet"}, &out, &errOut); c != exitUsage {
		t.Fatalf("exit %d", c)
	}
	settings := writeFile(t, "settings.json", `{"realmd":"127.0.0.1:3724","accounts":{"ACC":"hunter2-pw"}}`)
	fl := writeFile(t, "fleet.json", `{"members":[{"account":"nope","character":"A","rpc":"127.0.0.1:9001"}]}`)
	errOut.Reset()
	if c := run(context.Background(), []string{"fleet", "--settings", settings, "--config", fl}, &out, &errOut); c != exitUsage {
		t.Fatalf("exit %d", c)
	}
	if s := out.String() + errOut.String(); strings.Contains(s, "hunter2-pw") || strings.Contains(s, "nope") {
		t.Fatalf("secret in output: %q", s)
	}
}

func TestPrefixWriterSplitsLines(t *testing.T) {
	var b bytes.Buffer
	pw := &prefixWriter{w: &b, mu: &sync.Mutex{}, prefix: "[X] "}
	io.WriteString(pw, "one\ntw")
	io.WriteString(pw, "o\n")
	if b.String() != "[X] one\n[X] two\n" {
		t.Fatalf("%q", b.String())
	}
}
