// Package events is the ordered event log the session appends to and RPC pages through.
package events

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/pjerra/turtle-agent/internal/packet"
)

const (
	KindSession     = "session"      // state changes: in_world, logout_pending, logged_out, disconnected, teleported, knocked_back, landed, reconnected
	KindChat        = "chat"         // say, party, yell, emote, channel, monster speech
	KindWhisper     = "whisper"      // whispers in and out
	KindSystem      = "system"       // system messages, GM command output, notifications
	KindParty       = "party"        // invites, command results, joins and leaves
	KindPartyStats  = "party_stats"  // SMSG_PARTY_MEMBER_STATS
	KindMelee       = "melee"        // SMSG_ATTACKERSTATEUPDATE
	KindSpellDamage = "spell_damage" // SMSG_SPELLNONMELEEDAMAGELOG
	KindPeriodic    = "periodic"     // SMSG_PERIODICAURALOG
	KindHeal        = "heal"         // SMSG_SPELLHEALLOG
	KindEnergize    = "energize"     // SMSG_SPELLENERGIZELOG
	KindSpellStart  = "spell_start"
	KindSpellGo     = "spell_go"
	KindSpellFail   = "spell_fail"
	KindSpellMiss   = "spell_miss"
	KindAuraApplied = "aura_applied"
	KindAuraRemoved = "aura_removed"
	KindDeath       = "death" // health reached zero
	KindKill        = "kill"  // SMSG_PARTYKILLLOG
	KindAttackStart = "attack_start"
	KindAttackStop  = "attack_stop"
	KindAttackError = "attack_error"
	KindParseError  = "parse_error"
	KindRaidMark    = "raid_mark" // MSG_RAID_TARGET_UPDATE: a raid icon set on or cleared from a unit
	// Group loot rolls, tied together by data.roll_id.
	KindLootStart     = "loot_roll_start" // SMSG_LOOT_START_ROLL: an item is up for rolls
	KindLootRoll      = "loot_roll"       // SMSG_LOOT_ROLL: a member's need/greed/pass, or their dice
	KindLootWon       = "loot_roll_won"   // SMSG_LOOT_ROLL_WON
	KindLootAllPassed = "loot_all_passed" // SMSG_LOOT_ALL_PASSED
	// KindInventoryError is SMSG_INVENTORY_CHANGE_FAILURE: an equip or move the server refused.
	KindInventoryError = "inventory_error"
	KindAddon          = "addon" // LANG_ADDON chat: prefix, text and sender (CHAT_MSG_ADDON in the client)
	// KindChannelNotify is SMSG_CHANNEL_NOTIFY: joined, left, kicked, wrong password, …
	KindChannelNotify  = "channel_notify"
	KindGossip         = "gossip"          // SMSG_GOSSIP_MESSAGE: an NPC's menu
	KindGossipComplete = "gossip_complete" // SMSG_GOSSIP_COMPLETE: the menu closed
	// The loot window of a corpse, its guid in source.
	KindLootOpen       = "loot_open"        // SMSG_LOOT_RESPONSE: the window with its money and items
	KindLootError      = "loot_error"       // SMSG_LOOT_RESPONSE refused: data.reason
	KindLootTaken      = "loot_taken"       // SMSG_LOOT_REMOVED: a slot left the window
	KindLootMoney      = "loot_money"       // SMSG_LOOT_CLEAR_MONEY: the window's money was taken
	KindLootMoneyShare = "loot_money_share" // SMSG_LOOT_MONEY_NOTIFY: this player's share of split money
	KindLootClosed     = "loot_closed"      // SMSG_LOOT_RELEASE_RESPONSE
	// Self state changes seen in the player's own update fields.
	KindMount       = "mount"        // UNIT_FIELD_MOUNTDISPLAYID went to or from 0: data.mounted
	KindMountResult = "mount_result" // SMSG_MOUNTRESULT and SMSG_DISMOUNTRESULT: data.result
	KindStandState  = "stand_state"  // the stand state changed (sit while eating or drinking): data.state
)

// ChatKinds is what the chat_log method returns.
var ChatKinds = []string{KindChat, KindWhisper, KindSystem}

// Event is one log entry. Names are resolved by the session before Append.
type Event struct {
	Seq        uint64         `json:"seq"`
	Time       time.Time      `json:"time"`
	Kind       string         `json:"kind"`
	Source     string         `json:"source,omitempty"`
	SourceGUID packet.GUID    `json:"source_guid,omitempty"`
	Target     string         `json:"target,omitempty"`
	TargetGUID packet.GUID    `json:"target_guid,omitempty"`
	SpellID    uint32         `json:"spell_id,omitempty"`
	Amount     int64          `json:"amount,omitempty"`
	Crit       bool           `json:"crit,omitempty"`
	Party      bool           `json:"party,omitempty"` // source or target is the player or a party member
	Text       string         `json:"text,omitempty"`
	Data       map[string]any `json:"data,omitempty"`
}

const (
	DefaultLimit = 500
	MaxLimit     = 5000
)

type Query struct {
	Since     uint64
	Kinds     []string
	PartyOnly bool
	Limit     int
}

type Page struct {
	Events  []Event `json:"events"`
	Next    uint64  `json:"next"`
	Oldest  uint64  `json:"oldest"`
	Dropped bool    `json:"dropped"`
	More    bool    `json:"more"`
}

// Log is a bounded ring of events; when full it drops the oldest tenth.
type Log struct {
	mu      sync.Mutex
	limit   int
	buf     []Event
	last    uint64
	changed chan struct{}
}

func New(capacity int) *Log {
	if capacity < 10 {
		capacity = 10
	}
	return &Log{limit: capacity, changed: make(chan struct{})}
}

// Append stores e with the next sequence number, stamps Time if unset and wakes waiters.
func (l *Log) Append(e Event) Event {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.last++
	e.Seq = l.last
	if e.Time.IsZero() {
		e.Time = time.Now()
	}
	if len(l.buf) >= l.limit {
		n := copy(l.buf, l.buf[l.limit/10:])
		clear(l.buf[n:])
		l.buf = l.buf[:n]
	}
	l.buf = append(l.buf, e)
	close(l.changed)
	l.changed = make(chan struct{})
	return e
}

func (l *Log) Last() uint64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.last
}

func (l *Log) Read(q Query) Page {
	l.mu.Lock()
	defer l.mu.Unlock()
	limit := q.Limit
	if limit <= 0 {
		limit = DefaultLimit
	}
	if limit > MaxLimit {
		limit = MaxLimit
	}
	p := Page{Events: []Event{}, Next: q.Since, Oldest: l.last + 1}
	if len(l.buf) > 0 {
		p.Oldest = l.buf[0].Seq
	}
	if q.Since > l.last {
		p.Next = l.last
		return p
	}
	p.Dropped = q.Since+1 < p.Oldest
	kinds := make(map[string]bool, len(q.Kinds))
	for _, k := range q.Kinds {
		kinds[k] = true
	}
	for i := sort.Search(len(l.buf), func(i int) bool { return l.buf[i].Seq > q.Since }); i < len(l.buf); i++ {
		if len(p.Events) == limit {
			p.More = true
			break
		}
		e := l.buf[i]
		p.Next = e.Seq
		if (len(kinds) > 0 && !kinds[e.Kind]) || (q.PartyOnly && !e.Party) {
			continue
		}
		p.Events = append(p.Events, e)
	}
	return p
}

// Wait returns once an event with Seq > since exists, or when ctx ends.
func (l *Log) Wait(ctx context.Context, since uint64) error {
	for {
		l.mu.Lock()
		if l.last > since {
			l.mu.Unlock()
			return nil
		}
		ch := l.changed
		l.mu.Unlock()
		select {
		case <-ch:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
