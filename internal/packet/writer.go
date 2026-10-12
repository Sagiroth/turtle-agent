package packet

import (
	"encoding/binary"
	"math"
)

// Writer builds a little-endian payload; every method returns the writer for chaining.
type Writer struct{ b []byte }

func NewWriter() *Writer { return &Writer{} }

func (w *Writer) U8(v uint8) *Writer   { w.b = append(w.b, v); return w }
func (w *Writer) U16(v uint16) *Writer { w.b = binary.LittleEndian.AppendUint16(w.b, v); return w }
func (w *Writer) U32(v uint32) *Writer { w.b = binary.LittleEndian.AppendUint32(w.b, v); return w }
func (w *Writer) I32(v int32) *Writer  { return w.U32(uint32(v)) }
func (w *Writer) U64(v uint64) *Writer { w.b = binary.LittleEndian.AppendUint64(w.b, v); return w }
func (w *Writer) F32(v float32) *Writer {
	return w.U32(math.Float32bits(v))
}
func (w *Writer) GUID(g GUID) *Writer      { return w.U64(uint64(g)) }
func (w *Writer) Append(p []byte) *Writer  { w.b = append(w.b, p...); return w }
func (w *Writer) CString(s string) *Writer { w.b = append(append(w.b, s...), 0); return w }
func (w *Writer) Bytes() []byte            { return w.b }
func (w *Writer) Len() int                 { return len(w.b) }

// PackedGUID writes ByteBuffer::appendPackGUID's form (shared/ByteBuffer.h:430-447).
func (w *Writer) PackedGUID(g GUID) *Writer {
	mask := byte(0)
	var parts []byte
	for i := 0; i < 8; i++ {
		if b := byte(uint64(g) >> (8 * i)); b != 0 {
			mask |= 1 << i
			parts = append(parts, b)
		}
	}
	w.b = append(append(w.b, mask), parts...)
	return w
}
