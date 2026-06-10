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

local function _decode_jwt_payload(token)
  if not token or token == "" then
    return nil
  end
  -- Strip "Bearer " prefix if present
  local lower = string.lower(token)
  if string.sub(lower, 1, 7) == "bearer " then
    token = string.sub(token, 8)
  end
  local first_dot = string.find(token, ".", 1, true)
  if not first_dot then
    return nil
  end
  local second_dot = string.find(token, ".", first_dot + 1, true)
  if not second_dot then
    return nil
  end
  local payload_b64 = string.sub(token, first_dot + 1, second_dot - 1)
  -- base64url -> base64
  payload_b64 = payload_b64:gsub("-", "+"):gsub("_", "/")
  local pad = #payload_b64 % 4
  if pad == 2 then
    payload_b64 = payload_b64 .. "=="
  elseif pad == 3 then
    payload_b64 = payload_b64 .. "="
  elseif pad ~= 0 then
    return nil
  end
  local decoded = ngx.decode_base64(payload_b64)
  if not decoded then
    return nil
  end
  local obj, err = cjson.decode(decoded)
  if err then
    return nil
  end
  return obj
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

  -- 200: allowed; inject user headers when Authorization is present.
  -- 这里直接从 JWT payload 解析用户信息（已由 auth-service 颁发、rbac 已校验权限）。
  if auth ~= "" then
    local payload = _decode_jwt_payload(auth)
    if payload then
      local uid = payload.user_id or payload.sub
      local uname = payload.username
      local tenant = payload.tenant_id or payload.tenant_code
      kong.log.debug("rbac-auth: jwt payload user_id=", uid, " username=", uname, " tenant=", tenant)
      if uid then
        kong.service.request.set_header("X-User-Id", tostring(uid))
      end
      if uname then
        kong.service.request.set_header("X-User-Name", tostring(uname))
      end
      if tenant then
        kong.service.request.set_header("X-Tenant-Id", tostring(tenant))
      end
    else
      kong.log.warn("rbac-auth: failed to decode JWT payload for header injection")
    end
  end
end

return RbacAuthHandler
