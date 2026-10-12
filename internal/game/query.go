package game

import (
	"fmt"

	"github.com/pjerra/turtle-agent/internal/packet"
)

// BuildNameQuery is CMSG_NAME_QUERY (QueryHandler.cpp:121-125).
func BuildNameQuery(g packet.GUID) []byte { return packet.NewWriter().GUID(g).Bytes() }

type NameInfo struct {
	GUID                packet.GUID
	Name                string
	Race, Gender, Class uint32
}

// ParseNameQueryResponse decodes SMSG_NAME_QUERY_RESPONSE (QueryHandler.cpp:45-52).
func ParseNameQueryResponse(body []byte) (NameInfo, error) {
	r := packet.NewReader(body)
	var n NameInfo
	n.GUID, n.Name = r.GUID(), r.CString()
	r.CString() // realm name, empty
	n.Race, n.Gender, n.Class = r.U32(), r.U32(), r.U32()
	if err := r.Err(); err != nil {
		return NameInfo{}, fmt.Errorf("SMSG_NAME_QUERY_RESPONSE: %w", err)
	}
	return n, nil
}

// BuildCreatureQuery is CMSG_CREATURE_QUERY (QueryHandler.cpp:141-147).
func BuildCreatureQuery(entry uint32, g packet.GUID) []byte {
	return packet.NewWriter().U32(entry).GUID(g).Bytes()
}

type CreatureInfo struct {
	Entry   uint32
	Found   bool
	Name    string
	SubName string
	Rank    uint32
}

// ParseCreatureQueryResponse decodes SMSG_CREATURE_QUERY_RESPONSE (QueryHandler.cpp:186-209).
func ParseCreatureQueryResponse(body []byte) (CreatureInfo, error) {
	r := packet.NewReader(body)
	entry := r.U32()
	if entry&0x80000000 != 0 {
		return CreatureInfo{Entry: entry &^ 0x80000000}, r.Err()
	}
	c := CreatureInfo{Entry: entry, Found: true}
	c.Name = r.CString()
	r.CString()
	r.CString()
	r.CString()
	c.SubName = r.CString()
	r.U32() // type flags
	r.U32() // type
	r.U32() // family
	c.Rank = r.U32()
	if err := r.Err(); err != nil {
		return CreatureInfo{}, fmt.Errorf("SMSG_CREATURE_QUERY_RESPONSE: %w", err)
	}
	return c, nil
}
