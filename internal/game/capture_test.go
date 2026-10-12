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

// Every chat packet the server sent during milestone 2 parses, and the .bot and say lines are there.
func TestM2ChatFromCapture(t *testing.T) {
	var sawVersion, sawSay bool
	for _, body := range capture.Bodies(fixture(t, "m2-chat.twcap"), opcode.SMSGMessagechat) {
		m, err := ParseMessageChat(body)
		if err != nil {
			t.Fatalf("% X: %v", body, err)
		}
		sawVersion = sawVersion || (m.Type == ChatSystem && strings.HasPrefix(m.Text, "TBM:VERSION|"))
		sawSay = sawSay || (m.Type == ChatSay && m.Text == "turtle-agent milestone 2")
	}
	if !sawVersion || !sawSay {
		t.Fatalf("version line %v, say line %v", sawVersion, sawSay)
	}
}
