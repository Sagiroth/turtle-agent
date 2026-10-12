# Live checks still open

Things the code review raised that only a running server can settle. Each is a short, one-off run.

## Anticheat as a player account (final review M4)

`Movement::VerifyMovementFlags` returns early for any account above SEC_PLAYER (Movement.cpp:121-123),
so every live run so far (GM level 3) never exercised the flag-based checks.

1. Grep every `CHEAT_TYPE_*` name in libanticheat.hpp:36-59 against the server log after the run, not a subset
   (the earlier grep left out WALL_CLIMB, TELEPORT, TELEPORT_FAR, BAD_ORDER_ACK and MULTIJUMP).
2. Log a SEC_PLAYER account in. Place the character with `.go xyz` from a GM session (a player cannot teleport itself).
3. Run `walk_to`, `face`, then a walk that stops, and read the server log for anticheat lines (FLY_HACK, WATER_WALK,
   ROOT_MOVE, FIXED_Z, ...). Expect none.

## Chat language (final review M5)

`BuildMessageChat` sends every say, party and whisper in LANG_UNIVERSAL. The core accepts it (skill 0,
ChatHandler.cpp:193-203), but a real client speaks its racial language. With one Horde and one Alliance
character in range, check what the other side sees from a non-GM account saying something. Only then decide
whether to send the racial language (and keep Universal for `.` commands).

## Cast result 7 (task 16)

Cast result 7 is mapped to a "wrong faction" text that was never seen live. Trigger it (for example a friendly
spell on an enemy-faction player) and compare the text.

## Falling after `.go xyz` and after a knockback (final review M13)

The agent still simulates no fall onto terrain. After a GM teleport, a walk that changed height or a knockback
landing (assumed back at the starting height), `status.z_unknown` is true and stays so until the server moves the
player itself with a spline on it (fear, charge), or until a reconnect. A teleport keeps it true, because its z is
a guess too. The core sends no other position of its own.

1. Teleport to a Z about 20 yards above the ground, wait, and compare `status.z` (and `z_unknown`) with the ground;
   then walk a few yards and read the server log for OVERSPEED_Z (expect none: walk steps are held to the bound).
   This decides whether a fall step is needed before navmesh walking lands.
2. Knock the character back (a mob or spell with a knockback effect) on flat and on sloped ground. Check the server
   keeps the ack (no "does not match pending change", MovementHandler.cpp:770-776), the landing ends the knockback
   state, there is no OVERSPEED_Z, JUMP_SPEED_CHANGE or SKIPPED_HEARTBEATS line, and whether fall damage follows the
   landing (the ack sets the fall start z to 0, MovementHandler.cpp:791).
3. With feather fall on, knock the character back, and once apply feather fall mid-flight: expect no OVERSPEED_Z
   under the 7 yd/s bound (Movement.cpp:874-881).
