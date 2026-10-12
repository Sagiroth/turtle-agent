package session

import (
	"bytes"
	"strings"
	"testing"

	"github.com/pjerra/turtle-agent/internal/events"
	"github.com/pjerra/turtle-agent/internal/game"
	"github.com/pjerra/turtle-agent/internal/opcode"
)

func TestRedactorScrub(t *testing.T) {
	r := newRedactor([]string{"Harness", "", "pw-secret", "HARNESS"})
	for in, want := range map[string]string{
		"":                             "",
		"nothing here":                 "nothing here",
		"Account: HARNESS (id 5)":      "Account: <redacted> (id 5)",
		"harness and hArNeSs":          "<redacted> and <redacted>",
		"x pw-secret,harness y":        "x <redacted>,<redacted> y",
		"Harnessb is the character":    "Harnessb is the character", // a name made from the account stays
		"xharness harness2 pw-secrets": "xharness harness2 pw-secrets",
		"'harness's'":                  "'<redacted>'s'",
		"(HARNESS)":                    "(<redacted>)",
		"PW-SECRET in upper case":      "<redacted> in upper case",
		"ünïcödé harness ünïcödé":      "ünïcödé <redacted> ünïcödé",
		"harnes s is not a full match": "harnes s is not a full match",
	} {
		if got := r.scrub(in); got != want {
			t.Errorf("scrub(%q) = %q, want %q", in, got, want)
		}
	}
	if len(r.needles) != 2 {
		t.Fatalf("needles %q", r.needles)
	}
	if !r.contains([]byte("..HaRnEsS..")) || r.contains([]byte("harnes")) || (redactor{}).contains([]byte("harness")) {
		t.Fatal("contains")
	}
}

// Review I3: an account name the server echoes (a .pinfo reply) never reaches the event
// log, and a packet holding it is never hex-dumped to the stderr log.
func TestEchoedAccountNameIsRedacted(t *testing.T) {
	var logBuf bytes.Buffer
	h := start(t, Options{Logger: &logBuf, Redact: []string{"TESTACC", "pw-secret"}})
	h.enterWorld()
	h.send(opcode.SMSGMessagechat, chatBody(game.ChatSystem, 0, "Player Harnessb (guid 42) Account: testacc (id 5) GMLevel: 3"))
	h.send(opcode.SMSGNotification, []byte("TestAcc pw-secret")) // no terminating NUL: a parse error
	h.logout()
	page := h.s.Log().Read(events.Query{Limit: events.MaxLimit})
	var all strings.Builder
	for _, e := range page.Events {
		all.WriteString(e.Text + e.Source + e.Target)
		for _, v := range e.Data {
			if s, ok := v.(string); ok {
				all.WriteString(s)
			}
		}
	}
	for _, secret := range []string{"testacc", "TESTACC", "TestAcc", "pw-secret"} {
		if strings.Contains(all.String(), secret) || strings.Contains(logBuf.String(), secret) {
			t.Errorf("%q leaked: events %q log %q", secret, all.String(), logBuf.String())
		}
	}
	if sys := ofKind(h.s.Log(), events.KindSystem); len(sys) != 1 || sys[0].Text != "Player Harnessb (guid 42) Account: <redacted> (id 5) GMLevel: 3" {
		t.Fatalf("system %+v", sys)
	}
	if !strings.Contains(logBuf.String(), "SMSG_NOTIFICATION") || !strings.Contains(logBuf.String(), "no dump") || strings.Contains(logBuf.String(), "00000000") {
		t.Fatalf("log %q", logBuf.String())
	}
}
