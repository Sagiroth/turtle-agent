// Package addonhost runs the real TortoiseBotsManager (TBM) addon headless: a lua5.1 sidecar
// loads the addon's Lua on the 1.12 widget mock (lua/mock.lua, shared with tools/tbmcontract)
// and is driven over stdin/stdout, one JSON object per line (lua/host.lua documents the ops).
// What the addon asks the client to do (SendAddonMessage, SendChatMessage, TargetUnit) comes
// back as Out values for the caller to perform; the host itself touches no connection.
//
// A sidecar rather than a Go Lua VM: the addon runs on the same C Lua 5.1 the contract test
// (tools/tbmcontract) uses, the module keeps no dependencies, and an addon that errors or
// loops cannot take the agent down: a call that does not answer in time kills the process.
package addonhost

import (
	"bufio"
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

//go:embed lua/*.lua
var luaFiles embed.FS

// DefaultLua is the interpreter the host runs when none is given.
const DefaultLua = "lua5.1"

// CallTimeout bounds one call: an addon that runs longer is taken to be stuck.
var CallTimeout = 10 * time.Second

// ErrStopped is returned by calls after the sidecar has exited or was stopped.
var ErrStopped = errors.New("addon host stopped")

// Out is one thing the addon asked the client to do during a call.
type Out struct {
	Out     string `json:"out"`               // "addon", "chat" or "target"
	Prefix  string `json:"prefix,omitempty"`  // addon: the message prefix
	Channel string `json:"channel,omitempty"` // addon: PARTY, RAID, GUILD, ...
	Type    string `json:"type,omitempty"`    // chat: SAY, PARTY, WHISPER, ...
	Text    string `json:"text,omitempty"`
	To      string `json:"to,omitempty"`   // whisper target or addon whisper target
	GUID    string `json:"guid,omitempty"` // target: the unit's guid, when the addon named a unit
	Name    string `json:"name,omitempty"` // target: the unit's name
	Error   string `json:"error,omitempty"`
}

// Sink performs an Out; its error is kept on the Out and does not fail the call (the client
// would not tell the addon either).
type Sink func(Out) error

// Host is one running sidecar. Calls are serialised.
type Host struct {
	mu     sync.Mutex
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	lines  chan []byte
	exited chan struct{}
	// quit tells the reader to drop lines nobody will read any more.
	quit     chan struct{}
	quitOnce sync.Once
	waitMu   sync.Mutex
	err      error // why the process ended
	stderr   *tail
	dir      string // the extracted Lua files
	sink     Sink
	next     int
}

// Start extracts the host scripts and runs lua with them on the addon in addonDir.
func Start(lua, addonDir string, sink Sink) (*Host, error) {
	if lua == "" {
		lua = DefaultLua
	}
	path, err := exec.LookPath(lua)
	if err != nil {
		return nil, fmt.Errorf("%s not found (apt install lua5.1): %w", lua, err)
	}
	if _, err := os.Stat(filepath.Join(addonDir, "TortoiseBotsManager.toc")); err != nil {
		return nil, fmt.Errorf("no TortoiseBotsManager.toc in %s", addonDir)
	}
	dir, err := os.MkdirTemp("", "turtle-agent-addonhost-")
	if err != nil {
		return nil, err
	}
	for _, name := range []string{"host.lua", "json.lua", "mock.lua"} {
		b, err := luaFiles.ReadFile("lua/" + name)
		if err == nil {
			err = os.WriteFile(filepath.Join(dir, name), b, 0o600)
		}
		if err != nil {
			os.RemoveAll(dir)
			return nil, err
		}
	}
	h := &Host{lines: make(chan []byte, 64), exited: make(chan struct{}), quit: make(chan struct{}), stderr: &tail{max: 4096}, dir: dir, sink: sink}
	h.cmd = exec.Command(path, filepath.Join(dir, "host.lua"), addonDir)
	h.cmd.Stderr = h.stderr
	if h.stdin, err = h.cmd.StdinPipe(); err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	stdout, err := h.cmd.StdoutPipe()
	if err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	if err := h.cmd.Start(); err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	go func() {
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 64*1024), 16*1024*1024)
		for sc.Scan() {
			select {
			case h.lines <- append([]byte(nil), sc.Bytes()...):
			case <-h.quit:
			}
		}
		err := h.cmd.Wait()
		h.waitMu.Lock()
		if err == nil {
			err = errors.New("exited")
		}
		h.err = err
		h.waitMu.Unlock()
		close(h.exited)
	}()
	return h, nil
}

// Call runs op with params (a JSON object, or nil) and returns its result and the Outs the
// addon produced on the way, each already passed to the sink.
func (h *Host) Call(op string, params any) (json.RawMessage, []Out, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	select {
	case <-h.exited:
		return nil, nil, h.stopped()
	default:
	}
	h.next++
	id := h.next
	req := map[string]any{}
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return nil, nil, err
		}
		if err := json.Unmarshal(b, &req); err != nil {
			return nil, nil, fmt.Errorf("params of %s must be an object: %w", op, err)
		}
	}
	req["id"], req["op"] = id, op
	b, _ := json.Marshal(req)
	if _, err := h.stdin.Write(append(b, '\n')); err != nil {
		return nil, nil, h.stopped()
	}
	var outs []Out
	timer := time.NewTimer(CallTimeout)
	defer timer.Stop()
	for {
		select {
		case line := <-h.lines:
			var m struct {
				ID     int             `json:"id"`
				Out    string          `json:"out"`
				Result json.RawMessage `json:"result"`
				Error  string          `json:"error"`
			}
			if err := json.Unmarshal(line, &m); err != nil {
				return nil, outs, fmt.Errorf("addon host wrote %q: %w", truncate(string(line), 200), err)
			}
			if m.Out != "" {
				var o Out
				json.Unmarshal(line, &o)
				if h.sink != nil {
					if err := h.sink(o); err != nil {
						o.Error = err.Error()
					}
				}
				outs = append(outs, o)
				continue
			}
			if m.ID != id {
				continue // a reply to an earlier call that timed out cannot happen: that kills the process
			}
			if m.Error != "" {
				return nil, outs, errors.New(m.Error)
			}
			return m.Result, outs, nil
		case <-h.exited:
			return nil, outs, h.stopped()
		case <-timer.C:
			h.kill()
			return nil, outs, fmt.Errorf("%w: %s took over %s and was killed (an endless loop in the addon?)", ErrStopped, op, CallTimeout)
		}
	}
}

func (h *Host) stopped() error {
	h.waitMu.Lock()
	err := h.err
	h.waitMu.Unlock()
	detail := ""
	if err != nil {
		detail += ": " + err.Error()
	}
	if s := strings.TrimSpace(h.stderr.String()); s != "" {
		detail += ": " + s
	}
	return fmt.Errorf("%w%s", ErrStopped, detail)
}

func (h *Host) kill() {
	h.quitOnce.Do(func() { close(h.quit) })
	if h.cmd.Process != nil {
		h.cmd.Process.Kill()
	}
}

// Close ends the sidecar (end of stdin, then a kill if it does not exit) and removes its files.
func (h *Host) Close() error {
	h.quitOnce.Do(func() { close(h.quit) })
	h.stdin.Close()
	select {
	case <-h.exited:
	case <-time.After(2 * time.Second):
		h.kill()
		<-h.exited
	}
	return os.RemoveAll(h.dir)
}

// Stderr is the tail of what the sidecar wrote to stderr.
func (h *Host) Stderr() string { return h.stderr.String() }

// tail keeps the last max bytes written to it.
type tail struct {
	mu  sync.Mutex
	max int
	buf []byte
}

func (t *tail) Write(p []byte) (int, error) {
	t.mu.Lock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.max {
		t.buf = t.buf[len(t.buf)-t.max:]
	}
	t.mu.Unlock()
	return len(p), nil
}

func (t *tail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(bytes.ToValidUTF8(t.buf, []byte("?")))
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}
