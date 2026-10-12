package tbm

import (
	"strings"
	"testing"
)

func mustFind(t *testing.T, name string) Button {
	t.Helper()
	b, err := Find(name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestFindNames(t *testing.T) {
	for _, name := range []string{"Fight.AoE", "fight.aoe", "Fight.aoe", "AoE"} {
		if b := mustFind(t, name); b.Name != "Fight.AoE" {
			t.Errorf("%q -> %s", name, b.Name)
		}
	}
	if b := mustFind(t, "Camp.Rest"); b.Command != "action rest" {
		t.Errorf("%+v", b)
	}
	if _, err := Find("Fight.Nope"); err == nil {
		t.Error("unknown button found")
	}
	if _, err := Find("Invite"); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Errorf("Invite is on the Roster and Guild tabs: %v", err)
	}
}

// Every button builds, with placeholders for its required arguments, and the table has no
// duplicate names or commands that start with ".bot".
func TestTableIsConsistent(t *testing.T) {
	seen := map[string]bool{}
	for _, b := range Buttons {
		if seen[b.Name] {
			t.Errorf("duplicate %s", b.Name)
		}
		seen[b.Name] = true
		if strings.HasPrefix(b.Command, ".bot") || b.Command == "" || b.Source == "" || b.Scope == "" || b.Reply == "" {
			t.Errorf("%+v", b)
		}
		args := map[string]string{"state": "on", "bot": "Gorm", "name": "Gorm", "mark": "moon", "bag": "0", "slot": "1", "strategy": "+arms", "role": "dps"}
		c, err := Build(b, Context{Args: pick(args, append(append([]string{}, b.Args...), b.Optional...))})
		if err != nil {
			t.Errorf("%s: %v", b.Name, err)
			continue
		}
		if strings.ContainsAny(c.Text, "{}") || ((b.Reply == ReplyAck || b.Reply == ReplyInventory) && c.Intent == "") {
			t.Errorf("%s: %+v", b.Name, c)
		}
		if b.Reply == ReplyText && len(b.Ok) == 0 {
			t.Errorf("%s: text reply without a success pattern", b.Name)
		}
	}
	if AddonCommit == "" || len(Buttons) < 40 {
		t.Errorf("table: %s, %d buttons", AddonCommit, len(Buttons))
	}
}

func pick(all map[string]string, keys []string) map[string]string {
	out := map[string]string{}
	for _, k := range keys {
		out[k] = all[k]
	}
	return out
}

func build(t *testing.T, name string, c Context) Command {
	t.Helper()
	cmd, err := Build(mustFind(t, name), c)
	if err != nil {
		t.Fatal(err)
	}
	return cmd
}

func TestBuildPlainActions(t *testing.T) {
	for name, want := range map[string]string{
		"Fight.Attack": "action attack", "Fight.ComeHold": "action come", "Fight.FocusSkull": "action focus skull",
		"Camp.Rest": "action rest", "Camp.CorpseRun": "action corpse run", "Raid.TankFaceAway": "action raid tankface",
		"Roster.Refresh": "roster",
	} {
		if got := build(t, name, Context{}).Text; got != want {
			t.Errorf("%s: %q, want %q", name, got, want)
		}
	}
	if c := build(t, "Camp.Rest", Context{}); c.Intent != "rest" {
		t.Errorf("%+v", c)
	}
}

// A switch click sets the opposite of what the bots report: on unless every bot is on, as
// SendPartyToggle does; an explicit state wins.
func TestBuildPartySwitch(t *testing.T) {
	states := map[string]string{"Gorm": "move=follow,aoe=on,loot=off", "Lia": "move=follow,aoe=on"}
	for _, tc := range []struct {
		name string
		ctx  Context
		want string
	}{
		{"Fight.AoE", Context{BotStates: states}, "action aoe off"},
		{"Fight.Loot", Context{BotStates: states}, "action loot on"},
		{"Fight.AutoCC", Context{BotStates: states}, "action auto cc on"},
		{"Fight.AoE", Context{}, "action aoe on"},
		{"Fight.AoE", Context{BotStates: map[string]string{"Gorm": "aoe=on", "Lia": "aoe=off"}}, "action aoe on"},
		{"Fight.AoE", Context{BotStates: states, Args: map[string]string{"state": "on"}}, "action aoe on"},
		{"Fight.AoE", Context{BotStates: states, Args: map[string]string{"state": "toggle"}}, "action aoe off"},
	} {
		c := build(t, tc.name, tc.ctx)
		if c.Text != tc.want || c.Intent == "" {
			t.Errorf("%s: %+v, want %q", tc.name, c, tc.want)
		}
	}
	if c := build(t, "Fight.AutoCC", Context{}); c.Intent != "auto cc" {
		t.Errorf("%+v", c)
	}
	if _, err := Build(mustFind(t, "Fight.AoE"), Context{Args: map[string]string{"state": "maybe"}}); err == nil {
		t.Error("bad state accepted")
	}
	// The per-bot panel switch reads that bot's own state.
	c := build(t, "BotPanel.Behavior.LootCorpses", Context{Args: map[string]string{"bot": "gorm"}, BotStates: states})
	if c.Text != "behavior gorm loot on" || c.Intent != "behavior loot" {
		t.Errorf("%+v", c)
	}
	c = build(t, "BotPanel.Behavior.AoESpells", Context{Args: map[string]string{"bot": "Gorm"}, BotStates: states})
	if c.Text != "behavior Gorm aoe off" {
		t.Errorf("%+v", c)
	}
}

// The addon narrows a formation to the targeted owned bot in the command text itself; the
// other action buttons carry no scope (the server reads the target).
func TestBuildScopeNarrowing(t *testing.T) {
	if c := build(t, "Camp.Formation.Arrow", Context{}); c.Text != "formation arrow" {
		t.Errorf("%+v", c)
	}
	if c := build(t, "Camp.Formation.Arrow", Context{TargetBot: "Gorm"}); c.Text != "formation Gorm arrow" {
		t.Errorf("%+v", c)
	}
	for _, bot := range []string{"", "Gorm"} {
		if c := build(t, "Fight.Stay", Context{TargetBot: bot}); c.Text != "action stay" {
			t.Errorf("target %q: %+v", bot, c)
		}
	}
}

// Pull and Pullback add the timer only when the server advertised pull-seconds (PullIntent).
func TestBuildPullTimer(t *testing.T) {
	for _, tc := range []struct {
		name string
		ctx  Context
		want string
	}{
		{"Fight.Pull", Context{}, "action pull"},
		{"Fight.Pull", Context{HasPull: true}, "action pull 10"},
		{"Fight.Pullback", Context{HasPull: true}, "action pullback 3"},
		{"Fight.Pull", Context{HasPull: true, Args: map[string]string{"seconds": "99"}}, "action pull 60"},
		{"Fight.Pull", Context{HasPull: true, Args: map[string]string{"seconds": "0"}}, "action pull 0"},
		{"Fight.Pull", Context{Args: map[string]string{"seconds": "5"}}, "action pull"},
	} {
		if c := build(t, tc.name, tc.ctx); c.Text != tc.want || c.Intent != strings.Fields(tc.want)[1] {
			t.Errorf("%s %+v: %+v, want %q", tc.name, tc.ctx, c, tc.want)
		}
	}
	// The addon rounds to the nearest second (ClampPullSeconds) and reads argument names in any case.
	for in, want := range map[string]string{"2.5": "action pull 3", "2.4": "action pull 2", "-3": "action pull 0"} {
		if c := build(t, "Fight.Pull", Context{HasPull: true, Args: map[string]string{"Seconds": in}}); c.Text != want {
			t.Errorf("seconds %s: %q, want %q", in, c.Text, want)
		}
	}
	if _, err := Build(mustFind(t, "Fight.Pull"), Context{HasPull: true, Args: map[string]string{"seconds": "x"}}); err == nil {
		t.Error("bad seconds accepted")
	}
}

func TestBuildArguments(t *testing.T) {
	c := build(t, "Fight.CCAssign", Context{Args: map[string]string{"mark": "moon", "bot": "Gorm"}})
	if c.Text != "action cc moon Gorm" || c.Intent != "cc moon Gorm" {
		t.Errorf("%+v", c)
	}
	if c := build(t, "Fight.CCClearAll", Context{}); c.Text != "action cc clear" || c.Intent != "cc clear" {
		t.Errorf("%+v", c)
	}
	if c := build(t, "BotPanel.Item.Give", Context{Args: map[string]string{"bot": "Gorm", "bag": "0", "slot": "3"}}); c.Text != "item Gorm give 0 3" {
		t.Errorf("%+v", c)
	}
	for _, bad := range []struct {
		name string
		args map[string]string
	}{
		{"Fight.CCAssign", map[string]string{"mark": "moon"}}, // no bot
		{"Roster.Login", nil},
		{"Fight.Attack", map[string]string{"bot": "Gorm"}}, // takes no arguments
	} {
		if _, err := Build(mustFind(t, bad.name), Context{Args: bad.args}); err == nil {
			t.Errorf("%s %v built", bad.name, bad.args)
		}
	}
}

func TestToggleState(t *testing.T) {
	if got := ToggleState(map[string]string{"A": "aoe=on", "B": "AOE=ON"}, "aoe"); got != "on" {
		t.Error(got)
	}
	if got := ToggleState(map[string]string{"A": "aoe=on", "B": "aoe=off"}, "aoe"); got != "mixed" {
		t.Error(got)
	}
	if got := ToggleState(map[string]string{"A": "move=follow"}, "aoe"); got != "" {
		t.Error(got)
	}
}
