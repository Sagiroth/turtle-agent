package game

import (
	"fmt"

	"github.com/pjerra/turtle-agent/internal/packet"
)

// Core cites in this file are tortoise-wow upstream/1181dev 5784fb0.

// BuildTrainerList is CMSG_TRAINER_LIST: the trainer's GUID (NPCHandler.cpp:108-115).
func BuildTrainerList(trainer packet.GUID) []byte { return packet.NewWriter().GUID(trainer).Bytes() }

// BuildTrainerBuySpell is CMSG_TRAINER_BUY_SPELL: trainer GUID and the spell id as the list
// shows it (NPCHandler.cpp:282-290).
func BuildTrainerBuySpell(trainer packet.GUID, spell uint32) []byte {
	return packet.NewWriter().GUID(trainer).U32(spell).Bytes()
}

// Trainer spell states (Player.h:194-197); the server never sends the custom "disabled" one.
const (
	TrainerGreen uint8 = 0 // can be learned
	TrainerRed   uint8 = 1 // not yet: level, skill, money or a missing earlier rank
	TrainerGray  uint8 = 2 // already known
)

// TrainerStateName is learnable, unavailable or known.
func TrainerStateName(state uint8) string {
	switch state {
	case TrainerGreen:
		return "learnable"
	case TrainerRed:
		return "unavailable"
	case TrainerGray:
		return "known"
	}
	return fmt.Sprintf("unknown(%d)", state)
}

// TrainerSpell is one line of SMSG_TRAINER_LIST, 38 bytes (SendTrainerSpellHelper,
// NPCHandler.cpp:117-159).
type TrainerSpell struct {
	Spell         uint32 `json:"spell_id"`
	State         string `json:"state"` // learnable, unavailable or known
	Cost          uint32 `json:"cost"`  // copper, after the reputation discount
	Level         uint8  `json:"level"` // level required
	ReqSkill      uint32 `json:"req_skill,omitempty"`
	ReqSkillValue uint32 `json:"req_skill_value,omitempty"`
	ReqSpell1     uint32 `json:"req_spell_1,omitempty"` // earlier rank or other spell needed first
	ReqSpell2     uint32 `json:"req_spell_2,omitempty"`
	// PrimaryProfession is set for the first rank of a primary profession, which takes one
	// of the character's profession points.
	PrimaryProfession bool `json:"primary_profession,omitempty"`
}

// TrainerList is SMSG_TRAINER_LIST (NPCHandler.cpp:161-263). Type is TRAINER_TYPE_* (0 class,
// 1 mounts, 2 tradeskills, 3 pets, SharedDefines.h:954-957).
type TrainerList struct {
	Trainer  packet.GUID
	Type     uint32
	Spells   []TrainerSpell
	Greeting string
}

func ParseTrainerList(body []byte) (TrainerList, error) {
	r := packet.NewReader(body)
	l := TrainerList{Trainer: r.GUID(), Type: r.U32()}
	for i, n := uint32(0), r.U32(); i < n && r.Err() == nil; i++ {
		s := TrainerSpell{Spell: r.U32(), State: TrainerStateName(r.U8()), Cost: r.U32()}
		r.U32() // can learn a primary profession (this player has a free point)
		s.PrimaryProfession = r.U32() != 0
		s.Level = r.U8()
		s.ReqSkill, s.ReqSkillValue = r.U32(), r.U32()
		s.ReqSpell1, s.ReqSpell2 = r.U32(), r.U32()
		r.U32() // always 0
		if r.Err() == nil {
			l.Spells = append(l.Spells, s)
		}
	}
	l.Greeting = r.CString()
	return l, done(r, "SMSG_TRAINER_LIST")
}

// TrainerSucceeded is SMSG_TRAINER_BUY_SUCCEEDED (NPCHandler.cpp:264-270).
type TrainerSucceeded struct {
	Trainer packet.GUID
	Spell   uint32
}

func ParseTrainerSucceeded(body []byte) (TrainerSucceeded, error) {
	r := packet.NewReader(body)
	s := TrainerSucceeded{Trainer: r.GUID(), Spell: r.U32()}
	return s, done(r, "SMSG_TRAINER_BUY_SUCCEEDED")
}

// TrainerFailed is SMSG_TRAINER_BUY_FAILED: the spell id of the request and a TRAIN_FAIL_*
// code (NPCHandler.cpp:272-280, SharedDefines.h:1294-1296).
type TrainerFailed struct {
	Trainer packet.GUID
	Spell   uint32
	Code    uint32
}

func ParseTrainerFailed(body []byte) (TrainerFailed, error) {
	r := packet.NewReader(body)
	f := TrainerFailed{Trainer: r.GUID(), Spell: r.U32(), Code: r.U32()}
	return f, done(r, "SMSG_TRAINER_BUY_FAILED")
}

// TrainerFailName is unavailable, not_enough_money or not_enough_skill.
func TrainerFailName(code uint32) string {
	switch code {
	case 0:
		return "unavailable"
	case 1:
		return "not_enough_money"
	case 2:
		return "not_enough_skill"
	}
	return fmt.Sprintf("unknown(%d)", code)
}
