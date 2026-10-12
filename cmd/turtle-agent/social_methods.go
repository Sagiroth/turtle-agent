package main

import (
	"context"
	"fmt"
	"time"

	"github.com/pjerra/turtle-agent/internal/events"
	"github.com/pjerra/turtle-agent/internal/rpc"
	"github.com/pjerra/turtle-agent/internal/session"
	"github.com/pjerra/turtle-agent/internal/tbm"
)

type addonParams struct {
	Prefix  string `json:"prefix"`
	Text    string `json:"text"`
	Channel string `json:"channel"`
	To      string `json:"to,omitempty"`
}

type tbmParams struct {
	Command string `json:"command"`
	Via     string `json:"via,omitempty"`
}

type handshakeParams struct {
	Via    string `json:"via,omitempty"`
	WaitMS int    `json:"wait_ms,omitempty"`
}

type sinceResult struct {
	Since uint64 `json:"since"`
}

type handshakeResult struct {
	Since uint64 `json:"since"` // the log cursor before the first command
	Next  uint64 `json:"next"`  // the cursor after the last event read
	tbm.Status
}

// registerSocialMethods wires addon messages, the TBM helpers, channels and gossip.
func registerSocialMethods(srv *rpc.Server, s *session.Session) {
	log := s.Log()
	srv.Register("addon_send", rpc.Typed(func(ctx context.Context, p addonParams) (empty, error) {
		return empty{}, rpcErr(s.AddonSend(ctx, p.Channel, p.Prefix, p.Text, p.To))
	}))
	srv.Register("tbm", rpc.Typed(func(ctx context.Context, p tbmParams) (sinceResult, error) {
		since := log.Last()
		return sinceResult{since}, rpcErr(sendTBM(ctx, s, p.Via, p.Command))
	}))
	srv.Register("tbm_handshake", rpc.Typed(func(ctx context.Context, p handshakeParams) (handshakeResult, error) {
		res := handshakeResult{Since: log.Last()}
		res.Lines = []string{}
		for _, c := range tbm.Handshake {
			if err := sendTBM(ctx, s, p.Via, c); err != nil {
				return res, rpcErr(err)
			}
		}
		res.Next = collectTBM(ctx, log, res.Since, waitBound(p.WaitMS, 5000), &res.Status)
		return res, nil
	}))
}

// sendTBM sends one TBM command on the addon's chat transport (".bot <command>" as say)
// or on its addon transport (prefix TBM on PARTY).
func sendTBM(ctx context.Context, s *session.Session, via, command string) error {
	switch via {
	case "", "chat":
		return s.GM(ctx, tbm.Chat(command))
	case "addon":
		return s.AddonSend(ctx, "party", tbm.Prefix, command, "")
	}
	return fmt.Errorf("via %q: use chat or addon", via)
}

// waitBound is waitMS, or def when unset, capped at 25 s like events.
func waitBound(waitMS, def int) time.Duration {
	if waitMS <= 0 {
		waitMS = def
	}
	if waitMS > 25000 {
		waitMS = 25000
	}
	return time.Duration(waitMS) * time.Millisecond
}

// collectTBM folds the TBM: lines logged after since into st until the roster response is
// complete or wait runs out, and returns the cursor after the last event read.
func collectTBM(ctx context.Context, log *events.Log, since uint64, wait time.Duration, st *tbm.Status) uint64 {
	wctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	cursor := since
	for {
		p := log.Read(events.Query{Since: cursor, Kinds: []string{events.KindSystem, events.KindAddon}, Limit: events.MaxLimit})
		for _, e := range p.Events {
			if line, ok := tbm.Line(e); ok && st.Add(line) {
				return e.Seq
			}
		}
		cursor = p.Next
		if p.More {
			continue
		}
		if log.Wait(wctx, cursor) != nil {
			return cursor
		}
	}
}
