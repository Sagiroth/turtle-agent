// Package gametest builds server packet bodies for tests in game and its callers.
package gametest

import (
	"github.com/pjerra/turtle-agent/internal/game"
	"github.com/pjerra/turtle-agent/internal/packet"
)

// GroupList transcribes Group::SendUpdate (Group.cpp:1402-1432) for the receiving player.
func GroupList(leader packet.GUID, members ...game.GroupMember) []byte {
	w := packet.NewWriter().U8(0).U8(0).U32(uint32(len(members)))
	for _, m := range members {
		w.CString(m.Name).GUID(m.GUID).U8(m.Status).U8(m.Flags)
	}
	w.GUID(leader)
	if len(members) > 0 {
		w.U8(3).U64(0).U8(2).U8(0) // group loot, no looter, uncommon threshold, difficulty byte
	}
	return w.Bytes()
}
