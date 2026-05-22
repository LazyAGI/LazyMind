"""Phase 2 unit tests for WorldModel persistence + pipeline snapshot.

Run: PYTHONPATH=. .venv/bin/python -m evo.tests.test_world_model
"""

from __future__ import annotations

import json
import sys
import tempfile
import threading
import traceback
from pathlib import Path
from typing import Callable

from evo.conductor.world_model import (
    Finding, Hypothesis, WorldModel, WorldModelStore, WORLD_MODEL_VERSION,
)


def _h(name: str) -> None:
    print(f"\n=== {name} ===")


def test_create_writes_initial_file() -> None:
    _h("WorldModelStore: constructor writes initial json file")
    with tempfile.TemporaryDirectory() as td:
        path = Path(td) / "world_model.json"
        WorldModelStore(path, run_id="r1")
        assert path.exists()
        data = json.loads(path.read_text(encoding="utf-8"))
        assert data["run_id"] == "r1" and data["version"] == WORLD_MODEL_VERSION
        assert data["status"] == "initializing" and data["hypotheses"] == []
    print("  -> OK")


def test_update_persists_and_reloads() -> None:
    _h("WorldModelStore: update mutates and reloads identically")
    with tempfile.TemporaryDirectory() as td:
        path = Path(td) / "world_model.json"
        store = WorldModelStore(path, run_id="r2")
        store.update(lambda w: w.hypotheses.append(Hypothesis(id="H001", claim="x")))
        store.update(lambda w: w.findings.append(Finding(
            id="F001", hypothesis_id="H001", claim="confirmed", verdict="confirmed",
        )))
        del store
        store2 = WorldModelStore(path, run_id="r2")
    assert len(store2.world.hypotheses) == 1
    assert store2.world.hypotheses[0].claim == "x"
    assert len(store2.world.findings) == 1
    assert store2.world.findings[0].verdict == "confirmed"
    print("  -> OK")


def test_concurrent_update_no_loss() -> None:
    _h("WorldModelStore: parallel update appends are sequenced")
    with tempfile.TemporaryDirectory() as td:
        store = WorldModelStore(Path(td) / "world_model.json", run_id="r3")

        def worker(start: int) -> None:
            for i in range(20):
                hid = f"H{start * 100 + i:04d}"
                store.update(lambda w, h=hid: w.hypotheses.append(Hypothesis(id=h, claim=h)))

        threads = [threading.Thread(target=worker, args=(k,)) for k in range(5)]
        for t in threads:
            t.start()
        for t in threads:
            t.join()
    assert len(store.world.hypotheses) == 100
    ids = {h.id for h in store.world.hypotheses}
    assert len(ids) == 100
    print("  -> OK")


def test_run_id_mismatch_starts_fresh() -> None:
    _h("WorldModelStore: existing file with different run_id is reset")
    with tempfile.TemporaryDirectory() as td:
        path = Path(td) / "world_model.json"
        store_a = WorldModelStore(path, run_id="r-a")
        store_a.update(lambda w: w.hypotheses.append(Hypothesis(id="H1", claim="x")))
        del store_a
        store_b = WorldModelStore(path, run_id="r-b")
    assert store_b.world.run_id == "r-b"
    assert store_b.world.hypotheses == []
    print("  -> OK")


def test_session_attaches_world_store() -> None:
    _h("create_session attaches WorldModelStore at runs/<run_id>/world_model.json")
    from evo.runtime.config import load_config
    from evo.runtime.session import create_session
    cfg = load_config()
    session = create_session(cfg)
    assert session.world_store is not None
    expected = cfg.storage.runs_dir / session.run_id / "world_model.json"
    assert session.world_store.path == expected
    assert session.world_store.world.run_id == session.run_id
    print("  -> OK")


def test_synthesis_result_serializes_with_asdict() -> None:
    _h("SynthesisResult / VerifiedAction: asdict round-trip preserves fields")
    from dataclasses import asdict
    from evo.conductor.synthesis import SynthesisResult, VerifiedAction

    a = VerifiedAction(
        id="A1", finding_id="F1", hypothesis_id="H1",
        hypothesis_category="rerank_failure",
        title="t", rationale="r", suggested_changes="s",
        priority="P0", expected_impact_metric="m",
        expected_direction="+", confidence=0.85,
        evidence_handles=["h_0001"], validity_score=0.9,
        supporting_evidence=["s1"], verifier_notes=["n1"],
    )
    r = SynthesisResult(summary="s", guidance="g", actions=[a],
                        open_gaps=["q1"], iterations=2)
    d = asdict(r)
    assert d["iterations"] == 2
    assert d["actions"][0]["validity_score"] == 0.9
    assert d["actions"][0]["evidence_handles"] == ["h_0001"]
    assert d["open_gaps"] == ["q1"]
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
        test_create_writes_initial_file,
        test_update_persists_and_reloads,
        test_concurrent_update_no_loss,
        test_run_id_mismatch_starts_fresh,
        test_session_attaches_world_store,
        test_synthesis_result_serializes_with_asdict,
    ])


if __name__ == "__main__":
    sys.exit(main())
