package objects

import (
	"fmt"

	"github.com/pjerra/turtle-agent/internal/packet"
)

// SubPacket is one packet bundled in SMSG_COMPRESSED_MOVES.
type SubPacket struct {
	Op   uint16
	Body []byte
}

// SplitCompressedMoves decodes SMSG_COMPRESSED_MOVES: inflate, then entries of u8 size
// (opcode + body), u16 opcode, body (UpdateData.cpp:187-217).
func SplitCompressedMoves(body []byte) ([]SubPacket, error) {
	plain, err := Inflate(body)
	if err != nil {
		return nil, fmt.Errorf("SMSG_COMPRESSED_MOVES: %w", err)
	}
	r := packet.NewReader(plain)
	var out []SubPacket
	for r.Remaining() > 0 {
		size := int(r.U8())
		if size < 2 {
			return out, fmt.Errorf("SMSG_COMPRESSED_MOVES: entry size %d", size)
		}
		op := r.U16()
		b := r.Bytes(size - 2)
		if err := r.Err(); err != nil {
			return out, fmt.Errorf("SMSG_COMPRESSED_MOVES: %w", err)
		}
		out = append(out, SubPacket{Op: op, Body: b})
	}
	return out, nil
}

// MonsterMove is the part of SMSG_MONSTER_MOVE the agent uses.
type MonsterMove struct {
	GUID     packet.GUID
	Start    [3]float32
	ID       uint32
	Stop     bool
	Dest     [3]float32
	Duration uint32
}

func vec(r *packet.Reader) [3]float32 { return [3]float32{r.F32(), r.F32(), r.F32()} }

// ParseMonsterMove decodes SMSG_MONSTER_MOVE (MoveSplineInit.cpp:134-156, packet_builder.cpp:45-152).
func ParseMonsterMove(body []byte) (MonsterMove, error) {
	r := packet.NewReader(body)
	var m MonsterMove
	m.GUID = r.PackedGUID()
	m.Start = vec(r)
	m.ID = r.U32()
	switch t := r.U8(); t { // MonsterMoveType (packet_builder.cpp:36-43)
	case 0: // normal
	case 1: // stop
		m.Stop, m.Dest = true, m.Start
		if err := r.Err(); err != nil {
			return m, fmt.Errorf("SMSG_MONSTER_MOVE: %w", err)
		}
		return m, nil
	case 2: // facing spot
		r.Skip(12)
	case 3: // facing target
		r.Skip(8)
	case 4: // facing angle
		r.Skip(4)
	default:
		if r.Err() == nil {
			return m, fmt.Errorf("SMSG_MONSTER_MOVE: facing type %d", t)
		}
	}
	flags := r.U32()
	m.Duration = r.U32()
	count := r.U32()
	if flags&splineFlying != 0 && r.Err() == nil { // smooth path: count points, the last is the destination (:115-127)
		if count == 0 || uint64(count)*12 > uint64(r.Remaining()) {
			return m, fmt.Errorf("SMSG_MONSTER_MOVE: smooth path of %d points in %d bytes", count, r.Remaining())
		}
		r.Skip(int(count-1) * 12)
	}
	m.Dest = vec(r) // linear path: destination first, packed offsets follow (:78-112)
	if err := r.Err(); err != nil {
		return m, fmt.Errorf("SMSG_MONSTER_MOVE: %w", err)
	}
	return m, nil
}
