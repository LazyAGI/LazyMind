# Desktop package dependency audit

Source artifact: `LazyMind-macos-arm64-0.3.0-alpha.3-0c8ffcc9.dmg`

## Exported inventories

- `python-environment-inventory.csv`: all 250 installed Python distributions, versions, logical bytes, file counts, and `Requires-Dist` metadata.
- `go-binary-inventory.csv`: all packaged Go executables, file size, linked module count, text size, and Go PC/line table size.

## Python environment totals

| Environment | Distributions | Distribution-owned logical bytes |
| --- | ---: | ---: |
| algorithm | 182 | 1,023,963,843 |
| auth-service | 35 | 65,603,783 |
| channel-gateway | 33 | 86,859,961 |
| Total | 250 | 1,176,427,587 |

The mounted filesystem reports about 1.22 GiB for `deps/python`. The difference from the logical-byte inventory is filesystem allocation, metadata, scripts, symlinks, and files not owned through a wheel `RECORD`.

## High-confidence optimization candidates

### Replace the monolithic Volcengine SDK

`volcengine-python-sdk==5.0.47` occupies 198,879,634 bytes. LazyLLM only imports `volcenginesdkarkruntime` for the Doubao/Ark online model provider, but the distribution ships clients for a large number of unrelated Volcengine cloud services. Volcengine now publishes the separate `arkruntime` package; migrating the LazyLLM adapter to it should preserve Ark chat, image, video, embedding, and file APIs without shipping the monolithic SDK. This requires compatibility tests and is not a delete-only change.

### Replace UMAP in Skill Review

`umap-learn` is used only for Skill Review embedding clustering when there are more than 20 drafts. Its dependency chain occupies 138,441,138 bytes: llvmlite, numba, pynndescent, and umap-learn. Replacing UMAP with the already-installed scikit-learn PCA or clustering directly in embedding space removes this chain. It does not affect chat, document parsing, Milvus, or ordinary Skills.

### Remove packaging tools from the finished runtime

The three environments contain 17,290,761 bytes of `pip` and `wheel`. Desktop runtime repair/install uses the external `uv pip` command, not the embedded `pip` package. These can be pruned after the environments are completely assembled, subject to packaged startup tests. Algorithm's setuptools must remain because LazyLLM and the runtime manager use `pkg_resources`; auth/channel setuptools should be tested independently before removal.

### Audit and probably remove nbconvert

`nbconvert==7.17.1` is an explicit direct dependency, but there is no production import or invocation of nbconvert in LazyMind, LazyLLM runtime code, backend, local runtime manager, or workflows. The identifiable nbconvert/Jupyter conversion stack occupies at least 3,288,502 bytes. The IPYNB reader uses nbformat rather than nbconvert, so nbformat may still need to remain if notebook ingestion is supported.

## Necessary large dependency families

- `pyarrow` (125,153,814 bytes) is a direct runtime requirement of Milvus Lite 3.0, which uses Arrow/Parquet storage and is launched as a required Desktop vector-store service.
- `faiss-cpu`, `grpcio`, `numpy`, and `pyarrow` form the Milvus Lite vector database runtime used by knowledge ingestion and RAG.
- `PyMuPDF` is used by the built-in bid technical proposal workflow for PDF parsing.
- `jieba`, `nltk`, spaCy, BM25, scikit-learn, pandas, lxml, Pillow, Office readers, and PDF readers support document ingestion, text splitting, retrieval/reranking, and document/workflow tools. Some are feature-specific rather than startup-critical, but are not accidental build dependencies.
- PostgreSQL drivers appear in both psycopg2 and psycopg3 forms because current LazyLLM storage paths explicitly normalize some SQLAlchemy URLs to psycopg2 while other services use psycopg3. Consolidation requires code changes.

## Go binary findings

All Go executables are arm64-only and are already built with `-trimpath` and `-ldflags="-s -w"`. They are statically linked, so every process carries its own Go runtime, HTTP/TLS stack, type metadata, and PC/line tables.

| Binary | Bytes | Modules | Text | Go PC/line table |
| --- | ---: | ---: | ---: | ---: |
| core | 57,185,296 | 59 | 19,236,084 | 27,252,369 |
| caddy | 47,500,976 | 140 | 18,987,528 | 17,706,419 |
| process-compose | 42,917,184 | 82 | 13,976,340 | 11,692,632 |
| scan-control-plane | 25,177,728 | 29 | 11,353,508 | 9,170,126 |
| lazymind | 9,859,792 | 11 | 4,113,332 | 3,546,520 |
| file-watcher | 7,583,648 | 6 | 3,181,124 | 2,711,095 |
| local-runtime-manager | 7,075,504 | 1 | 3,030,932 | 2,619,017 |
| local-proxy | 6,953,776 | 1 | 2,907,668 | 2,480,391 |

The largest surprising component is `__gopclntab`: 27.3 MB in Core and 17.7 MB in Caddy. `-s -w` removes the ordinary symbol table and DWARF, but Go retains runtime function/PC/line metadata for stack unwinding, panic traces, reflection, and profiling.

Practical Go optimization order:

1. Replace full Caddy with the narrow reverse/static proxy behavior LazyMind actually needs, or build a deliberately minimal Caddy command. The stock Caddy command links 140 modules and costs 47.5 MB.
2. Replace process-compose with a smaller purpose-built supervisor, or move its required supervision behavior into local-runtime-manager. It costs 42.9 MB and links terminal UI, HTTP API, scheduler, OpenAPI, health, and other capabilities that Desktop may not use.
3. Profile Core package contribution. Pure-Go SQLite (`modernc`) and Parquet/compression stacks are major likely contributors. Removing unused database drivers or relocating history/package processing out of the always-shipped Core can reduce both code and PC/line metadata.
4. Evaluate merging small sidecars only where lifecycle/security boundaries permit. Static linking duplicates several megabytes of Go runtime metadata per executable; combining local-proxy or file-watcher with an existing process can save more than linker flag tuning.
5. `-buildid=` and newer linker flags provide only marginal savings. UPX is not recommended for a signed/notarized macOS application and much of its compression benefit is already captured by the compressed DMG.

