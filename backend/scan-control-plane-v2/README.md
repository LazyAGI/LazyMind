# scan-control-plane-v2 standalone readiness

This directory is a standalone v2 Go module. Build and smoke it from this directory only; do not rename, replace, or retarget any existing service as part of this readiness check.

## Verify

```sh
go test ./...
```

## Provider auth boundary

Provider OAuth differences belong in auth-service, not scan-control-plane-v2. Scan receives a `binding.auth_connection_id`, calls auth-service through one shared boundary, and uses the returned provider access token in the connector.

Required scan-side auth settings:

```text
SCAN_CONTROL_PLANE_AUTH_SERVICE_BASE_URL=http://auth-service:8000
LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN=...
```

Provider-specific scan settings are for data APIs only. For example, Feishu uses `SCAN_CONTROL_PLANE_FEISHU_BASE_URL` for the Feishu data adapter, while token acquisition still goes through auth-service. Adding another provider such as Notion should add a provider data API setting/connector, not a provider-specific auth-service URL.

## Postgres migration rehearsal

The v2 module does not add a Go Postgres driver for this rehearsal. Use the local Docker image and the bundled `psql` client in the container instead:

```sh
scripts/postgres_migration_smoke.sh
```

The script starts a temporary `postgres:16` container named `scan-control-plane-v2-pg-smoke`, applies `migrations/20260519101723_init.up.sql` to an empty database, checks required tables/indexes/forbidden legacy names, exercises duplicate binding/document/task idempotency constraints, inserts worker lease/dead-letter/checkpoint/run rows, and removes the temporary container on exit.

## Explicit schema reset

Normal `init.up.sql` is a create-only migration path and does not drop existing tables. For old test environments that intentionally reset scan-control-plane state, run:

```bash
SCAN_CONTROL_PLANE_DB_DSN='postgres://root:123456@localhost:5432/scan_control_plane?sslmode=disable' \
SCAN_CONTROL_PLANE_RESET_CONFIRM=drop-scan-control-plane-owned-tables \
scripts/reset_scan_control_plane_schema.sh
```

The reset script prints the exact table list first, drops only scan-control-plane owned tables from plan-9, and then reapplies the init migration. It is not part of the normal runtime startup path.

## Build the v2 image

```sh
docker build -t scan-control-plane-v2:readiness .
```

The Dockerfile builds only the v2 module entrypoint:

```sh
go build -trimpath -o /out/scan-control-plane ./cmd/scan-control-plane
```

The build context copies only `go.mod`, `cmd/`, `internal/`, and `migrations/` from this directory. It does not copy or reference any sibling service tree.

## Optional container smoke

```sh
docker run --rm -p 18080:18080 scan-control-plane-v2:readiness
```

In another shell:

```sh
curl -sS http://127.0.0.1:18080/healthz
```

Expected response:

```json
{"status":"ok"}
```

This smoke only verifies that the HTTP server starts; production configuration still requires Postgres, Core, temp storage, and enabled real connector adapters.
