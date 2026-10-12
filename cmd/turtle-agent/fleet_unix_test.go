//go:build unix

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
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
	c, err := startProcess(exe, "/cfg/settings.json", fleetMember{Account: "ACC", Character: "Stay", RPC: "127.0.0.1:9002"}, &buf)
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
	c2, err := startProcess(exe, "/cfg/settings.json", m, &b2)
	if err != nil {
		t.Fatal(err)
	}
	<-c2.Done()
	if c2.Code() != 3 {
		t.Fatalf("code %d", c2.Code())
	}
}
