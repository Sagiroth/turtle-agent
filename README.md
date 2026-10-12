# turtle-agent

[![test](https://github.com/pjerra/turtle-agent/actions/workflows/test.yml/badge.svg)](https://github.com/pjerra/turtle-agent/actions/workflows/test.yml)

A headless World of Warcraft client for **vanilla 1.12 / Turtle WoW 1.18.1** servers (login build 7272, world build 5875).
One process logs one character in, with no game window, and exposes it over a local JSON-RPC API. Use it to drive a
test character from scripts or AI agents: chat and GM commands, party, units around you, target, attack, cast, short
walks and GM teleports, and an ordered event log of chat and combat.

It was built to test server-side bots ([TortoiseBots](https://github.com/Sagiroth/TortoiseBots)) on a private test
server: log in, hire or invite bots, start a fight, then read back the combat log and what each bot did.

> Use it only on servers you run or are allowed to test on. It has no Warden emulation and does no rendering,
> pathfinding or inventory management.

## Quick start

Requirements: Go 1.27.1 or newer (see `go.mod`), and a server whose realmd and world ports you can reach.

```sh
make build                      # bin/turtle-agent
make test                       # gofmt check, go vet, go test -race
```

Create the settings file (default `~/.config/turtle-agent/settings.json`, mode **0600**; the agent refuses a file other
users can read):

```json
{
  "realmd": "127.0.0.1:3724",
  "accounts": { "MYACCOUNT": "my-password" },
  "rpc": "127.0.0.1:8765",
  "spot": { "map": 1, "x": -1287.0, "y": -2379.6, "z": 91.7 },
  "test_spell": 52524
}
```

Only `realmd` and `accounts` are required. `rpc` must be a loopback address. `realm` and `world_addr` override what the
realm list returns; `bots`, `spot` and `test_spell` are optional hints for scripts.

Log a character in and keep it online with the RPC server running:

```sh
bin/turtle-agent login --rpc 127.0.0.1:8765 MYACCOUNT MyCharacter
```

When the settings file holds exactly one account, leave the account name off (`login MyCharacter`): it then stays out of `ps` and shell history.

Ctrl-C logs out cleanly. If the server refuses the logout (in combat), the agent retries for 35 s; a second Ctrl-C quits.

## CLI

```
turtle-agent login [--config FILE] [--capture FILE] [--stay DURATION] [--rpc ADDR] [<account>] <character>
turtle-agent fleet --config FILE [--settings FILE] [--stagger DURATION] [--wait DURATION]
turtle-agent scrub-check [--config FILE] <capture>...
turtle-agent capture-stats <capture>
turtle-agent version
```

| Exit code | Meaning |
| ---: | --- |
| 0 | OK |
| 2 | bad flags, arguments or settings file (also: RPC address busy or not loopback) |
| 3 | realmd rejected the login |
| 4 | realm missing or offline |
| 5 | world server rejected the session or the character |
| 6 | the server closed the connection |
| 7 | `scrub-check` found a secret in a capture |
| 8 | the server kept refusing the logout |
| 9 | `fleet`: a child failed, or not every character reached the world in time |

`--capture` records every decrypted packet to a file (opened exclusively, mode 0600). The auth session body and
Warden packets are never written, and any packet holding the account name, password or session key is redacted.
`scrub-check` proves a capture is clean before you share or commit it.

## JSON-RPC API

JSON-RPC 2.0 over HTTP POST on the loopback address (`Content-Type: application/json`, no batches). Requests from a
browser (`Origin` header) and other `Host` names are refused.

| Method | Params | Result |
| --- | --- | --- |
| `status` | | state, character, map, x/y/z, health, power, target, last event seq |
| `chat` | `type` (say, party, yell, emote, guild, raid, whisper), `text`, `to` | |
| `gm` | `text` (a `.`-command; the dot is added if missing) | |
| `events` | `since`, `kinds`, `limit`, `wait_ms` | page: `events`, `next`, `more`, `dropped` |
| `chat_log` | `since`, `limit` | chat and system events only |
| `invite` / `accept_invite` / `leave_group` | `name` / – / – | |
| `party` | | `in_group`, `leader`, `members[]` with health and online state, `marks` (raid icon name to guid) |
| `units` | `limit`, `name` | nearby units, nearest first: guid, name, level, health, dead, hostile, in_party; `hostile` means an attackable-looking creature (no NPC flags, not player-controlled), not a faction check: it counts neutral critters and never an enemy-faction player |
| `target` | `guid` or `name` (prefers living units) | |
| `attack` / `stop_attack` | | |
| `cast` | `spell_id`, `target` | |
| `face` | `guid` | |
| `walk_to` | `x`, `y`, `z` (short, straight-line walks: Z is interpolated and terrain is ignored, so keep walks short and on flat ground; after a `teleport` to a Z above the ground the character does not fall) | |
| `teleport` | `x`, `y`, `z`, `map` (a GM `.go xyz`; needs a GM account) | |
| `mark` | `guid` or `name`, `icon` (skull, cross, square, moon, triangle, diamond, circle, star, clear) | `icon`, `guid`; waits for the server's echo (group leader or assistant only) |
| `logout` | | |

Events have `seq`, `time`, `kind`, `source`, `target`, `spell_id`, `amount`, `crit`, `text`, `party` and `data`.
Kinds cover chat, system, whispers, melee, spell damage, periodic ticks, heals, energize, spell start/go/miss/fail,
auras, deaths, kills, party changes, raid icons (`raid_mark`) and session state. Cursors are exclusive; `dropped` says older events were evicted.

Text the server echoes back (for example the account name in a GM reply) is redacted before it reaches events, logs
or replies.

## Fleet: several players at once

`turtle-agent fleet` starts one `login` process per entry of a fleet file, for example a 5-player dungeon party or both
battleground sides, each with its own RPC port.

```json
{
  "members": [
    { "account": "TANKACC",   "character": "Tankone", "rpc": "127.0.0.1:8771" },
    { "account": "HEALACC",   "character": "Healtwo", "rpc": "127.0.0.1:8772" }
  ]
}
```

```sh
bin/turtle-agent fleet --config fleet.json            # settings from ~/.config/turtle-agent/settings.json
bin/turtle-agent fleet --config fleet.json --settings other.json --stagger 3s --wait 3m
```

- `account` is a key into the settings file's `accounts`; passwords stay there, are read by each child, and never appear in the
  fleet file, on a command line or in output. Every account in the fleet file must exist in the settings file.
- `rpc` must be a loopback address with a fixed port (no port 0), and ports and characters must be distinct. The
  supervisor polls each port's `status` method.
- Children start `--stagger` apart (default 2 s, realmd throttles logins). When all are `in_world` the supervisor prints a
  table of character, RPC address and state; each child's log goes to stderr with a `[Character]` prefix.
- Ctrl-C (or SIGTERM) sends one interrupt to every child so they all log out cleanly; a child that has not exited after 60 s
  is killed.
- A child that exits non-zero, or a fleet that is not fully in world after `--wait` (default 2 m), stops the others and
  makes `fleet` exit 9. It exits 0 only when every child exited 0.

## Python

The companion test harness (wow-play-harness, not public yet) has a `HeadlessClient` that wraps this API
with the same calls as its real-client backend (`chat`, `gm`, `state`, `tail_chat`, `tail_combat_log`,
`fight_report`, `live_lines`), plus MCP tools and a scripted fight (`tools/headless_mvp.py`).

## Layout

| Package | What it does |
| --- | --- |
| `internal/auth` | SRP6 and realmd: logon challenge/proof, realm list |
| `internal/worldconn` | TCP framing, the 1.12 header cipher, auth session with the addon block |
| `internal/session` | single-owner session loop: login, ping, chat, names, party, world, actions, combat, logout |
| `internal/game` | packet codecs for chat, queries, groups, actions and the combat log |
| `internal/objects` | update-object parser and the object store |
| `internal/events` | ordered event log with cursors |
| `internal/capture` | packet capture files and the secret scan |
| `internal/rpc`, `internal/netutil` | loopback JSON-RPC server |
| `cmd/turtle-agent` | the CLI (`login`, `fleet`, capture tools) |

Design and implementation plan: `docs/superpowers/`.

## Licence

MIT, see [LICENSE](LICENSE). Parts are ported from [agent-wow](https://github.com/agent-wow/agent-wow) (MIT); see
[NOTICE](NOTICE). World of Warcraft is a trademark of Blizzard Entertainment; this project is not affiliated with it.
