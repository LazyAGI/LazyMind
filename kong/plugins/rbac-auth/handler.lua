local http  = require("resty.http")
local cjson = require("cjson.safe")

local RbacAuthHandler = {
  PRIORITY = 900,
  VERSION = "0.1.0",
}

local function _strip_trailing_slash(s)
  return (s:gsub("/+$", ""))
end

local function _parse_json(body)
  if not body or body == "" then
    return nil
  end
  local obj, err = cjson.decode(body)
  if err then
    return nil
  end
  return obj
end

local function _trim(s)
  return (s:gsub("^%s+", ""):gsub("%s+$", ""))
end

local function _string_claim(value)
  if type(value) ~= "string" then
    return nil
  end
  value = _trim(value)
  if value == "" then
    return nil
  end
  return value
end

function RbacAuthHandler:access(conf)
  local method = kong.request.get_method()
  local path = kong.request.get_path()
  local auth = kong.request.get_header("Authorization") or ""

  local base = _strip_trailing_slash(conf.auth_service_url or "http://auth-service:8000")
  local url = base .. "/api/authservice/auth/authorize"
  local body = cjson.encode({ method = method, path = path })

  local timeout_ms = conf.timeout_ms and conf.timeout_ms > 0 and conf.timeout_ms or 5000
  local httpc = http.new()
  httpc:set_timeout(timeout_ms)
  local res, err = httpc:request_uri(url, {
    method = "POST",
    body = body,
    headers = {
      ["Content-Type"] = "application/json",
      ["Authorization"] = auth,
    },
  })

  if err then
    kong.log.err("rbac-auth: auth-service request failed: ", err)
    return kong.response.exit(503, { message = "Authorization service unavailable" },
      { ["Content-Type"] = "application/json" })
  end

  if res.status == 401 then
    return kong.response.exit(401, { detail = "Unauthorized" },
      { ["Content-Type"] = "application/json" })
  end

  if res.status == 403 then
    return kong.response.exit(403, { detail = "Forbidden" },
      { ["Content-Type"] = "application/json" })
  end

  if res.status ~= 200 then
    kong.log.err("rbac-auth: auth-service returned ", res.status)
    return kong.response.exit(502, { message = "Authorization check failed" },
      { ["Content-Type"] = "application/json" })
  end
  local authz_payload = _parse_json(res.body) or {}
  local authz_data = authz_payload.data or authz_payload
  local authz_user_id = type(authz_data) == "table" and _string_claim(authz_data.user_id) or nil
  local authz_username = type(authz_data) == "table" and _string_claim(authz_data.username) or nil
  local authz_tenant = type(authz_data) == "table" and _string_claim(authz_data.tenant_id) or nil
  local authz_role = type(authz_data) == "table" and _string_claim(authz_data.role) or nil

  -- 200: allowed; inject user headers when Authorization is present.
  -- User identity and role come from auth-service's verified token and current DB authorization result.
  kong.service.request.clear_header("X-User-Id")
  kong.service.request.clear_header("X-User-Name")
  kong.service.request.clear_header("X-Tenant-Id")
  kong.service.request.clear_header("X-User-Role")
  if authz_user_id then
    kong.service.request.set_header("X-User-Id", tostring(authz_user_id))
  end
  if authz_username then
    kong.service.request.set_header("X-User-Name", tostring(authz_username))
  end
  if authz_tenant then
    kong.service.request.set_header("X-Tenant-Id", tostring(authz_tenant))
  end
  if authz_role then
    kong.service.request.set_header("X-User-Role", tostring(authz_role))
  end
end

return RbacAuthHandler
