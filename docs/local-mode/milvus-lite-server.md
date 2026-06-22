# Local Milvus Lite Server

## Intent

Local browser mode can replace the built-in Milvus container with a host Milvus Lite server. This keeps the vector-store client path compatible with Cloud mode because LazyMind already reads the vector endpoint from `LAZYMIND_MILVUS_URI`.

## Scope

This profile is for Linux/macOS local development while the rest of the stack still runs through Docker Compose. It is not an Electron packaging profile and does not change the default Cloud/Server startup path.

## Non-goals

- Do not remove the built-in Milvus, etcd, or MinIO compose services.
- Do not change the default `docker-compose.yml` Cloud path.
- Do not replace OpenSearch, PostgreSQL, Redis, or other middleware in this step.
- Do not use Milvus Lite as a production-scale vector store.

## Configuration

Milvus Lite server mode must be installed on the host. Use a Milvus Lite version that exposes the `milvus-lite server` command.

```bash
python -m pip install -U milvus-lite
```

Start the local server:

```bash
make local-milvus-lite-start
```

Start LazyMind with the local Milvus overlay:

```bash
make up-local-milvus-lite
```

Equivalent explicit compose form:

```bash
COMPOSE_FILE=docker-compose.yml:docker-compose.local-milvus-lite.yml \
LAZYMIND_MILVUS_URI=http://host.docker.internal:19530 \
make up
```

Stop the local server:

```bash
make local-milvus-lite-stop
```

## Startup Path

The local server launcher runs:

```bash
milvus-lite server --data-dir ./.lazymind-local/milvus-lite/data --port 19530
```

Compose services that need Milvus receive:

```text
LAZYMIND_MILVUS_URI=http://host.docker.internal:19530
```

The local compose overlay adds `host.docker.internal:host-gateway` only to services that need the host Milvus endpoint.

## Data Path

Development data is stored under:

```text
./.lazymind-local/milvus-lite/
  data/
  logs/
  run/
```

This is a development-only path. Packaged macOS mode must use `~/Library/Application Support/LazyMind/` for data and `~/Library/Logs/LazyMind/` for logs.

## Cloud Compatibility Impact

Default Cloud/Server behavior is unchanged:

- `LAZYMIND_MILVUS_URI` still defaults to `http://milvus:19530`.
- The base `docker-compose.yml` Milvus profile remains available.
- Kong, RBAC/auth-service, PostgreSQL, Redis, OpenSearch, Milvus standalone, and Evo behavior are not removed.
- Local Milvus Lite is opt-in through `make up-local-milvus-lite` or an explicit compose overlay.

## Acceptance Steps

Cloud gate:

```bash
make up
docker compose ps milvus
```

Local browser gate for this profile:

```bash
make local-milvus-lite-start
make local-milvus-lite-status
make up-local-milvus-lite
docker compose ps
```

Then verify through the product:

- create a knowledge base
- upload a file
- parse and index it
- run a RAG chat and confirm streamed response
- restart `make down && make up-local-milvus-lite` and confirm vector data persists

## Known Limitations

- Milvus Lite is intended for local and small-scale workloads.
- Server mode requires a Milvus Lite package that provides `milvus-lite server`; older pinned runtime dependencies may only support embedded `.db` mode.
- Attu is not started for this profile because the vector endpoint is external to the built-in Milvus compose profile.
- This step does not replace OpenSearch; segment-store local mode should be handled separately.

## ADR Note

Use Milvus Lite as an external host process for local mode instead of editing the Cloud compose Milvus service. This preserves the default Cloud stack and validates the existing `LAZYMIND_MILVUS_URI` abstraction before any deeper Local Runtime supervision work.
