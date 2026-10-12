# TortoiseBots vision

turtle-agent was built to test [TortoiseBots](https://github.com/Sagiroth/TortoiseBots). Anyone who uses it for TortoiseBots,
person or AI agent, judges what they see and what they change against the maintainer's vision below. It is copied
verbatim from the maintainer (2026-10-12); the TortoiseBots repository is the source if the two ever differ.

---

## TortoiseBots: my vision (context for contributors and their agents)

### The one-sentence version
TortoiseBots should make Tortoise WoW feel like a living, populated vanilla server for a player who plays alone. The player levels and gears up at their own pace, and bots fill the world and their group the way real players would.

### The player experience I'm building for
The player is a solo player on their own realm. Bots are there to complete their experience, not to replace playing the game.
Progression should feel slow and earned, like real vanilla. The loop I want:
1. Level up in a world full of other "players" (bots) who are levelling too.
2. Hire a random companion, or bring my own alts, to run dungeons.
3. Gear them up in those dungeons. They get stronger, I get stronger, and we clear harder content together.
4. Over time, unlock stronger content and stronger companions (see the hire gear ladder, #490).

Nothing should feel handed out. Gear, gold and power come from playing.

### Bots should feel like real players
- Organic, not synthetic. A bot should look and behave like a human player: it quests, grinds, travels by foot and flight path, uses towns, vendors, trainers and the AH, and moves on from zones it has outgrown.
- Fresh realm feel. Random pool bots start at level 1 in their starting zones and spread over the world as they level. Teleports exist only as a safety net (stuck recovery), never as the normal way to move.
- Always alive. Bots are active all the time. I don't want bots that freeze and wake up artificially.
- Behaviour standard: mod-playerbots (AzerothCore) is the gold standard for how a bot should play. When something can be ported from it and adapted to 1.12, prefer that over inventing new logic or keeping older cmangos-lineage code.

### The economy must be real
- Items on the AH are looted or crafted by bots. There is no synthetic supply, no seeded listings and no fake buyers.
- No money injection. Bots earn gold by grinding, selling and questing. If a bot can't afford its trainer spells, that's a bug in its economy, not a reason to give it gold.
- No free gear on level-up. Gear comes from drops, quests, crafting and vendors.

Deliberate, limited exceptions, where pure realism would just break bots:
- Ammo and reagents are unlimited for bots (the item cheat).
- Anything a behaviour needs in order to work at all (profession tools, Lockpicking, Thieves' Tools, encounter items like Hourglass Sand) is auto-granted, so the feature doesn't silently never fire.
- The raised bot XP rate and the bot-only green and blue drop boost are temporary helpers while the 1-60 loop is being fixed. The goal is to reduce them once bots level and gear up naturally.

### The vision is the default, not the only way to play
Everything above is the leading way of playing: the defaults ship that experience. But players are different, and I want them to be able to shape the experience for themselves.
- Things that don't fit the vision (faster levelling, gold or gear handouts, synthetic AH supply, teleport travel, activity throttling, all hire tiers unlocked at once, and so on) are welcome as long as they sit behind a config option.
- We already work this way in many places: XP and loot rates for bots, the AH market and synthetic supply switches, activity throttling, the hire gear ladder options (account-wide unlocks, all tiers at once, Turtle content optional), opt-in dungeon follow holds.
- Rule of thumb: default = the vision; config = the player's choice. A config option should be clearly named, documented in the config file, and must not change anything for players who leave it at its default.

### The kinds of bots
| Kind | What it is | Persistence |
| --- | --- | --- |
| Pool bots | The living world: random characters levelling from 1 | Permanent, part of the world |
| Own alts | Characters the player created on their own account | Permanent, fully the player's |
| Hired companions | Temporary mercenaries hired for a session | Deleted after dismissal, party leave or logout grace. Never released into the pool, because that would pollute the organic world with hire-level gear |

Future (roadmap #289): claim a world bot into your guild (#489) and gear and progress your own team from there. Unlocking stronger hires goes through content: quest and raid clears raise what a hired bot can wear (#490). Bots copy their master's consumables in a way that fits their role (#491). World buffs are unlocked once and can then be bought (#492).

### Player control
- Explicit player orders always beat automation. If the player says stay, attack or CC, the bot does it.
- New automation that acts on the player's own bots goes behind a toggle. World-life behaviour (pool bots) defaults to the organic vision above, with a config flag where it makes sense.

### Performance
- Defaults must run on an average PC. The bot count is the player's knob: weak hardware runs 100-200 bots, strong hardware 2000+. I don't chase per-device tuning.
- Watch the per-tick cost of new logic: no world scans or pathfinding every tick.

### Ideas on the list (not started)
- PvP and duel logic: bots that duel well and are genuinely hard to beat.
- Porting the bots to other vanilla and TBC cores, much later.

### How to judge a change (for agents)
Ask these questions:
1. Does it make bots feel more like real players, or more synthetic?
2. Does it keep progression earned? No free gold, gear or items outside the exceptions above.
3. If it doesn't fit the vision but players might want it: is it behind a config option, off by default?
4. Is it a bug fix (the bot fails at what it is supposed to do), or a behaviour change (it plays differently than today)? Bug fixes are welcome directly. Behaviour changes need my decision, so describe what changes in game and ask.
5. Does it respect player control and stay cheap per tick?
6. Is there a mod-playerbots behaviour to port instead?

When you find bugs or improvements, open an issue or PR with a plain-language "what you see in game / what changes in game" section. Your current PRs already do this well.
