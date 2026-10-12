package session

import (
	"context"

	"github.com/pjerra/turtle-agent/internal/events"
	"github.com/pjerra/turtle-agent/internal/game"
	"github.com/pjerra/turtle-agent/internal/opcode"
)

// registerSocial installs the chat channel and gossip handlers.
func (s *Session) registerSocial() {
	s.handlers[opcode.SMSGChannelNotify] = s.onChannelNotify
}

// JoinChannel joins a chat channel (CMSG_JOIN_CHANNEL); the server answers with
// SMSG_CHANNEL_NOTIFY, logged as channel_notify events.
func (s *Session) JoinChannel(ctx context.Context, name, password string) error {
	body, err := game.BuildJoinChannel(name, password)
	if err != nil {
		return err
	}
	return s.send(ctx, opcode.CMSGJoinChannel, body)
}

// LeaveChannel leaves a chat channel (CMSG_LEAVE_CHANNEL).
func (s *Session) LeaveChannel(ctx context.Context, name string) error {
	body, err := game.BuildLeaveChannel(name)
	if err != nil {
		return err
	}
	return s.send(ctx, opcode.CMSGLeaveChannel, body)
}

// onChannelNotify logs a channel notice. The player it is about is the source; a kick, ban
// or unban has the target as target and whoever did it as source.
func (s *Session) onChannelNotify(body []byte) error {
	n, err := game.ParseChannelNotify(body)
	if err != nil {
		return err
	}
	e := events.Event{Kind: events.KindChannelNotify, SourceGUID: n.Player, Text: n.Text(),
		Data: map[string]any{"notice": n.Notice.String(), "channel": n.Channel}}
	if n.Source != 0 {
		e.SourceGUID, e.TargetGUID = n.Source, n.Player
	}
	if n.Name != "" {
		e.Data["name"] = n.Name
	}
	switch n.Notice {
	case game.ChannelYouJoinedNotice:
		e.Data["flags"] = n.Flags
	case game.ChannelModeChangeNotice:
		e.Data["old_flags"], e.Data["new_flags"] = n.OldFlags, n.NewFlags
	}
	s.emit(e)
	return nil
}
