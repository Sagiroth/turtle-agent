package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestRunWithoutArgsPrintsUsage(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run(context.Background(), nil, &out, &errOut); code != exitUsage {
		t.Fatalf("exit code %d, want %d", code, exitUsage)
	}
	if !strings.Contains(errOut.String(), "turtle-agent login") {
		t.Fatalf("usage missing: %q", errOut.String())
	}
}

func TestRunVersion(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run(context.Background(), []string{"version"}, &out, &errOut); code != exitOK {
		t.Fatalf("exit code %d", code)
	}
	if got := strings.TrimSpace(out.String()); got != "turtle-agent 0.1.0" {
		t.Fatalf("got %q", got)
	}
}
