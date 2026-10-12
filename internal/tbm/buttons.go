package tbm

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// Scope says how the addon aims a button at the party or at one bot.
const (
	// ScopeTarget: the command text is the same either way and the server picks the scope from
	// the player's WoW target (the addon never sends a scope): the whole party, or the one
	// owned bot that is targeted (UI.lua targetScope, BotCommands.cpp ResolveDynamicScope).
	ScopeTarget = "target"
	// ScopeFormation: the addon narrows the command text itself, using BotCommand when an
	// owned bot is targeted (Core.lua SetFormation).
	ScopeFormation = "formation"
	// ScopeBot: the command names one bot, from the button's "bot" argument.
	ScopeBot = "bot"
	// ScopeNone: no scope (roster verbs name a character, help and roster are global).
	ScopeNone = "none"
)

// Reply kinds: what the addon waits for after the button.
const (
	// ReplyAck: one TBM:ACTION_ACK or TBM:ACTION_ERR whose intent is Button.Intent.
	ReplyAck = "ack"
	// ReplyText: a plain reply line matching Ok or Fail; the addon then polls the roster
	// (FollowUp) and reconciles from that snapshot.
	ReplyText = "text"
	// ReplyInventory: the TBM:INV_BEGIN ... TBM:INV_END snapshot of the bot, or an ERR.
	ReplyInventory = "inventory"
	// ReplySnapshot: a roster response, ended by its TBM:TRANSPORT trailer.
	ReplySnapshot = "snapshot"
)

// Button is one addon control and the command it sends. The generated table (buttons_gen.go)
// lists them all; Command is the text after ".bot " that both transports carry.
type Button struct {
	Name  string `json:"name"`  // "Fight.AoE": tab, then the label without spaces or marks
	Label string `json:"label"` // the text the addon shows
	Tab   string `json:"tab"`   // Fight, Camp, Party, Roster, Guild, Raid or BotPanel
	// Command is the template: {arg} is required, {arg?} dropped when the argument is absent.
	Command string `json:"command"`
	// BotCommand replaces Command while an owned bot is targeted (ScopeFormation only).
	BotCommand string   `json:"bot_command,omitempty"`
	Args       []string `json:"args,omitempty"`     // required arguments
	Optional   []string `json:"optional,omitempty"` // optional arguments
	Scope      string   `json:"scope"`
	// Toggle names the BOTSTATE field a switch flips ("aoe"); {state} defaults to the opposite
	// of what the bots report (on unless every bot says on).
	Toggle string `json:"toggle,omitempty"`
	// NeedsEnemy: the addon greys the button out without a live enemy target.
	NeedsEnemy bool `json:"needs_enemy,omitempty"`
	// RequiresCap: the addon greys the button out unless the module advertised this TBM:CAPS
	// capability ("flee").
	RequiresCap string `json:"requires_cap,omitempty"`
	// PullSeconds: the addon appends a timer (this default, or the user's setting) when the
	// server advertises the pull-seconds capability.
	PullSeconds int    `json:"pull_seconds,omitempty"`
	Reply       string `json:"reply"`
	// Intent is the intent the ACK/ERR carries; it uses the same placeholders.
	Intent string `json:"intent,omitempty"`
	// Ok and Fail are regular expressions for ReplyText lines.
	Ok   []string `json:"ok,omitempty"`
	Fail []string `json:"fail,omitempty"`
	// Multi: the reply is one ACK per bot with no end marker; collect them until a quiet gap.
	Multi bool `json:"multi,omitempty"`
	// FollowUp: the addon polls the roster about 1.4 s after sending (Comms.lua OnCommandSent).
	FollowUp bool `json:"follow_up,omitempty"`
	// MiniBar: the MiniBar icon sends the same command.
	MiniBar bool   `json:"mini_bar,omitempty"`
	Source  string `json:"source"` // where in the addon the click is handled
}

// Key folds a button name or alias for lookup: lower case, letters and digits only.
func Key(s string) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(unicode.ToLower(r))
		}
	}
	return b.String()
}

// Find returns the button called name ("Fight.AoE", "fight.aoe"); a name without a tab
// matches when exactly one button has that label ("AoE").
func Find(name string) (Button, error) {
	want := Key(name)
	var hits []Button
	for _, b := range Buttons {
		if Key(b.Name) == want {
			return b, nil
		}
		if _, rest, ok := strings.Cut(b.Name, "."); ok && Key(rest) == want {
			hits = append(hits, b)
		}
	}
	switch len(hits) {
	case 0:
		return Button{}, fmt.Errorf("no button %q (tbm_buttons lists them)", name)
	case 1:
		return hits[0], nil
	}
	names := make([]string, len(hits))
	for i, b := range hits {
		names[i] = b.Name
	}
	sort.Strings(names)
	return Button{}, fmt.Errorf("button %q is ambiguous: %s", name, strings.Join(names, ", "))
}

// Expand fills the {arg} placeholders of a template. A whole word "{arg?}" disappears when
// arg is empty; a missing required argument is an error naming it.
func Expand(tmpl string, args map[string]string) (string, error) {
	var out []string
	for _, w := range strings.Fields(tmpl) {
		if name, ok := strings.CutSuffix(strings.TrimPrefix(w, "{"), "?}"); ok && strings.HasPrefix(w, "{") {
			if v := strings.TrimSpace(args[name]); v != "" {
				out = append(out, v)
			}
			continue
		}
		for {
			i := strings.IndexByte(w, '{')
			j := strings.IndexByte(w, '}')
			if i < 0 || j < i {
				break
			}
			name := w[i+1 : j]
			v := strings.TrimSpace(args[name])
			if v == "" {
				return "", fmt.Errorf("missing argument %q", name)
			}
			w = w[:i] + v + w[j+1:]
		}
		out = append(out, w)
	}
	return strings.Join(out, " "), nil
}

// ParseBotState splits a TBM:BOTSTATE value ("move=follow,loot=on") into lower-case keys and
// values, as Comms.lua parseStateFields does.
func ParseBotState(s string) map[string]string {
	m := map[string]string{}
	for _, pair := range strings.Split(s, ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(pair), "=")
		k, v = strings.ToLower(strings.TrimSpace(k)), strings.ToLower(strings.TrimSpace(v))
		if ok && k != "" && !strings.ContainsAny(v, " \t") {
			m[k] = v
		}
	}
	return m
}

// ToggleState is what the bots in states (name -> BOTSTATE value) say about key: "on" or
// "off" when they all agree, "mixed" when they differ, "" when none says (Roster/BotPanel.lua
// GetPartyToggleState).
func ToggleState(states map[string]string, key string) string {
	on, off := false, false
	for _, s := range states {
		switch ParseBotState(s)[key] {
		case "on":
			on = true
		case "off":
			off = true
		}
	}
	switch {
	case on && off:
		return "mixed"
	case on:
		return "on"
	case off:
		return "off"
	}
	return ""
}

// NextToggle is the state a click sets: off when the bots are on, else on (SendPartyToggle).
func NextToggle(current string) string {
	if current == "on" {
		return "off"
	}
	return "on"
}

// Command is a button resolved to the text the addon sends.
type Command struct {
	Button Button
	Text   string // the command, without ".bot "
	Intent string // the ACK/ERR intent to wait for, when the button has one
	Args   map[string]string
}

// Context is what the addon looks at when a button is clicked.
type Context struct {
	Args      map[string]string
	TargetBot string            // the targeted owned bot, or ""
	BotStates map[string]string // latest TBM:BOTSTATE values by bot name
	HasPull   bool              // the server advertised the pull-seconds capability
}

// Build resolves a button into the command the addon sends right now: it narrows a
// formation to the targeted bot, flips a switch from the reported state, and appends the
// pull timer the server accepts.
func Build(b Button, c Context) (Command, error) {
	args := map[string]string{}
	for k, v := range c.Args {
		args[strings.ToLower(k)] = v
	}
	known := map[string]bool{"state": b.Toggle != "", "seconds": b.PullSeconds > 0}
	for _, a := range b.Args {
		known[a] = true
	}
	for _, a := range b.Optional {
		known[a] = true
	}
	for k := range args {
		if !known[k] {
			return Command{}, fmt.Errorf("%s takes no argument %q", b.Name, k)
		}
	}
	tmpl := b.Command
	if b.Scope == ScopeFormation && c.TargetBot != "" {
		tmpl = b.BotCommand
		args["bot"] = c.TargetBot
	}
	if b.Toggle != "" {
		switch st := strings.ToLower(args["state"]); st {
		case "", "toggle":
			key := b.Toggle
			var cur string
			if bot := args["bot"]; bot != "" {
				for name, v := range c.BotStates {
					if strings.EqualFold(name, bot) {
						cur = ParseBotState(v)[key]
					}
				}
			} else {
				cur = ToggleState(c.BotStates, key)
			}
			args["state"] = NextToggle(cur)
		case "on", "off":
			args["state"] = st
		default:
			return Command{}, fmt.Errorf("%s: state must be on, off or toggle, got %q", b.Name, st)
		}
	}
	if b.PullSeconds > 0 {
		// ClampPullSeconds: tonumber, round to the nearest second, 0-60.
		secs := args["seconds"]
		args["seconds"] = ""
		if c.HasPull {
			n := float64(b.PullSeconds)
			if secs != "" {
				var err error
				if n, err = strconv.ParseFloat(secs, 64); err != nil || math.IsNaN(n) {
					return Command{}, fmt.Errorf("%s: seconds %q is not a number", b.Name, secs)
				}
			}
			args["seconds"] = strconv.Itoa(int(min(max(math.Floor(n+0.5), 0), 60)))
		}
	}
	text, err := Expand(tmpl, args)
	if err != nil {
		return Command{}, fmt.Errorf("%s: %w", b.Name, err)
	}
	intent := ""
	if b.Intent != "" {
		if intent, err = Expand(b.Intent, args); err != nil {
			return Command{}, fmt.Errorf("%s: %w", b.Name, err)
		}
	}
	return Command{Button: b, Text: text, Intent: intent, Args: args}, nil
}
