package session

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/pjerra/turtle-agent/internal/auth"
	"github.com/pjerra/turtle-agent/internal/events"
	"github.com/pjerra/turtle-agent/internal/opcode"
	"github.com/pjerra/turtle-agent/internal/packet"
	"github.com/pjerra/turtle-agent/internal/worldconn"
	"github.com/pjerra/turtle-agent/internal/worldconn/worldtest"
)

var testKey = bytes.Repeat([]byte{0x5A, 0xA5}, 20)

var me = Character{GUID: 0x2A, Name: "Harnessb", Race: 1, Class: 1, Gender: 0, Level: 60, Zone: 12, Map: 0,
	X: -8913, Y: -136, Z: 82, Guild: 0, Flags: 0}

type harness struct {
	t   *testing.T
	s   *Session
	srv *worldtest.Server
	run chan error
}

func start(t *testing.T, opts Options) *harness {
	t.Helper()
	return startKeyed(t, opts, testKey, testKey)
}

// startKeyed keys the client with clientKey and the scripted server with serverKey,
// the form the world server derives from the account's stored K.
func startKeyed(t *testing.T, opts Options, clientKey, serverKey []byte) *harness {
	t.Helper()
	cli, srvConn := net.Pipe()
	t.Cleanup(func() { cli.Close(); srvConn.Close() })
	ch := make(chan *worldtest.Server, 1)
	go func() {
		srv, err := worldtest.Accept(srvConn, serverKey, worldtest.Options{})
		if err != nil {
			t.Error(err)
		}
		ch <- srv
	}()
	conn, err := worldconn.Client(cli, "TESTACC", clientKey, nil)
	if err != nil {
		<-ch // the server goroutine reports through t; let it finish before the test does
		t.Fatal(err)
	}
	if opts.Character == "" {
		opts.Character = "Harnessb"
	}
	h := &harness{t: t, srv: <-ch, run: make(chan error, 1)}
	h.s = New(conn, opts)
	go func() { h.run <- h.s.Run(context.Background()) }()
	// Ruling P1/P2: no Run goroutine outlives its test. Closing the pipes ends Run;
	// Done closes before Run returns into the buffered h.run, drained or not.
	t.Cleanup(func() {
		cli.Close()
		srvConn.Close()
		select {
		case <-h.s.Done():
		case <-time.After(5 * time.Second):
			t.Error("Run still running after the pipes closed")
		}
	})
	return h
}

// charEnumBody builds SMSG_CHAR_ENUM the way Player::BuildEnumData writes it (Player.cpp:2198-2285).
func charEnumBody(chars ...Character) []byte {
	w := packet.NewWriter().U8(uint8(len(chars)))
	for _, c := range chars {
		w.GUID(c.GUID).CString(c.Name).U8(c.Race).U8(c.Class).U8(c.Gender)
		w.U8(1).U8(2).U8(3).U8(4).U8(5) // skin, face, hair style, hair colour, facial hair
		w.U8(c.Level).U32(c.Zone).U32(c.Map).F32(c.X).F32(c.Y).F32(c.Z).U32(c.Guild).U32(c.Flags)
		if c.FirstLogin {
			w.U8(1)
		} else {
			w.U8(0)
		}
		w.U32(0).U32(0).U32(0) // pet display, level, family
		for slot := 0; slot < 20; slot++ {
			w.U32(uint32(1000 + slot)).U8(uint8(slot))
		}
	}
	return w.Bytes()
}

func (h *harness) expect(op uint16) worldconn.Packet {
	h.t.Helper()
	p, err := h.srv.Expect(op, 2*time.Second)
	if err != nil {
		h.t.Fatal(err)
	}
	return p
}

func (h *harness) send(op uint16, body []byte) {
	h.t.Helper()
	if err := h.srv.Send(op, body); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) enterWorld() {
	h.t.Helper()
	h.expect(opcode.CMSGCharEnum)
	h.send(opcode.SMSGCharEnum, charEnumBody(Character{GUID: 0x2B, Name: "Alt", Level: 1}, me))
	if p := h.expect(opcode.CMSGPlayerLogin); packet.NewReader(p.Body).GUID() != me.GUID {
		h.t.Fatalf("logged in % X", p.Body)
	}
	h.send(opcode.SMSGLoginVerifyWorld, packet.NewWriter().U32(0).F32(me.X).F32(me.Y).F32(me.Z).F32(1.5).Bytes())
	h.expect(opcode.CMSGSetActiveMover)
	select {
	case <-h.s.Ready():
	case <-time.After(2 * time.Second):
		h.t.Fatal("not ready")
	}
}

func (h *harness) logout() {
	h.t.Helper()
	done := make(chan error, 1)
	go func() { done <- h.s.Logout(context.Background()) }()
	h.expect(opcode.CMSGLogoutRequest)
	h.send(opcode.SMSGLogoutResponse, packet.NewWriter().U32(0).U8(1).Bytes())
	h.send(opcode.SMSGLogoutComplete, nil)
	if err := <-done; err != nil {
		h.t.Fatal(err)
	}
	if err := <-h.run; err != nil {
		h.t.Fatal(err)
	}
}

func ofKind(l *events.Log, kind string) []events.Event {
	return l.Read(events.Query{Kinds: []string{kind}, Limit: events.MaxLimit}).Events
}

func sessionStates(l *events.Log) []string {
	var out []string
	for _, e := range ofKind(l, events.KindSession) {
		out = append(out, fmt.Sprint(e.Data["state"]))
	}
	return out
}

func TestCharEnumTailIs150Bytes(t *testing.T) {
	body := charEnumBody(me)
	if len(body) != 1+8+len(me.Name)+1+150 {
		t.Fatalf("builder wrote %d bytes", len(body))
	}
	chars, err := ParseCharEnum(body)
	if err != nil || len(chars) != 1 || chars[0] != me {
		t.Fatalf("%+v %v", chars, err)
	}
	if _, err := ParseCharEnum(append(body, 0)); err == nil || !strings.Contains(err.Error(), "unread") {
		t.Fatalf("trailing byte: %v", err)
	}
}

func TestLoginToWorldAndLogout(t *testing.T) {
	h := start(t, Options{})
	h.enterWorld()
	if st := h.s.Status(); st.State != StatusInWorld || st.Character != "Harnessb" || st.GUID != me.GUID || st.X != me.X || st.O != 1.5 {
		t.Fatalf("%+v", st)
	}
	h.logout()
	if st := h.s.Status(); st.State != StatusLoggedOut {
		t.Fatalf("%+v", st)
	}
	if got := strings.Join(sessionStates(h.s.Log()), ","); got != "in_world,logout_pending,logged_out" {
		t.Fatalf("session events %s", got)
	}
}

func TestMissingCharacterEndsSession(t *testing.T) {
	h := start(t, Options{})
	h.expect(opcode.CMSGCharEnum)
	h.send(opcode.SMSGCharEnum, charEnumBody(Character{GUID: 0x2B, Name: "Alt"}))
	if err := <-h.run; err == nil || !strings.Contains(err.Error(), "Alt") {
		t.Fatalf("err = %v", err)
	}
}

// Review Focus 4.
func TestDisconnectInWorld(t *testing.T) {
	h := start(t, Options{})
	h.enterWorld()
	h.srv.Close()
	if err := <-h.run; !errors.Is(err, ErrDisconnected) {
		t.Fatalf("run err = %v", err)
	}
	if st := h.s.Status(); st.State != StatusDisconnected || st.Error == "" {
		t.Fatalf("%+v", st)
	}
	if states := sessionStates(h.s.Log()); states[len(states)-1] != "disconnected" {
		t.Fatalf("events %v", states)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := h.s.Logout(ctx); !errors.Is(err, ErrClosed) {
		t.Fatalf("logout after disconnect: %v", err)
	}
}

func TestDisconnectDuringLogout(t *testing.T) {
	h := start(t, Options{})
	h.enterWorld()
	done := make(chan error, 1)
	go func() { done <- h.s.Logout(context.Background()) }()
	h.expect(opcode.CMSGLogoutRequest)
	h.srv.Close()
	if err := <-done; !errors.Is(err, ErrDisconnected) {
		t.Fatalf("logout err = %v", err)
	}
	if err := <-h.run; !errors.Is(err, ErrDisconnected) {
		t.Fatalf("run err = %v", err)
	}
}

func TestLogoutRefusedInCombat(t *testing.T) {
	h := start(t, Options{})
	h.enterWorld()
	done := make(chan error, 1)
	go func() { done <- h.s.Logout(context.Background()) }()
	h.expect(opcode.CMSGLogoutRequest)
	h.send(opcode.SMSGLogoutResponse, packet.NewWriter().U32(1).U8(0).Bytes())
	var le *LogoutError
	if err := <-done; !errors.As(err, &le) || le.Reason != 1 {
		t.Fatalf("err = %v", err)
	}
	if h.s.State() != StatusInWorld {
		t.Fatalf("state %s", h.s.State())
	}
	h.logout()
}

// Review Focus 3: unknown opcodes are counted, a bad packet is logged, the session lives on.
func TestUnknownOpcodeCountedAndParseErrorSurvives(t *testing.T) {
	var logBuf bytes.Buffer
	h := start(t, Options{Logger: &logBuf})
	h.enterWorld()
	h.send(opcode.SMSGInitWorldStates, []byte{1, 2, 3})
	h.send(opcode.SMSGWardenData, []byte{0, 1, 2})
	h.send(opcode.SMSGPong, nil) // too short for its u32
	h.logout()
	st := h.s.Status()
	if st.Unknown["SMSG_INIT_WORLD_STATES"] != 1 || st.Unknown["SMSG_WARDEN_DATA"] != 0 || st.ParseErrors != 1 {
		t.Fatalf("%+v", st)
	}
	if pe := ofKind(h.s.Log(), events.KindParseError); len(pe) != 1 || pe[0].Data["opcode"] != "SMSG_PONG" {
		t.Fatalf("parse events %+v", pe)
	}
	if !strings.Contains(logBuf.String(), "SMSG_PONG") {
		t.Fatalf("log %q", logBuf.String())
	}
}

func TestPingCadence(t *testing.T) {
	h := start(t, Options{PingInterval: 50 * time.Millisecond})
	h.enterWorld()
	p := h.expect(opcode.CMSGPing)
	if packet.NewReader(p.Body).U32() != 1 {
		t.Fatalf("first ping % X", p.Body)
	}
	h.send(opcode.SMSGPong, packet.NewWriter().U32(1).Bytes())
	if p := h.expect(opcode.CMSGPing); packet.NewReader(p.Body).U32() != 2 {
		t.Fatalf("second ping % X", p.Body)
	}
	h.logout()
}

func TestPongTimeoutDisconnects(t *testing.T) {
	h := start(t, Options{PingInterval: 30 * time.Millisecond, PongTimeout: 100 * time.Millisecond})
	h.enterWorld()
	select {
	case err := <-h.run:
		if !errors.Is(err, ErrDisconnected) {
			t.Fatalf("err = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no disconnect without pongs")
	}
}

// Ruling T6: the world connection is keyed with Ticket.KeyBytes(), never Ticket.Key[:].
// The server hashes and keys its cipher with K.AsByteArray() (WorldSocket.cpp:296, :341),
// which drops high zero bytes, so a K whose high byte is zero is 39 bytes on both sides.
func TestZeroHighByteKeyEndToEnd(t *testing.T) {
	var tk auth.Ticket
	for i := range tk.Key {
		tk.Key[i] = byte(0x11 + i)
	}
	tk.Key[len(tk.Key)-1] = 0 // little-endian: the high byte
	// The server's rendering, derived independently: K as a number, little-endian, minimal length.
	be := make([]byte, len(tk.Key))
	for i, b := range tk.Key {
		be[len(be)-1-i] = b
	}
	n := new(big.Int).SetBytes(be).Bytes()
	serverKey := make([]byte, len(n))
	for i, b := range n {
		serverKey[len(n)-1-i] = b
	}
	if len(serverKey) != 39 || !bytes.Equal(tk.KeyBytes(), serverKey) {
		t.Fatalf("KeyBytes is %d bytes, the server form %d; they must match", len(tk.KeyBytes()), len(serverKey))
	}
	h := startKeyed(t, Options{}, tk.KeyBytes(), serverKey)
	h.enterWorld()
	if st := h.s.Status(); st.State != StatusInWorld || st.GUID != me.GUID {
		t.Fatalf("%+v", st)
	}
	h.logout()
}
