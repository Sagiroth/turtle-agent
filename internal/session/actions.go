package session

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/pjerra/turtle-agent/internal/events"
	"github.com/pjerra/turtle-agent/internal/game"
	"github.com/pjerra/turtle-agent/internal/objects"
	"github.com/pjerra/turtle-agent/internal/opcode"
	"github.com/pjerra/turtle-agent/internal/packet"
)

const (
	maxWalk         = 40  // yards; longer moves use Teleport
	defaultRunSpeed = 7.0 // yards per second until the server sets another
	// maxWalkTime keeps a slow walk inside the 35 s an RPC client waits for walk_to
	// (wow-play-harness HeadlessClient); a 40-yard walk at the 7 yd/s run speed takes 5.7 s.
	maxWalkTime = 30 * time.Second
)

type walkState struct {
	from, to [3]float32
	o        float32
	start    time.Time
	dur      time.Duration
	nextBeat time.Time
	done     chan error
}

// at is the walk's position at now, clamped to the segment.
func (w *walkState) at(now time.Time) [3]float32 {
	frac := float32(1)
	if w.dur > 0 {
		frac = float32(now.Sub(w.start)) / float32(w.dur)
	}
	frac = min(max(frac, 0), 1)
	var p [3]float32
	for i := range p {
		p[i] = w.from[i] + (w.to[i]-w.from[i])*frac
	}
	return p
}

// speedAcks maps each forced speed change to its ack (Opcodes_1_12_1.h:229-238, :733-738).
var speedAcks = map[uint16]uint16{
	opcode.SMSGForceRunSpeedChange:      opcode.CMSGForceRunSpeedChangeAck,
	opcode.SMSGForceRunBackSpeedChange:  opcode.CMSGForceRunBackSpeedChangeAck,
	opcode.SMSGForceSwimSpeedChange:     opcode.CMSGForceSwimSpeedChangeAck,
	opcode.SMSGForceWalkSpeedChange:     opcode.CMSGForceWalkSpeedChangeAck,
	opcode.SMSGForceSwimBackSpeedChange: opcode.CMSGForceSwimBackSpeedChangeAck,
	opcode.SMSGForceTurnRateChange:      opcode.CMSGForceTurnRateChangeAck,
}

var swingErrors = map[uint16]string{
	opcode.SMSGAttackswingNotinrange:  "not_in_range",
	opcode.SMSGAttackswingBadfacing:   "bad_facing",
	opcode.SMSGAttackswingNotstanding: "not_standing",
	opcode.SMSGAttackswingDeadtarget:  "dead_target",
	opcode.SMSGAttackswingCantAttack:  "cant_attack",
}

func (s *Session) registerActions() {
	s.handlers[opcode.MSGMoveTeleportAck] = s.onTeleportAck
	s.handlers[opcode.SMSGTransferPending] = s.onTransferPending
	s.handlers[opcode.SMSGNewWorld] = s.onNewWorld
	for op, ack := range speedAcks {
		s.handlers[op] = s.speedHandler(op, ack)
	}
	s.handlers[opcode.SMSGForceMoveRoot] = s.rootHandler(opcode.CMSGForceMoveRootAck, true)
	s.handlers[opcode.SMSGForceMoveUnroot] = s.rootHandler(opcode.CMSGForceMoveUnrootAck, false)
	s.handlers[opcode.SMSGMoveKnockBack] = s.onKnockBack
	for op, o := range toggleOrders {
		s.handlers[op] = s.toggleHandler(o)
	}
	s.handlers[opcode.SMSGAttackstart] = s.onAttackStart
	s.handlers[opcode.SMSGAttackstop] = s.onAttackStop
	for op, reason := range swingErrors {
		s.handlers[op] = func([]byte) error {
			s.emit(events.Event{Kind: events.KindAttackError, Text: "attack swing failed: " + reason, Data: map[string]any{"reason": reason}})
			return nil
		}
	}
}

func (s *Session) inWorld() error {
	if st := s.State(); st != StatusInWorld {
		return fmt.Errorf("%w (state %s)", ErrNotInWorld, st)
	}
	return nil
}

// clockAt is the MovementInfo client time at now: milliseconds on the session's monotonic
// clock, plus one, because the anticheat flags a zero time and a clock that runs backwards
// or more than 5 s ahead of the server's (Movement.cpp:377, :434-475). It never goes back,
// even for a now read before the last packet's. Owner loop only.
func (s *Session) clockAt(now time.Time) uint32 {
	c := max(uint32(now.Sub(s.start).Milliseconds())+1, s.lastClock)
	s.lastClock = c
	return c
}

func (s *Session) clock() uint32 { return s.clockAt(time.Now()) }

// moveInfoAt is the player's MovementInfo at now, its client time read from the same
// instant as the position, so a step never covers more ground than its time gap allows. In
// the air it carries the knockback's jump values and the time fallen. The caller holds s.mu.
func (s *Session) moveInfoAt(flags uint32, now time.Time) game.MoveInfo {
	m := game.MoveInfo{Flags: flags, Time: s.clockAt(now), X: s.pos[0], Y: s.pos[1], Z: s.pos[2], O: s.pos[3]}
	if f := s.fall; f != nil && flags&game.MoveJumping != 0 {
		m.FallTime = uint32(max(now.Sub(f.start), 0).Milliseconds())
		m.JumpZSpeed, m.JumpCos, m.JumpSin, m.JumpXYSpeed = f.kb.ZSpeed, f.kb.Cos, f.kb.Sin, f.kb.XYSpeed
	}
	return m
}

// currentFlags are the movement flags the player has now. MoveRoot stays on while rooted,
// or the anticheat records ROOT_MOVE (Movement.cpp:186-204); water walking, hover and
// feather fall stay on while the server has them applied.
func (s *Session) currentFlags() uint32 {
	f := s.toggles
	if s.walk != nil {
		f |= game.MoveForward
	}
	if s.fall != nil {
		f |= game.MoveJumping
	}
	if s.rooted {
		f |= game.MoveRoot
	}
	return f
}

// syncWalk moves s.pos to where the running walk or flight is at now.
func (s *Session) syncWalk(now time.Time) {
	s.syncFall(now)
	if s.walk == nil {
		return
	}
	p := s.walk.at(now)
	s.mu.Lock()
	s.pos[0], s.pos[1], s.pos[2] = p[0], p[1], p[2]
	s.mu.Unlock()
}

// airborne refuses an action the player cannot take in the air.
func (s *Session) airborne(what string) error {
	if f := s.fall; f != nil {
		return fmt.Errorf("in the air after a knockback; %s waits for the landing in %.1f s", what, time.Until(f.start.Add(f.land)).Seconds())
	}
	return nil
}

// Target selects a unit by guid, or by name, and returns it. A name picks the nearest
// living match; a corpse (the last run's kill) only when no living one is in view, and the
// returned unit then says dead. A guid selects exactly that unit, dead or alive.
func (s *Session) Target(ctx context.Context, g packet.GUID, name string) (objects.Unit, error) {
	g, err := s.pickUnit("target", g, name)
	if err != nil {
		return objects.Unit{}, err
	}
	_, err = s.do(ctx, func() (any, error) {
		if err := s.inWorld(); err != nil {
			return nil, err
		}
		if err := s.conn.WritePacket(opcode.CMSGSetSelection, game.BuildSetSelection(g)); err != nil {
			return nil, err
		}
		s.mu.Lock()
		s.target = g
		s.mu.Unlock()
		return nil, nil
	})
	if err != nil {
		return objects.Unit{}, err
	}
	x, y, z, _ := s.selfPos()
	u, _ := s.store.Get(g, x, y, z)
	u.GUID = g
	u.Name, _ = s.cachedName(g)
	u.InParty = s.inParty(g)
	return u, nil
}

// pickUnit returns g, or for a name the nearest living unit whose name contains it, or
// the nearest such corpse when none lives. verb names the caller in errors.
func (s *Session) pickUnit(verb string, g packet.GUID, name string) (packet.GUID, error) {
	if g != 0 {
		return g, nil
	}
	if name == "" {
		return 0, fmt.Errorf("%s needs a guid or a name", verb)
	}
	us := s.Units(objects.Filter{Name: name, Limit: math.MaxInt})
	if len(us) == 0 {
		return 0, fmt.Errorf("no unit named %q nearby", name)
	}
	for _, u := range us {
		if !u.Dead {
			return u.GUID, nil
		}
	}
	return us[0].GUID, nil
}

func (s *Session) Attack(ctx context.Context) error {
	_, err := s.do(ctx, func() (any, error) {
		if err := s.inWorld(); err != nil {
			return nil, err
		}
		s.mu.Lock()
		t := s.target
		s.mu.Unlock()
		if t == 0 {
			return nil, errors.New("no target selected; call target first")
		}
		return nil, s.conn.WritePacket(opcode.CMSGAttackswing, game.BuildAttackSwing(t))
	})
	return err
}

func (s *Session) StopAttack(ctx context.Context) error {
	return s.send(ctx, opcode.CMSGAttackstop, nil)
}

// Cast casts spell on target, or on the current target when target is 0, or on the
// player when nothing is selected.
func (s *Session) Cast(ctx context.Context, spell uint32, target packet.GUID) error {
	if spell == 0 {
		return errors.New("cast needs a spell id")
	}
	_, err := s.do(ctx, func() (any, error) {
		if err := s.inWorld(); err != nil {
			return nil, err
		}
		t := target
		if t == 0 {
			s.mu.Lock()
			t = s.target
			s.mu.Unlock()
		}
		return nil, s.conn.WritePacket(opcode.CMSGCastSpell, game.BuildCastSpell(spell, t))
	})
	return err
}

// CancelCast cancels the cast, channel or auto-repeat of spell, or of any spell when spell
// is 0 (CMSG_CANCEL_CAST, SpellHandler.cpp:411-426, Object.cpp:5428-5440).
func (s *Session) CancelCast(ctx context.Context, spell uint32) error {
	return s.send(ctx, opcode.CMSGCancelCast, game.BuildCancelCast(spell))
}

// Face turns the player towards a known unit (MSG_MOVE_SET_FACING).
func (s *Session) Face(ctx context.Context, g packet.GUID) error {
	x, y, z, _ := s.selfPos()
	u, ok := s.store.Get(g, x, y, z)
	if !ok {
		return fmt.Errorf("unit %s is not in view", g)
	}
	o := game.Facing(x, y, u.X, u.Y)
	_, err := s.do(ctx, func() (any, error) {
		if err := s.inWorld(); err != nil {
			return nil, err
		}
		if s.walk != nil {
			return nil, errors.New("walking; wait for walk_to to finish")
		}
		if err := s.airborne("face"); err != nil {
			return nil, err
		}
		s.mu.Lock()
		s.pos[3] = o
		mi := s.moveInfoAt(s.currentFlags(), time.Now())
		s.mu.Unlock()
		if err := s.checkStep(mi); err != nil {
			return nil, fmt.Errorf("face refused: %w", err)
		}
		return nil, s.sendMove(opcode.MSGMoveSetFacing, mi, game.BuildMovement(mi))
	})
	return err
}

// WalkTo walks in a straight line to a point at most maxWalk yards away and returns on
// arrival. The walk is timed on the 3D distance, so it climbs or drops no faster than the
// run speed, the bound the anticheat holds a z change to (Movement.cpp:885-887, OVERSPEED_Z
// at :593-595); every step is checked against that bound before it is sent.
func (s *Session) WalkTo(ctx context.Context, x, y, z float32) error {
	v, err := s.do(ctx, func() (any, error) {
		if err := s.inWorld(); err != nil {
			return nil, err
		}
		if s.walk != nil {
			return nil, errors.New("already walking")
		}
		if s.rooted {
			return nil, errors.New("rooted; walk_to waits until the server releases the root")
		}
		if left := time.Until(s.splineUntil); left > 0 {
			return nil, fmt.Errorf("moved by the server (fear, charm or a scripted move); walk_to waits %.1f s until it ends", left.Seconds())
		}
		if err := s.airborne("walk_to"); err != nil {
			return nil, err
		}
		s.mu.Lock()
		from := [3]float32{s.pos[0], s.pos[1], s.pos[2]}
		s.mu.Unlock()
		dx, dy, dz := float64(x-from[0]), float64(y-from[1]), float64(z-from[2])
		dist := math.Sqrt(dx*dx + dy*dy + dz*dz)
		if dist > maxWalk {
			return nil, fmt.Errorf("walk_to is for short moves: %.1f yards is over %d, use teleport", dist, maxWalk)
		}
		done := make(chan error, 1)
		if dist < 0.1 {
			done <- nil
			return done, nil
		}
		dur := time.Duration(dist / float64(s.runSpeed) * float64(time.Second))
		if dur > maxWalkTime {
			return nil, fmt.Errorf("walk_to would take %.0f s at %.1f yd/s, over %s; walk a shorter way or teleport", dur.Seconds(), s.runSpeed, maxWalkTime)
		}
		now := time.Now()
		w := &walkState{from: from, to: [3]float32{x, y, z}, o: game.Facing(from[0], from[1], x, y), start: now,
			dur: dur, nextBeat: now.Add(500 * time.Millisecond), done: done}
		s.mu.Lock()
		s.pos[3] = w.o
		mi := s.moveInfoAt(s.currentFlags()|game.MoveForward, now)
		s.mu.Unlock()
		if err := s.checkStep(mi); err != nil {
			return nil, fmt.Errorf("walk_to refused: the start is not where the server has the player: %w", err)
		}
		if err := s.sendMove(opcode.MSGMoveStartForward, mi, game.BuildMovement(mi)); err != nil {
			return nil, err
		}
		s.walk = w
		return done, nil
	})
	if err != nil {
		return err
	}
	done := v.(chan error)
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		_, _ = s.do(context.Background(), func() (any, error) {
			if s.walk != nil && s.walk.done == done { // not a walk started after this one ended
				s.stopWalk(errors.New("walk cancelled"), true)
			}
			return nil, nil
		})
		return ctx.Err()
	case <-s.done:
		return ErrClosed
	}
}

// tickWalk advances a walk: heartbeat every 500 ms, stop at the destination. It reads the
// clock once, not the tick's send time: a tick read late from a busy loop would place the
// heartbeat behind the time it carries, and the next step would exceed the run speed.
func (s *Session) tickWalk() error {
	w := s.walk
	if w == nil {
		return nil
	}
	now := time.Now()
	if !now.Before(w.start.Add(w.dur)) {
		s.walk = nil
		s.mu.Lock()
		s.pos = [4]float32{w.to[0], w.to[1], w.to[2], w.o}
		// A straight line that changed height may end above or below the ground.
		s.zUnknown = s.zUnknown || math.Abs(float64(w.to[2]-w.from[2])) > 0.5
		mi := s.moveInfoAt(s.currentFlags(), now)
		s.mu.Unlock()
		if err := s.checkStep(mi); err != nil {
			return s.abortWalk(w, err)
		}
		err := s.sendMove(opcode.MSGMoveStop, mi, game.BuildMovement(mi))
		w.done <- err
		return err
	}
	if now.Before(w.nextBeat) {
		return nil
	}
	w.nextBeat = now.Add(500 * time.Millisecond)
	s.syncWalk(now)
	s.mu.Lock()
	mi := s.moveInfoAt(s.currentFlags(), now)
	s.mu.Unlock()
	if err := s.checkStep(mi); err != nil {
		s.walk = nil
		return s.abortWalk(w, err)
	}
	return s.sendMove(opcode.MSGMoveHeartbeat, mi, game.BuildMovement(mi))
}

// abortWalk ends a walk whose next step would break the anticheat's bound: it stops where
// the server last had the player, a step of zero length, and reports why. s.walk is nil.
func (s *Session) abortWalk(w *walkState, reason error) error {
	s.mu.Lock()
	s.pos[0], s.pos[1], s.pos[2] = s.last.pos[0], s.last.pos[1], s.last.pos[2]
	mi := s.moveInfoAt(s.currentFlags(), time.Now())
	s.mu.Unlock()
	err := s.sendMove(opcode.MSGMoveStop, mi, game.BuildMovement(mi))
	w.done <- fmt.Errorf("walk stopped: %w", reason)
	return err
}

// stopWalk ends a walk early; sendStop is false when the server moved the player. The
// stop is sent from where the walk has got to and carries MoveRoot while rooted.
func (s *Session) stopWalk(reason error, sendStop bool) {
	w := s.walk
	if w == nil {
		return
	}
	now := time.Now()
	if sendStop {
		s.syncWalk(now)
	}
	s.walk = nil
	if sendStop {
		s.mu.Lock()
		mi := s.moveInfoAt(s.currentFlags(), now)
		s.mu.Unlock()
		if s.checkStep(mi) != nil {
			_ = s.abortWalk(w, reason)
			return
		}
		_ = s.sendMove(opcode.MSGMoveStop, mi, game.BuildMovement(mi))
	}
	w.done <- reason
}

// Teleport sends ".go xyz x y z [map]" (Commands.cpp:8347-8367) and waits up to 10 s for
// the server's teleport (MSG_MOVE_TELEPORT_ACK or SMSG_NEW_WORLD).
func (s *Session) Teleport(ctx context.Context, x, y, z float32, mapID int) error {
	cmd := fmt.Sprintf(".go xyz %.2f %.2f %.2f", x, y, z)
	if mapID >= 0 {
		cmd += fmt.Sprintf(" %d", mapID)
	}
	body, err := game.BuildMessageChat(game.ChatSay, "", cmd)
	if err != nil {
		return err
	}
	v, err := s.do(ctx, func() (any, error) {
		if err := s.inWorld(); err != nil {
			return nil, err
		}
		ch := make(chan struct{})
		s.teleportWaiters = append(s.teleportWaiters, ch)
		return ch, s.conn.WritePacket(opcode.CMSGMessagechat, body)
	})
	if err != nil {
		return err
	}
	tctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	ch := v.(chan struct{})
	select {
	case <-ch:
		return nil
	case <-tctx.Done():
		s.dropTeleportWaiter(ch)
		return fmt.Errorf("teleport: no MSG_MOVE_TELEPORT_ACK or SMSG_NEW_WORLD within 10 s (does the account have GM rights?): %w", tctx.Err())
	case <-s.done:
		return ErrClosed
	}
}

// dropTeleportWaiter forgets a waiter whose Teleport gave up, so waiters do not pile up
// until the next teleport. Best effort: the session may be closing.
func (s *Session) dropTeleportWaiter(ch chan struct{}) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	s.do(ctx, func() (any, error) {
		for i, w := range s.teleportWaiters {
			if w == ch {
				s.teleportWaiters = append(s.teleportWaiters[:i], s.teleportWaiters[i+1:]...)
				break
			}
		}
		return nil, nil
	})
}

func (s *Session) teleported() {
	for _, ch := range s.teleportWaiters {
		close(ch)
	}
	s.teleportWaiters = nil
}

func (s *Session) selfGUID() packet.GUID {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.self.GUID
}

func (s *Session) onTeleportAck(body []byte) error {
	a, err := game.ParseTeleportAck(body)
	if err != nil {
		return err
	}
	self := s.selfGUID()
	if a.GUID != self {
		return nil
	}
	s.stopWalk(errors.New("teleported by the server"), false)
	s.gossip = nil // the client closes an NPC window when the player is moved away
	// A .go xyz puts the player at the given z, perhaps in the air; the next position from
	// the server settles it (review M13).
	s.serverPosition([3]float32{a.Move.X, a.Move.Y, a.Move.Z}, false)
	s.mu.Lock()
	s.pos[3] = a.Move.O
	s.mu.Unlock()
	s.store.MoveTo(self, a.Move.X, a.Move.Y, a.Move.Z, a.Move.O)
	if err := s.conn.WritePacket(opcode.MSGMoveTeleportAck, game.BuildTeleportAck(self, a.Counter, s.clock())); err != nil {
		return &fatalError{err}
	}
	s.emit(events.Event{Kind: events.KindSession, Data: map[string]any{"state": "teleported", "x": a.Move.X, "y": a.Move.Y, "z": a.Move.Z}})
	s.teleported()
	return nil
}

func (s *Session) onTransferPending(body []byte) error {
	m, err := game.ParseTransferPending(body)
	if err != nil {
		return err
	}
	s.emit(events.Event{Kind: events.KindSession, Data: map[string]any{"state": "transfer_pending", "map": m}})
	return nil
}

// onNewWorld finishes a far teleport. The units of the old map are gone, the selection
// with them.
func (s *Session) onNewWorld(body []byte) error {
	nw, err := game.ParseNewWorld(body)
	if err != nil {
		return err
	}
	s.stopWalk(errors.New("teleported to another map"), false)
	s.gossip = nil
	s.serverPosition([3]float32{nw.X, nw.Y, nw.Z}, false)
	s.mu.Lock()
	s.mapID, s.pos = nw.Map, [4]float32{nw.X, nw.Y, nw.Z, nw.O}
	s.target = 0
	self := s.self.GUID
	s.mu.Unlock()
	s.store.Clear()
	if err := s.conn.WritePacket(opcode.MSGMoveWorldportAck, nil); err != nil { // MovementHandler.cpp:43-49
		return &fatalError{err}
	}
	if err := s.conn.WritePacket(opcode.CMSGSetActiveMover, packet.NewWriter().GUID(self).Bytes()); err != nil {
		return &fatalError{err}
	}
	s.emit(events.Event{Kind: events.KindSession, Data: map[string]any{"state": "new_world", "map": nw.Map, "x": nw.X, "y": nw.Y, "z": nw.Z}})
	s.teleported()
	return nil
}

// speedHandler acknowledges a forced speed change with its own counter and the speed it
// carried. A new run speed re-times a running walk from where it has got to.
func (s *Session) speedHandler(op, ack uint16) handler {
	return func(body []byte) error {
		c, err := game.ParseMovementChange(body, true)
		if err != nil {
			return err
		}
		self := s.selfGUID()
		if c.GUID != self {
			return nil
		}
		now := time.Now()
		s.syncWalk(now)
		if op == opcode.SMSGForceRunSpeedChange {
			s.setRunSpeed(c.Speed, now)
		}
		s.mu.Lock()
		mi := s.moveInfoAt(s.currentFlags(), now)
		s.mu.Unlock()
		speed := c.Speed
		return s.sendMove(ack, mi, game.BuildMovementChangeAck(self, c.Counter, mi, &speed))
	}
}

// setRunSpeed sets the run speed; a running walk keeps its course from where it has got
// to and takes the rest at the new speed.
func (s *Session) setRunSpeed(speed float32, now time.Time) {
	if speed <= 0 {
		return
	}
	s.runSpeed = speed
	if w := s.walk; w != nil {
		w.from = w.at(now)
		dx, dy, dz := float64(w.to[0]-w.from[0]), float64(w.to[1]-w.from[1]), float64(w.to[2]-w.from[2])
		w.start, w.dur = now, time.Duration(math.Sqrt(dx*dx+dy*dy+dz*dz)/float64(speed)*float64(time.Second))
	}
}

// seedRunSpeed takes the run speed from the player's own create block (Speeds[1],
// Object.cpp:436-441), so a walk right after login or a far teleport uses the speed the
// server already has, mounts and auras included.
func (s *Session) seedRunSpeed(blocks []objects.Block) {
	self := s.selfGUID()
	for _, b := range blocks {
		if (b.Type == objects.BlockCreate || b.Type == objects.BlockCreate2) && b.GUID == self && b.Move != nil && b.Move.Living {
			s.setRunSpeed(b.Move.Speeds[1], time.Now())
		}
	}
}

// rootHandler acknowledges SMSG_FORCE_MOVE_(UN)ROOT. A root stops a running walk first,
// with a MSG_MOVE_STOP that already carries MoveRoot (ruling T23).
func (s *Session) rootHandler(ack uint16, apply bool) handler {
	return func(body []byte) error {
		c, err := game.ParseMovementChange(body, false)
		if err != nil {
			return err
		}
		self := s.selfGUID()
		if c.GUID != self {
			return nil
		}
		s.rooted = apply
		if apply {
			s.stopWalk(errors.New("rooted by the server; walk stopped"), true)
		}
		now := time.Now()
		s.syncWalk(now)
		s.mu.Lock()
		mi := s.moveInfoAt(s.currentFlags(), now)
		s.mu.Unlock()
		return s.sendMove(ack, mi, game.BuildMovementChangeAck(self, c.Counter, mi, nil))
	}
}

func (s *Session) onAttackStart(body []byte) error {
	a, err := game.ParseAttackStart(body)
	if err != nil {
		return err
	}
	s.emit(events.Event{Kind: events.KindAttackStart, SourceGUID: a.Attacker, TargetGUID: a.Victim})
	return nil
}

func (s *Session) onAttackStop(body []byte) error {
	a, err := game.ParseAttackStop(body)
	if err != nil {
		return err
	}
	s.emit(events.Event{Kind: events.KindAttackStop, SourceGUID: a.Attacker, TargetGUID: a.Victim})
	return nil
}
