//go:build unix

package main

import (
	"bytes"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// startProcess runs a stand-in executable: the argv carries the account key but no
// password, stderr comes back prefixed, and the exit code and an interrupt are honoured.
func TestStartProcessWithStubExecutable(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "stub")
	script := "#!/bin/sh\necho \"args: $*\" >&2\ntrap 'echo bye >&2; exit 0' INT\nif [ \"$5\" = fail ]; then exit 3; fi\nwhile :; do sleep 0.05; done\n"
	if err := os.WriteFile(exe, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	m := fleetMember{Account: "ACC", Character: "Fail", RPC: "127.0.0.1:9001"}

	var buf bytes.Buffer
	c, err := startProcess(exe, childArgs("/cfg/settings.json", fleetMember{Account: "ACC", Character: "Stay", RPC: "127.0.0.1:9002"}, 0, ""), nil, &buf)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	c.Stop()
	select {
	case <-c.Done():
	case <-time.After(5 * time.Second):
		c.Kill()
		t.Fatal("child ignored the interrupt")
	}
	if c.Code() != 0 || !strings.Contains(buf.String(), "args: login --config /cfg/settings.json --rpc 127.0.0.1:9002 ACC Stay") || !strings.Contains(buf.String(), "bye") {
		t.Fatalf("code %d output %q", c.Code(), buf.String())
	}

	var b2 bytes.Buffer
	script2 := "#!/bin/sh\nexit 3\n"
	os.WriteFile(exe, []byte(script2), 0o755)
	c2, err := startProcess(exe, childArgs("/cfg/settings.json", m, 0, ""), nil, &b2)
	if err != nil {
		t.Fatal(err)
	}
	<-c2.Done()
	if c2.Code() != 3 {
		t.Fatalf("code %d", c2.Code())
	}
}

// The budget token reaches the child through its environment, never its argv.
func TestStartProcessPassesEnvNotArgv(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "stub")
	script := "#!/bin/sh\necho \"args: $*\" >&2\necho \"token: $" + budgetTokenEnv + "\" >&2\n"
	if err := os.WriteFile(exe, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	m := fleetMember{Account: "ACC", Character: "Env", RPC: "127.0.0.1:9004"}
	c, err := startProcess(exe, childArgs("/cfg/settings.json", m, 1, "127.0.0.1:5000"), []string{budgetTokenEnv + "=secret-token-value"}, &buf)
	if err != nil {
		t.Fatal(err)
	}
	<-c.Done()
	out := buf.String()
	if !strings.Contains(out, "token: secret-token-value") || !strings.Contains(out, "--login-budget 127.0.0.1:5000") {
		t.Fatalf("output %q", out)
	}
	if strings.Contains(strings.SplitN(out, "token:", 2)[0], "secret-token-value") {
		t.Fatalf("token on the command line: %q", out)
	}
}

// A closed terminal (SIGHUP) ends the run like Ctrl-C, so the characters log out.
func TestSIGHUPEndsTheRun(t *testing.T) {
	if signal.Ignored(syscall.SIGHUP) {
		t.Skip("SIGHUP is ignored in this environment (nohup)")
	}
	ctx := signalContext()
	if err := syscall.Kill(os.Getpid(), syscall.SIGHUP); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("SIGHUP did not end the context")
	}
}
