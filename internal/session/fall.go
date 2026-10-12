package session

import (
	"math"
	"time"

	"github.com/pjerra/turtle-agent/internal/events"
	"github.com/pjerra/turtle-agent/internal/game"
	"github.com/pjerra/turtle-agent/internal/navmesh"
	"github.com/pjerra/turtle-agent/internal/opcode"
)

// FloorFinder lists the navmesh surfaces at a point, highest first (navmesh.Planner is one).
// A Planner that is one also lands falls and knockbacks on the ground.
type FloorFinder interface {
	Floors(mapID uint32, x, y, lo, hi float32) ([]navmesh.Floor, error)
}

var _ FloorFinder = (*navmesh.Planner)(nil)

// floorFunc is FloorFinder on the player's map.
type floorFunc func(x, y, lo, hi float32) ([]navmesh.Floor, error)

const (
	traceStep  = 0.05 // seconds between the samples a flight is checked against the floor at
	fallReach  = 600  // yards below a sample a floor is looked for: a 10 s flight drops less
	wallReach  = 50   // yards above a sample a floor counts as the top of a wall
	standSlack = 0.5  // a floor this far above the player's z is the one it stands on
	settleGap  = 0.05 // a floor this far below the player's z is the one it stands on
)

// floorFunc is the navmesh floor query on the player's map, or nil without one (no
// mmaps_dir, or a Planner that cannot answer).
func (s *Session) floorFunc() floorFunc {
	ff, ok := s.opts.Planner.(FloorFinder)
	if !ok {
		return nil
	}
	s.mu.Lock()
	m := s.mapID
	s.mu.Unlock()
	return func(x, y, lo, hi float32) ([]navmesh.Floor, error) { return ff.Floors(m, x, y, lo, hi) }
}

// highest is the highest floor from lo to hi in a list sorted highest first.
func highest(fl []navmesh.Floor, lo, hi float32) (navmesh.Floor, bool) {
	for _, f := range fl {
		if f.Z <= hi && f.Z >= lo {
			return f, true
		}
	}
	return navmesh.Floor{}, false
}

// trace flies the current arc against the navmesh, a sample every traceStep, up to limit
// seconds, and sets the landing: the first floor the arc meets between two samples (or,
// going across, one that rose under it by no more than a slope climbs in a step). A floor
// above that, with no floor under the arc, is a wall the arc ran into: the flight stops
// going across where it was and drops from there, as the client slides down a wall. It
// says false when the arc meets no floor in time, or a query fails.
func (f *fallState) trace(limit float64) (float64, bool) {
	rise := float32(float64(f.kb.XYSpeed)*traceStep*1.5) + 0.5 // a 56° slope, and the mesh's error
	prev := f.pos(0)
	for i := 1; float64(i-1)*traceStep < limit; {
		t := min(float64(i)*traceStep, limit)
		p := f.pos(t)
		top := max(prev[2], p[2]) + rise
		fl, err := f.floor(p[0], p[1], p[2]-fallReach, p[2]+wallReach)
		if err != nil {
			return 0, false
		}
		if g, ok := highest(fl, p[2]-0.01, top); ok {
			f.landOn(g, max(t-traceStep, 0), t, prev[2], rise)
			return f.land.Sub(f.start).Seconds(), true
		}
		if f.stopXY > t && f.kb.XYSpeed != 0 && len(fl) > 0 && fl[0].Z > top {
			if _, under := highest(fl, -math.MaxFloat32, top); !under {
				f.stopXY = max(t-traceStep, 0) // into a wall: check this sample again, without going across
				continue
			}
		}
		prev = p
		i++
	}
	return 0, false
}

// landOn sets the landing on floor g, met between samples t0 and t1. Coming down onto it,
// the landing is where the arc meets the floor (by bisection, the floor followed near g's
// height as the arc goes across); a floor that rose above the arc (a slope going across) is
// landed on at t1.
func (f *fallState) landOn(g navmesh.Floor, t0, t1 float64, z0, rise float32) {
	at := func(t float64) ([3]float32, navmesh.Floor) {
		p := f.pos(t)
		fl, err := f.floor(p[0], p[1], g.Z-rise, g.Z+rise)
		best := g
		if err == nil {
			for i, c := range fl {
				if i == 0 || math.Abs(float64(c.Z-g.Z)) < math.Abs(float64(best.Z-g.Z)) {
					best = c
				}
			}
		}
		return p, best
	}
	p, floor := at(t1)
	t := t1
	if z0 >= g.Z {
		lo, hi := t0, t1
		for range 12 {
			mid := (lo + hi) / 2
			if q, fm := at(mid); q[2] > fm.Z {
				lo = mid
			} else {
				hi = mid
			}
		}
		t = hi
		p, floor = at(t)
	}
	f.land = f.start.Add(time.Duration(t * float64(time.Second)))
	p[2] = floor.Z
	f.landPos, f.onFloor, f.water = p, true, floor.Flags&navmesh.NavWater != 0
}

// floorKind says what the landing is on, for the landed event.
func (f *fallState) floorKind() string {
	switch {
	case !f.onFloor:
		return "guess"
	case f.water:
		return "water"
	}
	return "navmesh"
}

// describe is the event data of a flight that began at from at now.
func (f *fallState) describe(state string, from [3]float32, now time.Time) map[string]any {
	return map[string]any{"state": state, "x": from[0], "y": from[1], "z": from[2],
		"speed_xy": f.kb.XYSpeed, "speed_z": f.kb.ZSpeed, "flight_ms": f.land.Sub(now).Milliseconds(),
		"land_x": f.landPos[0], "land_y": f.landPos[1], "land_z": f.landPos[2], "floor": f.floorKind()}
}

// dropToFloor looks for the navmesh floor under where a GM teleport put the player. On it
// (within settleGap below or standSlack above), the z is known; above it, the player falls
// to it from rest as the client does. The fall begins with a heartbeat at the teleport's
// position: the server keeps the player's movement flags across a teleport
// (Unit::TeleportPositionRelocation changes only the position, Unit.cpp:10555-10568) and
// the anticheat has no grace for the first packet after one (Movement::HandleTeleport has
// no caller), so a first packet that had already dropped would break the z bound of a
// player standing still (Movement.cpp:863-867, :593-595). Without a navmesh, or with no
// floor under the point, the z stays a guess.
func (s *Session) dropToFloor(now time.Time) error {
	floor := s.floorFunc()
	if floor == nil {
		return nil
	}
	s.mu.Lock()
	p, o := [3]float32{s.pos[0], s.pos[1], s.pos[2]}, s.pos[3]
	s.mu.Unlock()
	fl, err := floor(p[0], p[1], p[2]-fallReach, p[2]+standSlack)
	if err != nil {
		return nil
	}
	g, ok := highest(fl, -math.MaxFloat32, p[2]+standSlack)
	if !ok {
		return nil
	}
	if p[2]-g.Z <= settleGap {
		s.mu.Lock()
		s.zUnknown = g.Flags&navmesh.NavWater != 0
		s.mu.Unlock()
		return nil
	}
	kb := game.KnockBack{Cos: float32(math.Cos(float64(o))), Sin: float32(math.Sin(float64(o)))}
	s.fall = &fallState{kb: kb, began: now, startZ: p[2], floor: floor, nextBeat: now.Add(500 * time.Millisecond)}
	s.fall.arc(p, now, 0, fallTerm(s.toggles), false)
	s.mu.Lock()
	s.falling = true
	mi := s.moveInfoAt(s.currentFlags(), now)
	s.mu.Unlock()
	if err := s.sendMove(opcode.MSGMoveHeartbeat, mi, game.BuildMovement(mi)); err != nil {
		return err
	}
	s.emit(events.Event{Kind: events.KindSession, Data: s.fall.describe("falling", p, now)})
	return nil
}
