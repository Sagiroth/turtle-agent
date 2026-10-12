// Copyright (c) 2026 agent-wow
// SPDX-License-Identifier: MIT
// Ported from github.com/agent-wow/agent-wow pkg/auth/srp.go at 5ef4916 (see NOTICE).
// Kept: the 256-bit group, the A/u/x/S/M1/M2 arithmetic and the SHA-1 helpers.
// Changed: every hash input uses the server's BigNumber::AsByteArray() form
// (little-endian, high zero bytes dropped), K comes from the full 32-byte S with no
// zero stripping, and NewProof redraws a private key whose M1 ends in a zero byte,
// matching the cmangos realmd in ~/tortoise-wow (AuthSocket.cpp:790-1009).

// Package auth logs in to realmd: SRP6, logon challenge and proof, realm list.
package auth

import (
	"crypto/rand"
	"crypto/sha1" // required by the WoW SRP6 protocol
	"errors"
	"io"
	"math/big"
	"slices"
	"strings"
)

var (
	srpN = mustHex("894B645E89E1535BBDAD5B8B290650530801B18EBFBF5E8FAB3C82872A3E9BB7") // AuthSocket.cpp:174
	srpG = big.NewInt(7)                                                               // AuthSocket.cpp:175
)

func mustHex(s string) *big.Int {
	n, ok := new(big.Int).SetString(s, 16)
	if !ok {
		panic("bad SRP constant")
	}
	return n
}

// Challenge is the SRP part of realmd's logon-challenge reply (AuthSocket.cpp:608-617).
type Challenge struct {
	B    [32]byte // little-endian, zero-padded to 32 bytes by the server
	Salt []byte   // little-endian, as sent
}

// Proof is the client half of the exchange. K is secret: never print or store it.
type Proof struct {
	A  [32]byte // little-endian, zero-padded: the wire field is 32 bytes
	M1 [20]byte
	M2 [20]byte // the server proof we expect
	K  [40]byte // session key, full width
}

// KeyBytes is K as the server's BigNumber::AsByteArray() renders it. The world server
// hashes this form into CMSG_AUTH_SESSION and keys the header cipher with it
// (WorldSocket.cpp:296, :341); it has 39 bytes when K's top byte is zero.
func (p Proof) KeyBytes() []byte { return asBytes(p.K[:]) }

// ComputeProof runs the client side of SRP6 for the private key a.
func ComputeProof(user, pass string, c Challenge, a *big.Int) (Proof, error) {
	I, P := strings.ToUpper(user), strings.ToUpper(pass)
	B := fromLE(c.B[:])
	if B.Sign() == 0 || new(big.Int).Mod(B, srpN).Sign() == 0 {
		return Proof{}, errors.New("srp: server sent an invalid B")
	}
	var p Proof
	p.A = toLE32(new(big.Int).Exp(srpG, a, srpN))
	aB, bB, sB := asBytes(p.A[:]), asBytes(c.B[:]), asBytes(c.Salt)
	u := fromLE(digestSlice(aB, bB)) // AuthSocket.cpp:801-805
	if u.Sign() == 0 {
		return Proof{}, errors.New("srp: scrambler u is zero")
	}
	id := digest([]byte(I + ":" + P))
	x := fromLE(digestSlice(sB, id[:])) // AuthSocket.cpp:316-329
	v := new(big.Int).Exp(srpG, x, srpN)
	base := new(big.Int).Sub(B, new(big.Int).Mul(big.NewInt(3), v))
	base.Mod(base, srpN)
	S := new(big.Int).Exp(base, new(big.Int).Add(a, new(big.Int).Mul(u, x)), srpN)
	p.K = interleave(toLE32(S)) // AuthSocket.cpp:808-841
	nLE := toLE32(srpN)
	hN, hG := digest(asBytes(nLE[:])), digest([]byte{7})
	var t3 [20]byte
	for i := range t3 {
		t3[i] = hN[i] ^ hG[i] // AuthSocket.cpp:845-859
	}
	hI := digest([]byte(I))
	p.M1 = digest(asBytes(t3[:]), hI[:], sB, aB, bB, p.KeyBytes()) // AuthSocket.cpp:867-871
	p.M2 = digest(aB, asBytes(p.M1[:]), p.KeyBytes())              // AuthSocket.cpp:1005-1007
	return p, nil
}

// NewProof draws a private key, redrawing while M1's last byte is zero: realmd compares
// M.AsByteArray() (then only 19 bytes) against the 20-byte proof (AuthSocket.cpp:920).
func NewProof(user, pass string, c Challenge) (Proof, error) {
	return newProofFrom(rand.Reader, user, pass, c)
}

func newProofFrom(rng io.Reader, user, pass string, c Challenge) (Proof, error) {
	limit := new(big.Int).Sub(srpN, big.NewInt(1))
	for range 64 {
		a, err := rand.Int(rng, limit)
		if err != nil {
			return Proof{}, err
		}
		a.Add(a, big.NewInt(1))
		p, err := ComputeProof(user, pass, c, a)
		if err != nil {
			return Proof{}, err
		}
		if p.M1[19] != 0 {
			return p, nil
		}
	}
	return Proof{}, errors.New("srp: no usable private key in 64 draws")
}

// interleave builds the 40-byte session key from S (AuthSocket.cpp:813-841).
func interleave(s [32]byte) [40]byte {
	var even, odd [16]byte
	for i := 0; i < 16; i++ {
		even[i], odd[i] = s[2*i], s[2*i+1]
	}
	he, ho := digest(even[:]), digest(odd[:])
	var k [40]byte
	for i := 0; i < 20; i++ {
		k[2*i], k[2*i+1] = he[i], ho[i]
	}
	return k
}

// asBytes is BigNumber::AsByteArray() with no minimum size (BigNumber.cpp:164-183):
// little-endian with the high (trailing) zero bytes removed.
func asBytes(le []byte) []byte {
	n := len(le)
	for n > 0 && le[n-1] == 0 {
		n--
	}
	return slices.Clone(le[:n])
}

func digest(parts ...[]byte) [20]byte {
	h := sha1.New()
	for _, p := range parts {
		h.Write(p)
	}
	var out [20]byte
	copy(out[:], h.Sum(nil))
	return out
}

func digestSlice(parts ...[]byte) []byte { d := digest(parts...); return d[:] }

func fromLE(b []byte) *big.Int {
	r := slices.Clone(b)
	slices.Reverse(r)
	return new(big.Int).SetBytes(r)
}

func toLE32(n *big.Int) [32]byte {
	var out [32]byte
	n.FillBytes(out[:])
	slices.Reverse(out[:])
	return out
}
