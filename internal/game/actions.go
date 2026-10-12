package game

import (
	"fmt"
	"math"

	"github.com/pjerra/turtle-agent/internal/packet"
)

// BuildSetSelection is CMSG_SET_SELECTION (MiscHandler.cpp:488-493).
func BuildSetSelection(g packet.GUID) []byte { return packet.NewWriter().GUID(g).Bytes() }

// BuildAttackSwing is CMSG_ATTACKSWING (CombatHandler.cpp:30-33); CMSG_ATTACKSTOP is empty.
func BuildAttackSwing(g packet.GUID) []byte { return packet.NewWriter().GUID(g).Bytes() }

// Spell target flags (DBCEnums.h:131-148).
const (
	TargetFlagSelf           uint16 = 0x0000
	TargetFlagUnit           uint16 = 0x0002
	TargetFlagItem           uint16 = 0x0010
	TargetFlagSourceLocation uint16 = 0x0020
	TargetFlagDestLocation   uint16 = 0x0040
	TargetFlagObjectUnk      uint16 = 0x0080
	TargetFlagPvPCorpse      uint16 = 0x0200
	TargetFlagObject         uint16 = 0x0800
	TargetFlagTradeItem      uint16 = 0x1000
	TargetFlagString         uint16 = 0x2000
	TargetFlagCorpse         uint16 = 0x8000
)

// BuildCastSpell is CMSG_CAST_SPELL (SpellHandler.cpp:325-328, Spell.cpp:180-226): a zero
// target casts on the caster.
func BuildCastSpell(spell uint32, target packet.GUID) []byte {
	w := packet.NewWriter().U32(spell)
	if target == 0 {
		return w.U16(TargetFlagSelf).Bytes()
	}
	return w.U16(TargetFlagUnit).PackedGUID(target).Bytes()
}

// BuildMovement is a client MSG_MOVE_*: MovementInfo, no guid (MovementHandler.cpp:295-321).
func BuildMovement(m MoveInfo) []byte {
	w := packet.NewWriter()
	WriteMoveInfo(w, m)
	return w.Bytes()
}

// Facing is the orientation from (x0, y0) towards (x1, y1), in [0, 2π).
func Facing(x0, y0, x1, y1 float32) float32 {
	a := math.Atan2(float64(y1-y0), float64(x1-x0))
	if a < 0 {
		a += 2 * math.Pi
	}
	return float32(a)
}

type TeleportAck struct {
	GUID    packet.GUID
	Counter uint32
	Move    MoveInfo
}

// ParseTeleportAck decodes the server's MSG_MOVE_TELEPORT_ACK (MovementPacketSender.cpp:155-158).
func ParseTeleportAck(body []byte) (TeleportAck, error) {
	r := packet.NewReader(body)
	a := TeleportAck{GUID: r.PackedGUID(), Counter: r.U32()}
	a.Move = ReadMoveInfo(r)
	if err := r.Err(); err != nil {
		return TeleportAck{}, fmt.Errorf("MSG_MOVE_TELEPORT_ACK: %w", err)
	}
	return a, nil
}

// BuildTeleportAck is the client's MSG_MOVE_TELEPORT_ACK (MovementHandler.cpp:228-233).
func BuildTeleportAck(g packet.GUID, counter, clientTime uint32) []byte {
	return packet.NewWriter().GUID(g).U32(counter).U32(clientTime).Bytes()
}

type NewWorld struct {
	Map        uint32
	X, Y, Z, O float32
}

// ParseNewWorld decodes SMSG_NEW_WORLD (Player.cpp:2827-2842).
func ParseNewWorld(body []byte) (NewWorld, error) {
	r := packet.NewReader(body)
	n := NewWorld{Map: r.U32(), X: r.F32(), Y: r.F32(), Z: r.F32(), O: r.F32()}
	if err := r.Err(); err != nil {
		return NewWorld{}, fmt.Errorf("SMSG_NEW_WORLD: %w", err)
	}
	return n, nil
}

// ParseTransferPending returns the destination map (Player.cpp:2784-2791).
func ParseTransferPending(body []byte) (uint32, error) {
	r := packet.NewReader(body)
	m := r.U32()
	return m, r.Err()
}

type MovementChange struct {
	GUID    packet.GUID
	Counter uint32
	Speed   float32 // speed changes only
}

// ParseMovementChange decodes SMSG_FORCE_*_SPEED_CHANGE (hasSpeed) and SMSG_FORCE_MOVE_(UN)ROOT
// (MovementPacketSender.cpp:57-66, :259-276).
func ParseMovementChange(body []byte, hasSpeed bool) (MovementChange, error) {
	r := packet.NewReader(body)
	c := MovementChange{GUID: r.PackedGUID(), Counter: r.U32()}
	if hasSpeed {
		c.Speed = r.F32()
	}
	if err := r.Err(); err != nil {
		return MovementChange{}, fmt.Errorf("movement change: %w", err)
	}
	return c, nil
}

// BuildMovementChangeAck acknowledges a forced change: u64 guid, u32 counter, MovementInfo,
// and the new speed for speed changes (MovementHandler.cpp:430-443, :657-668). The server
// matches the echoed speed within 0.01 (Unit.cpp:7497-7523). A nil speed also gives
// CMSG_MOVE_KNOCK_BACK_ACK (MovementHandler.cpp:739-751), whose MovementInfo must carry
// MoveJumping and the knockback's values (Unit.cpp:7479-7495). The water-walk, hover and
// feather-fall acks add a u32 apply after MovementInfo (MovementHandler.cpp:543-558); this
// builder does not write it.
func BuildMovementChangeAck(g packet.GUID, counter uint32, m MoveInfo, speed *float32) []byte {
	w := packet.NewWriter().GUID(g).U32(counter)
	WriteMoveInfo(w, m)
	if speed != nil {
		w.F32(*speed)
	}
	return w.Bytes()
}

type AttackPair struct {
	Attacker, Victim packet.GUID
}

// ParseAttackStart decodes SMSG_ATTACKSTART (Unit.cpp:2704-2709).
func ParseAttackStart(body []byte) (AttackPair, error) {
	r := packet.NewReader(body)
	a := AttackPair{Attacker: r.GUID(), Victim: r.GUID()}
	return a, r.Err()
}

// ParseAttackStop decodes SMSG_ATTACKSTOP (Unit.cpp:2715-2723).
func ParseAttackStop(body []byte) (AttackPair, error) {
	r := packet.NewReader(body)
	a := AttackPair{Attacker: r.PackedGUID(), Victim: r.PackedGUID()}
	r.U32()
	return a, r.Err()
}
