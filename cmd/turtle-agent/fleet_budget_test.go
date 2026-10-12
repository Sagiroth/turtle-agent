package main

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestLoginBudgetStaysUnderTheLimit(t *testing.T) {
	const window = 300 * time.Second
	span := window + window/30
	t0 := time.Unix(1000, 0)
	b := newLoginBudget(realmdThrottleLogins-1, window)
	for i := 0; i < realmdThrottleLogins-1; i++ {
		if got := b.reserve(t0.Add(time.Duration(i) * time.Second)); !got.Equal(t0.Add(time.Duration(i) * time.Second)) {
			t.Fatalf("login %d booked at +%s", i+1, got.Sub(t0))
		}
	}
	// The 15th must wait until the first has left the window.
	if got := b.reserve(t0.Add(20 * time.Second)); !got.Equal(t0.Add(span)) {
		t.Fatalf("15th login at +%s, want +%s", got.Sub(t0), span)
	}
	// The 16th goes after the second.
	if got := b.reserve(t0.Add(21 * time.Second)); !got.Equal(t0.Add(time.Second + span)) {
		t.Fatalf("16th login at +%s", got.Sub(t0))
	}
	// Whatever was booked, no window holds more than 14 logins.
	slots := append([]time.Time(nil), b.slots...)
	for i := 0; i+realmdThrottleLogins-1 < len(slots); i++ {
		if slots[i+realmdThrottleLogins-1].Sub(slots[i]) < window {
			t.Fatalf("15 logins within %s", slots[i+realmdThrottleLogins-1].Sub(slots[i]))
		}
	}
}

// A login booked far ahead (a long backoff) does not hold up one that can go now.
func TestLoginBudgetBooksOutOfOrder(t *testing.T) {
	t0 := time.Unix(1000, 0)
	b := newLoginBudget(2, 100*time.Second)
	if got := b.reserve(t0.Add(50 * time.Second)); !got.Equal(t0.Add(50 * time.Second)) {
		t.Fatal(got.Sub(t0))
	}
	if got := b.reserve(t0); !got.Equal(t0) {
		t.Fatalf("early login booked at +%s", got.Sub(t0))
	}
	// Two are booked 50 s apart; a third at +10 s would make three within a window.
	if got := b.reserve(t0.Add(10 * time.Second)); got.Before(t0.Add(100 * time.Second)) {
		t.Fatalf("third login at +%s, inside the window of the first", got.Sub(t0))
	}
}

func TestLoginBudgetBusyClosesTheWindow(t *testing.T) {
	t0 := time.Unix(1000, 0)
	b := newLoginBudget(14, 300*time.Second)
	b.busy(t0)
	if got := b.reserve(t0.Add(time.Second)); got.Sub(t0) < 300*time.Second {
		t.Fatalf("login booked at +%s after a busy answer", got.Sub(t0))
	}
}

const testBudgetToken = "test-budget-token-0123456789"

func budgetPost(t *testing.T, addr, path, body string, hdr map[string]string, host string) int {
	t.Helper()
	req, _ := http.NewRequest("POST", "http://"+addr+path, strings.NewReader(body))
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	if host != "" {
		req.Host = host
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func TestLoginBudgetServer(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b := newLoginBudget(1, 600*time.Millisecond)
	addr, stop, err := serveLoginBudget(ctx, b, testBudgetToken)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	if !strings.HasPrefix(addr, "127.0.0.1:") {
		t.Fatalf("budget listens on %s", addr)
	}
	c := newBudgetClient(addr, testBudgetToken)
	d, err := c.reserve(ctx, 100*time.Millisecond)
	if err != nil || d < 90*time.Millisecond || d > 200*time.Millisecond {
		t.Fatalf("first login: %v, %v", d, err)
	}
	// One login per window: the next waits for the first to leave it.
	d, err = c.reserve(ctx, 0)
	if err != nil || d < 600*time.Millisecond {
		t.Fatalf("second login: %v, %v", d, err)
	}
	c.busy(ctx)
	if b.busyUntil.IsZero() {
		t.Fatal("busy not recorded")
	}
	// Anything but a well-formed POST is refused.
	auth := map[string]string{"Authorization": "Bearer " + testBudgetToken}
	for _, bad := range []struct{ method, path, body string }{
		{"GET", "/login", ""}, {"POST", "/login", "{"}, {"POST", "/login", `{"after_ms":-5}`}, {"GET", "/busy", ""},
	} {
		req, _ := http.NewRequest(bad.method, "http://"+addr+bad.path, strings.NewReader(bad.body))
		req.Header.Set("Authorization", auth["Authorization"])
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s %s %q: %s", bad.method, bad.path, bad.body, resp.Status)
		}
	}
	stop()
	if _, err := c.reserve(ctx, 0); err == nil {
		t.Error("a stopped budget still answers")
	}
}

// Neither another local process nor a web page can freeze or flood the fleet's logins:
// every call needs the token, and a browser's Origin or a rebinding Host is refused.
func TestLoginBudgetServerRefusesStrangers(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b := newLoginBudget(14, 300*time.Second)
	addr, stop, err := serveLoginBudget(ctx, b, testBudgetToken)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	good := map[string]string{"Authorization": "Bearer " + testBudgetToken}
	for name, c := range map[string]struct {
		hdr  map[string]string
		host string
		want int
	}{
		"no token":    {nil, "", http.StatusUnauthorized},
		"wrong token": {map[string]string{"Authorization": "Bearer nope-nope-nope-nope"}, "", http.StatusUnauthorized},
		"basic":       {map[string]string{"Authorization": "Basic " + testBudgetToken}, "", http.StatusUnauthorized},
		"origin":      {map[string]string{"Authorization": good["Authorization"], "Origin": "http://evil.example"}, "", http.StatusForbidden},
		"host":        {good, "evil.example:80", http.StatusForbidden},
	} {
		for _, path := range []string{"/busy", "/login"} {
			if got := budgetPost(t, addr, path, `{"after_ms":0}`, c.hdr, c.host); got != c.want {
				t.Errorf("%s %s: %d, want %d", name, path, got, c.want)
			}
		}
	}
	if !b.busyUntil.IsZero() || len(b.slots) != 0 {
		t.Fatalf("a refused call changed the budget: busy %v, %d slots", b.busyUntil, len(b.slots))
	}
	if _, _, err := serveLoginBudget(ctx, b, "short"); err == nil {
		t.Error("a short token was accepted")
	}
}

// The earliest time a client may ask for is capped, and so is the number of bookings.
func TestLoginBudgetServerCaps(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b := newLoginBudget(14, 300*time.Second)
	addr, stop, err := serveLoginBudget(ctx, b, testBudgetToken)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	auth := map[string]string{"Authorization": "Bearer " + testBudgetToken}
	for _, after := range []string{"3600001", "9223372036854775807", "100000000000000000"} {
		if got := budgetPost(t, addr, "/login", `{"after_ms":`+after+`}`, auth, ""); got != http.StatusBadRequest {
			t.Errorf("after_ms %s: %d", after, got)
		}
	}
	if got := budgetPost(t, addr, "/login", `{"after_ms":3600000}`, auth, ""); got != http.StatusOK {
		t.Errorf("one hour: %d", got)
	}
	// The client clamps what it asks for instead of failing.
	c := newBudgetClient(addr, testBudgetToken)
	if d, err := c.reserve(ctx, 1000*time.Hour); err != nil || d > time.Hour+time.Minute {
		t.Errorf("clamped: %v %v", d, err)
	}
	// Bookings stop at the cap; the supervisor's own are not limited.
	for len(b.slots) < maxBudgetSlots {
		b.reserve(time.Now())
	}
	if got := budgetPost(t, addr, "/login", `{"after_ms":0}`, auth, ""); got != http.StatusTooManyRequests {
		t.Errorf("past the cap: %d", got)
	}
	b.reserve(time.Now())
	if len(b.slots) != maxBudgetSlots+1 {
		t.Errorf("supervisor booking refused: %d slots", len(b.slots))
	}
}

func TestLoginBudgetMarginCoversTheStartDelay(t *testing.T) {
	b := newLoginBudget(realmdThrottleLogins-1, 300*time.Second)
	if b.margin < 10*time.Second {
		t.Fatalf("margin %s", b.margin)
	}
}
