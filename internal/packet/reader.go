// Package packet holds the little-endian payload codec shared by realmd and world code.
package packet

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

// ErrShort reports a read past the end of a payload.
var ErrShort = errors.New("packet: short read")

// Reader decodes a payload. The first failure sticks: later reads return zero values
// and Err reports the offset of the first short read.
type Reader struct {
	b   []byte
	off int
	err error
}

func NewReader(b []byte) *Reader { return &Reader{b: b} }

func (r *Reader) take(n int) []byte {
	if r.err != nil {
		return nil
	}
	if n < 0 || len(r.b)-r.off < n {
		r.err = fmt.Errorf("%w: need %d bytes at offset %d, have %d", ErrShort, n, r.off, len(r.b)-r.off)
		return nil
	}
	s := r.b[r.off : r.off+n]
	r.off += n
	return s
}

func (r *Reader) U8() uint8 {
	if s := r.take(1); s != nil {
		return s[0]
	}
	return 0
}

func (r *Reader) U16() uint16 {
	if s := r.take(2); s != nil {
		return binary.LittleEndian.Uint16(s)
	}
	return 0
}

func (r *Reader) U32() uint32 {
	if s := r.take(4); s != nil {
		return binary.LittleEndian.Uint32(s)
	}
	return 0
}

func (r *Reader) I32() int32 { return int32(r.U32()) }

func (r *Reader) U64() uint64 {
	if s := r.take(8); s != nil {
		return binary.LittleEndian.Uint64(s)
	}
	return 0
}

func (r *Reader) F32() float32 { return math.Float32frombits(r.U32()) }

// GUID reads a full 8-byte ObjectGuid (`data << ObjectGuid` in the core).
func (r *Reader) GUID() GUID { return GUID(r.U64()) }

// PackedGUID reads ByteBuffer::readPackGUID's form (shared/ByteBuffer.h:354-370).
func (r *Reader) PackedGUID() GUID {
	mask := r.U8()
	var g uint64
	for i := 0; i < 8; i++ {
		if mask&(1<<i) != 0 {
			g |= uint64(r.U8()) << (8 * i)
		}
	}
	return GUID(g)
}

// CString reads a NUL-terminated string (ByteBuffer::operator<<(std::string), ByteBuffer.h:170-175).
func (r *Reader) CString() string {
	if r.err != nil {
		return ""
	}
	for i := r.off; i < len(r.b); i++ {
		if r.b[i] == 0 {
			s := string(r.b[r.off:i])
			r.off = i + 1
			return s
		}
	}
	r.err = fmt.Errorf("%w: unterminated string at offset %d", ErrShort, r.off)
	return ""
}

// Bytes returns a copy of the next n bytes.
func (r *Reader) Bytes(n int) []byte {
	s := r.take(n)
	if s == nil {
		return nil
	}
	return append([]byte(nil), s...)
}

func (r *Reader) Skip(n int)     { r.take(n) }
func (r *Reader) Remaining() int { return len(r.b) - r.off }
func (r *Reader) Offset() int    { return r.off }
func (r *Reader) Err() error     { return r.err }
