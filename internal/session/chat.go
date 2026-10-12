package session

import (
	"context"
	"fmt"
	"strings"

	"github.com/pjerra/turtle-agent/internal/events"
	"github.com/pjerra/turtle-agent/internal/game"
	"github.com/pjerra/turtle-agent/internal/opcode"
)

func (s *Session) registerChat() {
	s.handlers[opcode.SMSGMessagechat] = s.onMessageChat
	s.handlers[opcode.SMSGNotification] = s.onNotification
	s.handlers[opcode.SMSGChatPlayerNotFound] = s.onPlayerNotFound
	s.handlers[opcode.SMSGNameQueryResponse] = s.onNameQueryResponse
	s.handlers[opcode.SMSGCreatureQueryResponse] = s.onCreatureQueryResponse
}

func chatKind(t game.ChatType) string {
	switch t {
	case game.ChatWhisper, game.ChatWhisperInform, game.ChatMonsterWhisper, game.ChatRaidBossWhisper:
		return events.KindWhisper
	case game.ChatSystem:
		return events.KindSystem
	}
	return events.KindChat
}

func (s *Session) onMessageChat(body []byte) error {
	m, err := game.ParseMessageChat(body)
	if err != nil {
		return err
	}
	data := map[string]any{"type": m.Type.String()}
	if m.Channel != "" {
		data["channel"] = m.Channel
	}
	if m.Tag != 0 {
		data["tag"] = m.Tag
	}
	s.emit(events.Event{Kind: chatKind(m.Type), Source: m.SenderName, SourceGUID: m.Sender,
		TargetGUID: m.Target, Text: m.Text, Data: data})
	return nil
}

func (s *Session) onNotification(body []byte) error {
	text, err := game.ParseCString(body)
	if err != nil {
		return err
	}
	s.emit(events.Event{Kind: events.KindSystem, Text: game.StripColors(text), Data: map[string]any{"type": "notification"}})
	return nil
}

func (s *Session) onPlayerNotFound(body []byte) error {
	name, err := game.ParseCString(body)
	if err != nil {
		return err
	}
	s.emit(events.Event{Kind: events.KindSystem, Text: fmt.Sprintf("No player named '%s' is currently playing.", name),
		Data: map[string]any{"type": "player_not_found", "name": name}})
	return nil
}

// Chat sends one chat line; typ is a sendable chat type name ("say", "party", "whisper", …).
func (s *Session) Chat(ctx context.Context, typ, text, to string) error {
	t, err := game.ChatTypeByName(typ)
	if err != nil {
		return err
	}
	body, err := game.BuildMessageChat(t, to, text)
	if err != nil {
		return err
	}
	return s.send(ctx, opcode.CMSGMessagechat, body)
}

// GM sends a GM or .bot command as say chat; the server runs "."-prefixed chat as a
// command and never broadcasts it (ChatHandler.cpp:83-96).
func (s *Session) GM(ctx context.Context, text string) error {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, ".") {
		text = "." + text
	}
	return s.Chat(ctx, "say", text, "")
}
