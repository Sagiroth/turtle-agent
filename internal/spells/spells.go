// Package spells names spells by id, and tells which of them apply SPELL_AURA_MOUNTED, both
// from the core's spell_template (sql/base), packed by tools/spellnamegen (make spellnames).
// The server sends spell ids only; the 1.12 client reads the names from its own Spell.dbc,
// which the agent does not have.
package spells

import (
	"bufio"
	"bytes"
	"compress/gzip"
	_ "embed"
	"strconv"
	"strings"
	"sync"
)

//go:embed names.tsv.gz
var packed []byte

//go:embed mounts.txt.gz
var packedMounts []byte

var (
	once      sync.Once
	names     map[uint32]string
	mountOnce sync.Once
	mounts    map[uint32]bool
)

// Name is the spell's name, or "" for an id the table does not hold (a spell added to
// the server's database after the table was made).
func Name(id uint32) string {
	once.Do(load)
	return names[id]
}

// IsMount reports whether the spell applies SPELL_AURA_MOUNTED (78, SpellAuraDefines.h:216)
// in one of its effects. A bridle teaches one of these spells; the agent casts only those.
func IsMount(id uint32) bool {
	mountOnce.Do(loadMounts)
	return mounts[id]
}

func load() {
	names = map[uint32]string{}
	zr, err := gzip.NewReader(bytes.NewReader(packed))
	if err != nil {
		return
	}
	sc := bufio.NewScanner(zr)
	for sc.Scan() {
		idText, name, ok := strings.Cut(sc.Text(), "\t")
		if !ok {
			continue // the header
		}
		if id, err := strconv.ParseUint(idText, 10, 32); err == nil {
			names[uint32(id)] = name
		}
	}
}

func loadMounts() {
	mounts = map[uint32]bool{}
	zr, err := gzip.NewReader(bytes.NewReader(packedMounts))
	if err != nil {
		return
	}
	sc := bufio.NewScanner(zr)
	for sc.Scan() {
		if id, err := strconv.ParseUint(sc.Text(), 10, 32); err == nil {
			mounts[uint32(id)] = true // the header is not a number
		}
	}
}
