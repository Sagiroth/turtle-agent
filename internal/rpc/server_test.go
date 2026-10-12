package rpc

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type addParams struct {
	A int `json:"a"`
	B int `json:"b"`
}

const testToken = "test-token-0123456789abcdef"

func newTestServer(t *testing.T) *httptest.Server {
	s := NewServer()
	s.SetToken(testToken)
	s.Register("add", Typed(func(ctx context.Context, p addParams) (int, error) { return p.A + p.B, nil }))
	s.Register("nothing", Typed(func(ctx context.Context, _ struct{}) (struct{}, error) { return struct{}{}, nil }))
	s.Register("offline", Typed(func(ctx context.Context, _ struct{}) (int, error) {
		return 0, &Error{Code: CodeNotInWorld, Message: "not in world"}
	}))
	s.Register("broken", Typed(func(ctx context.Context, _ struct{}) (int, error) { return 0, errors.New("boom") }))
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return ts
}

func post(t *testing.T, ts *httptest.Server, body string, header map[string]string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/rpc", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+testToken)
	for k, v := range header {
		if v == "" {
			req.Header.Del(k)
		} else {
			req.Header.Set(k, v)
		}
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func errCode(m map[string]any) float64 {
	e, _ := m["error"].(map[string]any)
	c, _ := e["code"].(float64)
	return c
}

func TestTypedRoundTripAndEmptyResult(t *testing.T) {
	ts := newTestServer(t)
	if _, out := post(t, ts, `{"jsonrpc":"2.0","id":1,"method":"add","params":{"a":2,"b":3}}`, nil); out["result"] != float64(5) {
		t.Fatalf("%v", out)
	}
	_, out := post(t, ts, `{"jsonrpc":"2.0","id":"x","method":"nothing"}`, nil)
	if r, ok := out["result"].(map[string]any); !ok || len(r) != 0 || out["id"] != "x" {
		t.Fatalf("%v", out)
	}
}

func TestErrorCodes(t *testing.T) {
	ts := newTestServer(t)
	for body, want := range map[string]float64{
		`{"jsonrpc":"2.0","id":1,"method":"nope"}`:                 CodeMethodNotFound,
		`{"jsonrpc":"2.0","id":1,"method":"add","params":{"c":1}}`: CodeInvalidParams,
		`{"jsonrpc":"2.0","id":1,"method":"add","params":[1,2]}`:   CodeInvalidParams,
		`{"jsonrpc":"2.0","id":1,"method":"offline"}`:              CodeNotInWorld,
		`{"jsonrpc":"2.0","id":1,"method":"broken"}`:               CodeInternal,
		`{"jsonrpc":"1.0","id":1,"method":"add"}`:                  CodeInvalidRequest,
		`{not json`: CodeParse,
	} {
		if _, out := post(t, ts, body, nil); errCode(out) != want {
			t.Errorf("%s: %v, want %v", body, out, want)
		}
	}
}

func TestRejectsBrowserRequestsAndNotifications(t *testing.T) {
	ts := newTestServer(t)
	if code, _ := post(t, ts, `{"jsonrpc":"2.0","id":1,"method":"add"}`, map[string]string{"Origin": "http://evil.example"}); code != http.StatusForbidden {
		t.Fatalf("origin: %d", code)
	}
	if code, _ := post(t, ts, `{"jsonrpc":"2.0","method":"add","params":{"a":1,"b":1}}`, nil); code != http.StatusNoContent {
		t.Fatalf("notification: %d", code)
	}
}

const okBody = `{"jsonrpc":"2.0","id":1,"method":"add","params":{"a":1,"b":2}}`

func TestTokenAcceptedAndRejected(t *testing.T) {
	ts := newTestServer(t)
	if _, out := post(t, ts, okBody, nil); out["result"] != float64(3) {
		t.Fatalf("right token: %v", out)
	}
	if _, out := post(t, ts, okBody, map[string]string{"Authorization": "bearer " + testToken}); out["result"] != float64(3) {
		t.Fatalf("scheme is case-insensitive: %v", out)
	}
	for name, h := range map[string]map[string]string{
		"missing":       {"Authorization": ""},
		"wrong":         {"Authorization": "Bearer " + testToken + "x"},
		"prefix":        {"Authorization": "Bearer " + testToken[:8]},
		"empty":         {"Authorization": "Bearer "},
		"no scheme":     {"Authorization": testToken},
		"basic":         {"Authorization": "Basic " + testToken},
		"other header":  {"Authorization": "", "X-Token": testToken},
		"double scheme": {"Authorization": "Bearer Bearer " + testToken},
	} {
		code, out := post(t, ts, okBody, h)
		if code != http.StatusUnauthorized || out != nil {
			t.Errorf("%s: status %d body %v, want a bare 401", name, code, out)
		}
	}
}

// The 401 comes before the body is read or the method looked up, so an unauthenticated
// caller learns nothing about the API (not even that a method exists).
func TestUnauthorizedRevealsNothing(t *testing.T) {
	ts := newTestServer(t)
	hdr := map[string]string{"Authorization": "Bearer nope"}
	_, a := post(t, ts, okBody, hdr)
	_, b := post(t, ts, `{"jsonrpc":"2.0","id":1,"method":"nope"}`, hdr)
	_, c := post(t, ts, `{not json`, hdr)
	if a != nil || b != nil || c != nil {
		t.Fatalf("bodies %v %v %v", a, b, c)
	}
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/rpc", strings.NewReader(okBody))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusUnauthorized || strings.TrimSpace(string(raw)) != "unauthorized" {
		t.Fatalf("%d %q", resp.StatusCode, raw)
	}
}

// Origin, Fetch-Site and Host stay checked in front of the token: a browser holding the
// token still gets 403.
func TestTokenDoesNotBypassHostAndOrigin(t *testing.T) {
	ts := newTestServer(t)
	for name, h := range map[string]map[string]string{
		"origin": {"Origin": "http://evil.example"},
		"fetch":  {"Sec-Fetch-Site": "cross-site"},
	} {
		if code, _ := post(t, ts, okBody, h); code != http.StatusForbidden {
			t.Errorf("%s: %d", name, code)
		}
	}
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/rpc", strings.NewReader(okBody))
	req.Host = "evil.example"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("rebound host: %d", resp.StatusCode)
	}
}

func TestAuthModes(t *testing.T) {
	newSrv := func(cfg func(*Server)) *httptest.Server {
		s := NewServer()
		s.Register("add", Typed(func(ctx context.Context, p addParams) (int, error) { return p.A + p.B, nil }))
		cfg(s)
		ts := httptest.NewServer(s.Handler())
		t.Cleanup(ts.Close)
		return ts
	}
	// Nothing configured: closed, with or without a header.
	closed := newSrv(func(*Server) {})
	if code, _ := post(t, closed, okBody, nil); code != http.StatusUnauthorized {
		t.Fatalf("unconfigured server: %d", code)
	}
	if code, _ := post(t, closed, okBody, map[string]string{"Authorization": "Bearer "}); code != http.StatusUnauthorized {
		t.Fatalf("unconfigured server, empty bearer: %d", code)
	}
	// An empty token never matches the empty bearer.
	empty := newSrv(func(s *Server) { s.SetToken("") })
	if code, _ := post(t, empty, okBody, map[string]string{"Authorization": "Bearer "}); code == http.StatusOK {
		t.Fatal("empty token accepted")
	}
	// Off lets header-less calls through.
	off := newSrv(func(s *Server) { s.DisableAuth() })
	if _, out := post(t, off, okBody, map[string]string{"Authorization": ""}); out["result"] != float64(3) {
		t.Fatalf("auth off: %v", out)
	}
}

func TestTokenMatchHashesBothSides(t *testing.T) {
	// The comparison is over fixed-size digests, so unequal lengths take the same path as
	// equal ones; this pins that the digest, not the raw token, is what is compared.
	want := sha256.Sum256([]byte("secret-secret-secret"))
	for header, ok := range map[string]bool{
		"Bearer secret-secret-secret":  true,
		"Bearer secret-secret-secre":   false,
		"Bearer secret-secret-secret ": false,
		"Bearer ":                      false,
		"Bearer":                       false,
		"":                             false,
	} {
		if got := tokenMatch(header, want); got != ok {
			t.Errorf("tokenMatch(%q) = %v", header, got)
		}
	}
}

func TestTokenFile(t *testing.T) {
	a, err := NewToken()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := NewToken()
	if len(a) != 43 || a == b || strings.ContainsAny(a, " +/=") {
		t.Fatalf("tokens %q %q", a, b)
	}
	dir := t.TempDir()
	settings := filepath.Join(dir, "settings.json")
	path := TokenFile(settings, 8771)
	if path != filepath.Join(dir, "rpc-8771.token") {
		t.Fatal(path)
	}
	// A stale file with loose permissions is replaced by a private one.
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	remove, err := WriteTokenFile(path, a)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("%v %v", info, err)
	}
	if got, err := ReadTokenFile(path); err != nil || got != a {
		t.Fatalf("read %q %v", got, err)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, "*.tmp")); len(left) != 0 {
		t.Fatalf("temp files left: %v", left)
	}
	remove()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("not removed: %v", err)
	}
	if _, err := ReadTokenFile(path); err == nil {
		t.Fatal("read a missing file")
	}
	if _, err := WriteTokenFile(filepath.Join(dir, "no", "such", "rpc-1.token"), a); err == nil {
		t.Fatal("wrote into a missing folder")
	}
}

func TestTokenFileSafety(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rpc-1.token")

	// Cleanup leaves a file a restarted agent has replaced.
	removeOld, err := WriteTokenFile(path, "old-agent-token-0123456789")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := WriteTokenFile(path, "new-agent-token-0123456789"); err != nil {
		t.Fatal(err)
	}
	removeOld()
	if got, err := ReadTokenFile(path); err != nil || got != "new-agent-token-0123456789" {
		t.Fatalf("the new agent's file was removed or changed: %q %v", got, err)
	}

	// Reads refuse a symlink, a directory and a file others can read.
	link := filepath.Join(dir, "link.token")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadTokenFile(link); err == nil {
		t.Error("followed a symlink")
	}
	if _, err := ReadTokenFile(dir); err == nil {
		t.Error("read a directory")
	}
	loose := filepath.Join(dir, "loose.token")
	if err := os.WriteFile(loose, []byte("tok-tok-tok-tok-tok"), 0o644); err != nil {
		t.Fatal(err)
	}
	os.Chmod(loose, 0o644)
	if _, err := ReadTokenFile(loose); err == nil {
		t.Error("read a group/world-readable file")
	}
	// Cleanup never removes through a symlink or a file that is not ours.
	removeLink, err := WriteTokenFile(filepath.Join(dir, "rpc-2.token"), "tok-tok-tok-tok-tok")
	if err != nil {
		t.Fatal(err)
	}
	os.Remove(filepath.Join(dir, "rpc-2.token"))
	if err := os.Symlink(loose, filepath.Join(dir, "rpc-2.token")); err != nil {
		t.Fatal(err)
	}
	removeLink()
	if _, err := os.Stat(loose); err != nil {
		t.Fatalf("cleanup removed a symlink target: %v", err)
	}

	// A world-writable folder is refused.
	open := filepath.Join(dir, "open")
	if err := os.Mkdir(open, 0o755); err != nil {
		t.Fatal(err)
	}
	os.Chmod(open, 0o777)
	if _, err := WriteTokenFile(filepath.Join(open, "rpc-3.token"), "tok-tok-tok-tok-tok"); err == nil {
		t.Error("wrote into a world-writable folder")
	}
}

func TestListenLoopbackOnly(t *testing.T) {
	if _, err := Listen(context.Background(), "0.0.0.0:0"); err == nil {
		t.Fatal("wildcard bind accepted")
	}
	ln, err := Listen(context.Background(), "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ln.Close()
}

func TestListenRefusals(t *testing.T) {
	for _, addr := range []string{"192.168.1.10:0", "example.com:0", "[::]:0", "127.0.0.1", "127.0.0.1:99999"} {
		if ln, err := Listen(context.Background(), addr); err == nil {
			ln.Close()
			t.Errorf("Listen(%q) accepted", addr)
		}
	}
}

// A long-poll in flight must not hold up shutdown, while a quick call in flight still
// answers with an uncancelled context (shutdownGrace).
func TestServeShutdownEndsLongPolls(t *testing.T) {
	ln, err := Listen(context.Background(), "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := NewServer()
	s.DisableAuth()
	started := make(chan struct{}, 2)
	s.Register("block", Typed(func(ctx context.Context, _ struct{}) (struct{}, error) {
		started <- struct{}{}
		<-ctx.Done()
		return struct{}{}, nil
	}))
	s.Register("quick", Typed(func(ctx context.Context, _ struct{}) (struct{}, error) {
		started <- struct{}{}
		time.Sleep(200 * time.Millisecond)
		return struct{}{}, ctx.Err()
	}))
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- Serve(ctx, ln, s.Handler()) }()
	url := "http://" + ln.Addr().String() + "/rpc"
	go http.Post(url, "application/json", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"block"}`))
	quick := make(chan map[string]any, 1)
	go func() {
		resp, err := http.Post(url, "application/json", strings.NewReader(`{"jsonrpc":"2.0","id":2,"method":"quick"}`))
		var out map[string]any
		if err == nil {
			json.NewDecoder(resp.Body).Decode(&out)
			resp.Body.Close()
		}
		quick <- out
	}()
	<-started
	<-started
	start := time.Now()
	cancel()
	select {
	case err := <-served:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(shutdownGrace + 2*time.Second):
		t.Fatal("Serve waited for the blocked call")
	}
	if d := time.Since(start); d < shutdownGrace/2 {
		t.Fatalf("shutdown after %s, before the grace", d)
	}
	if out := <-quick; out == nil || out["error"] != nil {
		t.Fatalf("quick call during shutdown: %v", out)
	}
}
