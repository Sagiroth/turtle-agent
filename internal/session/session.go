// Copyright (c) 2026 agent-wow
// SPDX-License-Identifier: MIT
// The single-owner loop shape (one select over packets, requests and timers; ping
// cadence; logout wait) is ported from github.com/agent-wow/agent-wow pkg/world/engine.go
// at 5ef4916 (see NOTICE). The login sequence, readiness rule and handlers are new.

// Package session owns one logged-in character. Run is the only goroutine that writes
// packets and changes state; other goroutines call methods that queue work for it.
package session

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/pjerra/turtle-agent/internal/events"
	"github.com/pjerra/turtle-agent/internal/opcode"
	"github.com/pjerra/turtle-agent/internal/packet"
	"github.com/pjerra/turtle-agent/internal/worldconn"
)

type Status string

const (
	StatusConnecting   Status = "connecting"
	StatusCharSelect   Status = "char_select"
	StatusLoggingIn    Status = "logging_in"
	StatusInWorld      Status = "in_world"
	StatusLoggingOut   Status = "logging_out"
	StatusLoggedOut    Status = "logged_out"
	StatusDisconnected Status = "disconnected"
)

var (
	ErrClosed       = errors.New("session closed")
	ErrDisconnected = errors.New("disconnected by the server")
	ErrNotInWorld   = errors.New("not in world")
)

// LogoutError is a refused logout (MiscHandler.cpp:350-365).
type LogoutError struct{ Reason uint32 }

func (e *LogoutError) Error() string {
	reason := "unknown reason"
	switch e.Reason {
	case 1:
		reason = "in combat"
	case 2:
		reason = "frozen by a GM"
	case 3:
		reason = "jumping or falling"
	}
	return fmt.Sprintf("logout refused: %s (reason %d)", reason, e.Reason)
}

type Options struct {
	Character     string        // character name, case-insensitive
	Log           *events.Log   // nil: a new 100000-event log
	Logger        io.Writer     // diagnostics such as parse errors with hex dumps; nil: discarded
	PingInterval  time.Duration // 0: 30 s (the server flags pings under 27 s, WorldSocket.cpp:413)
	PongTimeout   time.Duration // 0: 90 s
	LogoutTimeout time.Duration // 0: 30 s
}

type StatusInfo struct {
	State       Status         `json:"state"`
	Character   string         `json:"character,omitempty"`
	GUID        packet.GUID    `json:"guid,omitempty"`
	Level       uint8          `json:"level,omitempty"`
	Map         uint32         `json:"map"`
	X           float32        `json:"x"`
	Y           float32        `json:"y"`
	Z           float32        `json:"z"`
	O           float32        `json:"o"`
	LatencyMS   uint32         `json:"latency_ms"`
	Unknown     map[string]int `json:"unknown_opcodes"`
	ParseErrors int            `json:"parse_errors"`
	LastSeq     uint64         `json:"last_seq"`
	Error       string         `json:"error,omitempty"`
}

type handler func(body []byte) error

// fatalError, returned by a handler, ends the session (login failures).
type fatalError struct{ err error }

func (f *fatalError) Error() string { return f.err.Error() }

type request struct {
	fn    func() (any, error)
	reply chan result
}

type result struct {
	v   any
	err error
}

type logoutOp struct {
	done chan struct{}
	err  error
}

func (op *logoutOp) finish(err error) { op.err = err; close(op.done) }

type Session struct {
	conn     *worldconn.Conn
	opts     Options
	log      *events.Log
	logger   io.Writer
	handlers map[uint16]handler
	reqs     chan request
	done     chan struct{}
	ready    chan struct{}
	start    time.Time

	mu        sync.Mutex // guards these fields for readers outside the loop
	state     Status
	self      Character
	mapID     uint32
	pos       [4]float32
	latency   uint32
	unknown   map[uint16]int
	parseErrs int
	lastErr   string

	// owner-loop only
	finished       bool
	nextPing       time.Time
	pingSent       time.Time
	pingSeq        uint32
	pongWait       bool
	logout         *logoutOp
	logoutDeadline time.Time
}

func New(conn *worldconn.Conn, opts Options) *Session {
	if opts.Log == nil {
		opts.Log = events.New(100000)
	}
	if opts.Logger == nil {
		opts.Logger = io.Discard
	}
	if opts.PingInterval == 0 {
		opts.PingInterval = 30 * time.Second
	}
	if opts.PongTimeout == 0 {
		opts.PongTimeout = 90 * time.Second
	}
	if opts.LogoutTimeout == 0 {
		opts.LogoutTimeout = 30 * time.Second
	}
	s := &Session{
		conn: conn, opts: opts, log: opts.Log, logger: opts.Logger,
		handlers: map[uint16]handler{}, reqs: make(chan request), done: make(chan struct{}),
		ready: make(chan struct{}), start: time.Now(), state: StatusConnecting, unknown: map[uint16]int{},
	}
	s.register()
	return s
}

// register installs every packet handler; each feature adds its own register call here.
func (s *Session) register() {
	s.registerCore()
}

func (s *Session) registerCore() {
	s.handlers[opcode.SMSGCharEnum] = s.onCharEnum
	s.handlers[opcode.SMSGCharacterLoginFailed] = s.onLoginFailed
	s.handlers[opcode.SMSGLoginVerifyWorld] = s.onVerifyWorld
	s.handlers[opcode.SMSGPong] = s.onPong
	s.handlers[opcode.SMSGLogoutResponse] = s.onLogoutResponse
	s.handlers[opcode.SMSGLogoutComplete] = s.onLogoutComplete
	// Warden is ignored: the timeout kick is commented out (Warden.cpp:498).
	s.handlers[opcode.SMSGWardenData] = func([]byte) error { return nil }
	s.handlers[opcode.SMSGAddonInfo] = func([]byte) error { return nil }
}

func (s *Session) Ready() <-chan struct{} { return s.ready }
func (s *Session) Done() <-chan struct{}  { return s.done }
func (s *Session) Log() *events.Log       { return s.log }

func (s *Session) State() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

func (s *Session) setState(st Status) {
	s.mu.Lock()
	s.state = st
	s.mu.Unlock()
}

func (s *Session) Status() StatusInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	unknown := make(map[string]int, len(s.unknown))
	for op, n := range s.unknown {
		unknown[opcode.Name(op)] = n
	}
	return StatusInfo{
		State: s.state, Character: s.self.Name, GUID: s.self.GUID, Level: s.self.Level, Map: s.mapID,
		X: s.pos[0], Y: s.pos[1], Z: s.pos[2], O: s.pos[3], LatencyMS: s.latency,
		Unknown: unknown, ParseErrors: s.parseErrs, LastSeq: s.log.Last(), Error: s.lastErr,
	}
}

// Run drives the character from the character list to logout. It returns nil after a
// clean logout and an error wrapping ErrDisconnected when the server closes the socket.
func (s *Session) Run(ctx context.Context) error {
	defer close(s.done)
	defer s.conn.Close()
	packets := make(chan worldconn.Packet, 256)
	readErr := make(chan error, 1)
	go func() {
		for {
			p, err := s.conn.ReadPacket()
			if err != nil {
				readErr <- err
				return
			}
			select {
			case packets <- p:
			case <-s.done:
				return
			}
		}
	}()
	if err := s.conn.WritePacket(opcode.CMSGCharEnum, nil); err != nil { // CharacterHandler.cpp:203
		return s.connLost(err)
	}
	s.setState(StatusCharSelect)
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return s.end(ctx.Err())
		case err := <-readErr:
			return s.connLost(err)
		case p := <-packets:
			if err := s.dispatch(p); err != nil {
				return s.end(err)
			}
			if s.finished {
				return nil
			}
		case r := <-s.reqs:
			v, err := r.fn()
			r.reply <- result{v, err}
		case now := <-tick.C:
			if err := s.onTick(now); err != nil {
				return s.connLost(err)
			}
		}
	}
}

// do runs fn on the owner goroutine and returns its result.
func (s *Session) do(ctx context.Context, fn func() (any, error)) (any, error) {
	r := request{fn: fn, reply: make(chan result, 1)}
	select {
	case s.reqs <- r:
	case <-s.done:
		return nil, ErrClosed
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	select {
	case res := <-r.reply:
		return res.v, res.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// send writes one packet from the owner goroutine once the character is in world.
func (s *Session) send(ctx context.Context, op uint16, body []byte) error {
	_, err := s.do(ctx, func() (any, error) {
		if st := s.State(); st != StatusInWorld {
			return nil, fmt.Errorf("%w (state %s)", ErrNotInWorld, st)
		}
		return nil, s.conn.WritePacket(op, body)
	})
	return err
}

// emit appends an event (Task 13 makes it wait for names first).
func (s *Session) emit(e events.Event) { s.log.Append(e) }

// flushPending appends events still waiting for names (filled in by Task 13).
func (s *Session) flushPending() {}

func (s *Session) dispatch(p worldconn.Packet) error {
	h, ok := s.handlers[p.Op]
	if !ok {
		s.mu.Lock()
		s.unknown[p.Op]++
		s.mu.Unlock()
		return nil
	}
	err := h(p.Body)
	if err == nil {
		return nil
	}
	var fatal *fatalError
	if errors.As(err, &fatal) {
		return fatal.err
	}
	s.mu.Lock()
	s.parseErrs++
	s.mu.Unlock()
	dump := p.Body
	if len(dump) > 256 {
		dump = dump[:256]
	}
	fmt.Fprintf(s.logger, "parse error in %s (0x%03X, %d bytes): %v\n%s", opcode.Name(p.Op), p.Op, len(p.Body), err, hex.Dump(dump))
	s.emit(events.Event{Kind: events.KindParseError, Text: err.Error(),
		Data: map[string]any{"opcode": opcode.Name(p.Op), "length": len(p.Body)}})
	return nil
}

func (s *Session) onTick(now time.Time) error {
	if st := s.State(); st == StatusInWorld || st == StatusLoggingOut {
		if s.pongWait && now.Sub(s.pingSent) > s.opts.PongTimeout {
			return errors.New("no SMSG_PONG within the pong timeout")
		}
		if !s.nextPing.IsZero() && !now.Before(s.nextPing) {
			s.pingSeq++
			if !s.pongWait { // the timeout runs from the oldest unanswered ping
				s.pingSent, s.pongWait = now, true
			}
			s.nextPing = now.Add(s.opts.PingInterval)
			s.mu.Lock()
			latency := s.latency
			s.mu.Unlock()
			if err := s.conn.WritePacket(opcode.CMSGPing, packet.NewWriter().U32(s.pingSeq).U32(latency).Bytes()); err != nil {
				return err
			}
		}
	}
	if s.logout != nil && now.After(s.logoutDeadline) {
		s.logout.finish(errors.New("logout timed out without SMSG_LOGOUT_COMPLETE"))
		s.logout = nil
		s.setState(StatusInWorld)
	}
	return nil
}

// end records why the session stopped, emits it and wakes a waiting Logout.
func (s *Session) end(err error) error {
	s.mu.Lock()
	s.state = StatusDisconnected
	s.lastErr = err.Error()
	s.mu.Unlock()
	s.flushPending()
	s.emit(events.Event{Kind: events.KindSession, Text: err.Error(), Data: map[string]any{"state": string(StatusDisconnected)}})
	if s.logout != nil {
		s.logout.finish(err)
		s.logout = nil
	}
	return err
}

func (s *Session) connLost(err error) error {
	if s.State() == StatusLoggedOut {
		return nil
	}
	return s.end(fmt.Errorf("%w: %v", ErrDisconnected, err))
}

func (s *Session) onCharEnum(body []byte) error {
	chars, err := ParseCharEnum(body)
	if err != nil {
		return &fatalError{err}
	}
	var names []string
	for _, c := range chars {
		if strings.EqualFold(c.Name, s.opts.Character) {
			s.mu.Lock()
			s.self, s.state = c, StatusLoggingIn
			s.mu.Unlock()
			if err := s.conn.WritePacket(opcode.CMSGPlayerLogin, packet.NewWriter().GUID(c.GUID).Bytes()); err != nil {
				return &fatalError{err}
			}
			return nil
		}
		names = append(names, c.Name)
	}
	return &fatalError{fmt.Errorf("character %q is not on this account (characters: %s)", s.opts.Character, strings.Join(names, ", "))}
}

func (s *Session) onLoginFailed(body []byte) error {
	code := -1
	if len(body) > 0 {
		code = int(body[0])
	}
	return &fatalError{fmt.Errorf("SMSG_CHARACTER_LOGIN_FAILED (code %d): character locked or already loading", code)}
}

func (s *Session) onVerifyWorld(body []byte) error {
	r := packet.NewReader(body)
	m, x, y, z, o := r.U32(), r.F32(), r.F32(), r.F32(), r.F32()
	if err := r.Err(); err != nil {
		return &fatalError{fmt.Errorf("SMSG_LOGIN_VERIFY_WORLD: %w", err)}
	}
	s.mu.Lock()
	s.mapID, s.pos, s.state = m, [4]float32{x, y, z, o}, StatusInWorld
	self := s.self
	s.mu.Unlock()
	s.nextPing = time.Now().Add(s.opts.PingInterval)
	if err := s.conn.WritePacket(opcode.CMSGSetActiveMover, packet.NewWriter().GUID(self.GUID).Bytes()); err != nil {
		return &fatalError{err}
	}
	s.emit(events.Event{Kind: events.KindSession, Source: self.Name, SourceGUID: self.GUID,
		Data: map[string]any{"state": string(StatusInWorld), "map": m}})
	select {
	case <-s.ready:
	default:
		close(s.ready)
	}
	return nil
}

func (s *Session) onPong(body []byte) error {
	r := packet.NewReader(body)
	seq := r.U32()
	if err := r.Err(); err != nil {
		return err
	}
	if s.pongWait && seq <= s.pingSeq {
		s.pongWait = false
		s.mu.Lock()
		s.latency = uint32(time.Since(s.pingSent).Milliseconds())
		s.mu.Unlock()
	}
	return nil
}

// Logout asks for a logout and waits for SMSG_LOGOUT_COMPLETE.
func (s *Session) Logout(ctx context.Context) error {
	v, err := s.do(ctx, func() (any, error) {
		if s.logout != nil {
			return s.logout, nil
		}
		if st := s.State(); st != StatusInWorld {
			return nil, fmt.Errorf("%w (state %s)", ErrNotInWorld, st)
		}
		if err := s.conn.WritePacket(opcode.CMSGLogoutRequest, nil); err != nil {
			return nil, err
		}
		s.logout = &logoutOp{done: make(chan struct{})}
		s.logoutDeadline = time.Now().Add(s.opts.LogoutTimeout)
		s.setState(StatusLoggingOut)
		return s.logout, nil
	})
	if err != nil {
		return err
	}
	op := v.(*logoutOp)
	select {
	case <-op.done:
		return op.err
	case <-s.done:
		select {
		case <-op.done:
			return op.err
		default:
			return ErrClosed
		}
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Session) onLogoutResponse(body []byte) error {
	r := packet.NewReader(body)
	reason, instant := r.U32(), r.U8()
	if err := r.Err(); err != nil {
		return err
	}
	if reason != 0 {
		if s.logout != nil {
			s.logout.finish(&LogoutError{Reason: reason})
			s.logout = nil
		}
		s.setState(StatusInWorld)
		s.emit(events.Event{Kind: events.KindSession, Data: map[string]any{"state": "logout_refused", "reason": reason}})
		return nil
	}
	s.emit(events.Event{Kind: events.KindSession, Data: map[string]any{"state": "logout_pending", "instant": instant == 1}})
	return nil
}

func (s *Session) onLogoutComplete([]byte) error {
	s.setState(StatusLoggedOut)
	s.flushPending()
	s.emit(events.Event{Kind: events.KindSession, Data: map[string]any{"state": string(StatusLoggedOut)}})
	if s.logout != nil {
		s.logout.finish(nil)
		s.logout = nil
	}
	s.finished = true
	return nil
}
