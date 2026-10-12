-- Minimal JSON for the addon host's stdin/stdout protocol (Lua 5.1, no libraries).
-- encode: nil -> null, tables with t[1] or json.array(t) -> arrays, other tables -> objects
-- (keys made strings). decode: objects -> tables, arrays -> tables, null -> json.null.

local json = {}

json.null = setmetatable({}, { __tostring = function() return "null" end })
local arrayMark = {}

-- array marks t so that an empty t still encodes as [].
function json.array(t)
    t = t or {}
    return setmetatable(t, arrayMark)
end

local escapes = { ['"'] = '\\"', ['\\'] = '\\\\', ['\b'] = '\\b', ['\f'] = '\\f', ['\n'] = '\\n', ['\r'] = '\\r', ['\t'] = '\\t' }

local function encodeString(s)
    return '"' .. string.gsub(s, '[%c"\\]', function(c)
        return escapes[c] or string.format("\\u%04x", string.byte(c))
    end) .. '"'
end

local encode

-- isArray: marked by json.array, or keys exactly 1..n (slot maps keyed 0..18 stay objects).
local function isArray(t)
    if getmetatable(t) == arrayMark then return true end
    if t[1] == nil then return false end
    local n = 0
    for _ in pairs(t) do n = n + 1 end
    return n == #t
end

encode = function(v, out)
    local kind = type(v)
    if v == nil or v == json.null then
        out[#out + 1] = "null"
    elseif kind == "boolean" then
        out[#out + 1] = v and "true" or "false"
    elseif kind == "number" then
        if v ~= v or v == math.huge or v == -math.huge then
            out[#out + 1] = "null"
        elseif v == math.floor(v) and math.abs(v) < 1e15 then
            out[#out + 1] = string.format("%d", v)
        else
            out[#out + 1] = string.format("%.14g", v)
        end
    elseif kind == "string" then
        out[#out + 1] = encodeString(v)
    elseif kind == "table" then
        if isArray(v) then
            out[#out + 1] = "["
            for i = 1, #v do
                if i > 1 then out[#out + 1] = "," end
                encode(v[i], out)
            end
            out[#out + 1] = "]"
        else
            out[#out + 1] = "{"
            local keys = {}
            for k in pairs(v) do keys[#keys + 1] = tostring(k) end
            table.sort(keys)
            local first = true
            for _, k in ipairs(keys) do
                local val = v[k]
                if val == nil then val = v[tonumber(k)] end
                if type(val) ~= "function" then
                    if not first then out[#out + 1] = "," end
                    first = false
                    out[#out + 1] = encodeString(k)
                    out[#out + 1] = ":"
                    encode(val, out)
                end
            end
            out[#out + 1] = "}"
        end
    else
        out[#out + 1] = encodeString(tostring(v))
    end
end

function json.encode(v)
    local out = {}
    encode(v, out)
    return table.concat(out)
end

-- ── decode ──────────────────────────────────────────────────────────────────
local function utf8(cp)
    if cp < 0x80 then return string.char(cp) end
    if cp < 0x800 then return string.char(0xC0 + math.floor(cp / 0x40), 0x80 + cp % 0x40) end
    if cp < 0x10000 then
        return string.char(0xE0 + math.floor(cp / 0x1000), 0x80 + math.floor(cp / 0x40) % 0x40, 0x80 + cp % 0x40)
    end
    return string.char(0xF0 + math.floor(cp / 0x40000), 0x80 + math.floor(cp / 0x1000) % 0x40,
        0x80 + math.floor(cp / 0x40) % 0x40, 0x80 + cp % 0x40)
end

local decodeValue

local function skip(s, i)
    return (string.find(s, "[^ \t\r\n]", i)) or (#s + 1)
end

local function decodeString(s, i)
    -- s:sub(i, i) == '"'
    local out, j = {}, i + 1
    while true do
        local c = string.sub(s, j, j)
        if c == "" then error("json: unterminated string") end
        if c == '"' then return table.concat(out), j + 1 end
        if c == "\\" then
            local e = string.sub(s, j + 1, j + 1)
            local map = { b = "\b", f = "\f", n = "\n", r = "\r", t = "\t", ['"'] = '"', ["\\"] = "\\", ["/"] = "/" }
            if map[e] then
                out[#out + 1] = map[e]
                j = j + 2
            elseif e == "u" then
                local cp = tonumber(string.sub(s, j + 2, j + 5), 16)
                if not cp then error("json: bad \\u escape") end
                j = j + 6
                if cp >= 0xD800 and cp <= 0xDBFF and string.sub(s, j, j + 1) == "\\u" then
                    local lo = tonumber(string.sub(s, j + 2, j + 5), 16)
                    if lo and lo >= 0xDC00 and lo <= 0xDFFF then
                        cp = 0x10000 + (cp - 0xD800) * 0x400 + (lo - 0xDC00)
                        j = j + 6
                    end
                end
                out[#out + 1] = utf8(cp)
            else
                error("json: bad escape \\" .. e)
            end
        else
            local k = string.find(s, '["\\]', j)
            if not k then error("json: unterminated string") end
            out[#out + 1] = string.sub(s, j, k - 1)
            j = k
        end
    end
end

decodeValue = function(s, i)
    i = skip(s, i)
    local c = string.sub(s, i, i)
    if c == "{" then
        local t = {}
        i = skip(s, i + 1)
        if string.sub(s, i, i) == "}" then return t, i + 1 end
        while true do
            i = skip(s, i)
            if string.sub(s, i, i) ~= '"' then error("json: object key expected at " .. i) end
            local k
            k, i = decodeString(s, i)
            i = skip(s, i)
            if string.sub(s, i, i) ~= ":" then error("json: ':' expected at " .. i) end
            local v
            v, i = decodeValue(s, i + 1)
            t[k] = v
            i = skip(s, i)
            local d = string.sub(s, i, i)
            if d == "}" then return t, i + 1 end
            if d ~= "," then error("json: ',' or '}' expected at " .. i) end
            i = i + 1
        end
    elseif c == "[" then
        local t = json.array({})
        i = skip(s, i + 1)
        if string.sub(s, i, i) == "]" then return t, i + 1 end
        while true do
            local v
            v, i = decodeValue(s, i)
            t[#t + 1] = v
            i = skip(s, i)
            local d = string.sub(s, i, i)
            if d == "]" then return t, i + 1 end
            if d ~= "," then error("json: ',' or ']' expected at " .. i) end
            i = i + 1
        end
    elseif c == '"' then
        return decodeString(s, i)
    elseif string.sub(s, i, i + 3) == "true" then
        return true, i + 4
    elseif string.sub(s, i, i + 4) == "false" then
        return false, i + 5
    elseif string.sub(s, i, i + 3) == "null" then
        return json.null, i + 4
    end
    local num = string.match(s, "^-?%d+%.?%d*[eE]?[-+]?%d*", i)
    if not num or num == "" then error("json: unexpected character at " .. i) end
    return tonumber(num), i + #num
end

function json.decode(s)
    local v, i = decodeValue(s, 1)
    if skip(s, i) <= #s then error("json: trailing data at " .. i) end
    return v
end

return json
