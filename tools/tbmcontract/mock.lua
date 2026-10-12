-- Minimal WoW 1.12 API mock for loading the TortoiseBotsManager addon (TBM) offline.
--
-- Copied from tests/regression.lua of github.com/Sagiroth/TortoiseBotsManager
-- (MIT, Copyright (c) 2026 Tortoise WoW Stack contributors; commit pinned in fetch.sh), trimmed: the widget
-- surface and the few unit/chat functions the addon calls while loading. The original
-- header said:
--   "The mock intentionally models the small Vanilla 1.12 widget surface used by the
--    addon. In particular, CheckButtons own checked state and every Button/CheckButton
--    may register clicks; generic Frames may not."
-- Keep in step with that file when the pinned commit moves. Returns the harness handles.


if not table.getn then table.getn = function(t) return t and #t or 0 end end
if not unpack then unpack = table.unpack end
if not math.mod then math.mod = math.fmod end
local frames = {}
local sent = {}
local addonSent = {}
local now = 10
local targetExists = true
local targetNameValue = "Training Dummy"
local partyMembers = {}
local partyClassName = "Priest"
tradePartner = nil

local function object(kind, parent)
    local value = {
        kind = kind,
        parent = parent,
        scripts = {},
        visible = true,
        text = "",
        enabled = true,
        checked = false,
    }
    local methods = {}

    function methods:SetWidth(width) self.width = width end
    function methods:SetHeight(height) self.height = height end
    function methods:SetPoint(...) self.point = {...} end
    function methods:ClearAllPoints() self.point = nil end
    function methods:SetFrameStrata(strata) self.strata = strata end
    function methods:SetMovable(movable) self.movable = movable end
    function methods:SetClampedToScreen(clamped) self.clamped = clamped end
    function methods:EnableMouse(enabled) self.mouseEnabled = enabled end
    function methods:RegisterForDrag(...) self.dragButtons = {...} end
    function methods:RegisterEvent(eventName)
        self.events = self.events or {}
        self.events[eventName] = true
    end
    function methods:RegisterForClicks(...)
        if self.kind ~= "Button" and self.kind ~= "CheckButton" then
            error("RegisterForClicks called on non-button")
        end
        self.clickButtons = {...}
    end
    function methods:SetScript(name, callback) self.scripts[name] = callback end
    function methods:SetBackdrop(...) end
    function methods:SetBackdropColor(...) end
    function methods:SetBackdropBorderColor(...) end
    function methods:CreateTexture() return object("Texture", self) end
    function methods:CreateFontString() return object("FontString", self) end
    function methods:SetTexture(...) self.texture = {...} end
    function methods:SetTexCoord(...) end
    function methods:SetAllPoints(...) end
    function methods:SetBlendMode(...) end
    function methods:SetAutoFocus(...) end
    function methods:SetJustifyH(...) end
    function methods:SetText(text) self.text = text end
    function methods:GetText() return self.text end
    function methods:SetTextColor(...) end
    function methods:SetVertexColor(...) end
    function methods:SetJustifyV(...) end
    function methods:SetChecked(checked) self.checked = checked and true or false end
    function methods:GetChecked() return self.checked end
    function methods:ClearFocus() end
    function methods:Show() self.visible = true end
    function methods:Hide() self.visible = false end
    function methods:IsVisible() return self.visible end
    function methods:SetAlpha(alpha) self.alpha = alpha end
    function methods:GetParent() return self.parent end
    function methods:StartMoving() end
    function methods:StopMovingOrSizing() end
    function methods:GetLeft() return self.x or 100 end
    function methods:GetTop() return (self.y or 100) + (self.height or 50) end
    function methods:GetPoint() return "CENTER", UIParent, "CENTER", 0, 15 end
    function methods:GetCenter() return 0, 0 end
    function methods:GetEffectiveScale() return 1 end
    function methods:Enable() self.enabled = true end
    function methods:Disable() self.enabled = false end

    methods.__index = methods
    return setmetatable(value, methods)
end

function CreateFrame(kind, name, parent)
    local frame = object(kind, parent)
    frame.name = name
    if name then frames[name] = frame end
    return frame
end

TortoiseBotsDB = { roster = { Stale = { addedAt = 1 } }, rosterList = { "Legacy" }, activeTab = "legacy" }
UIParent = CreateFrame("Frame", "UIParent")
Minimap = CreateFrame("Frame", "Minimap")
UISpecialFrames = {}
SlashCmdList = {}
local defaultChatMessages = {}
DEFAULT_CHAT_FRAME = { AddMessage = function(self, msg) table.insert(defaultChatMessages, msg) end }
local chatFilters = {}
function ChatFrame_AddMessageEventFilter(eventName, filter) chatFilters[eventName] = filter end
local chatEventPassed = false
function ChatFrame_OnEvent() chatEventPassed = true end
GameTooltip = {
    SetOwner = function() end,
    SetText = function() end,
    AddLine = function() end,
    Show = function() end,
    Hide = function() end,
}

function GetTime() return now end
function time() return 123 end
function SendChatMessage(message) table.insert(sent, message) end
function SendAddonMessage(prefix, message, channel)
    table.insert(addonSent, { prefix = prefix, message = message, channel = channel })
end
function GetCursorPosition() return 0, 0 end
function UnitExists(unit)
    if unit == "player" then return true end
    if unit == "target" then return targetExists end
    local _, _, index = string.find(unit or "", "^party(%d+)$")
    return index and tonumber(index) <= table.getn(partyMembers) or false
end
local deadUnits, ghostUnits = {}, {}
function UnitIsDead(unit) return deadUnits[unit] or false end
function UnitIsGhost(unit) return ghostUnits[unit] or false end
local itemCache = {}
function GetItemInfo(id)
    local item = itemCache[id]
    if not item then return nil end
    return item.name, "item:" .. id .. ":0:0:0", item.quality or 1, 1, "Armor", "Cloth", 1, "", item.texture
end
function UnitLevel(unit) return 60 end
function UnitClass(unit)
    if unit == "player" then return "Warrior", "WARRIOR" end
    return partyClassName, string.upper(partyClassName)
end
function GetNumPartyMembers() return table.getn(partyMembers) end
function GetNumRaidMembers() return 0 end
function UnitName(unit)
    if unit == "player" then return "Tester" end
    if unit == "target" then return targetExists and targetNameValue or nil end
    if unit == "NPC" then return tradePartner end
    local _, _, index = string.find(unit or "", "^party(%d+)$")
    return index and partyMembers[tonumber(index)] or nil
end
function FauxScrollFrame_Update(frame, count) frame.itemCount = count end
function FauxScrollFrame_GetOffset() return 0 end
function FauxScrollFrame_OnVerticalScroll() end

return {
    frames = frames,
    chatFilters = chatFilters,
    sent = sent,
    addonSent = addonSent,
    defaultChatMessages = defaultChatMessages,
}
