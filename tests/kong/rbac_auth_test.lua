-- Run from the repository root with resty, or pipe into the Kong container's resty.
local cjson = require("cjson.safe")
local handler_path = "kong/plugins/rbac-auth/handler.lua"
local load_handler = loadfile(handler_path)
if not load_handler then
  load_handler = assert(loadfile("/usr/local/share/lua/5.1/kong/plugins/rbac-auth/handler.lua"))
end

local function equal(actual, expected, label)
  assert(actual == expected, label .. ": expected " .. tostring(expected) .. ", got " .. tostring(actual))
end

local passed, failed = 0, 0
local function test(name, fn)
  local ok, err = pcall(fn)
  if ok then
    passed = passed + 1
    print("PASS " .. name)
  else
    failed = failed + 1
    print("FAIL " .. name .. ": " .. tostring(err))
  end
end

local success = { status = 200, body = cjson.encode({ data = {
  user_id = "fixture-user", username = "fixture-name", tenant_id = "fixture-tenant", role = "user",
} }) }

local function run(responses)
  local calls, clients, logs = {}, {}, {}
  local headers = { ["X-User-Id"] = "forged", ["X-User-Role"] = "admin" }
  local exit
  package.loaded["resty.http"] = { new = function()
    local client = {}
    clients[#clients + 1] = client
    function client:set_timeout(value) self.timeout = value end
    function client:request_uri(url, params)
      -- Snapshot because a retry may reuse the options table.
      calls[#calls + 1] = { url = url, params = cjson.decode(cjson.encode(params)), client = self }
      local response = assert(responses[#calls], "unexpected extra request")
      return response.res, response.err
    end
    return client
  end }
  local kong = {
    request = {
      get_method = function() return "POST" end,
      get_path = function() return "/api/core/documents" end,
      get_header = function(name) equal(name, "Authorization", "header"); return "Bearer fixture-token" end,
    },
    service = { request = {
      clear_header = function(name) headers[name] = nil end,
      set_header = function(name, value) headers[name] = value end,
    } },
    response = { exit = function(status, body) exit = { status = status, body = body }; return exit end },
    log = { err = function(...) logs[#logs + 1] = table.concat({...}) end },
  }
  setfenv(load_handler, setmetatable({ kong = kong }, { __index = _G }))
  local handler = load_handler()
  handler:access({ auth_service_url = "http://auth-service:8000/", timeout_ms = 1234 })
  for _, call in ipairs(calls) do
    equal(call.url, "http://auth-service:8000/api/authservice/auth/authorize", "only authorize endpoint")
    equal(call.params.method, "POST", "authorize method")
    equal(call.params.headers.Authorization, "Bearer fixture-token", "authorization preserved")
    equal(call.params.headers["Content-Type"], "application/json", "content type")
    local payload = cjson.decode(call.params.body)
    equal(payload.method, "POST", "original business method")
    equal(payload.path, "/api/core/documents", "original business path")
    equal(call.client.timeout, 1234, "configured timeout")
  end
  for _, log in ipairs(logs) do assert(not log:find("fixture%-token"), "credential leaked") end
  return calls, headers, exit
end

test("normal success retains identity and expires idle connections before the server", function()
  local calls, headers, exit = run({ { res = success } })
  equal(#calls, 1, "request count")
  equal(calls[1].params.keepalive_timeout, 1000, "pool idle timeout")
  equal(headers["X-User-Id"], "fixture-user", "trusted user")
  equal(headers["X-User-Name"], "fixture-name", "trusted name")
  equal(headers["X-Tenant-Id"], "fixture-tenant", "trusted tenant")
  equal(headers["X-User-Role"], "user", "trusted role")
  equal(exit, nil, "successful authorization")
end)

for _, err in ipairs({ "connection reset by peer", "broken pipe" }) do
  test(err .. " retries once using a fresh connection", function()
    local calls, headers, exit = run({ { err = err }, { res = success } })
    equal(#calls, 2, "request count")
    assert(calls[1].client ~= calls[2].client, "retry needs new client")
    assert(calls[2].params.pool and calls[2].params.pool ~= calls[1].params.pool, "retry pool must be isolated")
    equal(calls[2].params.keepalive, false, "retry pool must never retain sockets")
    equal(headers["X-User-Id"], "fixture-user", "trusted identity after retry")
    equal(exit, nil, "successful retry")
  end)
  test(err .. " still fails closed after one retry", function()
    local calls, _, exit = run({ { err = err }, { err = err } })
    equal(#calls, 2, "bounded retry")
    equal(exit.status, 503, "failure status")
    equal(exit.body.message, "Authorization service unavailable", "failure contract")
  end)
end

for _, status in ipairs({ 401, 403, 500 }) do
  for _, retry in ipairs({ false, true }) do
    test("HTTP " .. status .. " is final, including after retry=" .. tostring(retry), function()
      local responses = { { res = { status = status } } }
      if retry then table.insert(responses, 1, { err = "broken pipe" }) end
      local calls, _, exit = run(responses)
      equal(#calls, #responses, "no HTTP-status retry")
      equal(exit.status, status == 500 and 502 or status, "HTTP status contract")
      equal(exit.body.detail or exit.body.message,
        status == 401 and "Unauthorized" or status == 403 and "Forbidden" or "Authorization check failed", "error body")
    end)
  end
end

for _, err in ipairs({ "timeout", "connection refused", "host not found", "closed" }) do
  test(err .. " does not retry", function()
    local calls, _, exit = run({ { err = err } })
    equal(#calls, 1, "no unrelated-error retry")
    equal(exit.status, 503, "failure status")
  end)
end

print(string.format("%d passed, %d failed", passed, failed))
assert(failed == 0, "RBAC regression tests failed")
