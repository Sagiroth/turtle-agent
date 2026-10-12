package tbm

import (
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)

func TestGroupKey(t *testing.T) {
	if GroupKey(false, false, nil) != "" {
		t.Error("ungrouped must be empty")
	}
	a, b := GroupKey(true, false, []uint64{5, 2}), GroupKey(true, false, []uint64{2, 5})
	if a != b || a == GroupKey(true, false, []uint64{2, 5, 9}) || a == GroupKey(true, true, []uint64{2, 5}) {
		t.Errorf("%q %q", a, b)
	}
}

// Transport choice (Core.lua SendBotCommand, Comms.lua AddonCommandChannel): the addon
// channel only while TBM:TRANSPORT|party is known for the group the player is in now.
func TestPickTransport(t *testing.T) {
	grouped := GroupKey(true, false, []uint64{7})
	for _, tc := range []struct {
		name  string
		lines []string
		fold  string // group key when the roster response arrived
		now   string // group key at click time
		want  string
	}{
		{"grouped and advertised party", []string{"TBM:TRANSPORT|party"}, grouped, grouped, TransportAddon},
		{"no verdict yet", nil, grouped, grouped, TransportChat},
		{"ungrouped", []string{"TBM:TRANSPORT|party"}, grouped, "", TransportChat},
		{"battleground group: server says none", []string{"TBM:TRANSPORT|none"}, grouped, grouped, TransportChat},
		{"group changed since the verdict", []string{"TBM:TRANSPORT|party"}, grouped, GroupKey(true, false, []uint64{7, 8}), TransportChat},
		{"verdict taken while ungrouped", []string{"TBM:TRANSPORT|none"}, "", grouped, TransportChat},
		{"unknown verdict is ignored", []string{"TBM:TRANSPORT|raid"}, grouped, grouped, TransportChat},
		{"later verdict wins", []string{"TBM:TRANSPORT|none", "TBM:TRANSPORT|party"}, grouped, grouped, TransportAddon},
		{"verdict is case-insensitive", []string{"TBM:TRANSPORT|Party"}, grouped, grouped, TransportAddon},
	} {
		var tr Tracker
		for _, l := range tc.lines {
			tr.Fold(l, tc.fold, t0)
		}
		if got, why := tr.Pick(tc.now); got != tc.want || why == "" {
			t.Errorf("%s: %s (%s), want %s", tc.name, got, why, tc.want)
		}
	}
}

func TestTrackerResetOnNewSession(t *testing.T) {
	var tr Tracker
	a, b := new(int), new(int)
	tr.Reset(a)
	tr.Fold("TBM:TRANSPORT|party", "g", t0)
	tr.Reset(a)
	if got, _ := tr.Pick("g"); got != TransportAddon {
		t.Fatal("same session lost its verdict")
	}
	tr.Reset(b)
	if got, _ := tr.Pick("g"); got != TransportChat {
		t.Fatal("a new session kept the verdict")
	}
}

func TestTrackerKnowsBots(t *testing.T) {
	var tr Tracker
	for _, l := range []string{
		"TBM:ROSTER_BEGIN|2", "TBM:ROSTER|7|Gorm|1|online|1|-", "TBM:ROSTER|9|Lia|5|offline|0|-", "TBM:ROSTER_END",
		"TBM:BOTSTATE_BEGIN|1", "TBM:BOTSTATE|Hired|move=follow,aoe=on", "TBM:BOTSTATE_END",
		"TBM:CAPS|pull-seconds,flee", "TBM:TRANSPORT|party", "not a TBM line",
	} {
		tr.Fold(l, "g", t0)
	}
	for name, want := range map[string]bool{"gorm": true, "Lia": false, "hired": true, "Nobody": false} {
		if tr.IsBot(name) != want {
			t.Errorf("IsBot(%s) != %v", name, want)
		}
	}
	if !tr.HasCap("PULL-SECONDS") || tr.HasCap("inventory") || !tr.HasRoster() {
		t.Error("caps or roster")
	}
	if got := tr.BotStates(); got["Hired"] != "move=follow,aoe=on" {
		t.Error(got)
	}
	if tr.Age(t0.Add(3*time.Second)) != 3*time.Second {
		t.Error(tr.Age(t0.Add(3 * time.Second)))
	}
	tr.Stale()
	if tr.Age(t0) < time.Hour {
		t.Error("stale tracker is fresh")
	}
}

// The addon spaces requests by SEND_DELAY whichever transport they take.
func TestThrottle(t *testing.T) {
	var tr Tracker
	if d := tr.Throttle(t0); d != 0 {
		t.Errorf("first send waits %v", d)
	}
	if d := tr.Throttle(t0.Add(100 * time.Millisecond)); d != 250*time.Millisecond {
		t.Errorf("second send waits %v", d)
	}
	if d := tr.Throttle(t0.Add(2 * time.Second)); d != 0 {
		t.Errorf("late send waits %v", d)
	}
}

// The addon's unforced roster poll waits ROSTER_THROTTLE after the last one.
func TestRosterDue(t *testing.T) {
	var tr Tracker
	if !tr.RosterDue(t0) {
		t.Error("first poll not due")
	}
	tr.NoteRoster(t0)
	if tr.RosterDue(t0.Add(4900*time.Millisecond)) || !tr.RosterDue(t0.Add(5*time.Second)) {
		t.Error("5 s throttle")
	}
}

func TestOrphans(t *testing.T) {
	var tr Tracker
	tr.AddOrphan("Rest", t0)
	tr.AddOrphan("rest", t0.Add(10*time.Second))
	if n := tr.Owed("REST", t0.Add(20*time.Second)); n != 2 {
		t.Errorf("owed %d", n)
	}
	if n := tr.Owed("rest", t0.Add(35*time.Second)); n != 1 { // the first lapsed after OrphanTTL
		t.Errorf("owed %d", n)
	}
	tr.Settle("rest", 5)
	if n := tr.Owed("rest", t0.Add(36*time.Second)); n != 0 {
		t.Errorf("owed %d", n)
	}
}

// A switch ACK sets the state the next click flips from.
func TestSetBotKey(t *testing.T) {
	var tr Tracker
	for _, l := range []string{"TBM:BOTSTATE_BEGIN|2", "TBM:BOTSTATE|Gorm|move=follow,aoe=on", "TBM:BOTSTATE|Lia|move=stay", "TBM:BOTSTATE_END"} {
		tr.Fold(l, "g", t0)
	}
	tr.SetBotKey("gorm", "aoe", "off")
	tr.SetBotKey("", "loot", "on")
	got := tr.BotStates()
	if got["Gorm"] != "move=follow,aoe=off,loot=on" || got["Lia"] != "move=stay,loot=on" {
		t.Errorf("%v", got)
	}
}
