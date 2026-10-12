// Package capture records decrypted world packets for test fixtures. A capture never
// holds realmd traffic, CMSG_AUTH_SESSION or Warden bodies, or any registered secret.
package capture

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/pjerra/turtle-agent/internal/opcode"
	"github.com/pjerra/turtle-agent/internal/packet"
	"github.com/pjerra/turtle-agent/internal/worldconn"
)

const (
	magic       = "TWCAP1\n"
	redactedBit = 0x80
)

// neverStored bodies are dropped whatever they contain.
var neverStored = map[uint16]bool{
	opcode.CMSGAuthSession: true, // account name and a key-derived digest (WorldSocket.cpp:144-149)
	opcode.SMSGWardenData:  true, // key-derived Warden payloads
	opcode.CMSGWardenData:  true,
}

type Record struct {
	Dir      worldconn.Direction
	Time     time.Time
	Op       uint16
	Body     []byte
	Redacted bool
}

type Writer struct {
	mu       sync.Mutex
	f        *os.File
	bw       *bufio.Writer
	secrets  [][]byte
	count    int
	redacted int
	err      error
	closed   bool
}

// ErrSecretTooShort rejects secrets that would match everywhere or nowhere useful.
var ErrSecretTooShort = errors.New("secret too short to redact safely")

const minSecret = 3

// Variants returns, deduplicated, the forms a secret can take in a packet: raw, reversed,
// lower and upper hex of both, and the ASCII upper and lower case of the raw bytes.
func Variants(secret []byte) [][]byte {
	rev := make([]byte, len(secret))
	for i, b := range secret {
		rev[len(secret)-1-i] = b
	}
	cands := [][]byte{
		secret, rev,
		[]byte(hex.EncodeToString(secret)), []byte(hex.EncodeToString(rev)),
		bytes.ToUpper([]byte(hex.EncodeToString(secret))), bytes.ToUpper([]byte(hex.EncodeToString(rev))),
		bytes.ToUpper(secret), bytes.ToLower(secret),
	}
	var out [][]byte
	for _, c := range cands {
		dup := false
		for _, o := range out {
			if bytes.Equal(o, c) {
				dup = true
				break
			}
		}
		if !dup {
			out = append(out, bytes.Clone(c))
		}
	}
	return out
}

// Create opens a new capture (never overwrites, mode 0600). Secrets are byte strings
// that must not appear in any stored body (account names, passwords, the session key),
// in any of the forms Variants lists. A secret under 3 bytes is an error.
func Create(path string, secrets ...[]byte) (*Writer, error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, fmt.Errorf("capture: %w", err)
	}
	w := &Writer{f: f, bw: bufio.NewWriter(f)}
	for _, s := range secrets {
		if len(s) < minSecret {
			f.Close()
			os.Remove(path)
			return nil, fmt.Errorf("capture: %w", ErrSecretTooShort)
		}
		w.secrets = append(w.secrets, Variants(s)...)
	}
	if _, err := w.bw.WriteString(magic); err != nil {
		f.Close()
		os.Remove(path)
		return nil, fmt.Errorf("capture: %w", err)
	}
	return w, nil
}

// Packet implements worldconn.Tap.
func (w *Writer) Packet(dir worldconn.Direction, op uint16, body []byte) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		if w.err == nil {
			w.err = errors.New("capture: packet after Close")
		}
		return
	}
	if w.err != nil {
		return
	}
	flags := uint8(dir)
	if neverStored[op] || w.hasSecret(body) {
		flags |= redactedBit
		body = nil
		w.redacted++
	}
	hdr := packet.NewWriter().U8(flags).U64(uint64(time.Now().UnixNano())).U16(op).U32(uint32(len(body)))
	if _, err := w.bw.Write(hdr.Bytes()); err != nil {
		w.err = err
		return
	}
	if _, err := w.bw.Write(body); err != nil {
		w.err = err
		return
	}
	w.count++
}

func (w *Writer) hasSecret(body []byte) bool {
	for _, s := range w.secrets {
		if bytes.Contains(body, s) {
			return true
		}
	}
	return false
}

func (w *Writer) Counts() (records, redacted int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.count, w.redacted
}

func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return w.err
	}
	w.closed = true
	if err := w.bw.Flush(); err != nil && w.err == nil {
		w.err = err
	}
	if err := w.f.Close(); err != nil && w.err == nil {
		w.err = err
	}
	return w.err
}

func ReadFile(path string) ([]Record, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("capture: %w", err)
	}
	if !bytes.HasPrefix(raw, []byte(magic)) {
		return nil, fmt.Errorf("capture %s: not a TWCAP1 file", path)
	}
	r := packet.NewReader(raw[len(magic):])
	var recs []Record
	for r.Remaining() > 0 {
		flags, ts, op, n := r.U8(), r.U64(), r.U16(), r.U32()
		body := r.Bytes(int(n))
		if err := r.Err(); err != nil {
			return nil, fmt.Errorf("capture %s: record %d: %w", path, len(recs), err)
		}
		recs = append(recs, Record{
			Dir: worldconn.Direction(flags &^ redactedBit), Time: time.Unix(0, int64(ts)),
			Op: op, Body: body, Redacted: flags&redactedBit != 0,
		})
	}
	return recs, nil
}

type Finding struct {
	Index  int    // record index
	Op     uint16 // record opcode
	Needle int    // index into the needles argument
}

// Scan reports every record whose body contains one of the needles in any form Variants
// lists. A needle under 3 bytes is an error: it cannot prove anything.
func Scan(recs []Record, needles ...[]byte) ([]Finding, error) {
	vars := make([][][]byte, len(needles))
	for j, n := range needles {
		if len(n) < minSecret {
			return nil, fmt.Errorf("capture: needle %d: %w", j, ErrSecretTooShort)
		}
		vars[j] = Variants(n)
	}
	var out []Finding
	for i, rec := range recs {
		for j := range needles {
			for _, v := range vars[j] {
				if bytes.Contains(rec.Body, v) {
					out = append(out, Finding{Index: i, Op: rec.Op, Needle: j})
					break
				}
			}
		}
	}
	return out, nil
}

// Bodies returns, in order, the bodies of server packets with opcode op.
func Bodies(recs []Record, op uint16) [][]byte {
	var out [][]byte
	for _, r := range recs {
		if r.Op == op && r.Dir == worldconn.ServerToClient && !r.Redacted {
			out = append(out, r.Body)
		}
	}
	return out
}

// Stats counts records per opcode name.
func Stats(recs []Record) map[string]int {
	out := map[string]int{}
	for _, r := range recs {
		out[opcode.Name(r.Op)]++
	}
	return out
}
