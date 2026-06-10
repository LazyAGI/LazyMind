# Architecture Reference

This document covers the full service dependency graph, request auth chain, environment variables, and optional service configuration for LazyMind.

---

## Service Dependencies

Dependency graph from `docker-compose.yml` (A → B means A waits for B to start):

```
db
├── auth-service
│   └── kong
│       └── frontend
├── core (also ← auth-service)
└── processor-server
    └── processor-worker (also ← db)
        └── parsing
            └── chat
```

| Service | Depends on |
|---------|------------|
| db | — |
| auth-service | db |
| kong | auth-service |
| frontend | kong |
| core | db, auth-service |
| processor-server | db |
| processor-worker | db, processor-server |
| parsing | processor-server, processor-worker |
| chat | parsing |

**Optional services** (profile-based):

| Service | Depends on |
|---------|------------|
| mineru | — |
| paddleocr-vlm-server | — |
| paddleocr | paddleocr-vlm-server |
| milvus-etcd, milvus-minio | — |
| milvus | milvus-etcd, milvus-minio |
| opensearch | — |

---

## Optional Services

| Service | Profile | When enabled | Purpose |
|---------|---------|--------------|---------|
| **mineru** | `mineru` | `LAZYMIND_DEPLOY_MINERU=1` | MinerU PDF parsing (on-prem; install variant/backend configurable) |
| **paddleocr** + **paddleocr-vlm-server** | `paddleocr` | `LAZYMIND_DEPLOY_PADDLEOCR=1` | PaddleOCR-VL PDF parsing (on-prem, GPU required) |
| **milvus** + **milvus-etcd** + **milvus-minio** | `milvus` | `LAZYMIND_MILVUS_URI=http://milvus:19530` | Vector store for embeddings |
| **attu** | `milvus-dashboard` | `LAZYMIND_ENABLE_MILVUS_DASHBOARD=1` and `LAZYMIND_MILVUS_URI=http://milvus:19530` | Milvus dashboard for collections, schema, and index troubleshooting |
| **opensearch** | `opensearch` | `LAZYMIND_OPENSEARCH_URI=https://opensearch:9200` | Segment store for document chunks |
| **opensearch-dashboards** | `opensearch-dashboard` | `LAZYMIND_ENABLE_OPENSEARCH_DASHBOARD=1` and `LAZYMIND_OPENSEARCH_URI=https://opensearch:9200` | OpenSearch dashboard for index, mapping, and query inspection |

**Store for parsing** (required when using Processor/Worker):

Milvus + OpenSearch are always required. If `LAZYMIND_MILVUS_URI` / `LAZYMIND_OPENSEARCH_URI` point to built-in services (`milvus:19530`, `opensearch:9200`), they are deployed automatically. If you provide external URIs, no deployment is needed.

**OCR modes for parsing:**

- OCR provider and endpoint are selected per request via the model provider UI (`DynamicPDFReader`).
- **mineru**: optional on-prem MinerU service (profile `mineru`, deploy with `LAZYMIND_DEPLOY_MINERU=1`).
- **paddleocr**: optional on-prem PaddleOCR-VL service (profile `paddleocr`, deploy with `LAZYMIND_DEPLOY_PADDLEOCR=1`, GPU required).

Built-in store dashboards are disabled by default. When enabled, they bind only to `127.0.0.1`:

- Attu (Milvus): http://127.0.0.1:3000
- OpenSearch Dashboards: http://127.0.0.1:5601
- OpenSearch Dashboards login: `admin` / `LAZYMIND_OPENSEARCH_PASSWORD`

If `LAZYMIND_MILVUS_URI` or `LAZYMIND_OPENSEARCH_URI` points to an external service, the matching built-in dashboard is not deployed even when the flag is set.

**MinerU configuration layers:**

- Install variant: `LAZYMIND_MINERU_PACKAGE_VARIANT` (e.g. `pipeline` or `all`).
- Reader runtime backend: `LAZYLLM_MINERU_BACKEND` (e.g. `pipeline` or `hybrid-auto-engine`).
- Local MinerU server backend: `LAZYMIND_MINERU_BACKEND`.
- Compatibility pin: `LAZYMIND_MINERU_NUMPY_VERSION` defaults to `1.26.4`.

For local CPU development on macOS, the default combination is `LAZYMIND_MINERU_PACKAGE_VARIANT=pipeline`
plus `LAZYLLM_MINERU_BACKEND=pipeline` and `LAZYMIND_MINERU_BACKEND=pipeline`.

---

## Request Auth Chain

User requests from the frontend pass through four verification layers:

```
Frontend
   │
   ├─► 1. auth-service (obtain JWT)
   │      Login / register → auth-service returns JWT → frontend stores token
   │
   └─► 2. Kong (RBAC)
          API request with JWT → Kong rbac-auth plugin → auth-service /api/auth/authorize
          → validates JWT and route permission → forwards if allowed
          │
          ▼
       3. Backend (core) — ACL + handler
          Core receives request → ACL check (resource-level, e.g. kb_id, dataset_id)
          → executes handler or proxies to algorithm
          │
          ▼
       4. Algorithm
          Core proxies to Python services (chat, parsing, etc.) for RAG / document processing
```

| Step | Component | Role |
|------|-----------|------|
| 1 | auth-service | Issues JWT on login/register; frontend stores it |
| 2 | Kong | RBAC: validates JWT and route permission via auth-service authorize |
| 3 | core (backend) | ACL: resource-level permission (kb, dataset); handler execution |
| 4 | algorithm | RAG chat, document parsing, task processing |

---

## API Summary

- **Kong**
  - `POST /api/auth/*` → auth-service (login, register, refresh, roles, authorize).
  - `POST /api/chat`, `POST /api/chat/stream` → chat (no Kong RBAC; frontend → Kong → chat).
  - `/api/*` (other) → core (with Kong RBAC).

- **auth-service** (via Kong): login, register, refresh, roles, permissions, user-role assignment, authorize (method + path).

**Swagger / API docs**: http://localhost:8080/docs.html — tabbed view of all service Swagger UIs. The frontend proxies to each service via Docker network, so no extra port mappings are needed.

---

## Environment Variables

| Service / scope | Variable | Example / note |
|-----------------|----------|----------------|
| auth-service | `DATABASE_URL` | PostgreSQL connection |
| auth-service | `JWT_SECRET`, `JWT_TTL_MINUTES`, `JWT_REFRESH_TTL_DAYS` | Token config |
| auth-service | `BOOTSTRAP_ADMIN_*` | Initial admin user |
| processor-* | `DOC_TASK_DATABASE_URL` | Same DB for doc tasks |
| parsing | `LAZYMIND_DEPLOY_MINERU`, `LAZYMIND_DEPLOY_PADDLEOCR` | Deploy built-in OCR profiles (`0` by default) |
| parsing | `LAZYMIND_MILVUS_URI`, `LAZYMIND_OPENSEARCH_URI`, `LAZYMIND_OPENSEARCH_USER`, `LAZYMIND_OPENSEARCH_PASSWORD` | Vector/segment stores (required) |
| opensearch (profile) | `LAZYMIND_OPENSEARCH_PASSWORD` | Override for production |
| milvus-minio (profile) | `MINIO_ACCESS_KEY`, `MINIO_SECRET_KEY` | Override for production |
| chat | `DOCUMENT_SERVER_URL`, `MAX_CONCURRENCY` | Document API and concurrency |

Override store endpoints when using external Milvus/OpenSearch; built-in services are deployed only when the URIs stay at `http://milvus:19530` and `https://opensearch:9200`.

---

## Runtime Model Config

- Use `LAZYMIND_MODEL_CONFIG_PATH` to select the config file. Three shorthand values are supported: `dynamic` (fully dynamic, key injected per request, default), `online` (public cloud API), `inner` (intranet/on-prem). An explicit file path is also accepted.
- Configure `llm`, `reranker`, and `embed_1~embed_3` directly with `source/api_key/model/type/url`.
- Keep real secrets out of git. Prefer env placeholders such as `${LAZYLLM_SILICONFLOW_API_KEY}`.
- For local debugging with a temporary config file, set `LAZYMIND_MODEL_CONFIG_PATH=/app/tmp/your-config.yaml`; `docker-compose.yml` mounts the repository `tmp/` directory into `/app/tmp` inside the containers.
- If only `embed_1` is configured, indexing, ingestion, and retrieval run in single-embedding mode automatically. Enabling `embed_2/embed_3` keeps parsing and retrieval on the same `embed_key` set.

---

## Lint

```bash
make lint              # Python (algorithm, backend) + Go (backend/core)
make lint-only-diff    # Lint only changed files (Python + Go)
```

Python uses flake8 (excluding submodule `algorithm/lazyllm` per `.flake8`); Go uses `gofmt`.

---

## Go Module

`backend/core` uses `module lazymind/core` by design; the short module path keeps imports concise.

## OpenAPI Specs

Specs live in `api/` and mirror service layout; keep them in sync when adding routes.
