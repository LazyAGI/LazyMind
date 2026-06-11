from __future__ import annotations

import json
import os
import threading
import uuid
from contextlib import contextmanager
from dataclasses import asdict
from difflib import unified_diff
from pathlib import Path
from typing import Any

from .. import validate_id
from .models import (ArtifactDiff, ArtifactDraft, ArtifactFragment,
                     ArtifactRef, ArtifactValidationReport, DiffEntry, ImpactReport, SnapshotRef)
from .schema import validate_artifact_payload

try:
    import fcntl
except ImportError:  # pragma: no cover - exercised only on non-POSIX hosts.
    fcntl = None

try:
    import msvcrt
except ImportError:  # pragma: no cover - exercised only on Windows hosts.
    msvcrt = None


class ArtifactGraph:
    '''File-backed artifact versions, fragments, lineage, diffs, and snapshots.'''

    _locks_guard = threading.RLock()
    _locks: dict[str, threading.RLock] = {}
    _lock_state = threading.local()

    def __init__(self, root: str | Path):
        self.root = Path(root)
        self.manifest_dir = self.root / 'artifacts' / 'manifests'
        self.blob_dir = self.root / 'artifacts' / 'blobs'
        self.fragment_dir = self.root / 'artifacts' / 'fragments'
        self.snapshot_dir = self.root / 'snapshots'
        self.index_dir = self.root / 'indexes'
        for path in (self.manifest_dir, self.blob_dir, self.fragment_dir, self.snapshot_dir, self.index_dir):
            path.mkdir(parents=True, exist_ok=True)
        self._lock_key = str(self.root.resolve())
        self._lock_path = self.root / 'artifacts' / '.artifact_graph.lock'

    def commit_artifact(self, draft: ArtifactDraft) -> ArtifactRef:
        with self._write_lock(): return self._commit_artifact_unlocked(draft)

    def commit_artifacts(self, drafts: list[ArtifactDraft]) -> list[ArtifactRef]:
        with self._write_lock():
            self._validate_commit_batch(drafts)
            return [self._commit_artifact_unlocked(draft) for draft in drafts]

    def get(self, ref: ArtifactRef) -> Any:
        with self._write_lock(): return self._read_json(self._blob_path(ref))

    def schema_name(self, ref: ArtifactRef) -> str:
        with self._write_lock(): return self._version_meta(ref)['schema_name']

    def version_metadata(self, ref: ArtifactRef) -> dict[str, Any]:
        with self._write_lock(): return dict(self._version_meta(ref))

    def latest_ref(self, artifact_id: str) -> ArtifactRef:
        with self._write_lock():
            validate_id(artifact_id, 'artifact_id')
            manifest = self._load_manifest(artifact_id)
            if not manifest: raise KeyError(artifact_id)
            return ArtifactRef(artifact_id, int(manifest['latest_version']))

    def fragments(self, ref: ArtifactRef) -> list[ArtifactFragment]:
        with self._write_lock():
            path = self.root / self._version_meta(ref).get('fragment_index_ref', '')
            if not path.exists(): return []
            return [ArtifactFragment(**row) for row in self._read_json(path)]

    def diff(self, old: ArtifactRef, new: ArtifactRef) -> ArtifactDiff:
        with self._write_lock():
            return ArtifactDiff(old_ref=old, new_ref=new, entries=_diff_values('', self.get(old), self.get(new)))

    def upstream(self, ref: ArtifactRef) -> set[ArtifactRef]:
        with self._write_lock():
            return {ArtifactRef.parse(v) for v in self._read_index('upstream_by_artifact.json').get(str(ref), [])}

    def downstream(self, ref: ArtifactRef) -> set[ArtifactRef]:
        with self._write_lock():
            return {ArtifactRef.parse(v) for v in self._read_index('downstream_by_artifact.json').get(str(ref), [])}

    def produced_by(self, producer_operation_run_id: str) -> set[ArtifactRef]:
        with self._write_lock():
            validate_id(producer_operation_run_id, 'producer_operation_run_id')
            index = self._read_index('artifacts_by_producer.json')
            return {ArtifactRef.parse(value) for value in index.get(producer_operation_run_id, [])}

    def impact(self, changed: list[ArtifactRef]) -> ImpactReport:
        with self._write_lock():
            seen = set(changed)
            frontier = list(changed)
            impacted: set[ArtifactRef] = set()
            while frontier:
                for child in self.downstream(frontier.pop()):
                    if child in seen: continue
                    seen.add(child)
                    impacted.add(child)
                    frontier.append(child)
            return ImpactReport(changed=set(changed), impacted=impacted)

    def create_snapshot(self, refs: dict[str, ArtifactRef]) -> SnapshotRef:
        with self._write_lock():
            snapshot = SnapshotRef(snapshot_id=f'snap_{uuid.uuid4().hex[:12]}')
            data = {'snapshot_id': snapshot.snapshot_id, 'artifact_refs': {key: str(ref) for key, ref in refs.items()}}
            self._atomic_write_json(self.snapshot_dir / f'{snapshot.snapshot_id}.json', data)
            return snapshot

    def rebuild_indexes(self) -> None:
        with self._write_lock(): self._rebuild_indexes_unlocked()

    def validate_visible_artifacts(self) -> ArtifactValidationReport:
        with self._write_lock():
            invalid: list[dict[str, Any]] = []
            visible_blobs: set[Path] = set()
            visible_fragments: set[Path] = set()

            def bad(ref: ArtifactRef, reason: str, **extra: Any) -> None:
                invalid.append({'artifact_ref': str(ref), 'reason': reason, **extra})

            for manifest_path in sorted(self.manifest_dir.glob('*.json')):
                manifest = self._read_json(manifest_path)
                artifact_id = manifest.get('artifact_id', manifest_path.stem)
                schema_name = manifest.get('schema_name', '')
                for version in manifest.get('versions', []):
                    ref = ArtifactRef(artifact_id, int(version['version']))
                    if version.get('schema_name', schema_name) != schema_name: bad(ref, 'schema_mismatch')
                    blob = self.root / version.get('payload_ref', '')
                    fragment_ref = version.get('fragment_index_ref', '')
                    fragment = self.root / fragment_ref if fragment_ref else None
                    visible_blobs.add(blob)
                    if fragment: visible_fragments.add(fragment)
                    if not blob.exists():
                        bad(ref, 'missing_payload', path=str(blob.relative_to(self.root)))
                    else:
                        try:
                            validate_artifact_payload(version.get('schema_name', schema_name), self._read_json(blob))
                        except ValueError as exc:
                            bad(ref, 'schema_invalid', message=str(exc))
                    if fragment and not fragment.exists():
                        bad(ref, 'missing_fragment_index', path=str(fragment.relative_to(self.root)))
            return ArtifactValidationReport(invalid, _relative_files(self.blob_dir, self.root, visible_blobs),
                                            _relative_files(self.fragment_dir, self.root, visible_fragments))

    def _commit_artifact_unlocked(self, draft: ArtifactDraft) -> ArtifactRef:
        validate_id(draft.producer_operation_run_id, 'producer_operation_run_id')
        validate_artifact_payload(draft.schema_name, draft.payload)
        manifest = self._load_manifest(draft.artifact_id)
        if manifest and manifest['schema_name'] != draft.schema_name:
            raise ValueError(f'schema mismatch for {draft.artifact_id}')
        ref = ArtifactRef(draft.artifact_id, int(manifest['latest_version']) + 1 if manifest else 1)
        self._atomic_write_json(self._blob_path(ref), draft.payload)
        fragments = [ArtifactFragment(f.fragment_id, ref.artifact_id, ref.version, f.json_pointer, f.kind, f.label)
                     for f in draft.fragments]
        if fragments:
            self._atomic_write_json(self._fragment_path(ref), [asdict(item) for item in fragments])
        manifest = manifest or {'artifact_id': draft.artifact_id, 'schema_name': draft.schema_name,
                                'latest_version': 0, 'versions': []}
        manifest['latest_version'] = ref.version
        fragment_path = self._fragment_path(ref)
        manifest['versions'].append({
            'version': ref.version, 'schema_name': draft.schema_name, 'status': 'active', 'role': draft.role,
            'producer_operation_run_id': draft.producer_operation_run_id,
            'input_refs': [str(item) for item in draft.input_refs],
            'payload_ref': str(self._blob_path(ref).relative_to(self.root)),
            'fragment_index_ref': str(fragment_path.relative_to(self.root)) if fragment_path.exists() else '',
        })
        self._atomic_write_json(self._manifest_path(ref.artifact_id), manifest)
        self._append_index(ref, draft.producer_operation_run_id, draft.input_refs)
        return ref

    def _validate_commit_batch(self, drafts: list[ArtifactDraft]) -> None:
        schemas: dict[str, str] = {}
        for draft in drafts:
            validate_id(draft.producer_operation_run_id, 'producer_operation_run_id')
            validate_artifact_payload(draft.schema_name, draft.payload)
            manifest = self._load_manifest(draft.artifact_id)
            if manifest and manifest['schema_name'] != draft.schema_name:
                raise ValueError(f'schema mismatch for {draft.artifact_id}')
            existing = schemas.get(draft.artifact_id)
            if existing is not None and existing != draft.schema_name:
                raise ValueError(f'schema mismatch for {draft.artifact_id}')
            schemas[draft.artifact_id] = draft.schema_name

    def _version_meta(self, ref: ArtifactRef) -> dict[str, Any]:
        manifest = self._load_manifest(ref.artifact_id)
        if not manifest: raise KeyError(str(ref))
        for version in manifest['versions']:
            if int(version['version']) == ref.version: return {**version, 'schema_name': manifest['schema_name']}
        raise KeyError(str(ref))

    def _load_manifest(self, artifact_id: str) -> dict[str, Any] | None:
        path = self._manifest_path(artifact_id)
        return self._read_json(path) if path.exists() else None

    def _read_index(self, name: str) -> dict[str, list[str]]:
        path = self.index_dir / name
        if not path.exists():
            with self._write_lock():
                if not path.exists(): self._rebuild_indexes_unlocked()
        return self._read_json(path) if path.exists() else {}

    def _append_index(self, ref: ArtifactRef, producer_operation_run_id: str, input_refs: list[ArtifactRef]) -> None:
        upstream = self._read_index('upstream_by_artifact.json')
        downstream = self._read_index('downstream_by_artifact.json')
        produced = self._read_index('artifacts_by_producer.json')
        upstream[str(ref)] = [str(item) for item in input_refs]
        for parent in input_refs:
            downstream.setdefault(str(parent), []).append(str(ref))
        produced.setdefault(producer_operation_run_id, []).append(str(ref))
        self._write_indexes(upstream, downstream, produced)

    def _rebuild_indexes_unlocked(self) -> None:
        upstream: dict[str, list[str]] = {}
        downstream: dict[str, list[str]] = {}
        produced: dict[str, list[str]] = {}
        for manifest_path in sorted(self.manifest_dir.glob('*.json')):
            manifest = self._read_json(manifest_path)
            for version in manifest['versions']:
                ref = ArtifactRef(manifest['artifact_id'], int(version['version']))
                parents = list(version.get('input_refs') or [])
                upstream[str(ref)] = parents
                for parent in parents:
                    downstream.setdefault(parent, []).append(str(ref))
                produced.setdefault(version['producer_operation_run_id'], []).append(str(ref))
        self._write_indexes(upstream, downstream, produced)

    def _write_indexes(self, upstream: dict, downstream: dict, produced: dict) -> None:
        for name, index in (('upstream_by_artifact.json', upstream), ('downstream_by_artifact.json', downstream),
                            ('artifacts_by_producer.json', produced)):
            self._atomic_write_json(self.index_dir / name, _sorted_index(index))

    def _manifest_path(self, artifact_id: str) -> Path:
        validate_id(artifact_id, 'artifact_id')
        return self.manifest_dir / f'{artifact_id}.json'

    def _blob_path(self, ref: ArtifactRef) -> Path:
        return self.blob_dir / ref.artifact_id / f'v{ref.version:04d}.json'

    def _fragment_path(self, ref: ArtifactRef) -> Path:
        return self.fragment_dir / f'{ref.artifact_id}_v{ref.version:04d}.json'

    @staticmethod
    def _read_json(path: Path) -> Any:
        return json.loads(path.read_text(encoding='utf-8'))

    @staticmethod
    def _atomic_write_json(path: Path, data: Any) -> None:
        path.parent.mkdir(parents=True, exist_ok=True)
        tmp = path.with_name(f'{path.name}.{os.getpid()}.{threading.get_ident()}.{uuid.uuid4().hex}.tmp')
        tmp.write_text(json.dumps(data, ensure_ascii=False, indent=2, sort_keys=True), encoding='utf-8')
        tmp.replace(path)

    def snapshot_lock(self):
        '''Hold the graph write lock while copying the store, so snapshots are never torn.'''
        return self._write_lock()

    @contextmanager
    def _write_lock(self):
        lock = self._root_lock()
        depths = getattr(self._lock_state, 'depths', None)
        if depths is None: depths = self._lock_state.depths = {}
        depth = depths.get(self._lock_key, 0)
        if depth:
            depths[self._lock_key] = depth + 1
            try:
                yield
            finally:
                depths[self._lock_key] = depth
            return
        with lock:
            self._lock_path.parent.mkdir(parents=True, exist_ok=True)
            with self._lock_path.open('a+', encoding='utf-8') as handle:
                _lock_file(handle)
                depths[self._lock_key] = 1
                try:
                    yield
                finally:
                    _unlock_file(handle)
                    depths.pop(self._lock_key, None)

    def _root_lock(self) -> threading.RLock:
        with self._locks_guard:
            return self._locks.setdefault(self._lock_key, threading.RLock())


def _sorted_index(index: dict[str, list[str]]) -> dict[str, list[str]]:
    return {key: sorted(set(values)) for key, values in sorted(index.items())}


def _lock_file(handle) -> None:
    if fcntl is not None:
        fcntl.flock(handle.fileno(), fcntl.LOCK_EX)
        return
    if msvcrt is None: raise RuntimeError('cross-process file locking requires fcntl or msvcrt')
    handle.seek(0)
    if not handle.read(1):
        handle.write('0')
        handle.flush()
    handle.seek(0)
    msvcrt.locking(handle.fileno(), msvcrt.LK_LOCK, 1)


def _unlock_file(handle) -> None:
    if fcntl is not None:
        fcntl.flock(handle.fileno(), fcntl.LOCK_UN)
    elif msvcrt is not None:
        handle.seek(0)
        msvcrt.locking(handle.fileno(), msvcrt.LK_UNLCK, 1)


def _relative_files(root: Path, base: Path, visible: set[Path]) -> list[str]:
    if not root.exists(): return []
    return sorted(str(path.relative_to(base)) for path in root.rglob('*.json') if path not in visible)


def _diff_values(path: str, old: Any, new: Any) -> list[DiffEntry]:
    if type(old) is not type(new): return [DiffEntry('replace', path or '/', old=old, new=new)]
    if isinstance(old, dict):
        entries: list[DiffEntry] = []
        for key in sorted(set(old) | set(new)):
            child = f'{path}/{_escape_pointer(str(key))}'
            if key not in old: entries.append(DiffEntry('add', child, new=new[key]))
            elif key not in new: entries.append(DiffEntry('remove', child, old=old[key]))
            else: entries.extend(_diff_values(child, old[key], new[key]))
        return entries
    if isinstance(old, list): return _diff_lists(path, old, new)
    if isinstance(old, str) and '\n' in old + new and old != new:
        text = '\n'.join(unified_diff(old.splitlines(), new.splitlines(), lineterm=''))
        return [DiffEntry('replace', path or '/', old=old, new=text)]
    return [DiffEntry('replace', path or '/', old=old, new=new)] if old != new else []


def _diff_lists(path: str, old: list[Any], new: list[Any]) -> list[DiffEntry]:
    if not (_list_has_ids(old) and _list_has_ids(new)):
        return [DiffEntry('replace', path or '/', old=old, new=new)] if old != new else []
    old_by_id = {str(item['id']): item for item in old}
    new_by_id = {str(item['id']): item for item in new}
    entries: list[DiffEntry] = []
    for item_id in sorted(set(old_by_id) | set(new_by_id)):
        child = f'{path}/{_escape_pointer(item_id)}'
        if item_id not in old_by_id: entries.append(DiffEntry('add', child, new=new_by_id[item_id]))
        elif item_id not in new_by_id: entries.append(DiffEntry('remove', child, old=old_by_id[item_id]))
        else: entries.extend(_diff_values(child, old_by_id[item_id], new_by_id[item_id]))
    return entries


def _list_has_ids(values: list[Any]) -> bool:
    return all(isinstance(item, dict) and 'id' in item for item in values)


def _escape_pointer(value: str) -> str:
    return value.replace('~', '~0').replace('/', '~1')
