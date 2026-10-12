package objects

import (
	"math"
	"time"
)

// More spline flags (Movement/spline/MoveSplineFlag.h:36-78).
const (
	splineDone   = 0x00000001
	splineCyclic = 0x00100000
)

// track is a server spline a unit follows: the route from its first point to its last,
// taken at an even speed over the spline's duration. The core times each point by its
// distance along the route (MoveSplineInit::Launch, MoveSpline::init), so even speed is
// what the server does on a linear path. A smooth (Catmull-Rom) path is followed along
// its control points, which the curve passes through, so a point between two of them is
// off by the curve's bow.
type track struct {
	id     uint32 // the spline's id (MoveSpline::GetId), 0 from a create block
	pts    [][3]float32
	cum    []float64 // route length up to each point
	start  time.Time // when the spline's time 0 was
	dur    time.Duration
	cyclic bool
	// finalO is the facing at the end, when the spline set one.
	finalO    float32
	hasFinalO bool
}

// newTrack returns nil for a route with no length to walk or no time to take, so the
// caller keeps the end point as a plain position.
func newTrack(pts [][3]float32, start time.Time, dur time.Duration, cyclic bool) *track {
	if len(pts) < 2 || dur <= 0 {
		return nil
	}
	t := &track{pts: pts, cum: make([]float64, len(pts)), start: start, dur: dur, cyclic: cyclic}
	for i := 1; i < len(pts); i++ {
		t.cum[i] = t.cum[i-1] + dist3(pts[i-1], pts[i])
	}
	if t.cum[len(pts)-1] == 0 {
		return nil
	}
	return t
}

// at is the point and facing at now, and whether the unit is still on its way. Before
// arriving, the facing is the way the current leg runs; at the end it is the spline's
// final facing, else the way the last leg ran. ok is false when no leg has horizontal
// travel, so the facing is not known from the route.
func (t *track) at(now time.Time) (p [3]float32, o float32, ok, moving bool) {
	el := now.Sub(t.start)
	if el < 0 {
		el = 0
	}
	if t.cyclic {
		el %= t.dur
	}
	moving = t.cyclic || el < t.dur
	total := t.cum[len(t.cum)-1]
	d := total
	if moving {
		d = total * float64(el) / float64(t.dur)
	}
	i := 1
	for i < len(t.pts)-1 && t.cum[i] < d {
		i++
	}
	a, b := t.pts[i-1], t.pts[i]
	f := 1.0
	if seg := t.cum[i] - t.cum[i-1]; seg > 0 {
		f = (d - t.cum[i-1]) / seg
	}
	for k := range p {
		p[k] = a[k] + float32(f)*(b[k]-a[k])
	}
	if !moving && t.hasFinalO {
		return p, t.finalO, true, false
	}
	// The facing of the leg the point is on, or of the last leg before it with any
	// horizontal travel.
	for j := i; j >= 1; j-- {
		dx, dy := float64(t.pts[j][0]-t.pts[j-1][0]), float64(t.pts[j][1]-t.pts[j-1][1])
		if dx != 0 || dy != 0 {
			return p, angle(dx, dy), true, moving
		}
	}
	return p, 0, false, moving
}

// rest is the route still ahead at now: the current point, then the points not reached
// yet, the last one included.
func (t *track) rest(now time.Time) [][3]float32 {
	p, _, _, moving := t.at(now)
	out := [][3]float32{p}
	if !moving {
		return out
	}
	d := t.cum[len(t.cum)-1] * float64(max(now.Sub(t.start), 0)%max(t.dur, 1)) / float64(t.dur)
	for i := range t.pts {
		if t.cum[i] > d {
			out = append(out, t.pts[i])
		}
	}
	return out
}

// angle is the facing of a move by dx, dy in [0, 2pi).
func angle(dx, dy float64) float32 {
	o := math.Atan2(dy, dx)
	if o < 0 {
		o += 2 * math.Pi
	}
	return float32(o)
}
