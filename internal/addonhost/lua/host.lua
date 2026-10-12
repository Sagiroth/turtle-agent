-- Headless addon host: runs the real TortoiseBotsManager addon (Sagiroth/TortoiseBotsManager,
-- MIT) on the 1.12 widget mock, driven by turtle-agent over stdin/stdout.
--
--   lua5.1 host.lua <addon dir>
--
-- stdin: one JSON request per line, {"id":n,"op":"...",...}. stdout: one JSON line per reply,
-- {"id":n,"result":...} or {"id":n,"error":"..."}, and before it, while the request runs, one
-- {"out":"addon"|"chat"|"target",...} line per thing the addon asked the client to do
-- (SendAddonMessage, SendChatMessage, TargetUnit, TargetByName). The agent performs those through
-- its session. The host exits at the end of stdin.
--
-- Ops:
--   load    {world, in_world}  load the .toc files, fire ADDON_LOADED, then PLAYER_LOGIN and
--                              PLAYER_ENTERING_WORLD when the character is in world
--   world   {world}            the units the addon may ask about (player, target, party, raid);
--                              fires PARTY_MEMBERS_CHANGED, RAID_ROSTER_UPDATE, PLAYER_TARGET_CHANGED
--                              and UNIT_HEALTH where they changed
--   events  {events:[{event, args}]}  fire game events to every frame that registered them
--   tick    {ms}               advance GetTime() and run OnUpdate of every visible frame once
--   click   {...}              click a widget the way a player does (see ops.click)
--   state   {}                 what the addon shows

local here = (arg[0]:match("^(.*)/[^/]*$")) or "."
local json = dofile(here .. "/json.lua")
local addonDir = arg[1]
if not addonDir then
    io.stderr:write("usage: lua5.1 host.lua <addon dir>\n")
    os.exit(2)
end

local function emit(t)
    io.stdout:write(json.encode(t), "\n")
    io.stdout:flush()
end

local H = dofile(here .. "/mock.lua")
-- The mock seeds a legacy saved-variables shape for the addon's own migration test; the host
-- starts like a fresh install instead.
TortoiseBotsDB = nil

-- ── bounded logs ────────────────────────────────────────────────────────────
local function pushBounded(list, item, max)
    list[#list + 1] = item
    while #list > max do table.remove(list, 1) end
end
local luaErrors, chatLog = {}, {}
local clock = 100 -- GetTime(): seconds, advanced by tick
local function recordError(where, err)
    pushBounded(luaErrors, { at = clock, where = where, error = tostring(err) }, 20)
end

local function strip(s)
    s = tostring(s or "")
    s = string.gsub(s, "|c%x%x%x%x%x%x%x%x", "")
    s = string.gsub(s, "|r", "")
    return s
end

-- ── clock and the client's own calls ────────────────────────────────────────
function GetTime() return clock end
function time() return os.time() end
function IsShiftKeyDown() return false end

DEFAULT_CHAT_FRAME = {
    AddMessage = function(self, msg) pushBounded(chatLog, { at = clock, source = "print", text = strip(msg) }, 40) end,
}
-- The chat frame's own handler: what reaches it is what the player reads in chat. The addon
-- hooks this global (1.12 has no message filters) to hide protocol lines.
function ChatFrame_OnEvent()
    pushBounded(chatLog, { at = clock, source = event, text = strip(arg1) }, 40)
end

function SendAddonMessage(prefix, text, channel, target)
    emit({ out = "addon", prefix = tostring(prefix or ""), text = tostring(text or ""), channel = tostring(channel or ""),
        to = target and tostring(target) or nil })
end
function SendChatMessage(text, chatType, language, target)
    emit({ out = "chat", type = tostring(chatType or "SAY"), text = tostring(text or ""), to = target and tostring(target) or nil })
end

-- ── units ───────────────────────────────────────────────────────────────────
-- W is the world the agent last pushed: player, target (or nil), party (party1..4) and raid
-- (raid1..40, empty outside a raid). Each unit: name, guid (string), level, class, class_file,
-- dead, ghost, online, player, enemy.
local W = { player = { name = "Player", player = true }, party = {}, raid = {} }

local function unitInfo(unit)
    unit = string.lower(unit or "")
    if unit == "player" then return W.player end
    if unit == "target" then return W.target end
    local p = tonumber(string.match(unit, "^party(%d)$"))
    if p then return W.party[p] end
    local r = tonumber(string.match(unit, "^raid(%d+)$"))
    if r then return W.raid[r] end
    return nil
end

function UnitExists(unit) return unitInfo(unit) ~= nil end
function UnitName(unit)
    local u = unitInfo(unit)
    return u and u.name or nil
end
function UnitIsPlayer(unit)
    local u = unitInfo(unit)
    return u and u.player and true or false
end
function UnitIsDead(unit)
    local u = unitInfo(unit)
    return u and u.dead and true or false
end
function UnitIsGhost(unit)
    local u = unitInfo(unit)
    return u and u.ghost and true or false
end
function UnitIsConnected(unit)
    local u = unitInfo(unit)
    return u and u.online ~= false or false
end
function UnitCanAttack(a, unit)
    local u = unitInfo(unit)
    return u and u.enemy and true or false
end
UnitIsEnemy = UnitCanAttack
function UnitLevel(unit)
    local u = unitInfo(unit)
    return u and u.level or 0
end
function UnitClass(unit)
    local u = unitInfo(unit)
    if u and u.class then return u.class, u.class_file end
    return nil
end
function GetNumPartyMembers() return table.getn(W.party) end
function GetNumRaidMembers() return table.getn(W.raid) end

-- ── frames ──────────────────────────────────────────────────────────────────
-- Every frame the addon creates, in creation order: events go to them in that order, OnUpdate
-- runs on the visible ones.
local frames = {}

-- Visible as the client means it: shown, and every ancestor shown.
local function visible(f)
    while f do
        if f.visible == false then return false end
        f = f.parent
    end
    return true
end

local baseCreateFrame = CreateFrame
function CreateFrame(kind, name, parent, template)
    local f = baseCreateFrame(kind, name, parent)
    f.template = template
    local mt = getmetatable(f)
    mt.IsVisible = visible
    mt.IsShown = function(self) return self.visible ~= false end
    mt.GetName = function(self) return self.name end
    mt.IsEnabled = function(self) return self.enabled ~= false and 1 or nil end
    local createFontString = mt.CreateFontString
    mt.CreateFontString = function(self, ...)
        local fs = createFontString(self, ...)
        self.fontStrings = self.fontStrings or {}
        self.fontStrings[#self.fontStrings + 1] = fs
        return fs
    end
    frames[#frames + 1] = f
    return f
end

local function setEventArgs(ev, args)
    args = args or {}
    event = ev
    arg1, arg2, arg3, arg4, arg5, arg6, arg7, arg8, arg9 = args[1], args[2], args[3], args[4], args[5], args[6], args[7], args[8], args[9]
end

local CHAT_FRAME_EVENTS = { CHAT_MSG_SYSTEM = true, CHAT_MSG_WHISPER = true }

local function fireEvent(ev, args)
    if CHAT_FRAME_EVENTS[ev] then
        -- the chat frame is created by FrameXML before any addon, so it sees the event first
        this = DEFAULT_CHAT_FRAME
        setEventArgs(ev, args)
        local ok, err = pcall(ChatFrame_OnEvent, ev)
        if not ok then recordError(ev .. " (chat frame)", err) end
    end
    for _, f in ipairs(frames) do
        if f.events and f.events[ev] and f.scripts.OnEvent then
            this = f
            setEventArgs(ev, args)
            local ok, err = pcall(f.scripts.OnEvent, f)
            if not ok then recordError(ev, err) end
        end
    end
end

function TargetUnit(unit)
    local u = unitInfo(unit)
    if not u then return end
    emit({ out = "target", guid = u.guid, name = u.name })
    W.target = u -- the client changes its target at once; the agent's next world push agrees
    fireEvent("PLAYER_TARGET_CHANGED", {})
end
function TargetByName(name)
    name = tostring(name or "")
    emit({ out = "target", name = name })
    for _, u in ipairs(W.party) do
        if string.lower(u.name or "") == string.lower(name) then
            W.target = u
            fireEvent("PLAYER_TARGET_CHANGED", {})
            return
        end
    end
end

-- ── helpers for clicks and state ────────────────────────────────────────────
local function textOf(w)
    if not w then return "" end
    local t = w.text
    if type(t) == "table" then t = t.text end
    if (t == nil or t == "") and w.fontStrings then
        for _, fs in ipairs(w.fontStrings) do
            if fs.text and fs.text ~= "" then
                t = fs.text
                break
            end
        end
    end
    return strip(t)
end

local function key(s) return (string.gsub(string.lower(strip(s)), "[^%w]", "")) end

local function within(w, root)
    while w do
        if w == root then return true end
        w = w.parent
    end
    return false
end

local function clickable(w)
    return (w.kind == "Button" or w.kind == "CheckButton") and w.scripts.OnClick ~= nil
end

-- lamp names a party-switch lamp by its colour (UI.lua RefreshToggleButtons).
local function lamp(tex)
    if type(tex) ~= "table" then return nil end
    local r, g = tex[1], tex[2]
    if r == 0.30 and g == 0.90 then return "green" end
    if r == 0.45 and g == 0.45 then return "grey" end
    if r == 0.95 and g == 0.72 then return "gold" end
    return string.format("%.2f,%.2f,%.2f", r or 0, g or 0, tex[3] or 0)
end

-- switchState reads a switch the way a player does: green on, grey off, gold with "Mixed"
-- mixed, gold otherwise unknown (the addon then still says "Off").
local function switchState(text, l)
    if l == "green" then return "on" end
    if l == "grey" then return "off" end
    if l == "gold" then
        if string.find(string.lower(text or ""), "mixed", 1, true) then return "mixed" end
        return "unknown"
    end
    return nil
end

local function describe(list)
    local out = {}
    for _, w in ipairs(list) do out[#out + 1] = "'" .. textOf(w) .. "'" end
    table.sort(out)
    return table.concat(out, ", ")
end

-- findRow is the visible frame inside root that holds a text equal to rowText (a roster or
-- party row by bot name, a CC row by mark).
local function findRow(root, rowText)
    local want = key(rowText)
    for _, f in ipairs(frames) do
        if f.kind == "Frame" and f ~= root and within(f, root) and visible(f) and f.fontStrings then
            for _, fs in ipairs(f.fontStrings) do
                if key(fs.text) == want then return f end
            end
        end
    end
    return nil
end

-- findButton is the one visible, clickable widget inside root whose text is label: an exact
-- match first, else the one whose text starts with it ("AoE" finds "AoE Off").
local function findButton(root, label, notFoundOk)
    local want = key(label)
    local exact, prefix, all = {}, {}, {}
    for _, f in ipairs(frames) do
        if clickable(f) and within(f, root) and visible(f) then
            all[#all + 1] = f
            local k = key(textOf(f))
            if k == want then
                exact[#exact + 1] = f
            elseif want ~= "" and string.sub(k, 1, string.len(want)) == want then
                prefix[#prefix + 1] = f
            end
        end
    end
    local hits = table.getn(exact) > 0 and exact or prefix
    if table.getn(hits) == 1 then return hits[1] end
    if notFoundOk and table.getn(hits) == 0 then return nil end
    if table.getn(hits) > 1 then
        error(string.format("button '%s' is ambiguous: %s", label, describe(hits)), 0)
    end
    error(string.format("no visible button '%s'; visible here: %s", label, describe(all)), 0)
end

local clicked
local function press(w, force)
    if w.enabled == false and not force then
        error(string.format("'%s' is greyed out (disabled), a player cannot click it; force:true calls its OnClick anyway",
            textOf(w)), 0)
    end
    if w.kind == "CheckButton" then w:SetChecked(not w:GetChecked()) end
    clicked[#clicked + 1] = textOf(w)
    this, arg1 = w, "LeftButton"
    local ok, err = pcall(w.scripts.OnClick, w)
    if not ok then
        recordError("OnClick " .. textOf(w), err)
        error("the addon's OnClick raised: " .. tostring(err), 0)
    end
end

local TB
local SLASH = function(msg)
    local f = SlashCmdList and SlashCmdList["TORTOISEBOTSMANAGER"]
    if not f then error("the addon registered no /tbm command", 0) end
    clicked[#clicked + 1] = "/tbm " .. msg
    f(msg)
end

local TAB_BUTTONS = { actions = "tabActions", camp = "tabCamp", party = "tabParty", roster = "tabRoster",
    guild = "tabGuild", raid = "tabRaid", log = "tabLog" }
local TAB_VIEWS = { actions = "actionsFrame", camp = "campFrame", party = "partyFrame", roster = "rosterFrame",
    guild = "guildFrame", raid = "raidFrame", log = "logFrame" }

local function ensureWindow()
    if not TB.frame then error("the addon has no main window (InitUI did not run)", 0) end
    if not TB.frame:IsVisible() then SLASH("full") end
end

local function openTab(name)
    ensureWindow()
    local tab, view = TB[TAB_BUTTONS[name] or ""], TB[TAB_VIEWS[name] or ""]
    if not tab or not view then error("unknown tab " .. tostring(name), 0) end
    if view:IsVisible() then return view end
    if not tab:IsVisible() then
        error(string.format("the %s tab is hidden%s", name, name == "raid" and " (the player is not in a raid)" or ""), 0)
    end
    press(tab)
    if not view:IsVisible() then error("the " .. name .. " tab did not open", 0) end
    return view
end

-- ── ops ─────────────────────────────────────────────────────────────────────
local ops = {}

local function tbl(v)
    if type(v) == "table" and v ~= json.null then return v end
    return nil
end

local function setWorld(w)
    w = tbl(w) or {}
    local function units(list)
        local out = {}
        for _, u in ipairs(tbl(list) or {}) do
            if tbl(u) then out[#out + 1] = u end
        end
        return out
    end
    local nw = {
        player = tbl(w.player) or W.player,
        target = tbl(w.target),
        party = units(w.party),
        raid = units(w.raid),
    }
    nw.player.player = true
    for _, u in ipairs(nw.party) do u.player = true end
    for _, u in ipairs(nw.raid) do u.player = true end
    local old = W
    W = nw
    return old
end

local function groupKey(w)
    local parts = {}
    for _, u in ipairs(w.party) do parts[#parts + 1] = (u.name or "?") .. ":" .. tostring(u.online ~= false) end
    parts[#parts + 1] = "|"
    for _, u in ipairs(w.raid) do parts[#parts + 1] = (u.name or "?") end
    return table.concat(parts, ",")
end
local function targetKey(w)
    local t = w.target
    if not t then return "" end
    return tostring(t.guid) .. "|" .. tostring(t.name) .. "|" .. tostring(t.dead) .. "|" .. tostring(t.enemy)
end

function ops.world(p)
    local old = setWorld(p.world)
    if groupKey(old) ~= groupKey(W) then
        fireEvent("PARTY_MEMBERS_CHANGED", {})
        if table.getn(W.raid) > 0 or table.getn(old.raid) > 0 then fireEvent("RAID_ROSTER_UPDATE", {}) end
    end
    if targetKey(old) ~= targetKey(W) then fireEvent("PLAYER_TARGET_CHANGED", {}) end
    for i, u in ipairs(W.party) do
        local o = old.party[i]
        if o and o.name == u.name and (o.dead ~= u.dead or o.ghost ~= u.ghost) then
            fireEvent("UNIT_HEALTH", { "party" .. i })
        end
    end
    return {}
end

function ops.load(p)
    if TB then error("the addon is already loaded", 0) end
    local toc = io.open(addonDir .. "/TortoiseBotsManager.toc", "r")
    if not toc then error("no TortoiseBotsManager.toc in " .. addonDir, 0) end
    local files, version = {}, nil
    for line in toc:lines() do
        line = string.gsub(line, "\r", "")
        local v = string.match(line, "^## Version:%s*(.-)%s*$")
        if v then version = v end
        if not string.find(line, "^#") and string.find(line, "%.lua%s*$") then
            files[#files + 1] = (string.gsub(line, "%s+$", ""))
        end
    end
    toc:close()
    for _, file in ipairs(files) do
        local chunk, err = loadfile(addonDir .. "/" .. file)
        if not chunk then error("cannot load " .. file .. ": " .. tostring(err), 0) end
        local ok, rerr = pcall(chunk)
        if not ok then error(file .. " raised while loading: " .. tostring(rerr), 0) end
    end
    TB = TortoiseBots
    if not TB then error("the addon did not create TortoiseBots", 0) end
    setWorld(p.world)
    fireEvent("ADDON_LOADED", { "TortoiseBotsManager" })
    if p.in_world then
        fireEvent("PLAYER_LOGIN", {})
        fireEvent("PLAYER_ENTERING_WORLD", {})
    end
    return { version = version, files = json.array(files) }
end

function ops.events(p)
    for _, e in ipairs(p.events or {}) do
        fireEvent(e.event, e.args)
    end
    return {}
end

function ops.tick(p)
    local ms = tonumber(p.ms) or 100
    if ms < 0 then ms = 0 end
    local dt = ms / 1000
    clock = clock + dt
    for _, f in ipairs(frames) do
        local cb = f.scripts.OnUpdate
        if cb and visible(f) then
            this, arg1 = f, dt
            local ok, err = pcall(cb, f)
            if not ok then recordError("OnUpdate " .. tostring(f.name or f.kind), err) end
        end
    end
    return { clock = clock }
end

-- click {tab, label, row?, select?, bot?, view?, item?, before?, force?}
--   tab      actions, camp, party, roster, guild, raid, log, or botpanel; the window and tab are
--            opened first, by /tbm and the tab button, like a player would
--   bot      botpanel: the bot whose panel to open (/tbm bags <bot>)
--   view     botpanel: the panel tab to show first (Gear, Bags, Behaviour)
--   select   roster/guild: tick exactly these rows' check boxes first
--   row      look for the button only inside the row that shows this text (a bot, a mark)
--   item     botpanel: {bag, slot}: click that item first (opens its menu)
--   before   a button to click first when label is not visible yet ("CC Mark" opens the marks panel)
--   label    the button to click; empty when the steps before are the click (opening the panel)
--   force    click a greyed-out button anyway
function ops.click(p)
    if not TB then error("the addon is not loaded", 0) end
    clicked = {}
    local root
    if p.tab == "botpanel" then
        if not p.bot or p.bot == "" then error("the bot panel needs a bot", 0) end
        ensureWindow()
        if TB.GetBotPanelBot() ~= TB.NormalizeName(p.bot) then SLASH("bags " .. p.bot) end
        root = TB.botPanel
        if not root or not root:IsVisible() then error("the bot panel did not open for " .. p.bot, 0) end
        if p.view and p.view ~= "" then
            local tab = findButton(root, p.view)
            if tab.enabled ~= false then press(tab) end
        end
    else
        root = openTab(p.tab or "actions")
    end

    if type(p.select) == "table" then
        local want = {}
        for _, name in ipairs(p.select) do
            local row = findRow(root, name)
            if not row then error(string.format("no visible row for %s (only the rows on screen can be ticked)", name), 0) end
            want[row] = true
        end
        for _, f in ipairs(frames) do
            if f.kind == "CheckButton" and f.parent and f.parent ~= root and within(f, root) and visible(f)
                and f.scripts.OnClick and f.parent.fontStrings then
                local should = want[f.parent] or false
                if (f:GetChecked() and true or false) ~= should then press(f) end
            end
        end
    end

    if type(p.item) == "table" then
        local bag, slot = tonumber(p.item.bag), tonumber(p.item.slot)
        local function findItem()
            for _, f in ipairs(frames) do
                if f.kind == "Button" and type(f.entry) == "table" and f.entry.bag == bag and f.entry.slot == slot
                    and within(f, root) and visible(f) then
                    return f
                end
            end
            return nil
        end
        local item = findItem()
        local pages = 0
        while not item and root.nextPage and root.nextPage:IsVisible() and root.nextPage.enabled ~= false and pages < 20 do
            press(root.nextPage)
            pages = pages + 1
            item = findItem()
        end
        if not item then error(string.format("no visible item at bag %s slot %s", tostring(bag), tostring(slot)), 0) end
        press(item)
    end

    -- The button, inside the row when one is named. With before, a button or row that is not on
    -- screen yet is first opened by clicking before.
    local function resolve(soft)
        local scope = root
        if p.row and p.row ~= "" then
            scope = findRow(root, p.row)
            if not scope then
                if soft then return nil end
                error("no visible row shows " .. p.row, 0)
            end
        end
        return findButton(scope, p.label, soft)
    end
    if p.label and p.label ~= "" then
        local btn = p.before and resolve(true) or nil
        if not btn and p.before then press(findButton(root, p.before)) end
        press(btn or resolve(false), p.force)
    end
    return { clicked = json.array(clicked) }
end

-- ── state ───────────────────────────────────────────────────────────────────
local function sortedKeys(t)
    local keys = {}
    for k in pairs(t or {}) do keys[#keys + 1] = k end
    table.sort(keys, function(a, b) return tostring(a) < tostring(b) end)
    return keys
end

local function copy(t)
    if type(t) ~= "table" then return t end
    local out = {}
    for k, v in pairs(t) do
        if type(v) ~= "function" and k ~= "st" then out[k] = copy(v) end
    end
    return out
end

function ops.state()
    if not TB then error("the addon is not loaded", 0) end
    local s = { clock = clock }
    s.server = {
        version = TB.serverVersion,
        caps = json.array(sortedKeys(TB._serverCaps)),
        transport = TB.AddonCommandChannel and TB.AddonCommandChannel() or nil,
    }
    s.window = {
        shown = TB.frame and TB.frame:IsVisible() or false,
        mode = TB.GetMode and TB.GetMode() or nil,
        tab = TortoiseBotsDB and TortoiseBotsDB.activeTab or nil,
        scope = textOf(TB.scopeHint),
    }
    s.status = textOf(TB.statusText)

    local switches = json.array({})
    for _, toggle in ipairs((TB.C and TB.C.PARTY_TOGGLES) or {}) do
        local btn = TB.toggleButtons and TB.toggleButtons[toggle.key]
        if btn then
            local text, l = textOf(btn), lamp(btn.lamp and btn.lamp.texture)
            switches[#switches + 1] = { key = toggle.key, text = text, lamp = l, state = switchState(text, l),
                enabled = btn.enabled ~= false }
        end
    end
    s.switches = switches

    local bots = {}
    for name, st in pairs(TB.GetBotStates and TB.GetBotStates() or {}) do bots[name] = copy(st) end
    s.bots = bots

    local roster = json.array({})
    for _, r in ipairs(TB.GetDisplayRows and TB.GetDisplayRows("") or {}) do
        roster[#roster + 1] = { name = r.name, guid = r.guid, class_id = r.classId, state = r.state,
            server_state = r.serverState, location = r.location, cc_mark = r.ccMark, grouped = r.inGroup and true or false,
            selected = TB.IsRosterSelected and TB.IsRosterSelected(r.name) and true or false }
    end
    s.roster = roster
    if TB.rosterFrame and TB.rosterFrame:IsVisible() and TB.rows then
        local shown = json.array({})
        for _, row in ipairs(TB.rows) do
            if row:IsVisible() and textOf(row.name) ~= "" then
                shown[#shown + 1] = { name = textOf(row.name), class = textOf(row.class), status = textOf(row.status),
                    location = textOf(row.location), checked = row.check and row.check.checked or false }
            end
        end
        s.roster_shown = shown
    end

    local claimed = json.array({})
    for _, e in ipairs(TB.GetClaimedBots and TB.GetClaimedBots("") or {}) do
        local name = type(e) == "table" and e.name or e
        local c = TB.GetClaimedEntry and TB.GetClaimedEntry(name) or e
        if type(c) == "table" then
            claimed[#claimed + 1] = { name = name, class_id = c.classId, level = c.level, state = c.serverState,
                grouped = c.group, location = c.location, gear_locked = c.gearLocked }
        end
    end
    s.claimed = claimed

    local marks = {}
    for _, mark in ipairs((TB.C and TB.C.CC_MARKS) or {}) do
        local owner = TB.GetCcOwner and TB.GetCcOwner(mark.id)
        if owner then marks[mark.id] = owner end
    end
    s.cc_marks = marks

    if TB.partyFrame and TB.partyFrame:IsVisible() and TB.partyFrame.rows then
        local rows = json.array({})
        for _, row in ipairs(TB.partyFrame.rows) do
            if row:IsVisible() and row.partyName then
                local roles = json.array({})
                for _, b in ipairs(row.roleButtons or {}) do
                    if b:IsVisible() and textOf(b) ~= "" then
                        roles[#roles + 1] = { text = textOf(b), enabled = b.enabled ~= false }
                    end
                end
                rows[#rows + 1] = { name = textOf(row.nameText), desc = textOf(row.descText), cc_mark = row.ccMark,
                    roles = roles }
            end
        end
        s.party_rows = rows
    end

    local bot = TB.GetBotPanelBot and TB.GetBotPanelBot()
    if bot then
        local panel = TB.botPanel
        local view = (panel.bags:IsVisible() and "bags") or (panel.behavior:IsVisible() and "behavior") or "gear"
        local bp = { bot = bot, view = view, title = textOf(panel.nameText), sub = textOf(panel.subText),
            money = textOf(panel.moneyText), state = copy(TB.GetBotState(bot)), inventory = copy(TB.GetInventory(bot)) }
        if view == "behavior" then
            local chips = json.array({})
            for _, def in ipairs((TB.C and TB.C.BEHAVIORS) or {}) do
                local chip = panel.behaviorChips and panel.behaviorChips[def.key]
                if chip and chip:IsVisible() then
                    local text, l = textOf(chip), lamp(chip.lamp and chip.lamp.texture)
                    chips[#chips + 1] = { key = def.key, text = text, lamp = l, enabled = chip.enabled ~= false }
                end
            end
            bp.behaviour = chips
        end
        s.bot_panel = bp
    end

    if TB.raidFrame and TB.raidFrame:IsVisible() and TB.raidFrame.customToggle then
        local t = TB.raidFrame.customToggle
        s.raid = { custom = textOf(t), lamp = lamp(t.lamp and t.lamp.texture), status = textOf(TB.raidFrame.statusList) }
    end

    local ack, er = TB.lastActionAck, TB.lastActionError
    if ack then s.last_ack = { intent = ack.intent, scope = ack.scope, count = ack.count, executor = ack.executor } end
    if er then s.last_error = { intent = er.intent, code = er.code, message = er.message } end
    s.chat = json.array(copy(chatLog))
    s.lua_errors = json.array(copy(luaErrors))
    return s
end

-- ── main loop ───────────────────────────────────────────────────────────────
for line in io.lines() do
    local ok, req = pcall(json.decode, line)
    if not ok or type(req) ~= "table" then
        emit({ error = "bad request: " .. tostring(req) })
    else
        local op = ops[req.op or ""]
        if not op then
            emit({ id = req.id, error = "unknown op " .. tostring(req.op) })
        else
            local fine, res = pcall(op, req)
            if fine then
                emit({ id = req.id, result = res })
            else
                emit({ id = req.id, error = tostring(res) })
            end
        end
    end
end
