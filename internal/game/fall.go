package game

import "math"

// Fall physics of the core's splines and anticheat (Movement/spline/util.cpp:25-29).
const (
	Gravity          = 19.29110527038574
	TerminalVelocity = 60.148003 // yards per second
	TerminalSafeFall = 7.0       // under feather fall (MoveSafeFall)
)

// FallDrop is how far a body has dropped t seconds after it left with downward speed v0
// (the MovementInfo jump z speed: negative is upwards), its downward speed capped at term.
// A negative drop is a height gain. It is computeFallElevation (util.cpp:59-83) with the
// cap applied to the speed reached, which is how the client falls under feather fall too.
func FallDrop(t, v0, term float64) float64 {
	if t <= 0 {
		return 0
	}
	v0 = math.Min(v0, term)
	tTerm := (term - v0) / Gravity // when the speed reaches the cap
	if t <= tTerm {
		return t * (v0 + t*Gravity/2)
	}
	return tTerm*(v0+tTerm*Gravity/2) + term*(t-tTerm)
}

// FallLandTime is when a body that left with downward speed v0 is back at its starting
// height, the ground assumed when nothing better is known; 0 when it starts level or down.
func FallLandTime(v0, term float64) float64 {
	if v0 >= 0 {
		return 0
	}
	up := -v0 / Gravity
	h := v0 * v0 / (2 * Gravity)        // apex height
	capH := term * term / (2 * Gravity) // the drop that reaches the cap from rest
	if h <= capH {
		return up + math.Sqrt(2*h/Gravity)
	}
	return up + term/Gravity + (h-capH)/term
}
