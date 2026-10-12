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

// A duration sent while a slot is empty waits for the spell that fills it, within
// pendingAuraWindow, and survives updates to other units and slots. A duration sent while a
// spell holds its slot is that spell's; a different spell that takes the slot within the window
// takes it (a rank swap), and one that takes it later does not.
func TestAuraDurationWaitsForItsSpell(t *testing.T) {
	s, c := newClockStore()
	s.Apply([]Block{createBlock(wolf, TypeUnit, 0, 0, map[uint16]uint32{UnitHealth: 50, UnitMaxHealth: 50})})
	s.AuraDuration(wolf, 2, 30000)
	s.Apply([]Block{{Type: BlockValues, GUID: wolf, Values: map[uint16]uint32{UnitHealth: 40}}})
	s.Apply([]Block{{Type: BlockValues, GUID: wolf, Values: map[uint16]uint32{UnitAura + 4: 1243}}})
	s.AuraDuration(wolf, 5, 30000)
	s.AuraDuration(wolf, 5, -1) // endless: nothing waits for slot 5
	c.add(500 * time.Millisecond)
	s.Apply([]Block{{Type: BlockValues, GUID: wolf, Values: map[uint16]uint32{UnitAura + 2: 433, UnitAura + 5: 1243}}})
	u, _ := s.Get(wolf, 0, 0, 0)
	if u.Health != 40 || len(u.Auras) != 3 || u.Auras[0].Slot != 2 || u.Auras[0].RemainingMS == nil || *u.Auras[0].RemainingMS != 29500 ||
		u.Auras[1].Slot != 4 || u.Auras[1].RemainingMS != nil || u.Auras[2].Slot != 5 || u.Auras[2].RemainingMS != nil {
		t.Fatalf("%+v", u)
	}
	// A refresh while Food holds slot 2 is Food's. Drink takes the slot in the same tick, as a
	// rank swap does, so it takes the time the refresh sent.
	s.AuraDuration(wolf, 2, 15000)
	if u, _ = s.Get(wolf, 0, 0, 0); u.Auras[0].RemainingMS == nil || *u.Auras[0].RemainingMS != 15000 {
		t.Fatalf("refresh: %+v", u.Auras[0])
	}
	s.Apply([]Block{{Type: BlockValues, GUID: wolf, Values: map[uint16]uint32{UnitAura + 2: 430}}})
	if u, _ = s.Get(wolf, 0, 0, 0); u.Auras[0].SpellID != 430 || u.Auras[0].RemainingMS == nil || *u.Auras[0].RemainingMS != 15000 {
		t.Fatalf("swap: %+v", u.Auras[0])
	}
}

// A duration counts for the spell that takes its slot within pendingAuraWindow of its arrival.
// For an empty slot that is the spell it was sent for; for a held slot, a different spell in the
// same tick (a rank swap). Past the window it is an orphan, from an aura that came and went
// within its tick, and must not time the spell that fills the slot later.
func TestPendingAuraWindow(t *testing.T) {
	for _, tc := range []struct {
		name    string
		held    bool
		after   time.Duration
		adopted bool
	}{
		{"empty slot, spell at once", false, 0, true},
		{"empty slot, spell at the window's end", false, pendingAuraWindow, true},
		{"empty slot, orphan past the window", false, pendingAuraWindow + time.Millisecond, false},
		{"rank swap at once", true, 0, true},
		{"rank swap at the window's end", true, pendingAuraWindow, true},
		{"rank swap past the window", true, pendingAuraWindow + time.Millisecond, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// held: Food holds slot 3 when the duration arrives; the spell that then takes the
			// slot is Drink, after tc.after.
			s, c := newClockStore()
			vals := map[uint16]uint32{}
			if tc.held {
				vals[UnitAura+3] = 433
			}
			s.Apply([]Block{createBlock(wolf, TypeUnit, 0, 0, vals)})
			s.AuraDuration(wolf, 3, 30000)
			c.add(tc.after)
			s.Apply([]Block{{Type: BlockValues, GUID: wolf, Values: map[uint16]uint32{UnitAura + 3: 430}}})
			u, _ := s.Get(wolf, 0, 0, 0)
			if len(u.Auras) != 1 || u.Auras[0].SpellID != 430 {
				t.Fatalf("%+v", u.Auras)
			}
			r := u.Auras[0].RemainingMS
			if !tc.adopted {
				if r != nil {
					t.Fatalf("orphan adopted: %+v", u.Auras[0])
				}
				return
			}
			if want := 30000 - tc.after.Milliseconds(); r == nil || *r != want {
				t.Fatalf("remaining %+v, want %d ms", u.Auras[0], want)
			}
		})
	}
}

// A refresh of the spell that holds a slot restarts that spell's timer. The spell does not
// change, so nothing takes the pending duration over, and a spell that fills another slot
// never does.
func TestSameSpellRefreshRestartsTimer(t *testing.T) {
	s, c := newClockStore()
	s.Apply([]Block{createBlock(wolf, TypeUnit, 0, 0, map[uint16]uint32{UnitAura + 3: 433})})
	s.AuraDuration(wolf, 3, 30000)
	c.add(10 * time.Second)
	s.AuraDuration(wolf, 3, 30000) // Food refreshed: a full 30 s from now
	s.Apply([]Block{{Type: BlockValues, GUID: wolf, Values: map[uint16]uint32{UnitAura + 3: 433, UnitAura + 4: 1243}}})
	u, _ := s.Get(wolf, 0, 0, 0)
	if len(u.Auras) != 2 || u.Auras[0].Slot != 3 || u.Auras[0].SpellID != 433 || u.Auras[0].RemainingMS == nil || *u.Auras[0].RemainingMS != 30000 ||
		u.Auras[1].Slot != 4 || u.Auras[1].RemainingMS != nil {
		t.Fatalf("%+v", u.Auras)
	}
}
