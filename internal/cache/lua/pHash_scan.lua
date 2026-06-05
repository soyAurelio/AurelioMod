-- pHash_scan.lua
-- Server-side Lua script for DragonflyDB / Redis: SCANs L2 cache keys,
-- computes Hamming distance via manual XOR + popcount, and returns
-- matching decisions within the specified threshold.
--
-- Lua 5.1 compatible: uses arithmetic operations instead of bitwise operators
-- because Redis/DragonflyDB Lua engine may not expose & ~ | << >> natively.
--
-- Usage:
--   EVAL script 0 <query_pHash> <max_distance>
--   EVALSHA <sha> 0 <query_pHash> <max_distance>
--
-- ARGV[1]: query pHash as integer string (decimal representation of uint64)
-- ARGV[2]: max Hamming distance (integer, typically 5)
--
-- Returns: Flat array alternating distance and data:
--   {dist1, data1, dist2, data2, ...} sorted by distance ascending.
--   Empty array if no keys within threshold.
--   Go client unpacks pairs: results[i]=distance, results[i+1]=data_json.

local query = tonumber(ARGV[1])
local max_dist = tonumber(ARGV[2])

-- popcount: count set bits using repeated division by 2.
local function popcount(n)
    local count = 0
    while n > 0 do
        if n % 2 == 1 then
            count = count + 1
        end
        n = math.floor(n / 2)
    end
    return count
end

-- bxor: bitwise XOR using bit-by-bit arithmetic.
local function bxor(a, b)
    local result = 0
    local place = 1
    while a > 0 or b > 0 do
        if (a % 2) ~= (b % 2) then
            result = result + place
        end
        place = place * 2
        a = math.floor(a / 2)
        b = math.floor(b / 2)
    end
    return result
end

local function hamming(a, b)
    return popcount(bxor(a, b))
end

-- Collect pairs as flat array: {dist, data, dist, data, ...}
local pairs = {}

local cursor = "0"
repeat
    local scan_result = redis.call("SCAN", cursor, "MATCH", "l2:*", "COUNT", 100)
    cursor = scan_result[1]

    for _, key in ipairs(scan_result[2]) do
        -- Extract pHash from key format: "l2:<16 hex chars>"
        local hex_part = string.sub(key, 4)
        local stored = tonumber(hex_part, 16)
        if stored then
            local dist = hamming(query, stored)
            if dist <= max_dist then
                local data = redis.call("GET", key)
                if data then
                    table.insert(pairs, {dist, data})
                end
            end
        end
    end
until cursor == "0"

-- Sort pair tables by distance ascending
table.sort(pairs, function(a, b)
    return a[1] < b[1]
end)

-- Flatten into single array for Go client: {dist, data, dist, data, ...}
local flat = {}
for _, p in ipairs(pairs) do
    table.insert(flat, p[1])  -- distance
    table.insert(flat, p[2])  -- data (JSON string)
end

return flat
