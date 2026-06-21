# Local Proxy (Developer Guide)

## Intent

Local Proxy is an ingress layer used for Local Mode browser workflows and an explicit transitional Cloud-service-stack mode:
- `LAZYMIND_GATEWAY_MODE=local-proxy` replaces Cloud Kong ingress with a host Local Proxy process.
- This is **not** a change to default Cloud behavior.

The current default remains:
- Cloud startup uses Kong (`LAZYMIND_GATEWAY_MODE=kong`), unchanged.
- Local Proxy is only used when explicitly enabled.

## Scope

- Keep route/contract behavior aligned with existing Kong-style API prefixes:
  - `/api/authservice`
  - `/api/chat`
  - `/api/scan`
  - `/api/core`
  - `/api/evo`
- Use Local Proxy only as API ingress/proxy+diagnostics for development.
- No Cloud routing rewrite, middleware replacement, Electron orchestration, or packaging logic is included here.

## One-command startup (explicit transitional mode)

```bash
make up-build LAZYMIND_GATEWAY_MODE=local-proxy
```

Stop it with:

```bash
make down
```

Expected behavior:
- Compose services start with `docker-compose.local-proxy.yml` applied.
- Kong is scaled to `0` and should not be serving ingress.
- Frontend remains available at `http://localhost:${LAZYMIND_FRONTEND_PORT:-8090}`.
- Compose nginx routes `/api/*` to host Local Proxy at:
  - `host.docker.internal:${LAZYMIND_LOCAL_PROXY_PORT:-5024}`.
- Local Proxy data and logs use `LAZYMIND_LOCAL_PROXY_BASE_ROOT` (default `./data/local-proxy`).

## Configs / startup assets

- Config examples are under `backend/local-proxy/configs/`:
  - `local.yaml` (full route set, `evo-route` enabled)
  - `local-no-evo.yaml` (`evo-route` disabled)
- Default Local Proxy endpoint:
  - `http://127.0.0.1:${LAZYMIND_LOCAL_PROXY_PORT:-5024}`
- Diagnostics:
  - `http://127.0.0.1:${LAZYMIND_LOCAL_PROXY_PORT:-5024}/_local/healthz`
- Log path:
  - `$(LAZYMIND_LOCAL_PROXY_BASE_ROOT)/logs/local-proxy.console.log`
  - default: `./data/local-proxy/logs/local-proxy.console.log`

## Required/important env vars (defaults)

- `LAZYMIND_GATEWAY_MODE=local-proxy`
- `LAZYMIND_FRONTEND_PORT` (default `8090`)
- `LAZYMIND_LOCAL_PROXY_PORT` (default `5024`)
- `LAZYMIND_LOCAL_PROXY_BASE_ROOT` (default `./data/local-proxy`)
- Backend routing ports if overridden:
  - `LAZYMIND_LOCAL_PROXY_AUTH_HOST_PORT` (default `18000`)
  - `LAZYMIND_LOCAL_PROXY_CORE_HOST_PORT` (default `18001`)
  - `LAZYMIND_LOCAL_PROXY_CHAT_HOST_PORT` (default `18046`)
  - `LAZYMIND_LOCAL_PROXY_SCAN_HOST_PORT` (default `18080`)
  - `LAZYMIND_LOCAL_PROXY_EVO_HOST_PORT` (default `18047`)

## Cloud compatibility impact

- No default Cloud semantics changed.
- `make up-build` (without `LAZYMIND_GATEWAY_MODE=local-proxy`) still uses Kong.
- `make down` is intentionally mode-agnostic: it attempts host Local Proxy/file-watcher cleanup, optional Desktop stop hooks, the local-proxy compose override stack, and the default Cloud/Kong compose stack.
- Default frontend/Kong files are unchanged; `docker-compose.local-proxy.yml` only overrides frontend ingress in the explicit local-proxy mode.
- There is **no `mirror-kong` mode**.

## Acceptance / verification

Dry run command checks:

1. Transitional startup command:
   - `make -n up-build LAZYMIND_GATEWAY_MODE=local-proxy`
2. Transitional shutdown command:
   - `make -n down`
3. Cloud default sanity:
   - `make -n up-build`

Additional checks:

4. Confirm compose override usage:
   - `docker compose -f docker-compose.yml -f docker-compose.local-proxy.yml config --services`
5. Confirm Kong scaling intent:
   - `make -n up-build LAZYMIND_GATEWAY_MODE=local-proxy | rg -- "--scale kong=0"`
6. Confirm Local Proxy is running:
   - `curl http://127.0.0.1:${LAZYMIND_LOCAL_PROXY_PORT:-5024}/_local/healthz`
7. Confirm logs path:
   - `tail -n 80 ./data/local-proxy/logs/local-proxy.console.log`

## Non-goals / known limitations

- `scripts/local-proxy-dev.sh` and this flow are browser-first helpers; they do not supervise process lifecycle the way Electron will.
- `scripts/local-proxy-dev.sh` does not launch backend services; the Makefile `LAZYMIND_GATEWAY_MODE=local-proxy` flow starts the compose service stack for this transitional mode.
- `/api-docs` parity is not guaranteed in this phase unless it already works.
- This is not production packaging guidance, and not a replacement for future Desktop/Electron launcher work.
