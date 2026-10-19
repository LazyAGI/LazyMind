# Algorithm Unit Tests

Tests are organized to mirror `LazyMind/algorithm`:

- `chat/`
- `lazyllm/`
- `parsing/`
- `processor/`

## Setup

```bash
pip install -r ../../algorithm/requirements.txt
pip install pytest httpx
```

## Run

From project root:

```bash
python -m pytest tests/algorithm/ -v
```

## Strategy

- `processor/db`: Pure functions, no mocks.
- `chat`: Component and pipeline regression tests.
- `parsing` / `processor`: Service-heavy parts should prefer integration tests or docker-compose.

## Local container verification on macOS

Run the startup timing tests from source on the existing container's own filesystem.
Imports through a macOS bind mount include host filesystem latency; a mounted-source
measurement is not comparable to the native Linux/Windows CI measurement. Keep the
3-second chat startup threshold and 30-second tool-doc subprocess timeout unchanged.

From the PR worktree, copy only tracked test inputs (including the exact LazyLLM
submodule revision). Do not copy `.env`, credentials, runtime data, virtualenvs,
dependency caches or AppleDouble (`._*`) files. The following uses the existing
chat container and its installed dependencies; it neither deploys nor restarts it:

```bash
set -euo pipefail
snapshot=$(docker exec lazymind-chat-1 mktemp -d /tmp/lazymind-tests.XXXXXX)
git archive HEAD algorithm tests/algorithm tests/fixtures tests/contracts workflows skills pytest.ini |
  docker exec -i lazymind-chat-1 tar -xf - -C "$snapshot"
git -C algorithm/lazyllm archive HEAD |
  docker exec -i lazymind-chat-1 tar -xf - -C "$snapshot/algorithm/lazyllm"
# If validating these two tests before committing, overlay their working copies.
docker cp tests/algorithm/chat/test_lazyllm_tool_docs.py "lazymind-chat-1:$snapshot/tests/algorithm/chat/"
docker cp tests/algorithm/chat/test_runtime_loader.py "lazymind-chat-1:$snapshot/tests/algorithm/chat/"
docker exec -w "$snapshot" \
  -e PYTHONPATH="$snapshot/algorithm/lazyllm:$snapshot/algorithm" \
  -e LAZYLLM_TRACE_LOCAL_STORAGE_DIR="$snapshot/traces" \
  lazymind-chat-1 python -c 'import pathlib, lazyllm, lazymind
root = pathlib.Path.cwd()
for module in (lazyllm, lazymind):
    source = pathlib.Path(module.__file__).resolve()
    assert source.is_relative_to(root), source
    print(source)'
docker exec -w "$snapshot" \
  -e PYTHONPATH="$snapshot/algorithm/lazyllm:$snapshot/algorithm" \
  -e LAZYLLM_TRACE_LOCAL_STORAGE_DIR="$snapshot/traces" \
  -e LAZYMIND_RUNTIME_MODE=cloud \
  lazymind-chat-1 python -m pytest \
  tests/algorithm/chat/test_runtime_loader.py \
  tests/algorithm/chat/test_lazyllm_tool_docs.py \
  tests/algorithm/chat/test_document_directories.py \
  tests/algorithm/chat/test_document_folders.py -q
```

The two subprocess probes override any inherited trace location with a temporary
directory whose lifetime covers child shutdown. Tracing stays enabled. This avoids
production trace scanning during exit and never requires removing user traces.
For the full algorithm suite, replace the explicit test paths with `tests/algorithm/`.
`LAZYMIND_RUNTIME_MODE=cloud` matches the default CI environment; local-mode tests
select that mode explicitly. This prevents an existing service's environment from
silently changing unrelated test assumptions. Retain the command, source SHAs,
import paths and output with the acceptance record; remove only this snapshot when
its evidence has been saved.

### Startup failure diagnostics

The chat startup test keeps its 3-second wall-clock limit around
`ensure_chat_runtime()`. If the timing or unloaded-RAG/docs assertions fail, the
assertion includes a structured report with runtime-loader import time, chat
runtime import time, chat runtime CPU time, Python/OS/architecture information,
installed dependency versions and the actual imported module paths. Installed
package metadata can differ from a checked-out source override; use the module
paths to identify which code ran.

Metadata collection occurs after timing and after capturing the RAG/docs state.
Successful tests emit no diagnostic output. The probe does not dump environment
variables, credentials, business data or raw child-process logs, and it does not
use per-module `importtime` instrumentation. This is permanent test-only
observability, with no changes to production startup or automatic retries.
