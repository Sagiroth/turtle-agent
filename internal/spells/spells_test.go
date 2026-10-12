package spells

import "testing"

func TestNames(t *testing.T) {
	for id, want := range map[uint32]string{430: "Drink", 433: "Food", 118: "Polymorph", 1953: "Blink", 77: "Turtle's Shell", 0: "", 4000000: ""} {
		if got := Name(id); got != want {
			t.Errorf("%d: %q, want %q", id, got, want)
		}
	}
}

// The mounts are the spells that apply SPELL_AURA_MOUNTED. 46499 is the bridle's own use
// (Add Mount to Collection, agent.cap #4300), which teaches a mount but is no mount.
func TestIsMount(t *testing.T) {
	for id, want := range map[uint32]bool{458: true, 6648: true, 46499: false, 116: false, 2383: false, 0: false} {
		if got := IsMount(id); got != want {
			t.Errorf("IsMount(%d) = %v, want %v", id, got, want)
		}
	}
}
