// Package objtest writes update-object payloads the way the core does, for tests only.
package objtest

import (
	"bytes"
	"compress/zlib"
	"sort"

	"github.com/pjerra/turtle-agent/internal/packet"
)

// Values writes Object::BuildValuesUpdate's mask and values (Objects/Object.cpp:553-556)
// for an object of count fields: u8 mask block count, the u32 mask words, then one u32
// per set bit in index order.
func Values(w *packet.Writer, count int, vals map[uint16]uint32) {
	blocks := (count + 31) / 32
	masks := make([]uint32, blocks)
	var idx []int
	for i := range vals {
		masks[i/32] |= 1 << (i % 32)
		idx = append(idx, int(i))
	}
	sort.Ints(idx)
	w.U8(uint8(blocks))
	for _, m := range masks {
		w.U32(m)
	}
	for _, i := range idx {
		w.U32(vals[uint16(i)])
	}
}

// Compress frames plain the way SMSG_COMPRESSED_UPDATE_OBJECT and SMSG_COMPRESSED_MOVES
// carry it: u32 uncompressed size, then a zlib stream (Objects/UpdateData.cpp:121-135, :200-217).
func Compress(plain []byte) []byte {
	var z bytes.Buffer
	zw := zlib.NewWriter(&z)
	zw.Write(plain)
	zw.Close()
	return packet.NewWriter().U32(uint32(len(plain))).Append(z.Bytes()).Bytes()
}
