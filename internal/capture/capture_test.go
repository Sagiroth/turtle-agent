package capture

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/pjerra/turtle-agent/internal/opcode"
	"github.com/pjerra/turtle-agent/internal/worldconn"
)

func TestRoundTripAndMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.twcap")
	w, err := Create(path)
	if err != nil {
		t.Fatal(err)
	}
	w.Packet(worldconn.ServerToClient, opcode.SMSGCharEnum, []byte{1, 2, 3})
	w.Packet(worldconn.ClientToServer, opcode.CMSGPing, []byte{4})
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", info.Mode())
	}
	recs, err := ReadFile(path)
	if err != nil || len(recs) != 2 {
		t.Fatalf("%v %v", recs, err)
	}
	if recs[0].Op != opcode.SMSGCharEnum || recs[0].Dir != worldconn.ServerToClient || !bytes.Equal(recs[0].Body, []byte{1, 2, 3}) ||
		recs[1].Dir != worldconn.ClientToServer || recs[1].Redacted {
		t.Fatalf("%+v", recs)
	}
	if got := Bodies(recs, opcode.SMSGCharEnum); len(got) != 1 {
		t.Fatalf("Bodies %v", got)
	}
	if Stats(recs)["SMSG_CHAR_ENUM"] != 1 {
		t.Fatalf("stats %v", Stats(recs))
	}
}

func TestNeverStoresAuthSessionOrWarden(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.twcap")
	w, _ := Create(path)
	w.Packet(worldconn.ClientToServer, opcode.CMSGAuthSession, []byte("HARNESS\x00digest"))
	w.Packet(worldconn.ServerToClient, opcode.SMSGWardenData, []byte{9, 9, 9})
	w.Close()
	recs, _ := ReadFile(path)
	for _, r := range recs {
		if !r.Redacted || len(r.Body) != 0 {
			t.Fatalf("%s stored: %+v", opcode.Name(r.Op), r)
		}
	}
}

func TestSecretsRedacted(t *testing.T) {
	key := bytes.Repeat([]byte{0xAB, 0xCD}, 20)
	path := filepath.Join(t.TempDir(), "c.twcap")
	w, _ := Create(path, []byte("HARNESS"), []byte("pw-secret"), key, []byte("x"))
	w.Packet(worldconn.ServerToClient, opcode.SMSGMessagechat, []byte("say pw-secret now"))
	w.Packet(worldconn.ServerToClient, opcode.SMSGMessagechat, append([]byte{1}, key...))
	w.Packet(worldconn.ServerToClient, opcode.SMSGNameQueryResponse, []byte("Harnessb\x00"))
	if n, red := w.Counts(); n != 3 || red != 2 {
		t.Fatalf("counts %d %d", n, red)
	}
	w.Close()
	recs, _ := ReadFile(path)
	if !recs[0].Redacted || !recs[1].Redacted || recs[2].Redacted {
		t.Fatalf("%+v", recs)
	}
	if f := Scan(recs, []byte("Harnessb")); len(f) != 1 || f[0].Index != 2 {
		t.Fatalf("scan %v", f)
	}
}

func TestCreateRefusesOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.twcap")
	w, _ := Create(path)
	w.Close()
	if _, err := Create(path); err == nil {
		t.Fatal("overwrote an existing capture")
	}
}

func TestReadFileRejectsGarbage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad")
	os.WriteFile(path, []byte("TWCAP1\n\x00\x01"), 0o600)
	if _, err := ReadFile(path); err == nil {
		t.Fatal("truncated record accepted")
	}
}
