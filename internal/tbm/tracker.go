package tbm

import (
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"
)

// GroupKey identifies the player's group for the transport verdict: empty when ungrouped,
// otherwise the kind and the sorted member guids. The addon drops its verdict on every
// PARTY_MEMBERS_CHANGED and RAID_ROSTER_UPDATE (Roster.lua group watcher); a changed key is
// the same event seen from a headless client.
func GroupKey(inGroup, raid bool, members []uint64) string {
	if !inGroup {
		return ""
	}
	g := slices.Clone(members)
	slices.Sort(g)
	kind := "party"
	if raid {
		kind = "raid"
	}
	return fmt.Sprintf("%s:%v", kind, g)
}

// Tracker holds what the addon keeps between clicks: the transport verdict and the module's
// last roster response (bots, switch states, capabilities), plus the send throttle. One
// tracker serves one session; Reset forgets everything when the session changes.
type Tracker struct {
	mu       sync.Mutex
	owner    any
	verdict  string // last TBM:TRANSPORT value
	verdictK string // GroupKey when it arrived
	known    bool
	st       Status
	stAt     time.Time // when the last BOTSTATE block or TRANSPORT trailer arrived
	lastSend time.Time
	// lastRoster is when a roster request last went out (the addon's PollList throttle).
	lastRoster time.Time
	// cursor is the event log position after the last exchange; what arrives past it before the
	// next request is late.
	cursor uint64
	// orphans are the intents of requests that timed out, by lower-case intent: their ACK may
	// still arrive and must not answer a later request.
	orphans map[string][]time.Time
}

// RosterThrottle is the addon's minimum gap between unforced roster polls; a variable so tests can shorten it.
var RosterThrottle = RosterThrottleMS * time.Millisecond

// OrphanTTL is how long a timed-out request may still be owed a reply.
const OrphanTTL = 30 * time.Second

// Reset forgets the transport verdict and the roster when owner is not the one the tracker
// last served: a new login starts with nothing known, like the addon on PLAYER_ENTERING_WORLD.
func (t *Tracker) Reset(owner any) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.owner == owner {
		return
	}
	t.owner, t.verdict, t.verdictK, t.known = owner, "", "", false
	t.st, t.stAt, t.lastSend, t.lastRoster = Status{}, time.Time{}, time.Time{}, time.Time{}
	t.cursor, t.orphans = 0, nil
}

// Fold takes in one TBM: line of a reply: it keeps the roster, switch states and capabilities
// current, and a TBM:TRANSPORT trailer becomes the verdict for the group as it is now.
func (t *Tracker) Fold(line, groupKey string, now time.Time) {
	if !strings.HasPrefix(line, "TBM:") {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.st.Add(line)
	t.st.Lines, t.st.Errors, t.st.Complete = nil, nil, false
	switch {
	case strings.HasPrefix(line, "TBM:TRANSPORT|"):
		v := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(line, "TBM:TRANSPORT|")))
		if v == "party" || v == "none" { // SetAddonTransport ignores anything else
			t.verdict, t.verdictK, t.known = v, groupKey, true
		}
		t.stAt = now
	case line == "TBM:BOTSTATE_END", strings.HasPrefix(line, "TBM:BOTSTATE|"):
		t.stAt = now
	}
}

// Pick chooses the transport for a command, with the addon's rule (Core.lua SendBotCommand,
// Comms.lua AddonCommandChannel): the addon command channel while the server advertised
// TBM:TRANSPORT|party for the group the player is in now, ".bot" chat otherwise. The second
// result says why.
func (t *Tracker) Pick(groupKey string) (string, string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	switch {
	case groupKey == "":
		return TransportChat, "not grouped"
	case !t.known:
		return TransportChat, "no TBM:TRANSPORT verdict yet"
	case t.verdictK != groupKey:
		return TransportChat, "group changed since the TBM:TRANSPORT verdict"
	case t.verdict != "party":
		return TransportChat, "server advertised TBM:TRANSPORT|" + t.verdict
	}
	return TransportAddon, "server advertised TBM:TRANSPORT|party for this group"
}

// VerdictCurrent reports whether a verdict exists for the group as it is now.
func (t *Tracker) VerdictCurrent(groupKey string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.known && t.verdictK == groupKey
}

// Stale marks the bot states out of date, so the next click that needs them polls the roster.
func (t *Tracker) Stale() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.stAt = time.Time{}
}

// Age is how long ago the bot states were last refreshed; a huge duration when never.
func (t *Tracker) Age(now time.Time) time.Duration {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.stAt.IsZero() {
		return time.Duration(1<<63 - 1)
	}
	return now.Sub(t.stAt)
}

// BotStates returns a copy of the latest TBM:BOTSTATE values by bot name.
func (t *Tracker) BotStates() map[string]string {
	t.mu.Lock()
	defer t.mu.Unlock()
	m := make(map[string]string, len(t.st.BotState))
	for k, v := range t.st.BotState {
		m[k] = v
	}
	return m
}

// HasCap reports whether the module advertised a TBM:CAPS capability.
func (t *Tracker) HasCap(name string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return slices.ContainsFunc(t.st.Caps, func(c string) bool { return strings.EqualFold(c, name) })
}

// IsBot reports whether name is one of the player's online bots as the addon knows them
// (UI.lua targetScope): an online roster row, or a name the module reports a BOTSTATE for.
func (t *Tracker) IsBot(name string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, r := range t.st.Roster {
		if strings.EqualFold(r.Name, name) {
			switch strings.ToLower(strings.TrimSpace(r.State)) {
			case "online", "in world", "in_world":
				return true
			}
		}
	}
	for n := range t.st.BotState {
		if strings.EqualFold(n, name) {
			return true
		}
	}
	return false
}

// HasRoster reports whether any roster response or bot state has been seen.
func (t *Tracker) HasRoster() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.st.Roster) > 0 || len(t.st.BotState) > 0
}

// Throttle reserves the next send slot and returns how long to wait before using it: the
// addon spaces its requests by SEND_DELAY, whichever transport they take.
func (t *Tracker) Throttle(now time.Time) time.Duration {
	t.mu.Lock()
	defer t.mu.Unlock()
	next := t.lastSend.Add(SendDelayMS * time.Millisecond)
	if next.Before(now) {
		next = now
	}
	t.lastSend = next
	return next.Sub(now)
}

// RosterDue reports whether the addon's PollList would send a roster request now: not within
// ROSTER_THROTTLE of the last one (only the forced polls after a command ignore this).
func (t *Tracker) RosterDue(now time.Time) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.lastRoster.IsZero() || now.Sub(t.lastRoster) >= RosterThrottle
}

// NoteRoster records that a roster request went out.
func (t *Tracker) NoteRoster(now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.lastRoster = now
}

// Cursor and SetCursor hold the event log position after the last exchange.
func (t *Tracker) Cursor() uint64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.cursor
}

func (t *Tracker) SetCursor(c uint64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.cursor = c
}

// AddOrphan records a request for intent that got no reply in time.
func (t *Tracker) AddOrphan(intent string, now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.orphans == nil {
		t.orphans = map[string][]time.Time{}
	}
	k := strings.ToLower(intent)
	t.orphans[k] = append(t.orphans[k], now)
}

// Owed is how many timed-out requests for intent may still be answered.
func (t *Tracker) Owed(intent string, now time.Time) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	k := strings.ToLower(intent)
	var live []time.Time
	for _, at := range t.orphans[k] {
		if now.Sub(at) < OrphanTTL {
			live = append(live, at)
		}
	}
	if len(live) == 0 {
		delete(t.orphans, k)
	} else {
		t.orphans[k] = live
	}
	return len(live)
}

// Settle takes n of the oldest owed replies for intent off the books (they arrived).
func (t *Tracker) Settle(intent string, n int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	k := strings.ToLower(intent)
	n = min(n, len(t.orphans[k]))
	if n == 0 {
		return
	}
	if t.orphans[k] = t.orphans[k][n:]; len(t.orphans[k]) == 0 {
		delete(t.orphans, k)
	}
}

// setKV replaces key=val in a BOTSTATE value, or appends it.
func setKV(state, key, val string) string {
	parts := strings.Split(state, ",")
	for i, p := range parts {
		if k, _, ok := strings.Cut(strings.TrimSpace(p), "="); ok && strings.EqualFold(k, key) {
			parts[i] = key + "=" + val
			return strings.Join(parts, ",")
		}
	}
	if state == "" {
		return key + "=" + val
	}
	return state + "," + key + "=" + val
}

// SetBotKey records the state a switch ACK reported for one bot (name empty: every bot), so
// the next click flips from it without waiting for a new roster.
func (t *Tracker) SetBotKey(name, key, val string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for n, st := range t.st.BotState {
		if name == "" || strings.EqualFold(n, name) {
			t.st.BotState[n] = setKV(st, key, val)
		}
	}
}
