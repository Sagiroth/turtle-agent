package auth

import (
	"bytes"
	"crypto/sha1"
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
			continue // the server cannot match it; TestNewProofRedrawsShortM1 covers this
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

func TestNewProofRedrawsShortM1(t *testing.T) {
	f := newFakeRealm("harness", "pw", bigFrom(3), bigFrom(4))
	found := false
	for i := uint64(1); i < 20000 && !found; i++ {
		p, err := ComputeProof("harness", "pw", f.challenge(), bigFrom(i+90000))
		if err != nil {
			t.Fatal(err)
		}
		if p.M1[19] == 0 {
			found = true
			if ok, _, _ := f.verify(p.A, p.M1); ok {
				t.Fatal("fake server accepted a 19-byte M; the mirror is wrong")
			}
		}
	}
	if !found {
		t.Fatal("no short-M1 vector in 20000 seeds")
	}
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

func TestRejectsZeroB(t *testing.T) {
	if _, err := ComputeProof("a", "b", Challenge{Salt: make([]byte, 32)}, big.NewInt(5)); err == nil {
		t.Fatal("B = 0 accepted")
	}
}
