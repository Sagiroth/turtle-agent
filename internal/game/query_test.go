package game

import (
	"bytes"
	"testing"

	"github.com/pjerra/turtle-agent/internal/packet"
)

func TestNameQuery(t *testing.T) {
	if b := BuildNameQuery(0x2B); !bytes.Equal(b, packet.NewWriter().GUID(0x2B).Bytes()) {
		t.Fatalf("% X", b)
	}
	resp := packet.NewWriter().GUID(0x2B).CString("Gorm").U8(0).U32(2).U32(0).U32(1).Bytes() // QueryHandler.cpp:45-52
	info, err := ParseNameQueryResponse(resp)
	if err != nil || info != (NameInfo{GUID: 0x2B, Name: "Gorm", Race: 2, Gender: 0, Class: 1}) {
		t.Fatalf("%+v %v", info, err)
	}
}

func TestCreatureQuery(t *testing.T) {
	if b := BuildCreatureQuery(299, 0xF13000012B000001); !bytes.Equal(b, packet.NewWriter().U32(299).GUID(0xF13000012B000001).Bytes()) {
		t.Fatalf("% X", b)
	}
	resp := packet.NewWriter().U32(299).CString("Young Wolf").U8(0).U8(0).U8(0).CString("").
		U32(0).U32(1).U32(1).U32(0).U32(0).U32(0).U32(161).U8(0).U8(0).Bytes() // QueryHandler.cpp:187-200
	info, err := ParseCreatureQueryResponse(resp)
	if err != nil || !info.Found || info.Entry != 299 || info.Name != "Young Wolf" {
		t.Fatalf("%+v %v", info, err)
	}
	info, err = ParseCreatureQueryResponse(packet.NewWriter().U32(299 | 0x80000000).Bytes()) // :207-209
	if err != nil || info.Found || info.Entry != 299 {
		t.Fatalf("unknown entry: %+v %v", info, err)
	}
}

func TestParseCString(t *testing.T) {
	if s, err := ParseCString([]byte("You are now invisible.\x00")); err != nil || s != "You are now invisible." {
		t.Fatalf("%q %v", s, err)
	}
}
