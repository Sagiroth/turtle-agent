package game

import (
	"strings"
	"testing"

	"github.com/pjerra/turtle-agent/internal/capture"
	"github.com/pjerra/turtle-agent/internal/opcode"
	"github.com/pjerra/turtle-agent/internal/testfixture"
)

// fixture loads a capture recorded on m910q by a milestone's live step (ruling P11:
// the shared testfixture.Load does the work).
func fixture(t *testing.T, name string) []capture.Record {
	t.Helper()
	return testfixture.Load(t, name)
}

// Every chat packet the server sent during milestone 2 parses, and the say, .bot, GM and
// player-not-found replies are there.
func TestM2ChatFromCapture(t *testing.T) {
	recs := fixture(t, "m2-chat.twcap")
	var sawVersion, sawSay, sawBotList bool
	for _, body := range capture.Bodies(recs, opcode.SMSGMessagechat) {
		m, err := ParseMessageChat(body)
		if err != nil {
			t.Fatalf("% X: %v", body, err)
		}
		sawVersion = sawVersion || (m.Type == ChatSystem && strings.HasPrefix(m.Text, "TBM:VERSION|"))
		sawSay = sawSay || (m.Type == ChatSay && m.Text == "turtle-agent milestone 2")
		sawBotList = sawBotList || (m.Type == ChatSystem && strings.HasPrefix(m.Text, "No owned PlayerBots"))
	}
	if !sawVersion || !sawSay || !sawBotList {
		t.Fatalf("version line %v, say line %v, .bot list reply %v", sawVersion, sawSay, sawBotList)
	}
	// The server answers .gm off and .gm visible on with SMSG_NOTIFICATION, not a system chat line.
	notes := map[string]bool{}
	for _, body := range capture.Bodies(recs, opcode.SMSGNotification) {
		text, err := ParseCString(body)
		if err != nil {
			t.Fatalf("% X: %v", body, err)
		}
		notes[text] = true
	}
	for _, want := range []string{"GM mode is OFF", "You are now visible."} {
		if !notes[want] {
			t.Errorf("no notification %q", want)
		}
	}
	notFound := capture.Bodies(recs, opcode.SMSGChatPlayerNotFound)
	if len(notFound) == 0 {
		t.Fatal("no SMSG_CHAT_PLAYER_NOT_FOUND captured")
	}
	for _, body := range notFound {
		if name, err := ParseCString(body); err != nil || name != "Zqxnobodyx" {
			t.Fatalf("player not found % X: %q, %v", body, name, err)
		}
	}
}
