package spells

import "testing"

func TestNames(t *testing.T) {
	for id, want := range map[uint32]string{430: "Drink", 433: "Food", 118: "Polymorph", 1953: "Blink", 77: "Turtle's Shell", 0: "", 4000000: ""} {
		if got := Name(id); got != want {
			t.Errorf("%d: %q, want %q", id, got, want)
		}
	}
}
