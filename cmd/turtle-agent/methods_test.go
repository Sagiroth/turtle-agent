package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pjerra/turtle-agent/internal/events"
	"github.com/pjerra/turtle-agent/internal/opcode"
	"github.com/pjerra/turtle-agent/internal/packet"
	"github.com/pjerra/turtle-agent/internal/rpc"
	"github.com/pjerra/turtle-agent/internal/session"
	"github.com/pjerra/turtle-agent/internal/worldconn"
	"github.com/pjerra/turtle-agent/internal/worldconn/worldtest"
)

type agentRig struct {
	t   *testing.T
	s   *session.Session
	srv *worldtest.Server
	ts  *httptest.Server
	run chan error
}

// newRig logs a session into a scripted world server and serves its methods over HTTP.
func newRig(t *testing.T) *agentRig {
	t.Helper()
	key := bytes.Repeat([]byte{7}, 40)
	cli, srvConn := net.Pipe()
	t.Cleanup(func() { cli.Close(); srvConn.Close() })
	ch := make(chan *worldtest.Server, 1)
	go func() { srv, _ := worldtest.Accept(srvConn, key, worldtest.Options{}); ch <- srv }()
	conn, err := worldconn.Client(cli, "TESTACC", key, nil)
	if err != nil {
		t.Fatal(err)
	}
	r := &agentRig{t: t, srv: <-ch, run: make(chan error, 1)}
	r.s = session.New(conn, session.Options{Character: "Harnessb"})
	go func() { r.run <- r.s.Run(context.Background()) }()
	srv := rpc.NewServer()
	registerMethods(srv, r.s)
	r.ts = httptest.NewServer(srv.Handler())
	t.Cleanup(r.ts.Close)
	r.expect(opcode.CMSGCharEnum)
	enum := packet.NewWriter().U8(1).GUID(0x2A).CString("Harnessb").Append(make([]byte, session.CharEnumTailLen)).Bytes()
	r.srv.Send(opcode.SMSGCharEnum, enum)
	r.expect(opcode.CMSGPlayerLogin)
	r.srv.Send(opcode.SMSGLoginVerifyWorld, packet.NewWriter().U32(0).F32(1).F32(2).F32(3).F32(0).Bytes())
	// Ready never closes when login fails, so Done ends the wait too.
	select {
	case <-r.s.Ready():
	case <-r.s.Done():
		t.Fatalf("login failed: %v", <-r.run)
	case <-time.After(2 * time.Second):
		t.Fatal("not in world after 2 s")
	}
	return r
}

func (r *agentRig) expect(op uint16) {
	r.t.Helper()
	if _, err := r.srv.Expect(op, 2*time.Second); err != nil {
		r.t.Fatal(err)
	}
}

func (r *agentRig) call(method, params string) map[string]any {
	r.t.Helper()
	body := `{"jsonrpc":"2.0","id":1,"method":"` + method + `","params":` + params + `}`
	resp, err := http.Post(r.ts.URL+"/rpc", "application/json", strings.NewReader(body))
	if err != nil {
		r.t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	return out
}

func TestStatusChatAndChatLog(t *testing.T) {
	r := newRig(t)
	if st := r.call("status", "{}")["result"].(map[string]any); st["state"] != "in_world" || st["character"] != "Harnessb" {
		t.Fatalf("%v", st)
	}
	if out := r.call("chat", `{"type":"say","text":"hi"}`); out["error"] != nil {
		t.Fatalf("%v", out)
	}
	r.expect(opcode.CMSGMessagechat)
	if out := r.call("chat", `{"type":"system","text":"hi"}`); errCode(out) != rpc.CodeGame {
		t.Fatalf("%v", out)
	}
	go func() {
		time.Sleep(100 * time.Millisecond)
		r.srv.Send(opcode.SMSGNotification, []byte("You are now visible.\x00"))
	}()
	page := r.call("chat_log", `{"since":0,"wait_ms":3000}`)["result"].(map[string]any)
	evs := page["events"].([]any)
	if len(evs) != 1 || evs[0].(map[string]any)["text"] != "You are now visible." {
		t.Fatalf("%v", page)
	}
}

// The HeadlessClient (wow-play-harness) reads status.last_seq, sends gm {text} and pages
// events by next/more/dropped/oldest; this pins those names.
func TestHeadlessClientShapes(t *testing.T) {
	r := newRig(t)
	st := r.call("status", "{}")["result"].(map[string]any)
	last, ok := st["last_seq"].(float64)
	if !ok || last < 1 {
		t.Fatalf("last_seq: %v", st)
	}
	if out := r.call("gm", `{"text":"gm visible on"}`); out["error"] != nil || len(out["result"].(map[string]any)) != 0 {
		t.Fatalf("%v", out)
	}
	if p, err := r.srv.Expect(opcode.CMSGMessagechat, 2*time.Second); err != nil || !bytes.Contains(p.Body, []byte(".gm visible on\x00")) {
		t.Fatalf("%v %q", err, p.Body)
	}
	if out := r.call("chat", `{"type":"whisper","to":"Someone","text":"psst"}`); out["error"] != nil {
		t.Fatalf("%v", out)
	}
	r.expect(opcode.CMSGMessagechat)
	page := r.call("events", `{"since":0,"kinds":["session"],"limit":5000}`)["result"].(map[string]any)
	for _, k := range []string{"events", "next", "more", "dropped", "oldest"} {
		if _, ok := page[k]; !ok {
			t.Fatalf("page lacks %q: %v", k, page)
		}
	}
	if page["next"] != last || page["more"] != false || page["dropped"] != false || len(page["events"].([]any)) != 1 {
		t.Fatalf("%v", page)
	}
	if out := r.call("events", `[0]`); errCode(out) != rpc.CodeInvalidParams {
		t.Fatalf("positional params: %v", out)
	}
}

// Ruling T7 minor 1: a cursor past the end of the log (a client that outlived an agent
// restart) is clamped, so a wait returns on the next append instead of blocking.
func TestStaleCursorWaitReturnsOnNextAppend(t *testing.T) {
	log := events.New(100)
	for i := 0; i < 3; i++ {
		log.Append(events.Event{Kind: events.KindSystem, Text: "old"})
	}
	got := make(chan events.Page, 1)
	go func() { got <- readEvents(context.Background(), log, events.Query{Since: 1000}, 3000) }()
	time.Sleep(50 * time.Millisecond)
	log.Append(events.Event{Kind: events.KindSystem, Text: "new"})
	select {
	case p := <-got:
		if len(p.Events) != 1 || p.Events[0].Text != "new" || p.Next != 4 {
			t.Fatalf("%+v", p)
		}
	case <-time.After(time.Second):
		t.Fatal("stale cursor blocked past the next append")
	}
}

func TestReadEventsWaitBoundsAndFilters(t *testing.T) {
	log := events.New(100)
	start := time.Now()
	if p := readEvents(context.Background(), log, events.Query{}, 100); len(p.Events) != 0 || time.Since(start) < 90*time.Millisecond {
		t.Fatalf("%+v after %s", p, time.Since(start))
	}
	got := make(chan events.Page, 1)
	go func() {
		got <- readEvents(context.Background(), log, events.Query{Kinds: []string{events.KindWhisper}}, 3000)
	}()
	time.Sleep(50 * time.Millisecond)
	log.Append(events.Event{Kind: events.KindSystem, Text: "skip"})
	log.Append(events.Event{Kind: events.KindWhisper, Text: "match"})
	if p := <-got; len(p.Events) != 1 || p.Events[0].Text != "match" || p.Next != 2 {
		t.Fatalf("%+v", p)
	}
}

// A gap before the cursor, seen on the first read, is still reported when the wait
// returns a later page (the follow-up reads start past the gap).
func TestReadEventsKeepsDroppedAcrossWait(t *testing.T) {
	log := events.New(10)
	for i := 0; i < 11; i++ { // the 11th append drops the oldest tenth (seq 1)
		log.Append(events.Event{Kind: events.KindSystem})
	}
	got := make(chan events.Page, 1)
	go func() {
		got <- readEvents(context.Background(), log, events.Query{Kinds: []string{events.KindWhisper}}, 3000)
	}()
	time.Sleep(50 * time.Millisecond)
	log.Append(events.Event{Kind: events.KindWhisper, Text: "match"})
	if p := <-got; len(p.Events) != 1 || !p.Dropped {
		t.Fatalf("%+v", p)
	}
}

func TestRPCErrMapping(t *testing.T) {
	for _, c := range []struct {
		err  error
		code int
	}{
		{session.ErrNotInWorld, rpc.CodeNotInWorld},
		{session.ErrDisconnected, rpc.CodeDisconnected},
		{session.ErrClosed, rpc.CodeDisconnected},
		{context.DeadlineExceeded, rpc.CodeTimeout},
		{context.Canceled, rpc.CodeDisconnected},
		{&session.LogoutError{Reason: 1}, rpc.CodeGame},
	} {
		var re *rpc.Error
		if !errors.As(rpcErr(c.err), &re) || re.Code != c.code {
			t.Errorf("%v: %v, want %d", c.err, rpcErr(c.err), c.code)
		}
	}
	re := rpcErr(&session.LogoutError{Reason: 1}).(*rpc.Error)
	if re.Data.(map[string]any)["reason"] != uint32(1) {
		t.Fatalf("%+v", re)
	}
	if rpcErr(nil) != nil {
		t.Fatal("nil error mapped")
	}
}

func TestLogoutRefusedThenComplete(t *testing.T) {
	r := newRig(t)
	got := make(chan map[string]any, 1)
	go func() { got <- r.call("logout", "{}") }()
	r.expect(opcode.CMSGLogoutRequest)
	r.srv.Send(opcode.SMSGLogoutResponse, packet.NewWriter().U32(1).U8(0).Bytes())
	out := <-got
	if errCode(out) != rpc.CodeGame || out["error"].(map[string]any)["data"].(map[string]any)["reason"] != float64(1) {
		t.Fatalf("refused: %v", out)
	}
	go func() { got <- r.call("logout", "{}") }()
	r.expect(opcode.CMSGLogoutRequest)
	r.srv.Send(opcode.SMSGLogoutResponse, packet.NewWriter().U32(0).U8(1).Bytes())
	r.srv.Send(opcode.SMSGLogoutComplete, nil)
	if out := <-got; out["error"] != nil || len(out["result"].(map[string]any)) != 0 {
		t.Fatalf("logout: %v", out)
	}
	if err := <-r.run; err != nil {
		t.Fatal(err)
	}
	if out := r.call("gm", `{"text":".gm on"}`); errCode(out) != rpc.CodeDisconnected {
		t.Fatalf("after logout: %v", out)
	}
}

// Review Focus 4: after the server drops the connection, calls fail fast with -32002.
func TestRPCAfterDisconnect(t *testing.T) {
	r := newRig(t)
	r.srv.Close()
	<-r.run
	start := time.Now()
	if out := r.call("chat", `{"type":"say","text":"hi"}`); errCode(out) != rpc.CodeDisconnected {
		t.Fatalf("%v", out)
	}
	if time.Since(start) > time.Second {
		t.Fatal("call after disconnect was slow")
	}
	if st := r.call("status", "{}")["result"].(map[string]any); st["state"] != "disconnected" || st["error"] == "" {
		t.Fatalf("%v", st)
	}
}

func errCode(m map[string]any) int {
	e, _ := m["error"].(map[string]any)
	c, _ := e["code"].(float64)
	return int(c)
}
