package objects

import (
	"testing"
	"time"
)

// Auras carry their slot, name, stacks (UNIT_FIELD_AURAAPPLICATIONS holds stacks - 1, a
// byte per slot, SpellAuras.cpp:7565-7574) and, for the player's own, the time left.
func TestAurasStandStateAndDurations(t *testing.T) {
	s, c := newClockStore()
	s.Apply([]Block{createBlock(wolf, TypeUnit, 0, 0, map[uint16]uint32{
		UnitAura + 1: 433, UnitAura + 5: 1243, UnitAura + 33: 589,
		UnitAuraApps: 0x0000_0000, UnitAuraApps + 1: 0x0000_0200, // slot 5: 3 stacks
		UnitBytes1: 0x0100_0001, // sit; byte 3 is the creep/untrackable flags
	})})
	s.AuraDuration(wolf, 1, 25000)
	s.AuraDuration(wolf, 5, -1) // endless
	c.add(5 * time.Second)
	u, _ := s.Get(wolf, 0, 0, 0)
	if u.StandState != "sit" || len(u.Auras) != 3 {
		t.Fatalf("%+v", u)
	}
	food, fort, pain := u.Auras[0], u.Auras[1], u.Auras[2]
	if food.Slot != 1 || food.SpellID != 433 || food.Name != "Food" || food.Stacks != 1 || food.Harmful || food.RemainingMS == nil || *food.RemainingMS != 20000 {
		t.Fatalf("food %+v", food)
	}
	if fort.Slot != 5 || fort.Stacks != 3 || fort.RemainingMS != nil || pain.Slot != 33 || !pain.Harmful {
		t.Fatalf("%+v %+v", fort, pain)
	}
	// Standing up and the food running out: the slot empties, its time goes with it.
	s.Apply([]Block{{Type: BlockValues, GUID: wolf, Values: map[uint16]uint32{UnitBytes1: 0x0100_0000, UnitAura + 1: 0}}})
	s.Apply([]Block{{Type: BlockValues, GUID: wolf, Values: map[uint16]uint32{UnitAura + 1: 430}}})
	if u, _ = s.Get(wolf, 0, 0, 0); u.StandState != "stand" || u.Auras[0].Name != "Drink" || u.Auras[0].RemainingMS != nil {
		t.Fatalf("%+v", u)
	}
	s.Apply([]Block{{Type: BlockValues, GUID: wolf, Values: map[uint16]uint32{UnitBytes1: 9}}})
	if u, _ = s.Get(wolf, 0, 0, 0); u.StandState != "unknown(9)" {
		t.Fatalf("unknown stand state %q", u.StandState)
	}
}

func TestCasting(t *testing.T) {
	s, c := newClockStore()
	s.Apply([]Block{createBlock(wolf, TypeUnit, 0, 0, nil)})
	if u, _ := s.Get(wolf, 0, 0, 0); u.Casting != nil || u.Auras == nil {
		t.Fatalf("idle: %+v", u)
	}
	s.StartCast(wolf, 1953, 0) // instant: no cast
	s.StartCast(wolf, 116, 2500)
	c.add(time.Second)
	s.DelayCast(wolf, 500)
	if u, _ := s.Get(wolf, 0, 0, 0); u.Casting == nil || u.Casting.SpellID != 116 || u.Casting.Name != "Frostbolt" || u.Casting.Channel || *u.Casting.EndsInMS != 2000 {
		t.Fatalf("casting: %+v", u.Casting)
	}
	s.EndCast(wolf, 133) // another spell's go
	if u, _ := s.Get(wolf, 0, 0, 0); u.Casting == nil {
		t.Fatal("ended by another spell")
	}
	s.EndCast(wolf, 116)
	if u, _ := s.Get(wolf, 0, 0, 0); u.Casting != nil {
		t.Fatalf("after go: %+v", u.Casting)
	}
	// A cast whose go never came stops showing castGrace after its end.
	s.StartCast(wolf, 116, 1000)
	c.add(1000*time.Millisecond + castGrace)
	if u, _ := s.Get(wolf, 0, 0, 0); u.Casting != nil {
		t.Fatalf("stale: %+v", u.Casting)
	}
	// A channel shows from UNIT_CHANNEL_SPELL, without an end.
	s.Apply([]Block{{Type: BlockValues, GUID: wolf, Values: map[uint16]uint32{UnitChannelSpell: 12051}}})
	if u, _ := s.Get(wolf, 0, 0, 0); u.Casting == nil || !u.Casting.Channel || u.Casting.Name != "Evocation" || u.Casting.EndsInMS != nil {
		t.Fatalf("channel: %+v", u.Casting)
	}
}
