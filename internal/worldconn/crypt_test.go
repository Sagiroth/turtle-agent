package worldconn

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

// Hand-computed from AuthCrypt.cpp:54-66 (encrypt) and :39-52 (decrypt) with key 01 02 03.
func TestHeaderCryptKnownVector(t *testing.T) {
	h := []byte{0x00, 0x04, 0xEE, 0x01}
	NewHeaderCrypt([]byte{1, 2, 3}).Encrypt(h)
	if !bytes.Equal(h, []byte{0x01, 0x07, 0xF4, 0xF4}) {
		t.Fatalf("encrypt % X", h)
	}
	NewHeaderCrypt([]byte{1, 2, 3}).Decrypt(h)
	if !bytes.Equal(h, []byte{0x00, 0x04, 0xEE, 0x01}) {
		t.Fatalf("decrypt % X", h)
	}
}

func TestHeaderCryptStreamsWith40And39ByteKeys(t *testing.T) {
	for _, n := range []int{40, 39} {
		key := bytes.Repeat([]byte{0xA5, 0x3C}, 20)[:n]
		send, recv := NewHeaderCrypt(key), NewHeaderCrypt(key)
		for i := 0; i < 1000; i++ {
			h := []byte{byte(i), byte(i >> 8), 0x95, 0x00, 0x00, 0x00}
			want := bytes.Clone(h)
			send.Encrypt(h)
			recv.Decrypt(h)
			if !bytes.Equal(h, want) {
				t.Fatalf("key %d bytes, header %d: got % X want % X", n, i, h, want)
			}
		}
	}
}

// The cipher holds K; formatting it must not print the key (ruling T4 concern 4 spirit).
func TestHeaderCryptFormattingRedactsKey(t *testing.T) {
	c := NewHeaderCrypt([]byte{0xAB, 0xCD, 0xEF})
	for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%x", "%d"} {
		for _, v := range []any{c, *c} {
			out := fmt.Sprintf(verb, v)
			if !strings.Contains(out, "redacted") || strings.Contains(strings.ToLower(out), "abcdef") ||
				strings.Contains(out, "171") {
				t.Fatalf("%s of %T printed %q", verb, v, out)
			}
		}
	}
}
