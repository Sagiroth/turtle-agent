package packet

import (
	"encoding/json"
	"errors"
	"math"
	"testing"
)

func TestWriterReaderRoundTrip(t *testing.T) {
	w := NewWriter().U8(0xAB).U16(0x1234).U32(0xDEADBEEF).I32(-5).U64(0x0102030405060708).
		F32(1.5).CString("Harnessb").GUID(0xF130000000001234).PackedGUID(0xF130000000001234).Append([]byte{9, 9})
	r := NewReader(w.Bytes())
	if r.U8() != 0xAB || r.U16() != 0x1234 || r.U32() != 0xDEADBEEF || r.I32() != -5 ||
		r.U64() != 0x0102030405060708 || r.F32() != 1.5 || r.CString() != "Harnessb" ||
		r.GUID() != 0xF130000000001234 || r.PackedGUID() != 0xF130000000001234 {
		t.Fatalf("round trip mismatch, err=%v", r.Err())
	}
	if got := r.Bytes(2); len(got) != 2 || got[0] != 9 || r.Remaining() != 0 || r.Err() != nil {
		t.Fatalf("tail %v remaining=%d err=%v", got, r.Remaining(), r.Err())
	}
}

// ByteBuffer::appendPackGUID (shared/ByteBuffer.h:430-447): a mask byte, then the non-zero bytes.
func TestPackedGUIDLayout(t *testing.T) {
	cases := []struct {
		g    GUID
		want []byte
	}{
		{0, []byte{0x00}},
		{0x17, []byte{0x01, 0x17}},
		{0xF130000000001234, []byte{0xC3, 0x34, 0x12, 0x30, 0xF1}},
	}
	for _, c := range cases {
		got := NewWriter().PackedGUID(c.g).Bytes()
		if string(got) != string(c.want) {
			t.Errorf("%s: got % X want % X", c.g, got, c.want)
		}
		if back := NewReader(got).PackedGUID(); back != c.g {
			t.Errorf("read back %s, want %s", back, c.g)
		}
	}
}

func TestShortReadIsStickyAndReportsOffset(t *testing.T) {
	r := NewReader([]byte{1, 2, 3})
	_ = r.U16()
	if v := r.U32(); v != 0 {
		t.Fatalf("short read returned %d", v)
	}
	if v := r.U8(); v != 0 {
		t.Fatalf("read after the first error returned %d", v)
	}
	if !errors.Is(r.Err(), ErrShort) {
		t.Fatalf("err = %v, want ErrShort", r.Err())
	}
	if r.Offset() != 2 {
		t.Fatalf("offset %d, want 2", r.Offset())
	}
}

func TestUnterminatedCString(t *testing.T) {
	r := NewReader([]byte("abc"))
	if s := r.CString(); s != "" || !errors.Is(r.Err(), ErrShort) {
		t.Fatalf("got %q err %v", s, r.Err())
	}
}

func TestGUIDParts(t *testing.T) {
	g := GUID(0xF13000064C001234) // HIGHGUID_UNIT (ObjectGuid.h:73), entry 0x64C, counter 0x1234
	if g.High() != HighUnit || g.Entry() != 0x64C || !g.IsCreature() || g.IsPlayer() {
		t.Fatalf("parts of %s wrong", g)
	}
	if p := GUID(0x2A); !p.IsPlayer() || p.IsCreature() {
		t.Fatal("player guid misclassified")
	}
}

func TestGUIDJSON(t *testing.T) {
	b, err := json.Marshal(struct{ G GUID }{0xF130000000001234})
	if err != nil || string(b) != `{"G":"0xF130000000001234"}` {
		t.Fatalf("marshal %s %v", b, err)
	}
	var v struct{ G GUID }
	if err := json.Unmarshal([]byte(`{"G":"0xf130000000001234"}`), &v); err != nil || v.G != 0xF130000000001234 {
		t.Fatalf("unmarshal %v %v", v.G, err)
	}
	if _, err := ParseGUID("zz"); err == nil {
		t.Fatal("ParseGUID accepted garbage")
	}
}

func TestFloatBits(t *testing.T) {
	if NewReader(NewWriter().F32(float32(math.Pi)).Bytes()).F32() != float32(math.Pi) {
		t.Fatal("float mismatch")
	}
}
