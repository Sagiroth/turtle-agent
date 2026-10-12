package game

import "github.com/pjerra/turtle-agent/internal/packet"

// Core cites in this file are tortoise-wow upstream/1181dev 5784fb0.

// BuildAutoEquipItem is CMSG_AUTOEQUIP_ITEM: u8 bag, u8 slot of the item to equip, as a
// right-click in the bags sends it (ItemHandler.cpp:159-163). The server picks the
// equipment slot and swaps out what is there.
func BuildAutoEquipItem(bag, slot uint8) []byte { return []byte{bag, slot} }

// EquipErrCantEquipLevel is EQUIP_ERR_CANT_EQUIP_LEVEL_I (Item.h:48), the one result
// that carries the required level.
const EquipErrCantEquipLevel uint8 = 1

// InventoryChangeFailure is SMSG_INVENTORY_CHANGE_FAILURE: an InventoryResult (Item.h:45)
// and the items involved (Player.cpp:13561-13579). Result 0 is success, sent without more.
type InventoryChangeFailure struct {
	Result        uint8
	RequiredLevel uint32
	Item, Item2   packet.GUID
}

func ParseInventoryChangeFailure(body []byte) (InventoryChangeFailure, error) {
	r := packet.NewReader(body)
	f := InventoryChangeFailure{Result: r.U8()}
	if f.Result != 0 {
		if f.Result == EquipErrCantEquipLevel {
			f.RequiredLevel = r.U32()
		}
		f.Item, f.Item2 = r.GUID(), r.GUID()
		r.U8() // bag subclass
	}
	return f, done(r, "SMSG_INVENTORY_CHANGE_FAILURE")
}
