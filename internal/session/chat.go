package session

import (
	"context"
	"errors"
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

// chatKind maps a chat type to its event kind. For whisper_inform the server puts the
// recipient's GUID in the sender field (MasterPlayerChat.cpp:58), so such an event's
// Source is the recipient, as the client shows it and headless.py expects.
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

// GM sends a GM or .bot command as say chat, adding the leading "." when it is missing.
// The server runs "."-prefixed say as a command instead of broadcasting it
// (ChatHandler.cpp:83-96), but says aloud a lone "." and anything starting with ".."
// (Chat.cpp:1769-1779), so GM refuses those. An account below GM level without
// PlayerCommands also says it aloud (Chat.cpp:1773-1774); that is server configuration.
func (s *Session) GM(ctx context.Context, text string) error {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, ".") {
		text = "." + text
	}
	if strings.TrimSpace(text[1:]) == "" {
		return errors.New("gm: empty command")
	}
	if strings.HasPrefix(text, "..") {
		return fmt.Errorf("gm: %q starts with \"..\", which the server says aloud instead of running", text)
	}
	return s.Chat(ctx, "say", text, "")
}
