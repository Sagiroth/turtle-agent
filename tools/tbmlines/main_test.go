package main

import (
	"strings"
	"testing"
)

func TestDecodeShapes(t *testing.T) {
	ev := `{"seq":1,"kind":"system","text":"TBM:VERSION|v1"}`
	for name, in := range map[string]string{
		"array":  `[` + ev + `]`,
		"events": `{"events":[` + ev + `]}`,
		"reply":  `{"jsonrpc":"2.0","id":1,"result":{"events":[` + ev + `]}}`,
	} {
		evs, err := decode([]byte(in))
		if err != nil || len(evs) != 1 {
			t.Errorf("%s: %d events, err %v", name, len(evs), err)
		}
	}
	if _, err := decode([]byte(`"nope"`)); err == nil {
		t.Error("a string decoded as events")
	}
}

func TestWrite(t *testing.T) {
	in := `[
	 {"seq":1,"kind":"chat","text":"TBM:VERSION|not a system line"},
	 {"seq":2,"kind":"system","text":"hello"},
	 {"seq":3,"kind":"system","text":"TBM:ACTION_ACKeady|party|3|-","data":{"raw":"TBM:ACTION_ACK|ready|party|3|-"}},
	 {"seq":4,"kind":"addon","text":"TBM:BOTSTATE|Bob|move=follow","data":{"prefix":"TBM","type":"guild"}},
	 {"seq":5,"kind":"addon","text":"TBM:VERSION|other","data":{"prefix":"OTHER","type":"guild"}}
	]`
	evs, err := decode([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	var sb strings.Builder
	n, err := Write(&sb, evs)
	if err != nil || n != 2 {
		t.Fatalf("wrote %d, err %v", n, err)
	}
	want := "system\tTBM:ACTION_ACK|ready|party|3|-\naddon\tGUILD\tTBM:BOTSTATE|Bob|move=follow\n"
	if sb.String() != want {
		t.Errorf("got %q, want %q", sb.String(), want)
	}
}
