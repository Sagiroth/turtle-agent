package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type addParams struct {
	A int `json:"a"`
	B int `json:"b"`
}

func newTestServer(t *testing.T) *httptest.Server {
	s := NewServer()
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
	for k, v := range header {
		req.Header.Set(k, v)
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

func TestIsLoopback(t *testing.T) {
	for host, want := range map[string]bool{
		"127.0.0.1": true, "127.1.2.3": true, "::1": true, "localhost": true,
		"0.0.0.0": false, "::": false, "192.168.1.10": false, "example.com": false, "": false,
	} {
		if got := IsLoopback(host); got != want {
			t.Errorf("IsLoopback(%q) = %v, want %v", host, got, want)
		}
	}
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
