package session

import (
	"context"
	"errors"
	"strings"

	"github.com/pjerra/turtle-agent/internal/events"
	"github.com/pjerra/turtle-agent/internal/game"
	"github.com/pjerra/turtle-agent/internal/opcode"
	"github.com/pjerra/turtle-agent/internal/packet"
)

type PartyMember struct {
	Name      string      `json:"name"`
	GUID      packet.GUID `json:"guid"`
	Online    bool        `json:"online"`
	Dead      bool        `json:"dead"`
	Leader    bool        `json:"leader"`
	Health    uint16      `json:"health"`
	MaxHealth uint16      `json:"max_health"`
	PowerType uint8       `json:"power_type"`
	Power     uint16      `json:"power"`
	MaxPower  uint16      `json:"max_power"`
	Level     uint16      `json:"level"`
	Zone      uint16      `json:"zone"`
}

// PartyInfo is the group seen from the player; Members never includes the player.
type PartyInfo struct {
	InGroup       bool          `json:"in_group"`
	Leader        packet.GUID   `json:"leader,omitempty"`
	Members       []PartyMember `json:"members"`
	PendingInvite string        `json:"pending_invite,omitempty"`
}

func (s *Session) registerParty() {
	s.handlers[opcode.SMSGGroupInvite] = s.onGroupInvite
	s.handlers[opcode.SMSGPartyCommandResult] = s.onPartyResult
	s.handlers[opcode.SMSGGroupList] = s.onGroupList
	s.handlers[opcode.SMSGGroupDestroyed] = func([]byte) error { s.clearParty("disbanded"); return nil }
	s.handlers[opcode.SMSGGroupUninvite] = func([]byte) error { s.clearParty("uninvited"); return nil }
	s.handlers[opcode.SMSGGroupSetLeader] = s.onGroupSetLeader
	s.handlers[opcode.SMSGPartyMemberStats] = s.onMemberStats
	s.handlers[opcode.SMSGPartyMemberStatsFull] = s.onMemberStats
}

// Party returns a copy of the group; Members is never nil, so the RPC sends [].
func (s *Session) Party() PartyInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.party
	p.Members = append([]PartyMember{}, s.party.Members...)
	return p
}

func (s *Session) inParty(g packet.GUID) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if g != 0 && g == s.self.GUID {
		return true
	}
	for _, m := range s.party.Members {
		if m.GUID == g {
			return true
		}
	}
	return false
}

func (s *Session) Invite(ctx context.Context, name string) error {
	if strings.TrimSpace(name) == "" {
		return errors.New("invite needs a character name")
	}
	return s.send(ctx, opcode.CMSGGroupInvite, game.BuildGroupInvite(name))
}

func (s *Session) AcceptInvite(ctx context.Context) error {
	_, err := s.do(ctx, func() (any, error) {
		if err := s.inWorld(); err != nil {
			return nil, err
		}
		s.mu.Lock()
		pending := s.party.PendingInvite
		s.mu.Unlock()
		if pending == "" {
			return nil, errors.New("no pending group invite")
		}
		if err := s.conn.WritePacket(opcode.CMSGGroupAccept, nil); err != nil {
			return nil, err
		}
		s.mu.Lock()
		s.party.PendingInvite = ""
		s.mu.Unlock()
		return nil, nil
	})
	return err
}

// LeaveGroup sends CMSG_GROUP_DISBAND, which removes the player from the group (GroupHandler.cpp:354-370).
func (s *Session) LeaveGroup(ctx context.Context) error {
	_, err := s.do(ctx, func() (any, error) {
		if err := s.inWorld(); err != nil {
			return nil, err
		}
		if !s.Party().InGroup {
			return nil, errors.New("not in a group")
		}
		return nil, s.conn.WritePacket(opcode.CMSGGroupDisband, nil)
	})
	return err
}

func (s *Session) onGroupInvite(body []byte) error {
	from, err := game.ParseCString(body)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.party.PendingInvite = from
	s.mu.Unlock()
	s.emit(events.Event{Kind: events.KindParty, Source: from, Text: from + " invites you to a group.",
		Data: map[string]any{"event": "invite", "from": from}})
	return nil
}

func (s *Session) onPartyResult(body []byte) error {
	res, err := game.ParsePartyCommandResult(body)
	if err != nil {
		return err
	}
	s.emit(events.Event{Kind: events.KindParty, Text: res.Text(),
		Data: map[string]any{"event": "result", "operation": res.Op, "member": res.Member, "result": res.Result}})
	return nil
}

func (s *Session) onGroupList(body []byte) error {
	g, err := game.ParseGroupList(body)
	if err != nil {
		return err
	}
	s.mu.Lock()
	old := map[packet.GUID]PartyMember{}
	for _, m := range s.party.Members {
		old[m.GUID] = m
	}
	next := PartyInfo{InGroup: len(g.Members) > 0, Leader: g.Leader, PendingInvite: s.party.PendingInvite}
	var joined []PartyMember
	for _, gm := range g.Members {
		m, known := old[gm.GUID]
		m.Name, m.GUID, m.Leader = gm.Name, gm.GUID, gm.GUID == g.Leader
		m.Online, m.Dead = gm.Status&game.MemberOnline != 0, gm.Status&(game.MemberDead|game.MemberGhost) != 0
		next.Members = append(next.Members, m)
		delete(old, gm.GUID)
		if !known {
			joined = append(joined, m)
		}
	}
	s.party = next
	s.mu.Unlock()
	for _, m := range joined {
		s.names.setPlayer(m.GUID, m.Name)
		// Ask at once, so health and level arrive without waiting for a change (GroupHandler.cpp:823-827).
		_ = s.conn.WritePacket(opcode.CMSGRequestPartyMemberStats, game.BuildRequestPartyMemberStats(m.GUID))
		s.emit(events.Event{Kind: events.KindParty, Source: m.Name, SourceGUID: m.GUID, Data: map[string]any{"event": "joined"}})
	}
	for _, m := range old {
		s.emit(events.Event{Kind: events.KindParty, Source: m.Name, SourceGUID: m.GUID, Data: map[string]any{"event": "left"}})
	}
	return nil
}

func (s *Session) clearParty(why string) {
	s.mu.Lock()
	s.party = PartyInfo{PendingInvite: s.party.PendingInvite}
	s.mu.Unlock()
	s.emit(events.Event{Kind: events.KindParty, Data: map[string]any{"event": why}})
}

func (s *Session) onGroupSetLeader(body []byte) error {
	name, err := game.ParseCString(body)
	if err != nil {
		return err
	}
	s.mu.Lock()
	for i := range s.party.Members {
		s.party.Members[i].Leader = strings.EqualFold(s.party.Members[i].Name, name)
		if s.party.Members[i].Leader {
			s.party.Leader = s.party.Members[i].GUID
		}
	}
	if strings.EqualFold(s.self.Name, name) {
		s.party.Leader = s.self.GUID
	}
	s.mu.Unlock()
	s.emit(events.Event{Kind: events.KindParty, Data: map[string]any{"event": "leader", "name": name}})
	return nil
}

// apply merges the fields st's mask carries into m.
func (m *PartyMember) apply(st game.MemberStats) {
	if st.Mask&game.GroupUpdateStatus != 0 {
		m.Online, m.Dead = st.Status&game.MemberOnline != 0, st.Status&(game.MemberDead|game.MemberGhost) != 0
	}
	if st.Mask&game.GroupUpdateCurHP != 0 {
		m.Health = st.HP
	}
	if st.Mask&game.GroupUpdateMaxHP != 0 {
		m.MaxHealth = st.MaxHP
	}
	if st.Mask&game.GroupUpdatePowerType != 0 {
		m.PowerType = st.PowerType
	}
	if st.Mask&game.GroupUpdateCurPower != 0 {
		m.Power = st.Power
	}
	if st.Mask&game.GroupUpdateMaxPower != 0 {
		m.MaxPower = st.MaxPower
	}
	if st.Mask&game.GroupUpdateLevel != 0 {
		m.Level = st.Level
	}
	if st.Mask&game.GroupUpdateZone != 0 {
		m.Zone = st.Zone
	}
}

// onMemberStats merges an update into the member and emits the merged state (ruling P4):
// a partial mask must not report the fields it leaves out as 0 or false.
func (s *Session) onMemberStats(body []byte) error {
	st, err := game.ParsePartyMemberStats(body)
	if err != nil {
		return err
	}
	s.mu.Lock()
	idx := -1
	for i := range s.party.Members {
		if s.party.Members[i].GUID == st.GUID {
			idx = i
			break
		}
	}
	var merged PartyMember // a GUID outside the group reports only what the packet carries
	if idx >= 0 {
		merged = s.party.Members[idx]
	}
	merged.apply(st)
	if idx >= 0 {
		s.party.Members[idx] = merged
	}
	s.mu.Unlock()
	s.emit(events.Event{Kind: events.KindPartyStats, Source: merged.Name, SourceGUID: st.GUID,
		Data: map[string]any{"mask": st.Mask, "health": merged.Health, "max_health": merged.MaxHealth,
			"online": merged.Online, "dead": merged.Dead}})
	return nil
}
