package session

import (
	"fmt"
	"math"
	"time"

	"github.com/pjerra/turtle-agent/internal/events"
	"github.com/pjerra/turtle-agent/internal/game"
	"github.com/pjerra/turtle-agent/internal/opcode"
)

// maxFlight bounds a knockback flight; a longer one lands early at the starting height.
const maxFlight = 10 * time.Second

// lastMove is the movement the server last has for the player: our last movement packet,
// or a position the server sent, which it holds as standing still there.
type lastMove struct {
	pos    [3]float32
	ctime  uint32
	flags  uint32
	jumpXY float32
}

// stepMargin is the slack the session allows a step over the anticheat's bound: half the
// 0.5 yard the server adds (Movement.cpp:576-578), the other half kept in reserve.
const stepMargin = 0.25

// checkStep holds a walk or facing packet to the distance the anticheat allows after the
// last movement (Movement::GetMaxAllowedDist, Movement.cpp:849-890, checked at :574-595):
// nothing after standing still, the run speed for the client-time gap while running, the
// jump speed across and the terminal speed up or down in the air. A z change past the bound
// is CHEAT_TYPE_OVERSPEED_Z (:593-595).
func (s *Session) checkStep(next game.MoveInfo) error {
	l := s.last
	dt := 0.0
	if next.Time > l.ctime {
		dt = float64(next.Time-l.ctime) / 1000
	}
	var dxy, dz float64
	switch {
	case l.flags&game.MoveMaskMoving == 0:
	case l.flags&(game.MoveJumping|game.MoveFallingFar) != 0:
		term := game.TerminalVelocity
		if l.flags&game.MoveSafeFall != 0 {
			term = game.TerminalSafeFall
		}
		dxy, dz = float64(l.jumpXY)*dt, term*dt
	default:
		dxy, dz = float64(s.runSpeed)*dt, float64(s.runSpeed)*dt
	}
	if d := math.Abs(float64(next.Z - l.pos[2])); d > dz+stepMargin {
		return fmt.Errorf("a %.2f yd change of height in %.3f s is over the %.2f yd the server allows (OVERSPEED_Z)", d, dt, dz)
	}
	if d := math.Hypot(float64(next.X-l.pos[0]), float64(next.Y-l.pos[1])); d > dxy+stepMargin {
		return fmt.Errorf("a %.2f yd step in %.3f s is over the %.2f yd the server allows", d, dt, dxy)
	}
	return nil
}

// sendMove writes a packet that carries mi and records mi as what the server now has.
func (s *Session) sendMove(op uint16, mi game.MoveInfo, body []byte) error {
	if err := s.conn.WritePacket(op, body); err != nil {
		return err
	}
	s.last = lastMove{pos: [3]float32{mi.X, mi.Y, mi.Z}, ctime: mi.Time, flags: mi.Flags, jumpXY: mi.JumpXYSpeed}
	return nil
}

// serverPosition records a position the server put the player at. zKnown is false when
// the z may be in the air (a GM teleport); the next position from the server settles it.
func (s *Session) serverPosition(p [3]float32, zKnown bool) {
	s.fall = nil
	s.last = lastMove{pos: p, ctime: s.last.ctime}
	s.mu.Lock()
	s.pos[0], s.pos[1], s.pos[2] = p[0], p[1], p[2]
	s.falling, s.zUnknown = false, !zKnown
	s.mu.Unlock()
}

// fallState is a knockback flight: a ballistic arc from where the server threw the player.
// A feather-fall order mid-flight restarts the arc from where it is (restart).
type fallState struct {
	kb       game.KnockBack // direction, xy speed, and the jump values every packet echoes
	began    time.Time      // the knockback; the MovementInfo fall time counts from here
	groundZ  float32        // the ground assumed: the height the flight started at
	from     [3]float32     // where the current arc starts
	start    time.Time      // and when
	v0, term float64        // its downward speed then (negative: up) and the speed cap
	land     time.Time      // back at groundZ
	nextBeat time.Time
}

// arc starts the current arc at from and start with downward speed v0 under the cap term.
// The speed is held to the cap both ways: with MoveSafeFall the server bounds a step up or
// down by 7 yd/s (Movement.cpp:874-881).
func (f *fallState) arc(from [3]float32, start time.Time, v0, term float64) {
	f.from, f.start, f.term = from, start, term
	f.v0 = max(min(v0, term), -term)
	d := time.Duration(game.FallTimeTo(float64(from[2]-f.groundZ), f.v0, term) * float64(time.Second))
	f.land = start.Add(d)
	if limit := f.began.Add(maxFlight); f.land.After(limit) {
		f.land = limit
	}
}

// restart continues the flight from where it is at now, at the speed it has, under a new
// cap: the fall a feather-fall order changes mid-air.
func (f *fallState) restart(now time.Time, term float64) {
	t := min(max(now.Sub(f.start), 0), f.land.Sub(f.start)).Seconds()
	f.arc(f.at(now), now, game.FallSpeed(t, f.v0, f.term), term)
}

func (f *fallState) at(now time.Time) [3]float32 {
	t := min(max(now.Sub(f.start), 0), f.land.Sub(f.start)).Seconds()
	xy := float64(f.kb.XYSpeed) * t
	return [3]float32{f.from[0] + f.kb.Cos*float32(xy), f.from[1] + f.kb.Sin*float32(xy),
		f.from[2] - float32(game.FallDrop(t, f.v0, f.term))}
}

func fallTerm(toggles uint32) float64 {
	if toggles&game.MoveSafeFall != 0 {
		return game.TerminalSafeFall
	}
	return game.TerminalVelocity
}

// syncFall moves s.pos to where the flight is at now.
func (s *Session) syncFall(now time.Time) {
	if s.fall == nil {
		return
	}
	p := s.fall.at(now)
	s.mu.Lock()
	s.pos[0], s.pos[1], s.pos[2] = p[0], p[1], p[2]
	s.mu.Unlock()
}

// onKnockBack acks SMSG_MOVE_KNOCK_BACK and starts the flight. The server throws the player
// from the position it last had (Unit.cpp:10662-10682, Movement.cpp:103-115): a walk's last
// heartbeat, not where the walk has got to since. The ack carries MoveJumping and the
// order's values (Unit.cpp:7479-7495). The server sends no knockback while the player is
// rooted or stunned (:10664).
func (s *Session) onKnockBack(body []byte) error {
	k, err := game.ParseKnockBack(body)
	if err != nil {
		return err
	}
	if k.GUID != s.selfGUID() {
		return nil
	}
	now := time.Now()
	s.stopWalk(errKnockedBack, false)
	from := s.last.pos
	s.fall = &fallState{kb: k, began: now, groundZ: from[2], nextBeat: now.Add(500 * time.Millisecond)}
	s.fall.arc(from, now, float64(k.ZSpeed), fallTerm(s.toggles))
	s.mu.Lock()
	s.pos[0], s.pos[1], s.pos[2] = from[0], from[1], from[2]
	s.falling = true
	mi := s.moveInfoAt(s.currentFlags(), now)
	s.mu.Unlock()
	if err := s.sendMove(opcode.CMSGMoveKnockBackAck, mi, game.BuildKnockBackAck(k, mi)); err != nil {
		return &fatalError{err}
	}
	s.emit(events.Event{Kind: events.KindSession, Data: map[string]any{"state": "knocked_back", "x": from[0], "y": from[1], "z": from[2],
		"speed_xy": k.XYSpeed, "speed_z": k.ZSpeed, "flight_ms": s.fall.land.Sub(now).Milliseconds()}})
	return nil
}

// tickFall sends the flight's heartbeats every 500 ms and MSG_MOVE_FALL_LAND when the arc
// is back at the starting height. With no terrain the agent cannot know the ground: the
// landing z is the starting z until the server sends a position (Status.ZUnknown).
func (s *Session) tickFall() error {
	f := s.fall
	if f == nil {
		return nil
	}
	now := time.Now()
	if !now.Before(f.land) {
		p := f.at(f.land)
		s.fall = nil
		s.mu.Lock()
		s.pos[0], s.pos[1], s.pos[2] = p[0], p[1], p[2]
		s.falling, s.zUnknown = false, true
		mi := s.moveInfoAt(s.currentFlags(), now)
		s.mu.Unlock()
		mi.FallTime = uint32(f.land.Sub(f.began).Milliseconds())
		if err := s.sendMove(opcode.MSGMoveFallLand, mi, game.BuildMovement(mi)); err != nil {
			return err
		}
		s.emit(events.Event{Kind: events.KindSession, Data: map[string]any{"state": "landed", "x": p[0], "y": p[1], "z": p[2], "z_unknown": true}})
		return nil
	}
	if now.Before(f.nextBeat) {
		return nil
	}
	f.nextBeat = now.Add(500 * time.Millisecond)
	s.syncFall(now)
	s.mu.Lock()
	mi := s.moveInfoAt(s.currentFlags(), now)
	s.mu.Unlock()
	return s.sendMove(opcode.MSGMoveHeartbeat, mi, game.BuildMovement(mi))
}

// toggleOrder is one water-walk, hover or feather-fall order (MovementPacketSender.cpp:259-276)
// and its ack (Opcodes_1_12_1.h:225-226, :245-249, :722-723).
type toggleOrder struct {
	ack   uint16
	flag  uint32
	apply bool
}

var toggleOrders = map[uint16]toggleOrder{
	opcode.SMSGMoveWaterWalk:   {opcode.CMSGMoveWaterWalkAck, game.MoveWaterWalking, true},
	opcode.SMSGMoveLandWalk:    {opcode.CMSGMoveWaterWalkAck, game.MoveWaterWalking, false},
	opcode.SMSGMoveSetHover:    {opcode.CMSGMoveHoverAck, game.MoveHover, true},
	opcode.SMSGMoveUnsetHover:  {opcode.CMSGMoveHoverAck, game.MoveHover, false},
	opcode.SMSGMoveFeatherFall: {opcode.CMSGMoveFeatherFallAck, game.MoveSafeFall, true},
	opcode.SMSGMoveNormalFall:  {opcode.CMSGMoveFeatherFallAck, game.MoveSafeFall, false},
}

// toggleHandler acks a toggle order with its counter, the MovementInfo with the flag as
// now applied, and the apply u32 the server matches against the order
// (MovementHandler.cpp:543-558, Unit.cpp:7437-7449). The flag stays in later movement, as
// a real client keeps it. Feather fall applied or removed mid-flight restarts the arc under
// the new speed cap, which the server applies from this ack on (Movement.cpp:874-881).
func (s *Session) toggleHandler(o toggleOrder) handler {
	return func(body []byte) error {
		c, err := game.ParseMovementChange(body, false)
		if err != nil {
			return err
		}
		self := s.selfGUID()
		if c.GUID != self {
			return nil
		}
		if o.apply {
			s.toggles |= o.flag
		} else {
			s.toggles &^= o.flag
		}
		now := time.Now()
		s.syncWalk(now)
		if f := s.fall; f != nil && o.flag == game.MoveSafeFall {
			f.restart(now, fallTerm(s.toggles))
			s.syncWalk(now)
		}
		s.mu.Lock()
		mi := s.moveInfoAt(s.currentFlags(), now)
		s.mu.Unlock()
		if err := s.sendMove(o.ack, mi, game.BuildMovementToggleAck(self, c.Counter, mi, o.apply)); err != nil {
			return &fatalError{err}
		}
		return nil
	}
}
