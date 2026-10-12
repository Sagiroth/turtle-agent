package objects

import (
	"reflect"
	"testing"
)

// The quest log is 20 slots of 3 words: quest id, counters (6 bits each, four) with the
// state in the top byte, and the timer (Player.h:538-543, :1481-1496).
func TestQuestLog(t *testing.T) {
	const me = 0x2A
	s := NewStore()
	if s.QuestLog(me) != nil {
		t.Fatal("log of an unknown player")
	}
	log := func(slot int) uint16 { return PlayerQuestLog1 + uint16(3*slot) }
	s.Apply([]Block{{Type: BlockCreate2, GUID: me, ObjType: TypePlayer, Values: map[uint16]uint32{
		log(0): 100, log(0) + 1: 3 | 2<<6 | 0x01<<24, // 3 kills, 2 items, complete
		log(5): 101, log(5) + 1: 0x02 << 24, // failed
		log(19): 102,
	}}})
	want := []QuestSlot{
		{Slot: 0, ID: 100, Complete: true, Counts: [4]uint8{3, 2, 0, 0}},
		{Slot: 5, ID: 101, Failed: true},
		{Slot: 19, ID: 102},
	}
	if got := s.QuestLog(me); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
	s.Apply([]Block{{Type: BlockValues, GUID: me, Values: map[uint16]uint32{log(0): 0}}})
	if got := s.QuestLog(me); len(got) != 2 || got[0].ID != 101 {
		t.Fatalf("%+v", got)
	}
}
