package session

import (
	"context"
	"errors"
	"fmt"

	"github.com/pjerra/turtle-agent/internal/events"
	"github.com/pjerra/turtle-agent/internal/game"
	"github.com/pjerra/turtle-agent/internal/objects"
	"github.com/pjerra/turtle-agent/internal/opcode"
)

func (s *Session) registerItems() {
	s.handlers[opcode.SMSGInventoryChangeFailure] = s.onInventoryChangeFailure
}

// Inventory lists the backpack (bag 255, slots 23-38) from the player's own update fields.
func (s *Session) Inventory() []objects.Item {
	return s.store.Backpack(s.selfGUID())
}

// EquipRef picks the item to equip: the first backpack item with Entry, or Bag/Slot
// (Bag defaults to the backpack, 255).
type EquipRef struct {
	Entry uint32
	Bag   *uint8
	Slot  *uint8
}

// Equip equips an item with CMSG_AUTOEQUIP_ITEM, as a right-click in the bags does. The
// server answers a refusal with SMSG_INVENTORY_CHANGE_FAILURE (an inventory_error event)
// and a success with the player's changed inventory fields (ItemHandler.cpp:159-200).
func (s *Session) Equip(ctx context.Context, ref EquipRef) (objects.Item, error) {
	if ref.Entry == 0 && ref.Slot == nil {
		return objects.Item{}, errors.New("equip needs an item_entry or a slot (bag defaults to the backpack, 255)")
	}
	v, err := s.do(ctx, func() (any, error) {
		if err := s.inWorld(); err != nil {
			return nil, err
		}
		it, err := s.pickItem(ref)
		if err != nil {
			return nil, err
		}
		return it, s.conn.WritePacket(opcode.CMSGAutoequipItem, game.BuildAutoEquipItem(it.Bag, it.Slot))
	})
	if err != nil {
		return objects.Item{}, err
	}
	return v.(objects.Item), nil
}

// pickItem finds ref in the backpack; a bag/slot outside it is sent as given.
func (s *Session) pickItem(ref EquipRef) (objects.Item, error) {
	pack := s.Inventory()
	if ref.Slot == nil {
		for _, it := range pack {
			if it.Entry == ref.Entry {
				return it, nil
			}
		}
		return objects.Item{}, fmt.Errorf("item %d is not in the backpack", ref.Entry)
	}
	it := objects.Item{Bag: objects.BackpackBag, Slot: *ref.Slot}
	if ref.Bag != nil {
		it.Bag = *ref.Bag
	}
	for _, p := range pack {
		if p.Bag == it.Bag && p.Slot == it.Slot {
			return p, nil
		}
	}
	return it, nil
}

func (s *Session) onInventoryChangeFailure(body []byte) error {
	f, err := game.ParseInventoryChangeFailure(body)
	if err != nil || f.Result == 0 {
		return err
	}
	d := map[string]any{"result": f.Result}
	if f.Result == game.EquipErrCantEquipLevel {
		d["required_level"] = f.RequiredLevel
	}
	if f.Item != 0 {
		d["item_guid"] = f.Item
		if e := s.store.Entry(f.Item); e != 0 {
			d["item_entry"] = e
		}
	}
	s.emit(events.Event{Kind: events.KindInventoryError, Text: fmt.Sprintf("inventory change failed (InventoryResult %d)", f.Result), Data: d})
	return nil
}
