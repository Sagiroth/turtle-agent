package objects

import (
	"testing"

	"github.com/pjerra/turtle-agent/internal/opcode"
	"github.com/pjerra/turtle-agent/internal/packet"
	"github.com/pjerra/turtle-agent/internal/testfixture"
	"github.com/pjerra/turtle-agent/internal/worldconn"
)

// invSlot sets inventory slot i of a player to item g (two words per slot from
// PLAYER_FIELD_INV_SLOT_HEAD, UpdateFields.h:202-205).
func invSlot(vals map[uint16]uint32, i int, g packet.GUID) map[uint16]uint32 {
	vals[PlayerInvSlotHead+uint16(2*i)] = uint32(g)
	vals[PlayerInvSlotHead+uint16(2*i)+1] = uint32(g >> 32)
	return vals
}

func itemCreate(g packet.GUID, typ uint8, entry, count uint32) Block {
	return Block{Type: BlockCreate2, GUID: g, ObjType: typ, Move: &Movement{UpdateFlags: 0x10},
		Values: map[uint16]uint32{FieldEntry: entry, ItemStackCount: count}}
}

func TestBackpackFromOwnFields(t *testing.T) {
	const (
		me                    = packet.GUID(0x2A)
		helm, bag             = packet.GUID(0x4000000000000001), packet.GUID(0x4000000000000002)
		sword, potions, later = packet.GUID(0x4000000000000003), packet.GUID(0x4000000000000004), packet.GUID(0x4000000000000005)
	)
	s := NewStore()
	if got := s.Backpack(me); len(got) != 0 {
		t.Fatalf("unknown player %+v", got)
	}
	vals := map[uint16]uint32{UnitHealth: 100}
	invSlot(vals, 0, helm)     // equipped head
	invSlot(vals, 19, bag)     // first bag slot
	invSlot(vals, 30, potions) // backpack, out of order on purpose
	invSlot(vals, 23, sword)
	invSlot(vals, 38, later) // last backpack slot; its create block has not arrived
	s.Apply([]Block{
		itemCreate(helm, TypeItem, 12064, 1), itemCreate(bag, TypeContainer, 4500, 1),
		itemCreate(sword, TypeItem, 2589, 1), itemCreate(potions, TypeItem, 118, 5),
		createBlock(me, TypePlayer, 0, 0, vals),
	})
	want := []Item{
		{Bag: BackpackBag, Slot: 23, GUID: sword, Entry: 2589, Count: 1},
		{Bag: BackpackBag, Slot: 30, GUID: potions, Entry: 118, Count: 5},
		{Bag: BackpackBag, Slot: 38, GUID: later},
	}
	got := s.Backpack(me)
	if len(got) != len(want) {
		t.Fatalf("%+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("item %d: %+v want %+v", i, got[i], want[i])
		}
	}
	// Equipping the sword empties slot 23 (a values update for the player).
	s.Apply([]Block{{Type: BlockValues, GUID: me, Values: invSlot(map[uint16]uint32{}, 23, 0)}})
	if got := s.Backpack(me); len(got) != 2 || got[0].Slot != 30 {
		t.Fatalf("after equip %+v", got)
	}
}

// The m1 login capture: Tagentone carries one item in the backpack, entry 80817 in slot 23,
// next to three equipped items and four bags.
func TestCapturedBackpack(t *testing.T) {
	recs := testfixture.Load(t, "m1-login.twcap")
	s := NewStore()
	var self packet.GUID
	apply := func(blocks []Block, err error) {
		if err != nil {
			t.Fatal(err)
		}
		for _, b := range blocks {
			if (b.Type == BlockCreate || b.Type == BlockCreate2) && b.Move != nil && b.Move.UpdateFlags&updateFlagSelf != 0 {
				self = b.GUID
			}
		}
		s.Apply(blocks)
	}
	for _, r := range recs { // in arrival order, as the session applies them
		switch {
		case r.Dir != worldconn.ServerToClient || r.Redacted:
		case r.Op == opcode.SMSGUpdateObject:
			apply(ParseUpdate(r.Body))
		case r.Op == opcode.SMSGCompressedUpdateObject:
			apply(ParseCompressedUpdate(r.Body))
		}
	}
	got := s.Backpack(self)
	if self == 0 || len(got) != 1 || got[0] != (Item{Bag: BackpackBag, Slot: 23, GUID: 0x4000000000028E4A, Entry: 80817, Count: 1}) {
		t.Fatalf("self %s backpack %+v", self, got)
	}
}
