package session

import (
	"context"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/pjerra/turtle-agent/internal/events"
	"github.com/pjerra/turtle-agent/internal/game"
	"github.com/pjerra/turtle-agent/internal/objects"
	"github.com/pjerra/turtle-agent/internal/opcode"
	"github.com/pjerra/turtle-agent/internal/packet"
)

// wolfNearby puts a named Young Wolf 10 yards east of the player.
func wolfNearby(h *harness) {
	h.t.Helper()
	wolfName(h)
	h.send(opcode.SMSGUpdateObject, unitCreate(wolfGUID, objects.TypeUnit, me.X+10, me.Y, me.Z, map[uint16]uint32{objects.UnitHealth: 50, objects.UnitMaxHealth: 50}))
	waitFor(h.t, func() bool { return len(h.s.Units(objects.Filter{Name: "young wolf"})) == 1 })
}

func TestTargetAttackStopCast(t *testing.T) {
	ctx := context.Background()
	h := start(t, Options{})
	h.enterWorld()
	if err := h.s.Attack(ctx); err == nil || !strings.Contains(err.Error(), "no target") {
		t.Fatalf("attack without target: %v", err)
	}
	wolfNearby(h)
	u, err := h.s.Target(ctx, 0, "young wolf")
	if err != nil || u.GUID != wolfGUID || u.Name != "Young Wolf" {
		t.Fatalf("%+v %v", u, err)
	}
	if p := h.expect(opcode.CMSGSetSelection); packet.NewReader(p.Body).GUID() != wolfGUID {
		t.Fatalf("% X", p.Body)
	}
	if err := h.s.Attack(ctx); err != nil {
		t.Fatal(err)
	}
	if p := h.expect(opcode.CMSGAttackswing); packet.NewReader(p.Body).GUID() != wolfGUID {
		t.Fatalf("% X", p.Body)
	}
	if err := h.s.Cast(ctx, 133, 0); err != nil {
		t.Fatal(err)
	}
	if p := h.expect(opcode.CMSGCastSpell); string(p.Body) != string(game.BuildCastSpell(133, wolfGUID)) {
		t.Fatalf("% X", p.Body)
	}
	if err := h.s.StopAttack(ctx); err != nil {
		t.Fatal(err)
	}
	h.expect(opcode.CMSGAttackstop)
	if h.s.Status().Target != wolfGUID {
		t.Fatal("status target")
	}
	h.send(opcode.SMSGAttackstart, packet.NewWriter().GUID(me.GUID).GUID(wolfGUID).Bytes())
	h.send(opcode.SMSGAttackswingNotinrange, nil)
	h.logout()
	if a := ofKind(h.s.Log(), events.KindAttackStart); len(a) != 1 || a[0].Source != "Harnessb" || a[0].Target != "Young Wolf" {
		t.Fatalf("%+v", a)
	}
	if e := ofKind(h.s.Log(), events.KindAttackError); len(e) != 1 || e[0].Data["reason"] != "not_in_range" {
		t.Fatalf("%+v", e)
	}
}

func TestFaceAndWalk(t *testing.T) {
	ctx := context.Background()
	h := start(t, Options{})
	h.enterWorld()
	wolfNearby(h)
	if err := h.s.Face(ctx, wolfGUID); err != nil {
		t.Fatal(err)
	}
	if mi := game.ReadMoveInfo(packet.NewReader(h.expect(opcode.MSGMoveSetFacing).Body)); math.Abs(float64(mi.O)) > 1e-4 || mi.X != me.X {
		t.Fatalf("%+v", mi)
	}
	if err := h.s.WalkTo(ctx, me.X+100, me.Y, me.Z); err == nil || !strings.Contains(err.Error(), "teleport") {
		t.Fatalf("long walk: %v", err)
	}
	if err := h.s.WalkTo(ctx, me.X+3.5, me.Y, me.Z); err != nil {
		t.Fatal(err)
	}
	if mi := game.ReadMoveInfo(packet.NewReader(h.expect(opcode.MSGMoveStartForward).Body)); mi.Flags != game.MoveForward {
		t.Fatalf("start %+v", mi)
	}
	if mi := game.ReadMoveInfo(packet.NewReader(h.expect(opcode.MSGMoveStop).Body)); mi.Flags != 0 || mi.X != me.X+3.5 {
		t.Fatalf("stop %+v", mi)
	}
	if st := h.s.Status(); st.X != me.X+3.5 {
		t.Fatalf("%+v", st)
	}
	h.logout()
}

func TestTeleportNearFarAndSpeed(t *testing.T) {
	ctx := context.Background()
	h := start(t, Options{})
	h.enterWorld()
	done := make(chan error, 1)
	go func() { done <- h.s.Teleport(ctx, 1, 2, 3, -1) }()
	if p := h.expect(opcode.CMSGMessagechat); !strings.Contains(string(p.Body), ".go xyz 1.00 2.00 3.00") {
		t.Fatalf("% X", p.Body)
	}
	w := packet.NewWriter().PackedGUID(me.GUID).U32(7)
	game.WriteMoveInfo(w, game.MoveInfo{X: 1, Y: 2, Z: 3})
	h.send(opcode.MSGMoveTeleportAck, w.Bytes())
	r := packet.NewReader(h.expect(opcode.MSGMoveTeleportAck).Body)
	if r.GUID() != me.GUID || r.U32() != 7 {
		t.Fatal("teleport ack")
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	h.send(opcode.SMSGTransferPending, packet.NewWriter().U32(1).Bytes())
	h.send(opcode.SMSGNewWorld, packet.NewWriter().U32(1).F32(10).F32(20).F32(30).F32(0).Bytes())
	h.expect(opcode.MSGMoveWorldportAck)
	h.expect(opcode.CMSGSetActiveMover)
	h.send(opcode.SMSGForceRunSpeedChange, packet.NewWriter().PackedGUID(me.GUID).U32(3).F32(14).Bytes())
	ack := packet.NewReader(h.expect(opcode.CMSGForceRunSpeedChangeAck).Body)
	if ack.GUID() != me.GUID || ack.U32() != 3 {
		t.Fatal("speed ack header")
	}
	game.ReadMoveInfo(ack)
	if ack.F32() != 14 || ack.Remaining() != 0 {
		t.Fatal("speed ack tail")
	}
	time.Sleep(50 * time.Millisecond)
	if st := h.s.Status(); st.Map != 1 || st.X != 10 {
		t.Fatalf("%+v", st)
	}
	h.logout()
}

// TestRootStopsWalkAndBlocksIt pins ruling T23: a root mid-walk stops with MoveRoot, the
// acks carry MoveRoot while rooted and a non-zero, non-decreasing client time, and walk_to
// refuses until the unroot.
func TestRootStopsWalkAndBlocksIt(t *testing.T) {
	ctx := context.Background()
	h := start(t, Options{})
	h.enterWorld()
	walked := make(chan error, 1)
	go func() { walked <- h.s.WalkTo(ctx, me.X+20, me.Y, me.Z) }()
	start := game.ReadMoveInfo(packet.NewReader(h.expect(opcode.MSGMoveStartForward).Body))
	if start.Time == 0 {
		t.Fatal("client time 0")
	}
	time.Sleep(300 * time.Millisecond)
	h.send(opcode.SMSGForceMoveRoot, packet.NewWriter().PackedGUID(me.GUID).U32(5).Bytes())
	stop := game.ReadMoveInfo(packet.NewReader(h.expect(opcode.MSGMoveStop).Body))
	if stop.Flags != game.MoveRoot || stop.X <= me.X || stop.X >= me.X+20 || stop.Time < start.Time {
		t.Fatalf("stop %+v (start %+v)", stop, start)
	}
	r := packet.NewReader(h.expect(opcode.CMSGForceMoveRootAck).Body)
	if r.GUID() != me.GUID || r.U32() != 5 {
		t.Fatal("root ack header")
	}
	if mi := game.ReadMoveInfo(r); mi.Flags != game.MoveRoot || mi.X != stop.X || mi.Time < stop.Time || r.Remaining() != 0 {
		t.Fatalf("root ack %+v", mi)
	}
	if err := <-walked; err == nil || !strings.Contains(err.Error(), "rooted") {
		t.Fatalf("walk under root: %v", err)
	}
	if st := h.s.Status(); st.X != stop.X {
		t.Fatalf("status %+v, stop at %v", st, stop.X)
	}
	if err := h.s.WalkTo(ctx, stop.X+1, me.Y, me.Z); err == nil || !strings.Contains(err.Error(), "rooted") {
		t.Fatalf("walk while rooted: %v", err)
	}
	h.send(opcode.SMSGForceMoveUnroot, packet.NewWriter().PackedGUID(me.GUID).U32(6).Bytes())
	r = packet.NewReader(h.expect(opcode.CMSGForceMoveUnrootAck).Body)
	if r.GUID() != me.GUID || r.U32() != 6 {
		t.Fatal("unroot ack header")
	}
	if mi := game.ReadMoveInfo(r); mi.Flags != 0 || mi.Time < stop.Time {
		t.Fatalf("unroot ack %+v", mi)
	}
	if err := h.s.WalkTo(ctx, stop.X, me.Y, me.Z); err != nil {
		t.Fatalf("walk after unroot: %v", err)
	}
	h.logout()
}
