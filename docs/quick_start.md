# Quick Start

This document covers two things only:

- How to configure environment variables
- How to start the services

All commands are run from the repository root by default.

## Prerequisites

- Docker and Docker Compose installed
- You are in the repository root
- If using a public cloud API model, have the corresponding API key ready
- If using an on-premises model, ensure the current machine can reach the internal service

## Environment Variables

### 1. Model configuration

Select a model config via `LAZYMIND_MODEL_CONFIG_PATH`. Three built-in shorthand values:

| Value | Description |
|-------|-------------|
| `dynamic` (default) | Key injected per request from frontend |
| `online` | Public cloud API (static config) |
| `inner` | On-premises / intranet deployment |

An explicit file path is also accepted.

For public cloud APIs, export the corresponding API key. The variable name must match the placeholder used in the config file. For example, if the config references `${LAZYLLM_SILICONFLOW_API_KEY}`, export that variable:

```bash
export LAZYLLM_SILICONFLOW_API_KEY=your-key
export LAZYMIND_MODEL_CONFIG_PATH=online
```

If the config references multiple providers, export all the corresponding keys at once. `docker-compose.yml` already passes through common LLM API key variables (`LAZYLLM_OPENAI_API_KEY`, `LAZYLLM_DEEPSEEK_API_KEY`, `LAZYLLM_SILICONFLOW_API_KEY`, etc.).

For on-premises models:

```bash
export LAZYMIND_MODEL_CONFIG_PATH=inner
```

### 2. OCR

OCR is disabled by default (built-in PDFReader is used):

```bash
export LAZYMIND_OCR_SERVER_TYPE=none   # default, can be omitted
```

To enable local MinerU:

```bash
export LAZYMIND_OCR_SERVER_TYPE=mineru
# LAZYMIND_OCR_SERVER_URL is auto-derived to http://mineru:8000 when not set
```

To reuse an existing MinerU deployed on ECS / intranet:

```bash
export LAZYMIND_OCR_SERVER_TYPE=mineru
export LAZYMIND_OCR_SERVER_URL=http://your-inner-mineru:port
```

When `LAZYMIND_OCR_SERVER_URL` points to an external address, `make up` will not start the local `mineru` profile.

To enable PaddleOCR (GPU required):

```bash
export LAZYMIND_OCR_SERVER_TYPE=paddleocr
# LAZYMIND_OCR_SERVER_URL is auto-derived to http://paddleocr:8080 when not set
```

### 3. Vector / segment stores

By default, Milvus and OpenSearch are deployed in-stack. To use external services:

```bash
export LAZYMIND_MILVUS_URI=http://your-milvus:19530
export LAZYMIND_OPENSEARCH_URI=https://your-opensearch:9200
export LAZYMIND_OPENSEARCH_USER=admin
export LAZYMIND_OPENSEARCH_PASSWORD=your-password
```

When the URIs stay at `http://milvus:19530` and `https://opensearch:9200`, the built-in services are deployed automatically.

### 4. Frontend port

The frontend defaults to port **8090**. Override if the port is occupied:

```bash
export LAZYMIND_FRONTEND_PORT=8080
```

### 5. Auth credentials (production)

Change these before deploying to production:

```bash
export LAZYMIND_JWT_SECRET=your-strong-secret
export LAZYMIND_BOOTSTRAP_ADMIN_USERNAME=admin
export LAZYMIND_BOOTSTRAP_ADMIN_PASSWORD=your-password
```

### 6. Using a `.env` file

All variables above can be placed in a `.env` file at the repository root. The Makefile loads it automatically:

```bash
# .env
LAZYMIND_MODEL_CONFIG_PATH=online
LAZYLLM_SILICONFLOW_API_KEY=your-key
LAZYMIND_OCR_SERVER_TYPE=none
LAZYMIND_FRONTEND_PORT=8090
```

---

## Starting Services

### Standard startup

```bash
make up
```

Starts all services in the background. Milvus and OpenSearch are deployed automatically.

### Build images and start

```bash
make up-build
```

Use this on first run or after changing Dockerfiles / dependencies.

### Start with specific services only

```bash
make up SERVICES=chat,core
```

### Start with MinerU OCR

```bash
export LAZYMIND_OCR_SERVER_TYPE=mineru
make up
```

### Start with PaddleOCR (GPU)

```bash
export LAZYMIND_OCR_SERVER_TYPE=paddleocr
make up
```

### Start with external Milvus / OpenSearch

```bash
make up \
  LAZYMIND_MILVUS_URI=http://your-milvus:19530 \
  LAZYMIND_OPENSEARCH_URI=https://your-opensearch:9200
```

### Enable store dashboards

```bash
make up LAZYMIND_ENABLE_STORE_DASHBOARDS=1
```

- Attu (Milvus): http://127.0.0.1:3000
- OpenSearch Dashboards: http://127.0.0.1:5601 (login: `admin` / `LAZYMIND_OPENSEARCH_PASSWORD`)

Dashboards bind to `127.0.0.1` only and are not started if the corresponding store is external.

---

## After Startup

| URL | Description |
|-----|-------------|
| http://localhost:8090 | Frontend (default port) |
| http://localhost:8000 | Kong API Gateway |
| http://localhost:8090/docs.html | Unified Swagger UI |
| http://localhost:8048 | evo API (self-evolution service) |

Default credentials: `admin` / `admin`

---

## Common Operations

Restart containers without rebuilding:

```bash
docker compose up -d --force-recreate
```

Stop services:

```bash
make down
```

Stop specific services:

```bash
make down SERVICES=chat,core
```

View service status:

```bash
docker compose ps
```

View logs:

```bash
docker compose logs --tail=200 -f
```

---

## Data Reset

### Reset knowledge base only

Wipes Milvus, OpenSearch, uploads, and KB-related PostgreSQL tables. User accounts, auth tokens, Redis, conversations, and prompts are **preserved**.

```bash
make reset-kb
make up LAZYMIND_RESET_ALGO_ON_STARTUP=true
```

`LAZYMIND_RESET_ALGO_ON_STARTUP=true` is required after `reset-kb` so the algo service rebuilds its schema tables on next startup.

### Fresh start (standard clean restart)

Equivalent to `reset-kb` + rebuild + start with algo reset:

```bash
make fresh-start
```

### Full reset (wipe everything)

Removes all persistent data including user accounts, auth tokens, Redis, and all volumes. Equivalent to a clean first-run state:

```bash
make reset-all
make up-build
```

### Clear containers and volumes

Stop services, remove all volumes, and clear Python cache (keeps built images):

```bash
make clear
make up-build
```

---

## Complete Startup Examples

### Public cloud API model

```bash
export LAZYLLM_SILICONFLOW_API_KEY=your-key
export LAZYMIND_MODEL_CONFIG_PATH=online
export LAZYMIND_OCR_SERVER_TYPE=none

make up-build
```

### On-premises model + local MinerU

```bash
export LAZYMIND_MODEL_CONFIG_PATH=inner
export LAZYMIND_OCR_SERVER_TYPE=mineru

make up-build
```

### On-premises model + external MinerU

```bash
export LAZYMIND_MODEL_CONFIG_PATH=inner
export LAZYMIND_OCR_SERVER_TYPE=mineru
export LAZYMIND_OCR_SERVER_URL=http://your-inner-mineru:port

make up-build
```

### On-premises model + external Milvus / OpenSearch

```bash
export LAZYMIND_MODEL_CONFIG_PATH=inner
export LAZYMIND_MILVUS_URI=http://your-milvus:19530
export LAZYMIND_OPENSEARCH_URI=https://your-opensearch:9200
export LAZYMIND_OPENSEARCH_USER=admin
export LAZYMIND_OPENSEARCH_PASSWORD=your-password

make up-build
```
