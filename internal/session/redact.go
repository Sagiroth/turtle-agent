package session

import (
	"bytes"
	"strings"

	"github.com/pjerra/turtle-agent/internal/events"
)

// redactedMark replaces a secret in event text and names.
const redactedMark = "<redacted>"

// redactor scrubs secrets the server echoes back (the account name in a .pinfo reply,
// ruling P10) out of events and diagnostics. Matching is ASCII case-insensitive and on
// whole words: a secret is only matched where no letter or digit adjoins it, so a
// character named after its account (account HARNESS, character Harnessb) keeps its name.
type redactor struct{ needles [][]byte }

func isWordByte(c byte) bool {
	return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9'
}

// wholeWord reports whether l[i:i+n] has no letter or digit on either side.
func wholeWord(l []byte, i, n int) bool {
	return (i == 0 || !isWordByte(l[i-1])) && (i+n == len(l) || !isWordByte(l[i+n]))
}

// index returns the first whole-word match of n in l at or after from, or -1.
func index(l, n []byte, from int) int {
	for from <= len(l)-len(n) {
		j := bytes.Index(l[from:], n)
		if j < 0 {
			return -1
		}
		if wholeWord(l, from+j, len(n)) {
			return from + j
		}
		from += j + 1
	}
	return -1
}

func newRedactor(secrets []string) redactor {
	var r redactor
	for _, s := range secrets {
		if s == "" {
			continue
		}
		n := asciiLower([]byte(s))
		dup := false
		for _, have := range r.needles {
			dup = dup || bytes.Equal(have, n)
		}
		if !dup {
			r.needles = append(r.needles, n)
		}
	}
	return r
}

// asciiLower lowers A-Z only, so byte offsets match the input.
func asciiLower(b []byte) []byte {
	out := make([]byte, len(b))
	for i, c := range b {
		if 'A' <= c && c <= 'Z' {
			c += 'a' - 'A'
		}
		out[i] = c
	}
	return out
}

// contains reports whether b holds any secret, as any substring: it guards the hex dump,
// where a false alarm only costs the dump.
func (r redactor) contains(b []byte) bool {
	if len(r.needles) == 0 {
		return false
	}
	l := asciiLower(b)
	for _, n := range r.needles {
		if bytes.Contains(l, n) {
			return true
		}
	}
	return false
}

// scrub replaces every run of bytes covered by a whole-word secret with redactedMark;
// overlapping or adjacent matches merge into one mark.
func (r redactor) scrub(s string) string {
	if len(r.needles) == 0 || s == "" {
		return s
	}
	l := asciiLower([]byte(s))
	var hit []bool
	for _, n := range r.needles {
		for i := index(l, n, 0); i >= 0; i = index(l, n, i+1) {
			if hit == nil {
				hit = make([]bool, len(l))
			}
			for k := i; k < i+len(n); k++ {
				hit[k] = true
			}
		}
	}
	if hit == nil {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		if !hit[i] {
			b.WriteByte(s[i])
			i++
			continue
		}
		b.WriteString(redactedMark)
		for i < len(s) && hit[i] {
			i++
		}
	}
	return b.String()
}

func (r redactor) scrubValue(v any) any {
	switch t := v.(type) {
	case string:
		return r.scrub(t)
	case []string:
		out := make([]string, len(t))
		for i, s := range t {
			out[i] = r.scrub(s)
		}
		return out
	case map[string]any:
		for k, x := range t {
			t[k] = r.scrubValue(x)
		}
		return t
	}
	return v
}

// scrubEvent scrubs the text, the names and the string data of e.
func (r redactor) scrubEvent(e *events.Event) {
	if len(r.needles) == 0 {
		return
	}
	e.Text, e.Source, e.Target = r.scrub(e.Text), r.scrub(e.Source), r.scrub(e.Target)
	for k, v := range e.Data {
		e.Data[k] = r.scrubValue(v)
	}
}
