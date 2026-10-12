-- TBM contract driver: replay server "TBM:" lines through the real TortoiseBotsManager
-- addon (Sagiroth/TortoiseBotsManager, MIT) and compare the addon's state with what the
-- lines say.
--
--   lua5.1 contract.lua <addon dir> <lines file>
--
-- The lines file has one record per line (blank lines and "#" comments are skipped):
--   system<TAB><text>                       a CHAT_MSG_SYSTEM line
--   addon<TAB><CHANNEL><TAB><text>          a CHAT_MSG_ADDON line with prefix TBM
-- tools/tbmlines writes this from an events file. Prints one line per mismatch and exits 1
-- if there is any. Each mismatch is tagged with its cause:
--   [server]   the line breaks the protocol format (a malformed pair the addon skips silently
--              is one of these)
--   [addon]    the addon refuses a valid line or stores something else than it says
--   [contract] this driver has no expectation for that verb
-- The expectations below are an independent reading of the protocol (commands/BotCommands.cpp
-- in TortoiseBots, and the comment block above TB.ParseBotPanelMessage in the addon's
-- Comms.lua), not a copy of the addon's parser.

local addonDir, linesFile = arg[1], arg[2]
if not addonDir or not linesFile then
    io.stderr:write("usage: lua5.1 contract.lua <addon dir> <lines file>\n")
    os.exit(2)
end
local scriptDir = (arg[0]:match("^(.*)/[^/]*$")) or "."

local H = dofile(scriptDir .. "/mock.lua")

-- Load the addon the way the client does: the .toc file order, then ADDON_LOADED.
local toc = assert(io.open(addonDir .. "/TortoiseBotsManager.toc", "r"), "addon .toc not found in " .. addonDir)
for line in toc:lines() do
    line = line:gsub("\r", "")
    if not line:find("^#") and line:find("%.lua%s*$") then
        dofile(addonDir .. "/" .. line:gsub("%s+$", ""))
    end
end
toc:close()

local TB = assert(TortoiseBots, "addon did not create TortoiseBots")
local coreFrame = assert(H.frames["TortoiseBotsManagerCoreFrame"], "core lifecycle frame missing")

local function fire(frame, ev, a1, a2, a3, a4)
    this, event, arg1, arg2, arg3, arg4 = frame, ev, a1, a2, a3, a4 -- 1.12 handlers read globals
    return pcall(frame.scripts.OnEvent, frame)
end
local ok, err = fire(coreFrame, "ADDON_LOADED", "TortoiseBotsManager")
assert(ok, err)
local commsFrame = assert(H.frames["TortoiseBotsManagerCommsFrame"], "comms frame missing (InitComms not run)")

-- Record what the addon's parsers answered for the current line.
local seen
local function wrap(name, key)
    local orig = TB[name]
    assert(orig, "addon has no " .. name)
    TB[name] = function(...)
        local handled, kind, data = orig(...)
        seen[key] = { handled = handled, kind = kind, data = data }
        return handled, kind, data
    end
end
wrap("ParseRosterMessage", "roster")
wrap("ParseActionMessage", "action")
wrap("ParseBotPanelMessage", "panel")

-- ── helpers ─────────────────────────────────────────────────────────────────
local function split(text, sep)
    local out, start = {}, 1
    while true do
        local at = string.find(text, sep, start, true)
        if not at then
            out[#out + 1] = string.sub(text, start)
            return out
        end
        out[#out + 1] = string.sub(text, start, at - 1)
        start = at + 1
    end
end

local function trim(s) return (s:gsub("^%s+", ""):gsub("%s+$", "")) end
local function normName(s)
    s = trim(s):lower()
    return (s:gsub("^%l", string.upper))
end
local function isnum(s) return s ~= nil and s:find("^%d+$") ~= nil end
local function show(v) return v == nil and "nil" or tostring(v) end

local MARKS = { skull = 1, cross = 1, square = 1, moon = 1, triangle = 1, diamond = 1, circle = 1, star = 1 }
local ONLINE = { online = 1, ["in world"] = 1, in_world = 1 }

local mismatches, lineNo, current = {}, 0, nil
-- Every mismatch says whose fault it is:
--   [server]   the line breaks the protocol format (bad count, field, value)
--   [addon]    the addon refuses a valid line or stores something other than what it says
--   [contract] this driver has no expectation for the line (a protocol change to model)
local function bad(tag, fmt, ...)
    local msg = string.format("[%s] ", tag) .. string.format(fmt, ...)
    local shown = current or "?"
    if #shown > 140 then shown = shown:sub(1, 140) .. "..." end
    mismatches[#mismatches + 1] = string.format("%s:%d: %s\n      %s", linesFile, lineNo, shown, msg)
end
local function badS(...) return bad("server", ...) end
local function badA(...) return bad("addon", ...) end
local function badC(...) return bad("contract", ...) end
local function expect(what, got, want)
    if got ~= want then badA("%s: addon has %s, server meant %s", what, show(got), show(want)) end
end
local function expectMap(what, got, want)
    if type(got) ~= "table" then
        badA("%s: addon has nothing, server meant a state", what)
        return
    end
    for k, v in pairs(want) do
        if got[k] ~= v then badA("%s.%s: addon has %s, server meant %s", what, k, show(got[k]), show(v)) end
    end
    for k, v in pairs(got) do
        if want[k] == nil then badA("%s.%s: addon has %s, server sent no such key", what, k, show(v)) end
    end
end
local function setEq(what, got, want)
    for k in pairs(want) do
        if not got[k] then badA("%s: missing %s", what, k) end
    end
    for k in pairs(got) do
        if not want[k] then badA("%s: unexpected %s", what, k) end
    end
end

-- ── expectations per verb ───────────────────────────────────────────────────
-- Each handler gets the fields after "TBM:<VERB>|", the raw text, and the oracle model O.
-- It first validates the line against the protocol (a violation is a mismatch of its own),
-- then, where a block ends or a single line stands alone, compares the addon's state.
local O = { roster = nil, cc = nil, claimed = nil, bot = nil, inv = nil, rosterRows = nil }
local V = {}

function V.VERSION(f, rest)
    expect("serverVersion", TB.serverVersion, rest:match("^(%S+)"))
end

function V.CAPS(f, rest)
    local want = {}
    for item in rest:gmatch("[^,%s|]+") do want[item:lower()] = true end
    local got = {}
    for k in pairs(TB._serverCaps or {}) do got[k] = true end
    setEq("capabilities", got, want)
    for k in pairs(want) do
        if not TB.HasServerCapability(k) then badA("HasServerCapability(%s) is false", k) end
    end
end

function V.TRANSPORT(f, rest)
    local want = trim(rest):lower()
    if want ~= "party" and want ~= "none" then
        badS("the server sent transport %q, protocol allows party or none", want)
        return
    end
    expect("addon command channel", TB.AddonCommandChannel(), want == "party" and "PARTY" or nil)
end

function V.ROSTER_BEGIN(f)
    O.roster = { expected = tonumber(f[1]), rows = {}, names = {}, ok = #f == 1 and isnum(f[1]) }
    if not O.roster.ok then badS("malformed ROSTER_BEGIN") end
end

function V.ROSTER(f)
    local b = O.roster
    if not b then badS("ROSTER row outside ROSTER_BEGIN/END") return end
    local guid, name, class, state, group, loc = f[1], f[2], f[3], f[4], f[5], f[6]
    -- the addon takes 6 fields, or 7 with a CC mark (Roster.lua), and any non-empty guid
    if (#f ~= 6 and #f ~= 7) or trim(guid or "") == "" or trim(name or "") == "" or not isnum(class)
        or trim(state or "") == "" or (group ~= "0" and group ~= "1") or trim(loc or "") == "" then
        badS("ROSTER row breaks the protocol (guid|name|class|state|grouped|location[|ccmark])")
        b.ok = false
        return
    end
    local n = normName(name)
    if b.names[n] then
        badS("duplicate ROSTER row for %s", n)
        b.ok = false
        return
    end
    b.names[n] = true
    b.rows[#b.rows + 1] = { guid = guid, name = n, class = tonumber(class), state = state:lower(),
        group = group == "1", loc = loc, hasMark = #f == 7, mark = f[7] and trim(f[7]):lower() }
end

function V.ROSTER_END(f)
    local b = O.roster
    O.roster = nil
    if not b or not b.ok then return end
    if #b.rows ~= b.expected then
        badS("ROSTER_BEGIN said %d rows, %d arrived", b.expected, #b.rows)
        return
    end
    O.rosterRows = b.rows
    expect("HasRosterSnapshot", TB.HasRosterSnapshot(), true)
    expect("GetRosterCount", TB.GetRosterCount(), #b.rows)
    local want, got = {}, {}
    for _, r in ipairs(b.rows) do
        want[r.name] = true
        local e = TB.GetRosterEntry(r.name)
        if not e then
            badA("roster row %s was not stored", r.name)
        else
            local w = "roster " .. r.name
            expect(w .. " guid", e.guid, r.guid)
            expect(w .. " classId", e.classId, r.class)
            expect(w .. " serverState", e.serverState, r.state)
            expect(w .. " group", e.group, r.group)
            expect(w .. " location", e.location, r.loc ~= "-" and r.loc or nil)
            if r.hasMark then expect(w .. " cc mark", e.ccMark, MARKS[r.mark or ""] and r.mark or nil) end
        end
    end
    for _, row in ipairs(TB.GetDisplayRows("")) do got[row.name] = true end
    setEq("roster rows shown", got, want)
end

function V.ROSTER_ERROR(f)
    -- the roster response is over; the addon keeps the error for the UI
    if #f ~= 2 then badS("ROSTER_ERROR needs code|message") return end
    local e = TB.GetRosterSnapshotError()
    if not e then
        badA("ROSTER_ERROR left no GetRosterSnapshotError")
    else
        expect("roster error code", e.code, trim(f[1]))
    end
end

function V.CC_ASSIGN_BEGIN(f)
    O.cc = { expected = tonumber(f[1]), rows = {}, names = {}, ok = #f == 1 and isnum(f[1]) }
    if not O.cc.ok then badS("malformed CC_ASSIGN_BEGIN") end
end

function V.CC_ASSIGN(f)
    local b = O.cc
    if not b then badS("CC_ASSIGN row outside CC_ASSIGN_BEGIN/END") return end
    local mark = f[2] and f[2]:lower()
    if #f ~= 2 or trim(f[1]) == "" or not MARKS[mark or ""] then
        badS("CC_ASSIGN row is not name|<raid mark> (got %q)", table.concat(f, "|"))
        b.ok = false
        return
    end
    local n = normName(f[1])
    if b.names[n] then
        badS("duplicate CC_ASSIGN row for %s", n)
        b.ok = false
        return
    end
    b.names[n] = true
    b.rows[#b.rows + 1] = { name = n, mark = mark }
end

function V.CC_ASSIGN_END(f)
    local b = O.cc
    O.cc = nil
    if not b or not b.ok then return end
    if #b.rows ~= b.expected then
        badS("CC_ASSIGN_BEGIN said %d rows, %d arrived", b.expected, #b.rows)
        return
    end
    for _, r in ipairs(b.rows) do
        expect("CC mark of " .. r.name, TB.GetCcAssignment(r.name), r.mark)
    end
    -- a live bot of the last roster that the snapshot does not name must carry no mark
    for _, r in ipairs(O.rosterRows or {}) do
        if ONLINE[r.state] and not b.names[r.name] then
            expect("CC mark of " .. r.name .. " (absent from CC_ASSIGN)", TB.GetCcAssignment(r.name), nil)
        end
    end
end

function V.CLAIMED_BEGIN(f)
    O.claimed = { expected = tonumber(f[1]), rows = {}, names = {}, ok = #f == 1 and isnum(f[1]) }
    if not O.claimed.ok then badS("malformed CLAIMED_BEGIN") end
end

function V.CLAIMED(f)
    local b = O.claimed
    if not b then badS("CLAIMED row outside CLAIMED_BEGIN/END") return end
    if #f ~= 8 or trim(f[1]) == "" or trim(f[2]) == "" or not isnum(f[3]) or not isnum(f[4])
        or trim(f[5]) == "" or (f[6] ~= "0" and f[6] ~= "1") or (f[8] ~= "0" and f[8] ~= "1") then
        badS("CLAIMED row breaks the 8-field protocol (guid|name|class|level|state|grouped|location|gearLocked)")
        b.ok = false
        return
    end
    local n = normName(f[2])
    if b.names[n] then
        badS("duplicate CLAIMED row for %s", n)
        b.ok = false
        return
    end
    b.names[n] = true
    b.rows[#b.rows + 1] = { guid = f[1], name = n, class = tonumber(f[3]), level = tonumber(f[4]),
        state = f[5]:lower(), group = f[6] == "1", loc = f[7], locked = f[8] == "1" }
end

function V.CLAIMED_END(f)
    local b = O.claimed
    O.claimed = nil
    if not b or not b.ok then return end
    if #b.rows ~= b.expected then
        badS("CLAIMED_BEGIN said %d rows, %d arrived", b.expected, #b.rows)
        return
    end
    expect("claimed bots stored", #TB.GetClaimedBots(""), #b.rows)
    for _, r in ipairs(b.rows) do
        local e = TB.GetClaimedEntry(r.name)
        if not e then
            badA("claimed row %s was not stored", r.name)
        else
            local w = "claimed " .. r.name
            expect(w .. " guid", e.guid, r.guid)
            expect(w .. " classId", e.classId, r.class)
            expect(w .. " level", e.level, r.level)
            expect(w .. " serverState", e.serverState, r.state)
            expect(w .. " group", e.group, r.group)
            expect(w .. " gearLocked", e.gearLocked, r.locked)
            expect(w .. " location", e.location, r.loc ~= "-" and r.loc or nil)
        end
    end
end

-- BOTSTATE: name|key=value,key=value,...
local function botStateRow(f)
    if #f ~= 2 or trim(f[1]) == "" then
        badS("BOTSTATE is not name|key=value,... (got %q)", table.concat(f, "|"))
        return nil
    end
    local want = {}
    for pair in f[2]:gmatch("[^,]+") do
        local k, v = pair:match("^%s*([%w_]+)=(%S*)%s*$")
        if not k then
            badS("BOTSTATE pair %q of %s is not key=value (the addon skips it silently)", pair, f[1])
            return nil
        end
        want[k:lower()] = v:lower()
    end
    return { name = normName(f[1]), state = want }
end

function V.BOTSTATE_BEGIN(f)
    O.bot = { expected = tonumber(f[1]), rows = {}, ok = #f == 1 and isnum(f[1]) }
    if not O.bot.ok then badS("malformed BOTSTATE_BEGIN") end
end

function V.BOTSTATE(f)
    local row = botStateRow(f)
    if O.bot then
        if row then O.bot.rows[#O.bot.rows + 1] = row else O.bot.ok = false end
    elseif row then
        expectMap("bot state " .. row.name, TB.GetBotState(row.name), row.state)
    end
end

function V.BOTSTATE_END(f)
    local b = O.bot
    O.bot = nil
    if not b or not b.ok then return end
    if #b.rows ~= b.expected then
        badS("BOTSTATE_BEGIN said %d rows, %d arrived", b.expected, #b.rows)
        return
    end
    local want, got = {}, {}
    for _, r in ipairs(b.rows) do
        want[r.name] = true
        expectMap("bot state " .. r.name, TB.GetBotState(r.name), r.state)
    end
    for name in pairs(TB.GetBotStates()) do got[name] = true end
    setEq("bots with state", got, want)
end

-- INV_*: one snapshot per INV_BEGIN..INV_END
function V.INV_BEGIN(f)
    O.inv = { name = normName(f[1] or ""), money = tonumber(f[2]), eq = {}, items = {},
        ok = #f == 2 and trim(f[1]) ~= "" and isnum(f[2]) }
    if not O.inv.ok then badS("malformed INV_BEGIN (want bot|copper)") end
end

function V.INV_EQ(f)
    local b = O.inv
    if not b then badS("INV_EQ outside INV_BEGIN/END") return end
    if #f ~= 4 or not (isnum(f[1]) and isnum(f[2]) and isnum(f[3]) and isnum(f[4])) then
        badS("INV_EQ is not slot|item|durability|max")
        b.ok = false
        return
    end
    b.eq[tonumber(f[1])] = { id = tonumber(f[2]), durability = tonumber(f[3]), maxDurability = tonumber(f[4]) }
end

function V.INV_ITEM(f)
    local b = O.inv
    if not b then badS("INV_ITEM outside INV_BEGIN/END") return end
    if #f ~= 5 or not (isnum(f[1]) and isnum(f[2]) and isnum(f[3]) and isnum(f[4])) then
        badS("INV_ITEM is not bag|slot|item|count|flags")
        b.ok = false
        return
    end
    local fl = f[5]
    b.items[#b.items + 1] = { bag = tonumber(f[1]), slot = tonumber(f[2]), id = tonumber(f[3]),
        count = tonumber(f[4]), equippable = fl:find("e", 1, true) ~= nil,
        upgrade = fl:find("u", 1, true) ~= nil, noTrade = fl:find("b", 1, true) ~= nil }
end

function V.INV_END(f)
    local b = O.inv
    O.inv = nil
    if not b or not b.ok then return end
    if #f ~= 3 or normName(f[1]) ~= b.name or not isnum(f[2]) or not isnum(f[3]) then
        badS("INV_END is not bot|free|total for %s", b.name)
        return
    end
    local inv = TB.GetInventory(b.name)
    if not inv then
        badA("inventory of %s was not stored", b.name)
        return
    end
    local w = "inventory " .. b.name
    expect(w .. " money", inv.money, b.money)
    expect(w .. " free", inv.free, tonumber(f[2]))
    expect(w .. " total", inv.total, tonumber(f[3]))
    for slot, e in pairs(b.eq) do
        local g = inv.equipped and inv.equipped[slot]
        if not g then
            badA("%s: equipped slot %d missing", w, slot)
        else
            expect(w .. " slot " .. slot .. " item", g.id, e.id)
            expect(w .. " slot " .. slot .. " durability", g.durability, e.durability)
            expect(w .. " slot " .. slot .. " max durability", g.maxDurability, e.maxDurability)
        end
    end
    for slot in pairs(inv.equipped or {}) do
        if not b.eq[slot] then badA("%s: equipped slot %d not sent", w, slot) end
    end
    expect(w .. " item count", #(inv.items or {}), #b.items)
    for i, e in ipairs(b.items) do
        local g = (inv.items or {})[i]
        if g then
            for _, k in ipairs({ "bag", "slot", "id", "count", "equippable", "upgrade", "noTrade" }) do
                expect(w .. " item " .. i .. " " .. k, g[k], e[k])
            end
        end
    end
end

function V.ACTION_ACK(f)
    local intent, scope, count, executor = f[1], f[2], f[3], f[4]
    local problem
    if #f ~= 4 then
        problem = string.format("%d fields, want intent|scope|count|executor (a '|' inside a field?)", #f)
    elseif trim(intent) == "" then
        problem = "empty intent"
    elseif not (scope == "party" or (scope:sub(1, 4) == "bot:" and trim(scope:sub(5)) ~= "")) then
        problem = string.format("scope %q is neither 'party' nor 'bot:<name>'", scope)
    elseif not isnum(count) then
        problem = string.format("count %q is not a number", count)
    elseif trim(executor) == "" then
        problem = "empty executor (the server sends '-' for none)"
    end
    if problem then
        badS("ACTION_ACK breaks the protocol, the addon shows 'Malformed action response': %s", problem)
        return
    end
    local a = TB.lastActionAck
    if not a then
        badA("addon kept no ACK (lastActionAck is nil; lastActionError=%s)",
            TB.lastActionError and TB.lastActionError.code or "nil")
        return
    end
    expect("ACK intent", a.intent, intent)
    expect("ACK scope", a.scope, scope)
    expect("ACK count", a.count, tonumber(count))
    expect("ACK executor", a.executor, executor)
    expect("lastActionError after ACK", TB.lastActionError, nil)
end

function V.ACTION_ERR(f)
    local problem
    if #f ~= 3 then
        problem = string.format("%d fields, want intent|code|message (a '|' inside the message?)", #f)
    elseif trim(f[1]) == "" then
        problem = "empty intent"
    elseif trim(f[2]) == "" then
        problem = "empty code"
    end
    if problem then
        badS("ACTION_ERR breaks the protocol, the addon shows 'Malformed action response': %s", problem)
        return
    end
    local e = TB.lastActionError
    if not e then
        badA("addon kept no error (lastActionError is nil)")
        return
    end
    expect("ERR intent", e.intent, f[1])
    expect("ERR code", e.code, f[2])
    expect("ERR message", e.message, f[3])
    expect("lastActionAck after ERR", TB.lastActionAck, nil)
end

-- ── replay ──────────────────────────────────────────────────────────────────
local fh = assert(io.open(linesFile, "r"), "cannot open " .. linesFile)
local replayed, byVerb = 0, {}
for raw in fh:lines() do
    lineNo = lineNo + 1
    local rec = raw:gsub("\r$", "")
    if rec ~= "" and not rec:find("^#") then
        local kind, channel, text
        local first = split(rec, "\t")
        kind = first[1]
        if kind == "system" and #first == 2 then
            text = first[2]
        elseif kind == "addon" and #first == 3 then
            channel, text = first[2], first[3]
        else
            error(string.format("%s:%d: not a 'system<TAB>text' or 'addon<TAB>CHANNEL<TAB>text' record", linesFile, lineNo))
        end
        current = text
        replayed = replayed + 1
        seen = {}
        local ok, err
        if kind == "system" then
            -- the 1.12 client has no message filters: the addon hooks ChatFrame_OnEvent, which the chat
            -- frame calls with the event globals set; a protocol line must not reach the original
            if text:find("TBM:", 1, true) then
                event, arg1 = "CHAT_MSG_SYSTEM", text
                H.resetChatPassed()
                ChatFrame_OnEvent()
                if H.chatPassed() then
                    badA("ChatFrame_OnEvent lets the protocol line through to the chat frame")
                end
            end
            ok, err = fire(commsFrame, "CHAT_MSG_SYSTEM", text)
        else
            ok, err = fire(commsFrame, "CHAT_MSG_ADDON", "TBM", text, channel, "Server")
        end
        if not ok then
            badA("addon raised a Lua error: %s", tostring(err))
        elseif text:sub(1, 4) == "TBM:" then
            local verb, rest = text:match("^TBM:([%w_]+)(.*)$")
            if not verb then
                badS("not a TBM:<VERB> line")
            else
                byVerb[verb] = (byVerb[verb] or 0) + 1
                rest = rest:sub(1, 1) == "|" and rest:sub(2) or rest
                local handler = V[verb]
                local handled = seen.roster and seen.roster.handled or seen.action and seen.action.handled
                    or seen.panel and seen.panel.handled or verb == "VERSION" or verb == "CAPS" or verb == "TRANSPORT"
                if not handler then
                    badC("no expectation for TBM:%s (new protocol line?); addon handled it: %s", verb, show(handled and true or false))
                else
                    if not handled then badA("addon did not recognise TBM:%s", verb) end
                    local before = #mismatches
                    handler(split(rest, "|"), rest)
                    -- a line the protocol allows must not be refused by the addon
                    if #mismatches == before then
                        local rk = seen.roster and seen.roster.kind
                        if rk == "error" and verb ~= "ROSTER_ERROR" then
                            local d = seen.roster.data
                            badA("addon rejected the line as an error (%s)", d and d.code or (TB.GetRosterSnapshotError() or {}).code or "?")
                        end
                        if seen.action and seen.action.kind == "error" and verb == "ACTION_ACK" then
                            badA("addon rejected the ACK as malformed")
                        end
                    end
                end
            end
        end
    end
end
fh:close()

local verbs = {}
for v, n in pairs(byVerb) do verbs[#verbs + 1] = v .. "=" .. n end
table.sort(verbs)
print(string.format("%s: %d records replayed (%s)", linesFile, replayed, table.concat(verbs, " ")))
if #mismatches > 0 then
    for _, m in ipairs(mismatches) do print("MISMATCH " .. m) end
    print(string.format("FAIL: %d mismatch(es)", #mismatches))
    os.exit(1)
end
print("ok: addon state matches the server lines")
