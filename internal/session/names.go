package session

import (
	"fmt"
	"sync"
	"time"

	"github.com/pjerra/turtle-agent/internal/events"
	"github.com/pjerra/turtle-agent/internal/game"
	"github.com/pjerra/turtle-agent/internal/opcode"
	"github.com/pjerra/turtle-agent/internal/packet"
)

// nameQueryInterval spaces repeated name or creature queries for one GUID or entry.
const nameQueryInterval = 5 * time.Second

// nameCache maps player GUIDs and creature entries to names. A creature's entry comes
// from the object store (OBJECT_FIELD_ENTRY), never from a pet's GUID bits, and a creature
// whose entry is unknown (0) stays nameless and unasked until its values arrive. mu guards
// the two maps, which RPC goroutines read; the rest belongs to the owner loop.
type nameCache struct {
	mu         sync.RWMutex
	players    map[packet.GUID]string
	creatures  map[uint32]string
	askedGUID  map[packet.GUID]time.Time
	askedEntry map[uint32]time.Time
	// silentGUID and silentEntry hold queries whose wait ran out unanswered: until the
	// time stored, the GUID or entry counts as nameless instead of stalling the queue again.
	silentGUID  map[packet.GUID]time.Time
	silentEntry map[uint32]time.Time
	waiting     []pendingEvent
}

type pendingEvent struct {
	e     events.Event
	until time.Time
	party bool // fixed when the event was emitted, not when its names arrive
}

func newNameCache() *nameCache {
	return &nameCache{
		players: map[packet.GUID]string{}, creatures: map[uint32]string{},
		askedGUID: map[packet.GUID]time.Time{}, askedEntry: map[uint32]time.Time{},
		silentGUID: map[packet.GUID]time.Time{}, silentEntry: map[uint32]time.Time{},
	}
}

func (c *nameCache) setPlayer(g packet.GUID, name string) {
	c.mu.Lock()
	c.players[g] = name
	c.mu.Unlock()
}

func (c *nameCache) setCreature(entry uint32, name string) {
	c.mu.Lock()
	c.creatures[entry] = name
	c.mu.Unlock()
}

// lookup returns the cached name, using entry for a creature; ok is false while a query
// is owed or, for a creature, while its entry is unknown.
func (c *nameCache) lookup(g packet.GUID, entry uint32) (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	switch {
	case g == 0:
		return "", true
	case g.IsPlayer():
		n, ok := c.players[g]
		return n, ok
	case g.IsCreature():
		n, ok := c.creatures[entry]
		return n, ok && entry != 0
	}
	return "", true // game objects and corpses have no name query in v1
}

// nameOf returns g's name and sends a name or creature query when it is unknown. A GUID
// whose last query went unanswered for a whole wait counts as known and nameless until
// that query's throttle lapses.
func (s *Session) nameOf(g packet.GUID) (string, bool) {
	n, ok := s.cachedName(g)
	if ok {
		return n, true
	}
	if s.silent(g, time.Now()) {
		return "", true
	}
	s.askName(g)
	return "", false
}

// cachedName is lookup with the creature entry taken from the store.
func (s *Session) cachedName(g packet.GUID) (string, bool) {
	var entry uint32
	if g.IsCreature() {
		entry = s.store.Entry(g)
	}
	return s.names.lookup(g, entry)
}

// silent reports whether g is in the short negative cache, dropping a lapsed entry.
func (s *Session) silent(g packet.GUID, now time.Time) bool {
	if g.IsPlayer() {
		until, ok := s.names.silentGUID[g]
		if ok && !now.Before(until) {
			delete(s.names.silentGUID, g)
			return false
		}
		return ok
	}
	entry := s.store.Entry(g)
	until, ok := s.names.silentEntry[entry]
	if ok && !now.Before(until) {
		delete(s.names.silentEntry, entry)
		return false
	}
	return ok
}

// giveUp puts g in the negative cache until its last query's throttle lapses.
func (s *Session) giveUp(g packet.GUID) {
	if g == 0 {
		return
	}
	if _, ok := s.cachedName(g); ok {
		return
	}
	if g.IsPlayer() {
		if t, ok := s.names.askedGUID[g]; ok {
			s.names.silentGUID[g] = t.Add(nameQueryInterval)
		}
		return
	}
	entry := s.store.Entry(g)
	if t, ok := s.names.askedEntry[entry]; ok {
		s.names.silentEntry[entry] = t.Add(nameQueryInterval)
	}
}

// askName sends CMSG_NAME_QUERY or CMSG_CREATURE_QUERY, at most once per
// nameQueryInterval for a GUID or entry. A creature whose entry is not known yet is not
// asked: its create block brings the entry and asks again. A write error surfaces on the
// next send.
func (s *Session) askName(g packet.GUID) {
	now := time.Now()
	if g.IsPlayer() {
		if t, ok := s.names.askedGUID[g]; ok && now.Sub(t) < nameQueryInterval {
			return
		}
		s.names.askedGUID[g] = now
		_ = s.conn.WritePacket(opcode.CMSGNameQuery, game.BuildNameQuery(g))
		return
	}
	entry := s.store.Entry(g)
	if entry == 0 {
		return
	}
	if t, ok := s.names.askedEntry[entry]; ok && now.Sub(t) < nameQueryInterval {
		return
	}
	s.names.askedEntry[entry] = now
	_ = s.conn.WritePacket(opcode.CMSGCreatureQuery, game.BuildCreatureQuery(entry, g))
}

// fillNames sets the source and target names it can and reports whether both are known.
func (s *Session) fillNames(e *events.Event) bool {
	ok := true
	if e.Source == "" && e.SourceGUID != 0 {
		n, known := s.nameOf(e.SourceGUID)
		e.Source, ok = n, ok && known
	}
	if e.Target == "" && e.TargetGUID != 0 {
		n, known := s.nameOf(e.TargetGUID)
		e.Target, ok = n, ok && known
	}
	return ok
}

// flushNames appends waiting events in arrival order, stopping at the first one whose
// names are unknown and whose wait has not run out. An event whose wait ran out is
// appended with the names known so far, and its unanswered GUIDs go to the negative cache.
func (s *Session) flushNames(now time.Time) {
	i := 0
	for ; i < len(s.names.waiting); i++ {
		p := &s.names.waiting[i]
		if !s.fillNames(&p.e) {
			if now.Before(p.until) {
				break
			}
			s.giveUp(p.e.SourceGUID)
			s.giveUp(p.e.TargetGUID)
		}
		s.appendEvent(p.e, p.party)
	}
	if i == len(s.names.waiting) {
		s.names.waiting = nil
	} else {
		s.names.waiting = s.names.waiting[i:]
	}
}

func (s *Session) onNameQueryResponse(body []byte) error {
	info, err := game.ParseNameQueryResponse(body)
	if err != nil {
		return err
	}
	s.names.setPlayer(info.GUID, info.Name)
	s.flushNames(time.Now())
	return nil
}

func (s *Session) onCreatureQueryResponse(body []byte) error {
	info, err := game.ParseCreatureQueryResponse(body)
	if err != nil {
		return err
	}
	name := info.Name
	if !info.Found {
		name = fmt.Sprintf("creature %d", info.Entry)
	}
	s.names.setCreature(info.Entry, name)
	s.flushNames(time.Now())
	return nil
}
