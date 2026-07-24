from __future__ import annotations

import re
import shutil
from collections.abc import Mapping
from pathlib import Path
from typing import Any

from evo.artifact_runtime.evo import catalog as C
from evo.artifact_runtime.kernel import SQLiteArtifactStore

from .router_ledger import RouterAlgorithmLedger

THREAD_ID = re.compile(r'[A-Za-z0-9][A-Za-z0-9_.-]{0,127}')

ALGORITHM_ARTIFACTS = {
    C.ABTEST_CANDIDATE_SERVICE,
    C.ABTEST_CANDIDATE_RAG_ANSWER,
    C.ABTEST_CANDIDATE_JUDGE_RESULT,
    C.ABTEST_CANDIDATE_EVAL_SUMMARY,
    C.ABTEST_COMPARISON,
}


def delete_algorithm_artifacts(store_root: Path, run_id: str, algorithm_id: str) -> int:
    store = SQLiteArtifactStore(store_root)
    try:
        records = {}
        for event in store.events_since(0, run_id):
            for ref in event.refs:
                if ref.key.artifact_id not in ALGORITHM_ARTIFACTS or ref in records:
                    continue
                record = store.get(run_id, ref)
                if record is not None:
                    records[ref] = record
        selected = {
            ref for ref, record in records.items()
            if ref.key.artifact_id == C.ABTEST_CANDIDATE_SERVICE
            and isinstance(record.value, Mapping)
            and record.value.get('algorithm_id') == algorithm_id
        }
        changed = True
        while changed:
            changed = False
            for ref, record in records.items():
                if ref in selected:
                    inputs = {
                        item for item in record.input_refs.values()
                        if item.key.artifact_id in ALGORITHM_ARTIFACTS
                    }
                    before = len(selected)
                    selected.update(inputs)
                    changed |= len(selected) != before
                elif any(item in selected for item in record.input_refs.values()):
                    selected.add(ref)
                    changed = True
        if not selected:
            return 0
        deleted = store.delete_artifacts(
            run_id,
            refs=tuple(sorted(selected)),
            idempotency_key=f'delete-router-algorithm:{algorithm_id}',
        )
        return len(deleted)
    finally:
        store.close()


def delete_managed_workspace(
    row: Mapping[str, Any],
    ledger: RouterAlgorithmLedger,
    managed_repair_root: Path,
) -> str:
    root = managed_repair_root.resolve()
    workspace = _managed_workspace(row, root)
    if workspace is None:
        return 'retained_external'
    code_path = Path(str(row.get('code_path') or '')).resolve()
    for other in ledger.list_algorithms():
        if (
            other['algorithm_id'] != row['algorithm_id']
            and Path(str(other.get('code_path') or '')).resolve() == code_path
        ):
            return 'retained_shared'
    if not workspace.exists():
        return 'missing'
    shutil.rmtree(workspace)
    parent = workspace.parent
    while parent != root:
        try:
            parent.rmdir()
        except OSError:
            break
        parent = parent.parent
    return 'deleted'


def _managed_workspace(row: Mapping[str, Any], root: Path) -> Path | None:
    thread_id = str(row.get('thread_id') or '')
    if THREAD_ID.fullmatch(thread_id) is None:
        return None
    code_path = Path(str(row.get('code_path') or '')).resolve()
    try:
        workspace = code_path.parents[2]
    except IndexError:
        return None
    expected = workspace / 'algorithm' / 'lazymind' / 'chat'
    thread_root = root / thread_id
    if (
        workspace.name != 'candidate'
        or code_path != expected
        or not workspace.is_relative_to(root)
        or not workspace.is_relative_to(thread_root)
    ):
        return None
    return workspace
