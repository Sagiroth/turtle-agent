package netutil

import "testing"

func TestIsLoopback(t *testing.T) {
	for host, want := range map[string]bool{
		"127.0.0.1": true, "127.1.2.3": true, "::1": true, "localhost": true,
		"0.0.0.0": false, "::": false, "192.168.1.10": false, "example.com": false, "": false,
	} {
		if got := IsLoopback(host); got != want {
			t.Errorf("IsLoopback(%q) = %v, want %v", host, got, want)
		}
	}
}

func TestCheckLoopbackAddr(t *testing.T) {
	for addr, want := range map[string]string{
		"127.0.0.1:8765": "127.0.0.1", "localhost:0": "127.0.0.1", "[::1]:65535": "::1",
	} {
		if h, _, err := CheckLoopbackAddr(addr); err != nil || h != want {
			t.Errorf("%s: %q %v, want %q", addr, h, err, want)
		}
	}
	if _, p, _ := CheckLoopbackAddr("127.0.0.1:8765"); p != 8765 {
		t.Errorf("port %d", p)
	}
	for _, addr := range []string{"0.0.0.0:8765", "[::]:0", "192.168.1.10:1", "example.com:1", "8765", "127.0.0.1:65536", "127.0.0.1:-1", "127.0.0.1:http", ""} {
		if _, _, err := CheckLoopbackAddr(addr); err == nil {
			t.Errorf("%q accepted", addr)
		}
	}
}
