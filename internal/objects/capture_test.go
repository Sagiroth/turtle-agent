package objects

import (
	"testing"

	"github.com/pjerra/turtle-agent/internal/capture"
	"github.com/pjerra/turtle-agent/internal/opcode"
	"github.com/pjerra/turtle-agent/internal/testfixture"
)

// Review Focus 2 and 3, pinned by captures: every update packet the server sent parses
// completely, compressed ones included, and so do the compressed moves. Task 22 adds the
// milestone 4 fixture to the list.
func TestCapturedUpdatesParse(t *testing.T) {
	compressed, blocks := 0, 0
	for _, name := range []string{"m1-login.twcap"} {
		recs := testfixture.Load(t, name)
		for _, body := range capture.Bodies(recs, opcode.SMSGUpdateObject) {
			b, err := ParseUpdate(body)
			if err != nil {
				t.Fatalf("%s SMSG_UPDATE_OBJECT: %v\n%x", name, err, body)
			}
			blocks += len(b)
		}
		for _, body := range capture.Bodies(recs, opcode.SMSGCompressedUpdateObject) {
			b, err := ParseCompressedUpdate(body)
			if err != nil {
				t.Fatalf("%s SMSG_COMPRESSED_UPDATE_OBJECT: %v\n%x", name, err, body)
			}
			compressed++
			blocks += len(b)
		}
		for _, body := range capture.Bodies(recs, opcode.SMSGCompressedMoves) {
			subs, err := SplitCompressedMoves(body)
			if err != nil {
				t.Fatalf("%s SMSG_COMPRESSED_MOVES: %v\n%x", name, err, body)
			}
			for _, sp := range subs {
				if sp.Op == opcode.SMSGMonsterMove {
					if _, err := ParseMonsterMove(sp.Body); err != nil {
						t.Fatalf("%s compressed monster move: %v\n%x", name, err, sp.Body)
					}
				}
			}
		}
		for _, body := range capture.Bodies(recs, opcode.SMSGMonsterMove) {
			if _, err := ParseMonsterMove(body); err != nil {
				t.Fatalf("%s SMSG_MONSTER_MOVE: %v\n%x", name, err, body)
			}
		}
	}
	if compressed == 0 || blocks < 10 {
		t.Fatalf("only %d compressed updates and %d blocks in the fixtures", compressed, blocks)
	}
}
