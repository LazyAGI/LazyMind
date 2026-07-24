# LazyMind

**[中文](README.CN.md)** | **English**

> **Enterprise RAG knowledge-base platform with built-in self-evolution** — not just a Q&A system, but one that can find its own problems, fix them, and verify the improvement automatically.

---

## What is this?

LazyMind is a **production-ready enterprise knowledge-base + RAG chat platform** with a built-in **automated RAG quality optimization loop (evo)**.

You can use it to:

- Connect local files, Feishu docs, and other data sources to build an enterprise knowledge base
- Serve RAG-powered conversations with multi-path retrieval and reranking
- Manage skills, vocabulary, usage habits, and other operational assets via the **Knowledge Ops** module
- Run the **evo self-evolution loop** to automatically evaluate RAG quality, analyze bad cases, generate code fixes, run A/B tests, and close the improvement cycle end-to-end

---

## Highlights

### 1. RAG Self-Evolution Loop (evo)

This is LazyMind's most distinctive capability. Traditional RAG systems rely on manual inspection after deployment. The evo module lets the system **run the entire optimization pipeline on its own**:

```
Generate dataset → Baseline eval → Analyze bad cases → Generate code fix → A/B test → Merge & deploy
```

The pipeline can run fully automatically or pause at key checkpoints for human review.

![evo self-evolution pipeline](docs/assets/evo-pipeline.png)

**Real-time orchestration view** — track the progress and status of each optimization step:

![evo execution orchestration](docs/assets/evo-run.png)

### 2. Multi-Source Data Ingestion

Unified management of local directories, object storage, and OAuth cloud sources (Feishu, etc.) — including connection, sync, and runtime status.

![Data source management — create new source](docs/assets/datasource-create.png)

### 3. Knowledge Ops Asset Management

Centrally manage vocabulary, system tools, skills (operation templates), and usage habits to build a traceable, operational memory hub.

![Knowledge Ops — skills, vocabulary, system tools](docs/assets/knowledge-ops.png)

### 4. Flexible OCR and Vector Storage

- **OCR**: built-in PDFReader / MinerU / PaddleOCR-VL (GPU) — three tiers
- **Vector store**: Milvus + OpenSearch, deploy in-stack or connect externally
- **Multi-embedding** (embed_1~3) for hybrid retrieval; single-embedding mode auto-activates when only embed_1 is configured

### 5. Enterprise-Grade Auth

Kong API Gateway + JWT/RBAC with four verification layers: Frontend → Kong RBAC → Core ACL → Algorithm services. Each layer enforces independent permission checks.

---

## Architecture

```
┌──────────────────────────────────────────────────────┐
│                    Frontend (8080)                   │
│           nginx SPA — knowledge base / chat / ops    │
└─────────────────────┬────────────────────────────────┘
                      │
             ┌────────▼────────┐
             │   Kong (8000)   │  API Gateway + RBAC
             └──┬──────-────┬──┘
                │           │
       ┌────────▼-──┐  ┌────▼──────────┐
       │auth-service│  │  core (Go)    │  dataset / doc / task / retrieval
       │  FastAPI   │  │  HTTP API     │
       └────────────┘  └──────┬────────┘
                              │ proxy
             ┌────────────────┼───────────────┐
             │                │               │
    ┌────────▼──────┐  ┌──────▼──────┐  ┌─────▼──────┐
    │   parsing     │  │    chat     │  │    evo     │
    │ doc parse /   │  │  RAG chat   │  │ self-evo   │
    │ vectorization │  │             │  │   loop     │
    └───────────────┘  └─────────────┘  └────────────┘
             │
    ┌────────┴──────────────┐
    │  Milvus + OpenSearch  │  vector + segment store
    └───────────────────────┘
```

For the full service dependency graph, environment variables, and request auth chain, see [`docs/architecture.md`](docs/architecture.md).

---

## Quick Start

**Local prerequisites:** Go, Python 3, uv, pnpm, and Node.js.

### Step 1 — Get a MinerU API key (for high-quality PDF parsing)

Apply for a MinerU API key at [https://mineru.net](https://mineru.net/apiManage/token).

```bash
export LAZYLLM_MINERU_API_KEY=your_mineru_key
```

> **Note:** Same prefix — `LAZYLLM_`, not `LAZYMIND_`.

> **Important:** Because reader are initialized at startup, the API key for your ocr provider **must be set before launching the stack**. We are working on frontend-based key configuration for OCR — stay tuned for the next release.

### Step 2 — Start the stack

```bash
make local-up
```

On native Windows PowerShell, use the isolated Windows lifecycle target:

```powershell
make local-win-up
```

After startup:
- Frontend: http://localhost:8090
- API docs: http://localhost:8090/docs.html
- Default credentials: `admin` / `admin`

### Step 3 — Configure models in the frontend

Log in and go to the model settings page to configure your **LLM**, **VLM**, **enbed**, **cross_embed** and **Reranker** models using the API key from Step 1.

For environment setup and detailed examples, see [`docs/quick_start.md`](docs/quick_start.md).

To stop the local runtime:

```bash
make local-down
```

Use `make local-win-down` on Windows.

---

## Testing

The legacy quick test command remains:

```bash
make test
```

`make test` uses the Python, Node/npm, and Go tools already available on the host, matching its historical behavior.

For a project-managed host environment that covers the same test scope, use:

```bash
make test-hermetic
```

`make test-hermetic` requires `uv`, either `fnm` or `nvm`, and Go `1.24.0`. It creates a repo-local Python 3.11 environment at `.venv-test/`, selects Node 20 through the available Node manager, installs frontend test dependencies with `npm ci`, and runs the same frontend, auth-service, backend/core, and algorithm tests as `make test`.

## Common Startup Configurations

| Scenario | Command |
|----------|---------|
| Local runtime on host (SQLite state backend, no containers) | `make local-up` |
| Stop local runtime | `make local-down` |
| Windows local runtime (PowerShell/native process management) | `make local-win-up` |
| Stop Windows local runtime | `make local-win-down` |
| Remove local application artifacts | `make local-clean` |
| Stop local runtime, remove runtime data, and remove local application artifacts | `make local-reset` |
| Container stack | `make up` |
| Deploy MinerU OCR (on-prem) | `make up LAZYMIND_DEPLOY_MINERU=1` |
| Deploy PaddleOCR (on-prem) | `make up LAZYMIND_DEPLOY_PADDLEOCR=1` |
| External Milvus/OpenSearch | `make up LAZYMIND_MILVUS_URI=http://your-milvus:19530 LAZYMIND_OPENSEARCH_URI=https://your-opensearch:9200` |
| Enable store dashboards | `make up LAZYMIND_ENABLE_STORE_DASHBOARDS=1` |

`make local-up` runs LazyMind directly on the host through `local/build/bin/local-runtime-manager`. If `local/config.env` does not exist, Make copies it from `local/config.env.example` and uses it for local build/run configuration. Application artifacts are repo-local under `local/build`: Go binaries in `local/build/bin`, managed runtimes in `local/build/runtimes`, Python dependencies in `local/build/deps/python`, Node dependencies in `local/build/deps/node`, and desktop staging app files in `local/build/app`. Runtime data, SQLite databases, state, generated startup files, logs, caches, and local document imports use platform paths. Override the `LAZYMIND_*` path variables in `local/config.env` only when a non-standard location is required.

`local-win-*` delegates to `local/scripts/local-win.ps1`, builds with `CGO_ENABLED=0`, uses `.exe`/`Scripts`/`.cmd` paths, and creates a Windows directory junction for frontend dependencies. Core SQLite uses a Pure Go driver, so MinGW is not required. Host-local runtimes on all three platforms install `algorithm/requirements-local.txt` and use pymilvus/Milvus Lite 3.0 under `LAZYMIND_LOCAL_MILVUS_DATA_DIR`; its storage format is intentionally incompatible with the old 2.x local database. Container builds instead install `algorithm/requirements-cloud.txt`, which explicitly pins the Cloud pymilvus client to 2.4.14.

### Desktop Builds

| Platform | Local source runtime | Desktop package |
|----------|----------------------|-----------------|
| macOS arm64 | `make local-up` / `make local-down` | `make desktop-darwin-arm64` |
| Windows x64 | `make local-win-up` / `make local-win-down` | `make desktop-windows-x64` |

Windows Desktop produces `desktop/dist/win-unpacked/` and a portable `desktop/dist/LazyMind-windows-x64-yyyyMMdd-HHmmss-<commit>.zip`, where the timestamp is the build host's local time and `<commit>` is the short Git commit. The package includes the compiled frontend and full Local/RAG runtime; it does not include raw frontend `node_modules` or model weights. See [`desktop/README.md`](desktop/README.md) for runtime ownership and platform details.

### Platform Path Examples

| Platform | Application artifacts | Runtime data and DB | Logs | Cache | Local documents |
|----------|-----------------------|---------------------|------|-------|-----------------|
| macOS | `<repo>/local/build` | `/Users/<User>/Library/Application Support/LazyMind` | `/Users/<User>/Library/Logs/LazyMind` | `/Users/<User>/Library/Caches/LazyMind` | `/Users/<User>/Documents/LazyMind` |
| Windows | `<repo>\local\build` | `%LOCALAPPDATA%\LazyMind` | `%LOCALAPPDATA%\LazyMind\Logs` | `%LOCALAPPDATA%\LazyMind\Cache` | `%USERPROFILE%\Documents\LazyMind` |
| Linux | `<repo>/local/build` | `${XDG_DATA_HOME:-/home/<user>/.local/share}/LazyMind` | `${XDG_STATE_HOME:-/home/<user>/.local/state}/LazyMind/logs` | `${XDG_CACHE_HOME:-/home/<user>/.cache}/LazyMind` | `/home/<user>/Documents/LazyMind` |

---

## Model Configuration

All algorithm services use `LAZYMIND_MODEL_CONFIG_PATH`. The default is `dynamic`,
so the frontend's per-user model/API-key selection can be injected per request.
Set `online` or `inner` only when forcing a static config.

| Value | Description |
|-------|-------------|
| `inner` | On-premises / intranet deployment |
| `online` | Public cloud API |
| `dynamic` (default) | Key injected per request |

Configure `llm`, `reranker`, and `embed_1~embed_3`. If only `embed_1` is set, single-embedding mode activates automatically.

---

## evo Self-Evolution Module

evo is a standalone FastAPI service (port 8047) that implements the full RAG quality optimization loop:

```
dataset_gen → eval → run (analysis) → apply (code fix) → merge → deploy → abtest
```

**Two execution modes:**
- **auto** — fully automated, no human intervention
- **interactive** — pauses at key steps for human approve / revise / cancel

**Natural-language driven:**

```bash
curl -sX POST "$BASE/v1/evo/threads/$THREAD_ID/messages" \
  -H "Content-Type: application/json" \
  -d '{"content":"Generate an eval set from KB_ID, analyze the report, fix the code, and run an A/B test"}'
```

Full API reference: [`evo/README.md`](evo/README.md).

---

## Optional Services

| Service | Profile | Purpose |
|---------|---------|---------|
| **mineru** | `mineru` | MinerU PDF parsing (layout analysis) |
| **paddleocr** | `paddleocr` | PaddleOCR-VL PDF parsing (GPU required) |
| **milvus** | `milvus` | Vector store |
| **opensearch** | `opensearch` | Segment store |
| **attu** | `milvus-dashboard` | Milvus visual management |
| **opensearch-dashboards** | `opensearch-dashboard` | OpenSearch visual management |

---

## Project Layout

```
LazyMind/
├── kong.yml                    # Kong declarative config
├── docker-compose.yml          # All services
├── Makefile                    # lint / startup shortcuts
├── backend/
│   ├── auth-service/           # FastAPI auth, JWT, RBAC
│   ├── core/                   # Go HTTP API (dataset / doc / task / retrieval)
│   └── scripts/
├── frontend/                   # nginx + SPA
├── algorithm/
│   ├── chat/                   # RAG chat (lazyllm)
│   ├── parsing/                # Document parsing (lazyllm + MinerU/PaddleOCR)
│   └── processor/              # Document task queue
├── evo/                        # Self-evolution loop service
├── api/                        # OpenAPI specs (centralized)
├── docs/                       # Quick start, CLI, architecture docs
└── tests/
    ├── backend/
    └── algorithm/
```

---

## Development

```bash
make lint              # Python (flake8) + Go (gofmt)
make lint-only-diff    # Lint changed files only
```

- Go module: `backend/core` uses `module lazymind/core`
- Python: 3.11+, dependencies in `algorithm/requirements.txt` (`lazyllm[rag-advanced]`)
- OpenAPI specs live in `api/` — keep them in sync when adding routes

---

## License

See repository for license information.
