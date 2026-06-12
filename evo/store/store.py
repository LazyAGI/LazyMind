from __future__ import annotations

import json
import os
import shutil
import threading
import time
from contextlib import contextmanager
from dataclasses import asdict, replace
from datetime import datetime, timezone
from pathlib import Path
from typing import Any, Callable

from ..artifacts import ArtifactDraft, ArtifactFragment, ArtifactGraph, ArtifactRef
from ..artifacts.graph import _lock_file, _unlock_file
from .. import validate_id
from ..operations import OperationGraph, OperationRunSnapshot
from ..runtime.models import CallRecord
from .models import Event, RecoveryReport
from .run_lifecycle import StoreRunLifecycle, settle_lifecycle


class EvoStore:
    _json_lock = threading.RLock()

    def __init__(self, root: str | Path):
        self.root = Path(root)
        self.runs_dir = self.root / 'runs'
        self.runs_dir.mkdir(parents=True, exist_ok=True)

    def create_run(self, run_id: str, data: dict[str, Any] | None = None) -> Path:
        validate_id(run_id, 'run_id')
        run_dir = self.ensure_run_dirs(run_id)
        payload = {'run_id': run_id, 'status': 'running', 'started_at': _now(), **(data or {})}
        self.atomic_write_json(run_dir / 'run.json', payload)
        if payload['status'] == 'running':
            self.append_event(Event('run.started', run_id, {'status': 'running'}))
        return run_dir

    def ensure_run_dirs(self, run_id: str) -> Path:
        validate_id(run_id, 'run_id')
        run_dir = self.run_dir(run_id)
        for name in ('operations', 'calls', 'checkpoints', 'artifacts', 'snapshots', 'indexes', 'projections', 'tmp'):
            (run_dir / name).mkdir(parents=True, exist_ok=True)
        return run_dir

    def acquire_active_run(self, run_id: str) -> None:
        validate_id(run_id, 'run_id')
        lock_path = self.root / 'lock'
        if lock_path.exists() and lock_path.read_text(encoding='utf-8').strip() != run_id:
            raise RuntimeError(f'active run already locked: {lock_path.read_text(encoding="utf-8").strip()}')
        self.root.mkdir(parents=True, exist_ok=True)
        tmp = self.root / 'lock.tmp'
        tmp.write_text(run_id, encoding='utf-8')
        tmp.replace(lock_path)

    def release_active_run(self, run_id: str) -> None:
        validate_id(run_id, 'run_id')
        lock_path = self.root / 'lock'
        if lock_path.exists() and lock_path.read_text(encoding='utf-8').strip() == run_id:
            lock_path.unlink()

    def delete_run(self, run_id: str) -> bool:
        validate_id(run_id, 'run_id')
        if self.active_run_id() == run_id: self.release_active_run(run_id)
        run_dir = self.run_dir(run_id)
        if not run_dir.exists(): return False
        shutil.rmtree(run_dir)
        return True

    def active_run_id(self) -> str | None:
        lock_path = self.root / 'lock'
        return lock_path.read_text(encoding='utf-8').strip() if lock_path.exists() else None

    def artifact_graph(self, run_id: str) -> ArtifactGraph:
        validate_id(run_id, 'run_id')
        self.ensure_run_dirs(run_id)
        return ArtifactGraph(self.run_dir(run_id))

    def write_operation(self, run_id: str, operation_run_id: str, data: dict[str, Any]) -> None:
        validate_id(operation_run_id, 'operation_run_id')
        self.ensure_run_dirs(run_id)
        path = self.operation_path(run_id, operation_run_id)
        operations = self._operations_index(run_id)
        operations[operation_run_id] = {'operation_run_id': operation_run_id, **data}
        self.atomic_write_json(path, operations)

    def finalize_operation_commit(self, run_id: str, tx_dir: Path) -> list[ArtifactRef]:
        tx_path = tx_dir / 'tx.json'
        tx = self.read_json(tx_path)
        operation_run_id = tx['operation_run_id']
        artifact_graph = self.artifact_graph(run_id)
        drafts = [_draft_from_tx_item(tx_dir, item) for item in tx.get('artifacts', [])]
        output_refs = _existing_tx_refs(artifact_graph, tx, drafts)
        if len(output_refs) < len(drafts):
            output_refs = _finalize_missing_drafts(artifact_graph, drafts, output_refs)
            tx['committed_refs'] = tx['output_refs'] = [str(ref) for ref in output_refs]
            self.atomic_write_json(tx_path, tx)
        operation_state = dict(tx.get('operation_state') or {})
        if operation_state:
            operation_state['output_refs'] = [str(ref) for ref in output_refs]
            self.write_operation(run_id, operation_run_id, operation_state)
        shutil.rmtree(tx_dir, ignore_errors=True)
        return output_refs

    def read_operation(self, run_id: str, operation_run_id: str) -> dict[str, Any]:
        validate_id(operation_run_id, 'operation_run_id')
        operations = self._operations_index(run_id)
        if operation_run_id in operations: return dict(operations[operation_run_id])
        legacy = self._legacy_operation_path(run_id, operation_run_id)
        if legacy.exists(): return self.read_json(legacy)
        raise FileNotFoundError(str(self.operation_path(run_id, operation_run_id)))

    def list_operations(self, run_id: str) -> list[dict[str, Any]]:
        return list(self._operation_records(run_id).values())

    def write_checkpoint(self, run_id: str, checkpoint_id: str, data: dict[str, Any]) -> None:
        self.ensure_run_dirs(run_id)
        self.atomic_write_json(self.run_dir(run_id) / 'checkpoints' / f'{checkpoint_id}.json', data)

    def append_event(self, event: Event) -> None:
        self.ensure_run_dirs(event.run_id)
        self._append_sequenced_jsonl(event.run_id, self.run_dir(event.run_id) / 'events.jsonl', asdict(event))

    def append_call(self, run_id: str, operation_run_id: str, record: CallRecord,
                    record_ref_factory: Callable[[CallRecord], str] | None = None) -> CallRecord:
        self.ensure_run_dirs(run_id)
        validate_id(operation_run_id, 'operation_run_id')
        persisted: dict[str, CallRecord] = {}

        def row(sequence: int) -> dict[str, Any]:
            sequenced = replace(record, sequence=sequence)
            if record_ref_factory is not None:
                sequenced = replace(sequenced, record_ref=record_ref_factory(sequenced))
            persisted['record'] = sequenced
            return asdict(sequenced)

        self._append_sequenced_jsonl(run_id, self.call_log_path(run_id, operation_run_id), row)
        return persisted['record']

    def read_calls(self, run_id: str, operation_run_id: str | None = None) -> list[CallRecord]:
        records = [CallRecord(**row) for row in self._read_jsonl(self.run_dir(run_id) / 'calls.jsonl')]
        if operation_run_id is None: return records
        validate_id(operation_run_id, 'operation_run_id')
        return [record for record in records if record.operation_run_id == operation_run_id]

    def read_events(self, run_id: str) -> list[Event]:
        return [Event(**row) for row in self._read_jsonl(self.run_dir(run_id) / 'events.jsonl')]

    def _read_jsonl(self, path: Path) -> list[dict[str, Any]]:
        if not path.exists(): return []
        rows = [(index, json.loads(line)) for index, line in enumerate(path.read_text(encoding='utf-8').splitlines())
                if line.strip()]
        rows.sort(key=lambda item: (item[1].get('sequence') or item[0] + 1, item[0]))
        return [row for _, row in rows]

    def operation_history(self, run_id: str) -> list[dict[str, Any]]:
        return [event.payload for event in self.read_events(run_id) if event.event_type.startswith('operation.')]

    def restore_operation_graph(self, run_id: str) -> OperationGraph:
        graph = OperationGraph()
        for data in self._operation_records(run_id).values():
            graph.restore_run(_operation_snapshot(data))
        return graph

    def recover_run(self, run_id: str) -> RecoveryReport:
        validate_id(run_id, 'run_id')
        run_dir = self.run_dir(run_id)
        running_operations = [str(data.get('operation_run_id') or operation_id) for operation_id, data
                              in self._operation_records(run_id).items() if data.get('status') == 'running']
        self.rebuild_indexes(run_id)
        self._finalize_pending_commit_txs(run_id)
        self._checkpoint_running_operations(run_id)
        self._settle_run_lifecycle(run_id)
        removed_tmp_files = self._clear_tmp(run_dir / 'tmp')
        rebuilt = self.rebuild_indexes(run_id)
        artifact_report = ArtifactGraph(run_dir).validate_visible_artifacts()
        _rebuild_frontend_state(self, run_id)
        return RecoveryReport(
            run_id=run_id, active_run_id=self.active_run_id(), running_operations=running_operations,
            latest_checkpoint_id=self._latest_checkpoint_id(run_id), removed_tmp_files=removed_tmp_files,
            artifact_indexes_rebuilt=rebuilt, invalid_artifacts=artifact_report.invalid_artifacts,
            orphan_blobs=artifact_report.orphan_blobs, orphan_fragments=artifact_report.orphan_fragments,
            producer_mismatches=self._producer_mismatches(run_id),
        )

    def rebuild_indexes(self, run_id: str) -> bool:
        validate_id(run_id, 'run_id')
        ArtifactGraph(self.run_dir(run_id)).rebuild_indexes()
        return True

    def run_dir(self, run_id: str) -> Path:
        validate_id(run_id, 'run_id')
        return self.runs_dir / run_id

    def operation_path(self, run_id: str, operation_run_id: str) -> Path:
        validate_id(operation_run_id, 'operation_run_id')
        return self.operations_index_path(run_id)

    def operations_index_path(self, run_id: str) -> Path:
        validate_id(run_id, 'run_id')
        return self.run_dir(run_id) / 'operations.json'

    def call_log_path(self, run_id: str, operation_run_id: str) -> Path:
        validate_id(operation_run_id, 'operation_run_id')
        return self.run_dir(run_id) / 'calls.jsonl'

    def relative_to_run(self, run_id: str, path: Path) -> str:
        return str(path.relative_to(self.run_dir(run_id)))

    def atomic_write_json(self, path: Path, data: dict[str, Any]) -> None:
        path.parent.mkdir(parents=True, exist_ok=True)
        tmp = path.with_suffix(f'{path.suffix}.{os.getpid()}.{time.time_ns()}.tmp')
        text = json.dumps(data, ensure_ascii=False, indent=2, sort_keys=True)
        with self._json_lock:
            tmp.write_text(text, encoding='utf-8')
            tmp.replace(path)

    def read_json(self, path: Path) -> dict[str, Any]:
        with self._json_lock:
            return json.loads(path.read_text(encoding='utf-8'))

    def _append_sequenced_jsonl(self, run_id: str, path: Path,
                                row: dict[str, Any] | Callable[[int], dict[str, Any]]) -> int:
        validate_id(run_id, 'run_id')
        path.parent.mkdir(parents=True, exist_ok=True)
        with self._json_lock:
            with _file_lock(self.run_dir(run_id) / '.jsonl_sequence.lock'):
                sequence = self._next_sequence_unlocked(run_id)
                payload = {**(row(sequence) if callable(row) else row), 'sequence': sequence}
                with path.open('a', encoding='utf-8') as handle:
                    handle.write(json.dumps(payload, ensure_ascii=False, sort_keys=True) + '\n')
                return sequence

    def _next_sequence_unlocked(self, run_id: str) -> int:
        sequence_path = self.run_dir(run_id) / 'sequence.json'
        current = 0
        if sequence_path.exists():
            current = int(json.loads(sequence_path.read_text(encoding='utf-8')).get('last_sequence', 0))
        if current == 0: current = self._existing_sequence_floor_unlocked(run_id)
        sequence = current + 1
        tmp = sequence_path.with_suffix(f'{sequence_path.suffix}.{os.getpid()}.{time.time_ns()}.tmp')
        tmp.write_text(json.dumps({'last_sequence': sequence}, ensure_ascii=False, indent=2, sort_keys=True),
                       encoding='utf-8')
        tmp.replace(sequence_path)
        return sequence

    def _existing_sequence_floor_unlocked(self, run_id: str) -> int:
        max_sequence = 0
        row_count = 0
        for path in (self.run_dir(run_id) / 'events.jsonl', self.run_dir(run_id) / 'calls.jsonl'):
            if not path.exists(): continue
            for line in path.read_text(encoding='utf-8').splitlines():
                if not line.strip(): continue
                row_count += 1
                try:
                    sequence = int(json.loads(line).get('sequence') or 0)
                except (TypeError, ValueError, json.JSONDecodeError):
                    sequence = 0
                max_sequence = max(max_sequence, sequence)
        return max(max_sequence, row_count)

    def _checkpoint_running_operations(self, run_id: str) -> None:
        for operation_id, data in self._operation_records(run_id).items():
            if data.get('status') != 'running': continue
            data['status'] = 'checkpointed'
            data['checkpointed_at'] = _now()
            self.write_operation(run_id, operation_id, data)
            self.append_event(Event('operation.checkpointed', run_id,
                                    {'before': None, 'after': data, 'reason': 'recover_run'}))

    def _settle_run_lifecycle(self, run_id: str) -> None:
        run_path = self.run_dir(run_id) / 'run.json'
        run = self.read_json(run_path) if run_path.exists() else {}
        if run.get('status') in {'cancelled', 'failed', 'ended'} or _has_dispatch_block(run):
            _rebuild_frontend_state(self, run_id)
            return
        state = self.restore_operation_graph(run_id).schedule_state()
        settle_lifecycle(StoreRunLifecycle(self, run_id), state, mark_running_when_idle=True)

    def _operation_records(self, run_id: str) -> dict[str, dict[str, Any]]:
        out = self._operations_index(run_id)
        operation_dir = self.run_dir(run_id) / 'operations'
        for path in sorted(operation_dir.glob('*.json')) if operation_dir.exists() else []:
            try:
                data = self.read_json(path)
            except (OSError, json.JSONDecodeError):
                continue
            out.setdefault(str(data.get('operation_run_id') or path.stem), data)
        return out

    def _operations_index(self, run_id: str) -> dict[str, dict[str, Any]]:
        path = self.operations_index_path(run_id)
        if not path.exists(): return {}
        data = self.read_json(path)
        if not isinstance(data, dict): raise ValueError(f'operation index must be object: {path}')
        return {str(k): dict(v) for k, v in data.items() if isinstance(v, dict)}

    def _legacy_operation_path(self, run_id: str, operation_run_id: str) -> Path:
        return self.run_dir(run_id) / 'operations' / f'{operation_run_id}.json'

    def _finalize_pending_commit_txs(self, run_id: str) -> None:
        tmp_dir = self.run_dir(run_id) / 'tmp'
        if not tmp_dir.exists(): return
        for tx_dir in sorted(tmp_dir.glob('drafts/*/tx')):
            if (tx_dir / 'tx.json').exists():
                self.finalize_operation_commit(run_id, tx_dir)

    def _latest_checkpoint_id(self, run_id: str) -> str | None:
        checkpoint_dir = self.run_dir(run_id) / 'checkpoints'
        if not checkpoint_dir.exists(): return None
        checkpoints = sorted(checkpoint_dir.glob('*.json'), key=lambda path: path.stat().st_mtime)
        return checkpoints[-1].stem if checkpoints else None

    def _clear_tmp(self, tmp_dir: Path) -> list[str]:
        if not tmp_dir.exists(): return []
        removed = sorted(str(path.relative_to(tmp_dir)) for path in tmp_dir.rglob('*') if path.is_file())
        shutil.rmtree(tmp_dir)
        tmp_dir.mkdir(parents=True, exist_ok=True)
        return removed

    def _producer_mismatches(self, run_id: str) -> list[dict[str, Any]]:
        mismatches: list[dict[str, Any]] = []
        artifact_graph = ArtifactGraph(self.run_dir(run_id))
        for manifest_path in sorted(artifact_graph.manifest_dir.glob('*.json')):
            manifest = self.read_json(manifest_path)
            artifact_id = manifest.get('artifact_id', manifest_path.stem)
            for version in manifest.get('versions', []):
                if version.get('role', 'operation_output') != 'operation_output': continue
                producer = version.get('producer_operation_run_id', '')
                if not producer: continue
                ref = '{}@v{}'.format(artifact_id, int(version['version']))
                try:
                    operation = self.read_operation(run_id, producer)
                except FileNotFoundError:
                    mismatches.append({'artifact_ref': ref, 'producer_operation_run_id': producer,
                                       'reason': 'producer_operation_missing'})
                    continue
                if ref not in operation.get('output_refs', []):
                    mismatches.append({'artifact_ref': ref, 'producer_operation_run_id': producer,
                                       'reason': 'producer_missing_output_ref'})
        return mismatches


def _now() -> str:
    return datetime.now(timezone.utc).isoformat()


def _has_dispatch_block(run: dict[str, Any]) -> bool:
    return any(run.get(key) for key in ('dispatch_block_reason', 'blocked_operations', 'root_blockers',
                                        'impacted_blockers'))


@contextmanager
def _file_lock(path: Path):
    path.parent.mkdir(parents=True, exist_ok=True)
    with path.open('a+', encoding='utf-8') as handle:
        _lock_file(handle)
        try:
            yield
        finally:
            _unlock_file(handle)


def _draft_from_tx_item(tx_dir: Path, item: dict[str, Any]) -> ArtifactDraft:
    draft_payload = json.loads((tx_dir / item['draft_ref']).read_text(encoding='utf-8'))
    return ArtifactDraft(
        artifact_id=item['artifact_id'], schema_name=item['schema_name'], payload=draft_payload['payload'],
        producer_operation_run_id=item['producer_operation_run_id'],
        input_refs=[ArtifactRef.parse(value) for value in item.get('input_refs', [])],
        fragments=[ArtifactFragment(**fragment) for fragment in draft_payload.get('fragments', [])],
        role=item.get('role', 'operation_output'),
    )


def _existing_tx_refs(artifact_graph: ArtifactGraph, tx: dict[str, Any],
                      drafts: list[ArtifactDraft]) -> list[ArtifactRef]:
    refs = [ArtifactRef.parse(value) for value in tx.get('committed_refs') or tx.get('output_refs', [])]
    existing: list[ArtifactRef] = []
    for ref, draft in zip(refs, drafts):
        if not _ref_matches_draft(artifact_graph, ref, draft): break
        existing.append(ref)
    return existing


def _finalize_missing_drafts(artifact_graph: ArtifactGraph, drafts: list[ArtifactDraft],
                             existing_refs: list[ArtifactRef]) -> list[ArtifactRef]:
    output_refs: list[ArtifactRef | None] = list(existing_refs)
    used = set(existing_refs)
    for draft in drafts[len(existing_refs):]:
        recovered = _recover_committed_ref(artifact_graph, draft, used)
        output_refs.append(recovered)
        if recovered is not None:
            used.add(recovered)
    missing = [(index, draft) for index, draft in enumerate(drafts)
               if index >= len(output_refs) or output_refs[index] is None]
    committed = artifact_graph.commit_artifacts([draft for _, draft in missing])
    for (index, _), ref in zip(missing, committed):
        output_refs[index] = ref
    return [ref for ref in output_refs if ref is not None]


def _recover_committed_ref(artifact_graph: ArtifactGraph, draft: ArtifactDraft,
                           used_refs: set[ArtifactRef]) -> ArtifactRef | None:
    produced = sorted(artifact_graph.produced_by(draft.producer_operation_run_id),
                      key=lambda ref: (ref.artifact_id, ref.version))
    for ref in produced:
        if ref not in used_refs and _ref_matches_draft(artifact_graph, ref, draft): return ref
    return None


def _ref_matches_draft(artifact_graph: ArtifactGraph, ref: ArtifactRef, draft: ArtifactDraft) -> bool:
    if ref.artifact_id != draft.artifact_id: return False
    try:
        metadata = artifact_graph.version_metadata(ref)
        return (
            metadata.get('schema_name') == draft.schema_name
            and metadata.get('producer_operation_run_id') == draft.producer_operation_run_id
            and metadata.get('role') == draft.role
            and artifact_graph.get(ref) == draft.payload
            and artifact_graph.upstream(ref) == set(draft.input_refs)
            and _fragment_keys(artifact_graph.fragments(ref)) == _fragment_keys(draft.fragments)
        )
    except (FileNotFoundError, KeyError):
        return False


def _fragment_keys(fragments: list[ArtifactFragment]) -> list[tuple[str, str, str, str]]:
    return sorted((item.fragment_id, item.json_pointer, item.kind, item.label) for item in fragments)


def _operation_snapshot(data: dict[str, Any]) -> OperationRunSnapshot:
    return OperationRunSnapshot(
        operation_run_id=data['operation_run_id'],
        operation_id=data.get('operation_id') or data['operation_run_id'],
        operation_type=data.get('operation_type', ''), status=data.get('status', 'pending'),
        attempt=int(data.get('attempt', 1)), category=data.get('category', 'pipeline'),
        flow_tag=data.get('flow_tag', ''), stage_tag=data.get('stage_tag', ''),
        input_refs=list(data.get('input_refs', [])), output_refs=list(data.get('output_refs', [])),
        depends_on=list(data.get('depends_on', [])), parent=data.get('parent', ''),
        source_message_id=data.get('source_message_id', ''), superseded_by=data.get('superseded_by', ''),
        supersede_reason=data.get('supersede_reason', ''), outcome=data.get('outcome', ''),
        tags=dict(data.get('tags', {})), params=dict(data.get('params', {})),
        required_artifact_refs=list(data.get('required_artifact_refs', [])),
        required_artifact_ids=list(data.get('required_artifact_ids', [])),
        required_artifact_sets=list(data.get('required_artifact_sets', [])),
        write_policy=data.get('write_policy', 'single'),
    )


def _rebuild_frontend_state(store: EvoStore, run_id: str) -> None:
    from ..projections import rebuild_frontend_state

    rebuild_frontend_state(store, run_id)
