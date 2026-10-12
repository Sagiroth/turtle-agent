package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sample = `enum OpcodesList
{
    MSG_NULL_ACTION                                 = 0x000,
    CMSG_BOOTME                                     = 0x001,
    SMSG_AUTH_CHALLENGE                             = 0x003, // gap at 0x002
    NUM_MSG_TYPES                                   = 0x004
};`

func flat(b []byte) string { return strings.Join(strings.Fields(string(b)), " ") }

func TestGenerateKeepsGapsAndLimit(t *testing.T) {
	out, err := Generate([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	s := flat(out)
	for _, want := range []string{
		"const NumMsgTypes uint16 = 0x004",
		"MSGNullAction uint16 = 0x000 // MSG_NULL_ACTION",
		"CMSGBootme uint16 = 0x001 // CMSG_BOOTME",
		"SMSGAuthChallenge uint16 = 0x003 // SMSG_AUTH_CHALLENGE",
		`0x003: "SMSG_AUTH_CHALLENGE",`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestGenerateRejectsBadInput(t *testing.T) {
	for name, src := range map[string]string{
		"decreasing": "enum OpcodesList\n{\n  CMSG_A = 0x002,\n  CMSG_B = 0x001,\n  NUM_MSG_TYPES = 0x003\n};",
		"duplicate":  "enum OpcodesList\n{\n  CMSG_A = 0x001,\n  CMSG_A = 0x002,\n  NUM_MSG_TYPES = 0x003\n};",
		"no limit":   "enum OpcodesList\n{\n  CMSG_A = 0x001\n};",
		"garbage":    "enum OpcodesList\n{\n  CMSG_A = 0x001,\n  int x;\n  NUM_MSG_TYPES = 0x002\n};",
	} {
		if _, err := Generate([]byte(src)); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

// The checked-in table must match the core header when the core is on this machine.
func TestGeneratedTableMatchesCore(t *testing.T) {
	home, _ := os.UserHomeDir()
	src, err := os.ReadFile(filepath.Join(home, "tortoise-wow/src/game/Protocol/Opcodes_1_12_1.h"))
	if err != nil {
		t.Skipf("core header not present: %v", err)
	}
	want, err := Generate(src)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile("../../internal/opcode/opcodes_gen.go")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("internal/opcode/opcodes_gen.go is stale: run make opcodes")
	}
}
