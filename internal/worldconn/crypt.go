package worldconn

import (
	"fmt"
	"io"
)

// HeaderCrypt is the vanilla rolling-XOR header cipher (shared/Auth/AuthCrypt.cpp:39-66).
// Encrypt is the sending direction and Decrypt the receiving one, each with its own
// index and carry; the client encrypts 6-byte headers and decrypts 4-byte ones, the
// server (worldtest) the other way round.
type HeaderCrypt struct {
	key          []byte
	sendI, sendJ uint8
	recvI, recvJ uint8
}

// NewHeaderCrypt keys the cipher with K as the server holds it (WorldSocket.cpp:341).
func NewHeaderCrypt(key []byte) *HeaderCrypt {
	k := append([]byte(nil), key...)
	if len(k) == 0 {
		k = []byte{0} // AuthCrypt::SetKey resizes an empty key to one byte (AuthCrypt.cpp:72-73)
	}
	return &HeaderCrypt{key: k}
}

// Encrypt mirrors AuthCrypt::EncryptSend: x = (d ^ K[i]) + j; j = x.
func (c *HeaderCrypt) Encrypt(h []byte) {
	for t := range h {
		c.sendI %= uint8(len(c.key))
		x := (h[t] ^ c.key[c.sendI]) + c.sendJ
		c.sendI++
		h[t], c.sendJ = x, x
	}
}

// Decrypt mirrors AuthCrypt::DecryptRecv: x = (d - j) ^ K[i]; j = d.
func (c *HeaderCrypt) Decrypt(h []byte) {
	for t := range h {
		c.recvI %= uint8(len(c.key))
		x := (h[t] - c.recvJ) ^ c.key[c.recvI]
		c.recvI++
		c.recvJ = h[t]
		h[t] = x
	}
}

// String and Format keep K out of every fmt verb (the session key never reaches logs).
func (c HeaderCrypt) String() string             { return "worldconn.HeaderCrypt{key=redacted}" }
func (c HeaderCrypt) Format(f fmt.State, _ rune) { _, _ = io.WriteString(f, c.String()) }
