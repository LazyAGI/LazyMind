"""Phase 1 unit tests for HandleStore + tool integration.

Run: PYTHONPATH=. .venv/bin/python -m evo.tests.test_handle_store
"""

from __future__ import annotations

import json
import sys
import tempfile
import threading
import traceback
from pathlib import Path
from typing import Callable

from evo.conductor.handle_store import Handle, HandleStore
from evo.domain.tool_result import ToolResult
from evo.runtime.config import load_config
from evo.runtime.session import create_session, session_scope
from evo.tools import register_all
from evo.harness import data_loader


def _h(name: str) -> None:
    print(f"\n=== {name} ===")


def test_append_increments_and_caches() -> None:
    _h("HandleStore: append assigns sequential ids and caches")
    with tempfile.TemporaryDirectory() as td:
        store = HandleStore(Path(td) / "handles.jsonl")
        h1 = store.append("foo", {"a": 1}, {"x": 1})
        h2 = store.append("bar", {}, {"y": 2})
        h3 = store.append("foo", {"a": 2}, [1, 2, 3])
    assert (h1, h2, h3) == ("h_0001", "h_0002", "h_0003")
    assert store.get(h1).tool == "foo"
    assert store.get(h2).result == {"y": 2}
    assert len(store) == 3
    print("  -> OK")


def test_round_trip_jsonl() -> None:
    _h("HandleStore: jsonl payload is valid + has required fields")
    with tempfile.TemporaryDirectory() as td:
        path = Path(td) / "handles.jsonl"
        store = HandleStore(path)
        store.append("foo", {"a": 1}, {"x": 1})
        store.append("bar", {"k": [1, 2]}, "hello")
        lines = path.read_text(encoding="utf-8").strip().splitlines()
    assert len(lines) == 2
    rows = [json.loads(line) for line in lines]
    assert {r["h"] for r in rows} == {"h_0001", "h_0002"}
    for r in rows:
        assert {"h", "ts", "tool", "args", "result"}.issubset(r.keys())
    print("  -> OK")


def test_concurrent_append_no_loss() -> None:
    _h("HandleStore: concurrent append produces unique sequential ids")
    with tempfile.TemporaryDirectory() as td:
        store = HandleStore(Path(td) / "handles.jsonl")
        seen: list[str] = []
        lock = threading.Lock()

        def worker(n: int) -> None:
            for _ in range(n):
                hid = store.append("t", {}, n)
                with lock:
                    seen.append(hid)

        threads = [threading.Thread(target=worker, args=(20,)) for _ in range(5)]
        for t in threads:
            t.start()
        for t in threads:
            t.join()
    assert len(seen) == len(set(seen)) == 100
    assert sorted(seen)[0] == "h_0001"
    assert sorted(seen)[-1] == "h_0100"
    print("  -> OK")


def test_get_unknown_returns_none() -> None:
    _h("HandleStore: get(unknown) returns None")
    with tempfile.TemporaryDirectory() as td:
        store = HandleStore(Path(td) / "handles.jsonl")
        assert store.get("h_0001") is None
    print("  -> OK")


def test_session_attaches_handle_store() -> None:
    _h("create_session attaches a HandleStore at runs/<run_id>/handles.jsonl")
    cfg = load_config()
    session = create_session(cfg)
    assert session.handle_store is not None
    expected = cfg.storage.runs_dir / session.run_id / "handles.jsonl"
    assert session.handle_store.path == expected
    print(f"  store path: {session.handle_store.path}")
    print("  -> OK")


def test_tool_call_writes_handle_to_result_and_store() -> None:
    _h("Tool wrapper writes handle to ToolResult AND HandleStore")
    register_all()
    session = create_session(load_config())
    with session_scope(session):
        data_loader.load_corpus(session)
        from evo.tools.stats import summarize_metrics
        result = summarize_metrics()
    assert result.ok and result.handle is not None
    assert result.handle.startswith("h_")
    h = session.handle_store.get(result.handle)
    assert h is not None
    assert h.tool == "summarize_metrics"
    print(f"  handle={result.handle}; cached={len(session.handle_store)}")
    print("  -> OK")


def test_tool_failure_does_not_create_handle() -> None:
    _h("Failing tool does NOT consume a handle id")
    register_all()
    session = create_session(load_config())
    with session_scope(session):
        data_loader.load_corpus(session)
        from evo.tools.data import inspect_step_for_case
        result = inspect_step_for_case(
            dataset_id="this_does_not_exist", step_key="any",
        )
    assert not result.ok
    assert result.handle is None
    print("  -> OK")


def _run(tests: list[Callable[[], None]]) -> int:
    failures = 0
    for t in tests:
        try:
            t()
        except Exception:
            failures += 1
            print(f"FAILED: {t.__name__}")
            traceback.print_exc(limit=5)
    print(f"\n{len(tests) - failures}/{len(tests)} passed")
    return 0 if failures == 0 else 1


def main() -> int:
    return _run([
        test_append_increments_and_caches,
        test_round_trip_jsonl,
        test_concurrent_append_no_loss,
        test_get_unknown_returns_none,
        test_session_attaches_handle_store,
        test_tool_call_writes_handle_to_result_and_store,
        test_tool_failure_does_not_create_handle,
    ])


if __name__ == "__main__":
    sys.exit(main())
