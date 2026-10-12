package objects

import (
	"math"
	"sort"
	"sync"

	"github.com/pjerra/turtle-agent/internal/packet"
)

type ChangeKind uint8

const (
	ChangeCreated ChangeKind = iota + 1
	ChangeHealth
	ChangeAura
	ChangeMount // UNIT_FIELD_MOUNTDISPLAYID changed: Old and New are the display ids
	ChangeStand // the stand state (byte 0 of UNIT_FIELD_BYTES_1) changed: Old and New are the states
)

// DefaultLimit is the Units limit when the filter sets none.
const DefaultLimit = 100

// Change is a state change the session turns into events. A create block reports only
// ChangeCreated, so a login burst does not flood the log with auras. Units leaving range
// and combat flag flips are not reported: no event consumes them.
type Change struct {
	GUID     packet.GUID
	Kind     ChangeKind
	Slot     int // aura slot, ChangeAura only
	Old, New uint32
}

// Unit is a player or creature snapshot. When the server's ShowHealthValues option is
// off, units the client may not see the health of arrive as a percent: Health 1..100
// (0 only when dead) and MaxHealth 100 (Object.cpp:727-748). The store passes those
// values through as sent, so Dead and ChangeHealth deltas then work on percents.
type Unit struct {
	GUID      packet.GUID `json:"guid"`
	Type      string      `json:"type"` // "player" or "creature"
	Entry     uint32      `json:"entry,omitempty"`
	Name      string      `json:"name"`
	X         float32     `json:"x"`
	Y         float32     `json:"y"`
	Z         float32     `json:"z"`
	O         float32     `json:"o"`
	Distance  float32     `json:"distance"`
	Health    uint32      `json:"health"`
	MaxHealth uint32      `json:"max_health"`
	PowerType uint8       `json:"power_type"`
	Power     uint32      `json:"power"`
	MaxPower  uint32      `json:"max_power"`
	Level     uint32      `json:"level"`
	Faction   uint32      `json:"faction"`
	Target    packet.GUID `json:"target,omitempty"`
	Flags     uint32      `json:"flags"`
	NPCFlags  uint32      `json:"npc_flags"`
	InCombat  bool        `json:"in_combat"`
	Dead      bool        `json:"dead"`
	Hostile   bool        `json:"hostile"`
	InParty   bool        `json:"in_party"`
	Auras     []uint32    `json:"auras,omitempty"`
}

type Filter struct {
	Near    float32 // 0: any distance
	Entry   uint32
	Hostile *bool
	Name    string // applied by the session after names are filled in
	Limit   int    // 0: 100
}

type object struct {
	guid       packet.GUID
	typ        uint8
	values     map[uint16]uint32
	x, y, z, o float32
}

type Store struct {
	mu   sync.RWMutex
	objs map[packet.GUID]*object
}

func NewStore() *Store { return &Store{objs: map[packet.GUID]*object{}} }

func (s *Store) get(g packet.GUID) (*object, bool) {
	o, ok := s.objs[g]
	if !ok {
		o = &object{guid: g, values: map[uint16]uint32{}}
		s.objs[g] = o
	}
	return o, ok
}

// Apply applies blocks in order and returns the resulting changes.
func (s *Store) Apply(blocks []Block) []Change {
	s.mu.Lock()
	defer s.mu.Unlock()
	var ch []Change
	for _, b := range blocks {
		switch b.Type {
		case BlockCreate, BlockCreate2:
			// A create block carries the whole object, so one for a GUID already held (a
			// respawn, a unit back in range) starts it afresh: no stale auras or target.
			o := &object{guid: b.GUID, typ: b.ObjType, values: map[uint16]uint32{}}
			s.objs[b.GUID] = o
			ch = append(ch, Change{GUID: b.GUID, Kind: ChangeCreated})
			moveObject(o, b.Move)
			ch = setValues(o, b.Values, ch, false)
		case BlockValues:
			o, existed := s.get(b.GUID) // values before a create we missed are kept
			ch = setValues(o, b.Values, ch, existed)
		case BlockMovement:
			if o, ok := s.objs[b.GUID]; ok {
				moveObject(o, b.Move)
			}
		case BlockOutOfRange:
			for _, g := range b.GUIDs {
				delete(s.objs, g)
			}
		}
	}
	return ch
}

func moveObject(o *object, m *Movement) {
	if m != nil && m.HasPosition {
		o.x, o.y, o.z, o.o = m.X, m.Y, m.Z, m.O
	}
}

func setValues(o *object, vals map[uint16]uint32, ch []Change, report bool) []Change {
	idx := make([]int, 0, len(vals))
	for i := range vals {
		idx = append(idx, int(i))
	}
	sort.Ints(idx)
	for _, i := range idx {
		k, v := uint16(i), vals[uint16(i)]
		old := o.values[k]
		o.values[k] = v
		if !report || old == v {
			continue
		}
		switch {
		case k == UnitHealth:
			ch = append(ch, Change{GUID: o.guid, Kind: ChangeHealth, Old: old, New: v})
		case k >= UnitAura && k < UnitAura+AuraSlots:
			ch = append(ch, Change{GUID: o.guid, Kind: ChangeAura, Slot: int(k - UnitAura), Old: old, New: v})
		case k == UnitMountDisplay:
			ch = append(ch, Change{GUID: o.guid, Kind: ChangeMount, Old: old, New: v})
		case k == UnitBytes1 && old&0xFF != v&0xFF:
			ch = append(ch, Change{GUID: o.guid, Kind: ChangeStand, Old: old & 0xFF, New: v & 0xFF})
		}
	}
	return ch
}

func (s *Store) Destroy(g packet.GUID) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.objs[g]
	delete(s.objs, g)
	return ok
}

func (s *Store) MoveTo(g packet.GUID, x, y, z, o float32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ob, ok := s.objs[g]; ok {
		ob.x, ob.y, ob.z, ob.o = x, y, z, o
	}
}

// MoveSpline puts g at a spline's destination and turns it along the way it travelled.
// A stop, or a move with no horizontal travel, keeps the facing it had.
func (s *Store) MoveSpline(g packet.GUID, x, y, z float32, stop bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ob, ok := s.objs[g]
	if !ok {
		return
	}
	if dx, dy := float64(x-ob.x), float64(y-ob.y); !stop && (dx != 0 || dy != 0) {
		o := math.Atan2(dy, dx)
		if o < 0 {
			o += 2 * math.Pi
		}
		ob.o = float32(o)
	}
	ob.x, ob.y, ob.z = x, y, z
}

// Clear forgets every object (SMSG_NEW_WORLD).
func (s *Store) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.objs = map[packet.GUID]*object{}
}

func (s *Store) Get(g packet.GUID, ox, oy, oz float32) (Unit, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	o, ok := s.objs[g]
	if !ok {
		return Unit{}, false
	}
	return toUnit(o, ox, oy, oz), true
}

// Entry is g's creature entry: its OBJECT_FIELD_ENTRY when the store holds that, else
// what the GUID encodes (packet.GUID.Entry), which is 0 for a pet whose values are unknown.
func (s *Store) Entry(g packet.GUID) uint32 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if o, ok := s.objs[g]; ok && o.values[FieldEntry] != 0 {
		return o.values[FieldEntry]
	}
	return g.Entry()
}

// Field is the update field idx of g as last sent, 0 when g or the field is unknown.
func (s *Store) Field(g packet.GUID, idx uint16) uint32 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if o, ok := s.objs[g]; ok {
		return o.values[idx]
	}
	return 0
}

// Owners returns the charmer, summoner and creator of g (zero when unset or unknown), so
// a pet, totem or charmed unit can be traced to its player in one lookup.
func (s *Store) Owners(g packet.GUID) [3]packet.GUID {
	s.mu.RLock()
	defer s.mu.RUnlock()
	o, ok := s.objs[g]
	if !ok {
		return [3]packet.GUID{}
	}
	return [3]packet.GUID{guidAt(o.values, UnitCharmedBy), guidAt(o.values, UnitSummonedBy), guidAt(o.values, UnitCreatedBy)}
}

func guidAt(v map[uint16]uint32, i uint16) packet.GUID {
	return packet.GUID(uint64(v[i]) | uint64(v[i+1])<<32)
}

// Units returns players and creatures matching f, nearest first.
func (s *Store) Units(f Filter, ox, oy, oz float32) []Unit {
	s.mu.RLock()
	var out []Unit
	for _, o := range s.objs {
		if !isUnit(o) {
			continue
		}
		u := toUnit(o, ox, oy, oz)
		if (f.Near > 0 && u.Distance > f.Near) || (f.Entry != 0 && u.Entry != f.Entry) || (f.Hostile != nil && u.Hostile != *f.Hostile) {
			continue
		}
		out = append(out, u)
	}
	s.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool {
		if out[i].Distance != out[j].Distance {
			return out[i].Distance < out[j].Distance
		}
		return out[i].GUID < out[j].GUID
	})
	limit := f.Limit
	if limit <= 0 {
		limit = DefaultLimit
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

func isUnit(o *object) bool {
	switch o.typ {
	case TypeUnit, TypePlayer:
		return true
	case 0: // values seen before a create block
		return o.guid.IsPlayer() || o.guid.IsCreature()
	}
	return false
}

func toUnit(o *object, ox, oy, oz float32) Unit {
	v := o.values
	u := Unit{
		GUID: o.guid, Type: "creature", Entry: v[FieldEntry], X: o.x, Y: o.y, Z: o.z, O: o.o,
		Health: v[UnitHealth], MaxHealth: v[UnitMaxHealth], PowerType: uint8(v[UnitBytes0] >> 24),
		Level: v[UnitLevel], Faction: v[UnitFaction], Flags: v[UnitFlags], NPCFlags: v[UnitNPCFlags],
		Target: guidAt(v, UnitTarget),
	}
	if o.typ == TypePlayer || o.guid.IsPlayer() {
		u.Type = "player"
	}
	if u.Entry == 0 && o.guid.IsCreature() {
		u.Entry = o.guid.Entry()
	}
	if u.PowerType < 5 {
		u.Power, u.MaxPower = v[UnitPower1+uint16(u.PowerType)], v[UnitMaxPower1+uint16(u.PowerType)]
	}
	u.InCombat = u.Flags&UnitFlagInCombat != 0
	u.Dead = u.MaxHealth > 0 && u.Health == 0
	u.Hostile = u.Type == "creature" && !u.Dead && u.NPCFlags == 0 &&
		u.Flags&(UnitFlagSpawning|UnitFlagPlayerControlled|UnitFlagImmuneToPlayer|UnitFlagNotSelectable) == 0
	for i := uint16(0); i < AuraSlots; i++ {
		if id := v[UnitAura+i]; id != 0 {
			u.Auras = append(u.Auras, id)
		}
	}
	dx, dy, dz := float64(o.x-ox), float64(o.y-oy), float64(o.z-oz)
	u.Distance = float32(math.Sqrt(dx*dx + dy*dy + dz*dz))
	return u
}

// Item is an item the player carries, at bag/slot as CMSG_AUTOEQUIP_ITEM addresses it.
type Item struct {
	Bag   uint8       `json:"bag"`
	Slot  uint8       `json:"slot"`
	GUID  packet.GUID `json:"guid"`
	Entry uint32      `json:"item_entry"` // 0, like Count, until the item's create block arrives
	Count uint32      `json:"count"`
}

// Backpack lists the items in player's backpack, in slot order, from the player's own
// inventory fields and each item's create block (Player::BuildCreateUpdateBlockForPlayer
// sends the items to their owner, Player.cpp:5224-5254).
func (s *Store) Backpack(player packet.GUID) []Item {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.objs[player]
	if !ok {
		return nil
	}
	var out []Item
	for slot := BackpackFirst; slot < BackpackEnd; slot++ {
		if it, ok := s.item(BackpackBag, slot, guidAt(p.values, PlayerInvSlotHead+2*uint16(slot))); ok {
			out = append(out, it)
		}
	}
	return out
}

// Bags lists the items inside the player's four bags (inventory slots 19-22), bag by bag
// in slot order, from each bag's CONTAINER_FIELD_SLOT_1 fields. A bag whose create block
// has not arrived holds nothing yet.
func (s *Store) Bags(player packet.GUID) []Item {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.objs[player]
	if !ok {
		return nil
	}
	var out []Item
	for bag := BagFirst; bag < BagEnd; bag++ {
		c, ok := s.objs[guidAt(p.values, PlayerInvSlotHead+2*uint16(bag))]
		if !ok {
			continue
		}
		for slot := uint32(0); slot < c.values[ContainerNumSlots] && slot < maxBagSlots; slot++ {
			if it, ok := s.item(bag, uint8(slot), guidAt(c.values, ContainerSlot1+2*uint16(slot))); ok {
				out = append(out, it)
			}
		}
	}
	return out
}

// maxBagSlots bounds a bag's slot count: a container holds at most 36 items.
const maxBagSlots = 36

// ItemAt is the item at bag/slot as CMSG_AUTOEQUIP_ITEM addresses it: bag 255 with an
// inventory slot up to the backpack's last (38), or a bag slot 19-22 with a slot inside
// that bag (its CONTAINER_FIELD_SLOT_1 fields). ok is false for an empty or unknown slot.
func (s *Store) ItemAt(player packet.GUID, bag, slot uint8) (Item, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.objs[player]
	if !ok {
		return Item{}, false
	}
	switch {
	case bag == BackpackBag && slot < BackpackEnd:
		return s.item(bag, slot, guidAt(p.values, PlayerInvSlotHead+2*uint16(slot)))
	case bag >= BagFirst && bag < BagEnd:
		c, ok := s.objs[guidAt(p.values, PlayerInvSlotHead+2*uint16(bag))]
		if !ok || uint32(slot) >= c.values[ContainerNumSlots] {
			return Item{}, false
		}
		return s.item(bag, slot, guidAt(c.values, ContainerSlot1+2*uint16(slot)))
	}
	return Item{}, false
}

// item describes the item g at bag/slot; the caller holds s.mu.
func (s *Store) item(bag, slot uint8, g packet.GUID) (Item, bool) {
	if g == 0 {
		return Item{}, false
	}
	it := Item{Bag: bag, Slot: slot, GUID: g}
	if o, ok := s.objs[g]; ok {
		it.Entry, it.Count = o.values[FieldEntry], o.values[ItemStackCount]
	}
	return it, true
}
