package auth

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"math/big"
	"math/rand/v2"
	"strings"
	"testing"
)

// fakeRealm is the server side of AuthSocket.cpp with BigNumber semantics, written
// independently of srp.go so the tests compare two implementations.
type fakeRealm struct {
	user          string
	s, v, b, bigB *big.Int
}

// bnBytes is BigNumber::AsByteArray(minSize) (BigNumber.cpp:164-183).
func bnBytes(n *big.Int, minSize int) []byte {
	be := n.Bytes()
	if len(be) < minSize {
		be = append(make([]byte, minSize-len(be)), be...)
	}
	out := make([]byte, len(be))
	for i := range be {
		out[i] = be[len(be)-1-i]
	}
	return out
}

func bnFromLE(b []byte) *big.Int {
	be := make([]byte, len(b))
	for i := range b {
		be[i] = b[len(b)-1-i]
	}
	return new(big.Int).SetBytes(be)
}

func sh(parts ...[]byte) []byte {
	h := sha1.New()
	for _, p := range parts {
		h.Write(p)
	}
	return h.Sum(nil)
}

func newFakeRealm(user, pass string, salt, b *big.Int) *fakeRealm {
	f := &fakeRealm{user: strings.ToUpper(user), s: salt, b: b}
	x := bnFromLE(sh(bnBytes(salt, 0), sh([]byte(f.user+":"+strings.ToUpper(pass))))) // :316-329
	f.v = new(big.Int).Exp(srpG, x, srpN)
	gmod := new(big.Int).Exp(srpG, b, srpN)
	f.bigB = new(big.Int).Mod(new(big.Int).Add(new(big.Int).Mul(f.v, big.NewInt(3)), gmod), srpN) // :603
	return f
}

func (f *fakeRealm) challenge() Challenge {
	var c Challenge
	copy(c.B[:], bnBytes(f.bigB, 32)) // :611
	c.Salt = bnBytes(f.s, 0)          // :616
	return c
}

// verify is _HandleLogonProof (:790-873, :920, :1005-1009).
func (f *fakeRealm) verify(a32 [32]byte, m1 [20]byte) (ok bool, m2, key []byte) {
	A := bnFromLE(a32[:])
	u := bnFromLE(sh(bnBytes(A, 0), bnBytes(f.bigB, 0)))
	S := new(big.Int).Exp(new(big.Int).Mul(A, new(big.Int).Exp(f.v, u, srpN)), f.b, srpN)
	t := bnBytes(S, 32)
	var t1 [16]byte
	for i := range t1 {
		t1[i] = t[i*2]
	}
	even := sh(t1[:])
	for i := range t1 {
		t1[i] = t[i*2+1]
	}
	odd := sh(t1[:])
	vK := make([]byte, 40)
	for i := 0; i < 20; i++ {
		vK[i*2], vK[i*2+1] = even[i], odd[i]
	}
	K := bnFromLE(vK)
	hash := sh(bnBytes(srpN, 0))
	hg := sh(bnBytes(srpG, 0))
	for i := range hash {
		hash[i] ^= hg[i]
	}
	t3 := bnFromLE(hash)
	M := bnFromLE(sh(bnBytes(t3, 0), sh([]byte(f.user)), bnBytes(f.s, 0), bnBytes(A, 0), bnBytes(f.bigB, 0), bnBytes(K, 0)))
	mb := bnBytes(M, 0)
	if len(mb) != 20 || !bytes.Equal(mb, m1[:]) { // memcmp over M.AsByteArray(): a 19-byte M never matches
		return false, nil, nil
	}
	return true, sh(bnBytes(A, 0), bnBytes(M, 0), bnBytes(K, 0)), bnBytes(K, 0)
}

// bigFrom is a deterministic 256-bit value with the top bit set, like BN_rand(256, 0, 1).
func bigFrom(seed uint64) *big.Int {
	var key [32]byte
	key[0], key[1], key[2] = byte(seed), byte(seed>>8), byte(seed>>16)
	b := make([]byte, 32)
	rand.NewChaCha8(key).Read(b)
	b[0] |= 0x80
	return new(big.Int).SetBytes(b)
}

func TestProofMatchesServer(t *testing.T) {
	for i := uint64(1); i <= 50; i++ {
		f := newFakeRealm("harness", "pw", bigFrom(i), bigFrom(1000+i))
		p, err := ComputeProof("Harness", "PW", f.challenge(), bigFrom(2000+i))
		if err != nil {
			t.Fatal(err)
		}
		if p.M1[19] == 0 {
			continue // the server cannot match it; TestNewProofFromRedrawsShortM1 covers this
		}
		ok, m2, key := f.verify(p.A, p.M1)
		if !ok || !bytes.Equal(m2, p.M2[:]) || !bytes.Equal(key, p.KeyBytes()) {
			t.Fatalf("seed %d: proof, M2 or K differ from the server", i)
		}
	}
}

// Review Focus 1: A, B and K with a zero high byte are hashed without it.
func TestProofEdgeVectors(t *testing.T) {
	edges := []struct {
		name string
		hit  func(f *fakeRealm, p Proof) bool
	}{
		{"B high byte zero", func(f *fakeRealm, p Proof) bool { return bnBytes(f.bigB, 32)[31] == 0 }},
		{"A high byte zero", func(f *fakeRealm, p Proof) bool { return p.A[31] == 0 }},
		{"K high byte zero", func(f *fakeRealm, p Proof) bool { return p.K[39] == 0 }},
	}
	for _, e := range edges {
		found := false
		for i := uint64(1); i < 20000 && !found; i++ {
			f := newFakeRealm("harness", "pw", bigFrom(7), bigFrom(i))
			p, err := ComputeProof("harness", "pw", f.challenge(), bigFrom(i+50000))
			if err != nil {
				t.Fatal(err)
			}
			if !e.hit(f, p) || p.M1[19] == 0 {
				continue
			}
			found = true
			ok, m2, key := f.verify(p.A, p.M1)
			if !ok || !bytes.Equal(m2, p.M2[:]) || !bytes.Equal(key, p.KeyBytes()) {
				t.Fatalf("%s (seed %d): the server would reject this login", e.name, i)
			}
			if e.name == "K high byte zero" && len(p.KeyBytes()) != 39 {
				t.Fatalf("KeyBytes has %d bytes, want 39", len(p.KeyBytes()))
			}
		}
		if !found {
			t.Fatalf("%s: no vector in 20000 seeds", e.name)
		}
	}
}

// privKey maps a seed into [1, N-1], the range newProofFrom draws from.
func privKey(seed uint64) *big.Int {
	limit := new(big.Int).Sub(srpN, big.NewInt(1))
	a := new(big.Int).Mod(bigFrom(seed), limit)
	return a.Add(a, big.NewInt(1))
}

// scriptedRand makes newProofFrom draw exactly these private keys, in order:
// crypto/rand.Int(r, N-1) reads 32 big-endian bytes per draw and returns any value
// below N-1 unchanged; newProofFrom then adds 1, so each chunk holds a-1.
func scriptedRand(keys ...*big.Int) *bytes.Buffer {
	var buf bytes.Buffer
	for _, a := range keys {
		var b [32]byte
		new(big.Int).Sub(a, big.NewInt(1)).FillBytes(b[:])
		buf.Write(b[:])
	}
	return &buf
}

// shortAndGoodKeys finds one private key whose M1 ends in a zero byte and one whose M1 does not.
func shortAndGoodKeys(t *testing.T, c Challenge) (short, good *big.Int) {
	t.Helper()
	for i := uint64(1); i < 20000 && (short == nil || good == nil); i++ {
		a := privKey(i + 90000)
		p, err := ComputeProof("harness", "pw", c, a)
		if err != nil {
			t.Fatal(err)
		}
		if p.M1[19] == 0 {
			if short == nil {
				short = a
			}
		} else if good == nil {
			good = a
		}
	}
	if short == nil || good == nil {
		t.Fatal("no short-M1 vector in 20000 seeds")
	}
	return short, good
}

// The mirror itself: realmd compares M.AsByteArray(), 19 bytes for such an M (AuthSocket.cpp:920).
func TestFakeRealmRejectsShortM1(t *testing.T) {
	f := newFakeRealm("harness", "pw", bigFrom(3), bigFrom(4))
	short, _ := shortAndGoodKeys(t, f.challenge())
	p, err := ComputeProof("harness", "pw", f.challenge(), short)
	if err != nil {
		t.Fatal(err)
	}
	if ok, _, _ := f.verify(p.A, p.M1); ok {
		t.Fatal("fake server accepted a 19-byte M; the mirror is wrong")
	}
}

func TestNewProofFromRedrawsShortM1(t *testing.T) {
	f := newFakeRealm("harness", "pw", bigFrom(3), bigFrom(4))
	c := f.challenge()
	short, good := shortAndGoodKeys(t, c)
	want, err := ComputeProof("harness", "pw", c, good)
	if err != nil {
		t.Fatal(err)
	}
	rng := scriptedRand(short, good)
	p, err := newProofFrom(rng, "harness", "pw", c)
	if err != nil {
		t.Fatal(err)
	}
	if rng.Len() != 0 {
		t.Fatalf("%d scripted bytes left: newProofFrom did not draw exactly twice", rng.Len())
	}
	if p != want {
		t.Fatal("newProofFrom did not return the proof for the second (redrawn) key")
	}
	if p.M1[19] == 0 {
		t.Fatal("final M1 ends in a zero byte")
	}
	if ok, _, _ := f.verify(p.A, p.M1); !ok {
		t.Fatal("server rejected the redrawn proof")
	}
}

func TestNewProofFromGivesUpAfter64ShortDraws(t *testing.T) {
	f := newFakeRealm("harness", "pw", bigFrom(3), bigFrom(4))
	short, _ := shortAndGoodKeys(t, f.challenge())
	keys := make([]*big.Int, 64)
	for i := range keys {
		keys[i] = short
	}
	if _, err := newProofFrom(scriptedRand(keys...), "harness", "pw", f.challenge()); err == nil {
		t.Fatal("64 short-M1 draws accepted")
	}
}

// NewProof wired to crypto/rand: every proof it returns must pass the server.
func TestNewProofAccepted(t *testing.T) {
	f := newFakeRealm("harness", "pw", bigFrom(3), bigFrom(4))
	for i := 0; i < 200; i++ {
		p, err := NewProof("harness", "pw", f.challenge())
		if err != nil {
			t.Fatal(err)
		}
		if ok, _, _ := f.verify(p.A, p.M1); !ok {
			t.Fatalf("draw %d: server rejected NewProof (M1[19]=%d)", i, p.M1[19])
		}
	}
}

// K is the session key and M1/M2 derive from it: no fmt verb may print their bytes.
func TestProofFormattingRedactsSecrets(t *testing.T) {
	f := newFakeRealm("harness", "pw", bigFrom(3), bigFrom(4))
	p, err := ComputeProof("harness", "pw", f.challenge(), privKey(1))
	if err != nil {
		t.Fatal(err)
	}
	var secrets []string
	for _, b := range [][]byte{p.K[:], p.M1[:], p.M2[:]} {
		goSyntax := make([]string, 8) // how %#v renders a byte array without GoString
		for i := range goSyntax {
			goSyntax[i] = fmt.Sprintf("%#x", b[i])
		}
		secrets = append(secrets, hex.EncodeToString(b), hex.EncodeToString(b[:8]),
			strings.ToUpper(hex.EncodeToString(b[:8])), strings.Trim(fmt.Sprint(b[:8]), "[]"),
			strings.Join(goSyntax, ", "))
	}
	wrapped := struct{ P Proof }{p}
	for _, verb := range []string{"%v", "%+v", "%#v", "%s"} {
		for _, arg := range []any{p, &p, wrapped} {
			out := fmt.Sprintf(verb, arg)
			for _, s := range secrets {
				if strings.Contains(out, s) {
					t.Fatalf("%s of %T leaks secret bytes: %s", verb, arg, out)
				}
			}
		}
	}
}

func TestRejectsZeroB(t *testing.T) {
	if _, err := ComputeProof("a", "b", Challenge{Salt: make([]byte, 32)}, big.NewInt(5)); err == nil {
		t.Fatal("B = 0 accepted")
	}
}
