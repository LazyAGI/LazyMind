from __future__ import annotations

import json
import uuid
from dataclasses import asdict
from difflib import unified_diff
from pathlib import Path
from typing import Any

from ..ids import validate_id
from .models import (ArtifactDiff, ArtifactDraft, ArtifactFragment, ArtifactPatch,
                     ArtifactRef, ArtifactValidationReport, DiffEntry, ImpactReport, SnapshotRef)


class ArtifactGraph:
    '''File-backed artifact versions, fragments, lineage, diffs, and snapshots.'''

    def __init__(self, root: str | Path):
        self.root = Path(root)
        self.manifest_dir = self.root / 'artifacts' / 'manifests'
        self.blob_dir = self.root / 'artifacts' / 'blobs'
        self.fragment_dir = self.root / 'artifacts' / 'fragments'
        self.snapshot_dir = self.root / 'snapshots'
        self.index_dir = self.root / 'indexes'
        for path in (self.manifest_dir, self.blob_dir, self.fragment_dir, self.snapshot_dir, self.index_dir):
            path.mkdir(parents=True, exist_ok=True)

    def commit_artifact(self, draft: ArtifactDraft) -> ArtifactRef:
        validate_id(draft.producer_operation_run_id, 'producer_operation_run_id')
        manifest = self._load_manifest(draft.artifact_id)
        if manifest and manifest['schema_name'] != draft.schema_name:
            raise ValueError(f'schema mismatch for {draft.artifact_id}')
        version = int(manifest['latest_version']) + 1 if manifest else 1
        ref = ArtifactRef(draft.artifact_id, version)
        self._write_blob(ref, draft.payload)
        fragments = [
            ArtifactFragment(
                fragment_id=f.fragment_id,
                artifact_id=ref.artifact_id,
                version=ref.version,
                json_pointer=f.json_pointer,
                kind=f.kind,
                label=f.label,
            )
            for f in draft.fragments
        ]
        if fragments:
            self._write_fragments(ref, fragments)
        self._save_manifest(
            artifact_id=draft.artifact_id,
            schema_name=draft.schema_name,
            ref=ref,
            producer_operation_run_id=draft.producer_operation_run_id,
            input_refs=draft.input_refs,
            status='active',
            role=draft.role,
        )
        self._append_index(ref, draft.producer_operation_run_id, draft.input_refs)
        return ref

    def patch_artifact(self, patch: ArtifactPatch) -> ArtifactRef:
        base = self._version_meta(patch.base_ref)
        draft = ArtifactDraft(
            artifact_id=patch.base_ref.artifact_id,
            schema_name=base['schema_name'],
            payload=patch.payload,
            producer_operation_run_id=patch.producer_operation_run_id,
            input_refs=[patch.base_ref],
            fragments=patch.fragments,
        )
        return self.commit_artifact(draft)

    def get(self, ref: ArtifactRef) -> Any:
        return self._read_json(self._blob_path(ref))

    def schema_name(self, ref: ArtifactRef) -> str:
        return self._version_meta(ref)['schema_name']

    def latest_ref(self, artifact_id: str) -> ArtifactRef:
        validate_id(artifact_id, 'artifact_id')
        manifest = self._load_manifest(artifact_id)
        if not manifest:
            raise KeyError(artifact_id)
        return ArtifactRef(artifact_id, int(manifest['latest_version']))

    def fragments(self, ref: ArtifactRef) -> list[ArtifactFragment]:
        meta = self._version_meta(ref)
        path = self.root / meta.get('fragment_index_ref', '')
        if not path.exists():
            return []
        return [ArtifactFragment(**row) for row in self._read_json(path)]

    def diff(self, old: ArtifactRef, new: ArtifactRef) -> ArtifactDiff:
        return ArtifactDiff(old_ref=old, new_ref=new, entries=_diff_values('', self.get(old), self.get(new)))

    def upstream(self, ref: ArtifactRef) -> set[ArtifactRef]:
        index = self._read_index('upstream_by_artifact.json')
        return {ArtifactRef.parse(value) for value in index.get(str(ref), [])}

    def downstream(self, ref: ArtifactRef) -> set[ArtifactRef]:
        index = self._read_index('downstream_by_artifact.json')
        return {ArtifactRef.parse(value) for value in index.get(str(ref), [])}

    def produced_by(self, producer_operation_run_id: str) -> set[ArtifactRef]:
        validate_id(producer_operation_run_id, 'producer_operation_run_id')
        index = self._read_index('artifacts_by_producer.json')
        return {ArtifactRef.parse(value) for value in index.get(producer_operation_run_id, [])}

    def impact(self, changed: list[ArtifactRef]) -> ImpactReport:
        seen = set(changed)
        frontier = list(changed)
        impacted: set[ArtifactRef] = set()
        while frontier:
            current = frontier.pop()
            for child in self.downstream(current):
                if child in seen:
                    continue
                seen.add(child)
                impacted.add(child)
                frontier.append(child)
        return ImpactReport(changed=set(changed), impacted=impacted)

    def create_snapshot(self, refs: dict[str, ArtifactRef]) -> SnapshotRef:
        snapshot = SnapshotRef(snapshot_id=f'snap_{uuid.uuid4().hex[:12]}')
        data = {'snapshot_id': snapshot.snapshot_id, 'artifact_refs': {key: str(ref) for key, ref in refs.items()}}
        self._atomic_write_json(self.snapshot_dir / f'{snapshot.snapshot_id}.json', data)
        return snapshot

    def get_snapshot(self, ref: SnapshotRef) -> dict[str, ArtifactRef]:
        data = self._read_json(self.snapshot_dir / f'{ref.snapshot_id}.json')
        return {key: ArtifactRef.parse(value) for key, value in data['artifact_refs'].items()}

    def rebuild_indexes(self) -> None:
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
        self._atomic_write_json(self.index_dir / 'upstream_by_artifact.json', _sorted_index(upstream))
        self._atomic_write_json(self.index_dir / 'downstream_by_artifact.json', _sorted_index(downstream))
        self._atomic_write_json(self.index_dir / 'artifacts_by_producer.json', _sorted_index(produced))

    def validate_visible_artifacts(self) -> ArtifactValidationReport:
        invalid: list[dict[str, Any]] = []
        visible_blobs: set[Path] = set()
        visible_fragments: set[Path] = set()
        for manifest_path in sorted(self.manifest_dir.glob('*.json')):
            manifest = self._read_json(manifest_path)
            artifact_id = manifest.get('artifact_id', manifest_path.stem)
            schema_name = manifest.get('schema_name', '')
            for version in manifest.get('versions', []):
                ref = ArtifactRef(artifact_id, int(version['version']))
                if version.get('schema_name', schema_name) != schema_name:
                    invalid.append({'artifact_ref': str(ref), 'reason': 'schema_mismatch'})
                blob = self.root / version.get('payload_ref', '')
                fragment_ref = version.get('fragment_index_ref', '')
                fragment = self.root / fragment_ref if fragment_ref else None
                visible_blobs.add(blob)
                if fragment:
                    visible_fragments.add(fragment)
                if not blob.exists():
                    invalid.append({'artifact_ref': str(ref), 'reason': 'missing_payload',
                                    'path': str(blob.relative_to(self.root))})
                if fragment and not fragment.exists():
                    invalid.append({'artifact_ref': str(ref), 'reason': 'missing_fragment_index',
                                    'path': str(fragment.relative_to(self.root))})
        orphan_blobs = _relative_files(self.blob_dir, self.root, visible_blobs)
        orphan_fragments = _relative_files(self.fragment_dir, self.root, visible_fragments)
        return ArtifactValidationReport(invalid, orphan_blobs, orphan_fragments)

    def mark_stale(self, refs: list[ArtifactRef]) -> None:
        for ref in refs:
            manifest = self._load_manifest(ref.artifact_id)
            if not manifest:
                continue
            for version in manifest['versions']:
                if int(version['version']) == ref.version:
                    version['status'] = 'stale'
            self._atomic_write_json(self._manifest_path(ref.artifact_id), manifest)

    def _save_manifest(self, *, artifact_id: str, schema_name: str, ref: ArtifactRef,
                       producer_operation_run_id: str, input_refs: list[ArtifactRef], status: str,
                       role: str,
                       ) -> None:
        manifest = self._load_manifest(artifact_id) or {
            'artifact_id': artifact_id,
            'schema_name': schema_name,
            'latest_version': 0,
            'versions': [],
        }
        fragment_path = self._fragment_path(ref)
        manifest['latest_version'] = ref.version
        manifest['versions'].append(
            {
                'version': ref.version,
                'schema_name': schema_name,
                'status': status,
                'role': role,
                'producer_operation_run_id': producer_operation_run_id,
                'input_refs': [str(item) for item in input_refs],
                'payload_ref': str(self._blob_path(ref).relative_to(self.root)),
                'fragment_index_ref': str(fragment_path.relative_to(self.root)) if fragment_path.exists() else '',
            }
        )
        self._atomic_write_json(self._manifest_path(artifact_id), manifest)

    def _version_meta(self, ref: ArtifactRef) -> dict[str, Any]:
        manifest = self._load_manifest(ref.artifact_id)
        if not manifest:
            raise KeyError(str(ref))
        for version in manifest['versions']:
            if int(version['version']) == ref.version:
                return {**version, 'schema_name': manifest['schema_name']}
        raise KeyError(str(ref))

    def _load_manifest(self, artifact_id: str) -> dict[str, Any] | None:
        validate_id(artifact_id, 'artifact_id')
        path = self._manifest_path(artifact_id)
        if not path.exists():
            return None
        return self._read_json(path)

    def _read_index(self, name: str) -> dict[str, list[str]]:
        path = self.index_dir / name
        if not path.exists():
            self.rebuild_indexes()
        return self._read_json(path) if path.exists() else {}

    def _append_index(self, ref: ArtifactRef, producer_operation_run_id: str, input_refs: list[ArtifactRef]) -> None:
        upstream = self._read_index('upstream_by_artifact.json')
        downstream = self._read_index('downstream_by_artifact.json')
        produced = self._read_index('artifacts_by_producer.json')
        upstream[str(ref)] = [str(item) for item in input_refs]
        for parent in input_refs:
            downstream.setdefault(str(parent), []).append(str(ref))
        produced.setdefault(producer_operation_run_id, []).append(str(ref))
        self._atomic_write_json(self.index_dir / 'upstream_by_artifact.json', _sorted_index(upstream))
        self._atomic_write_json(self.index_dir / 'downstream_by_artifact.json', _sorted_index(downstream))
        self._atomic_write_json(self.index_dir / 'artifacts_by_producer.json', _sorted_index(produced))

    def _write_blob(self, ref: ArtifactRef, payload: Any) -> None:
        path = self._blob_path(ref)
        path.parent.mkdir(parents=True, exist_ok=True)
        self._atomic_write_json(path, payload)

    def _write_fragments(self, ref: ArtifactRef, fragments: list[ArtifactFragment]) -> None:
        path = self._fragment_path(ref)
        path.parent.mkdir(parents=True, exist_ok=True)
        self._atomic_write_json(path, [asdict(item) for item in fragments])

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
        tmp = path.with_suffix(path.suffix + '.tmp')
        tmp.write_text(json.dumps(data, ensure_ascii=False, indent=2, sort_keys=True), encoding='utf-8')
        tmp.replace(path)


def _sorted_index(index: dict[str, list[str]]) -> dict[str, list[str]]:
    return {key: sorted(set(values)) for key, values in sorted(index.items())}


def _relative_files(root: Path, base: Path, visible: set[Path]) -> list[str]:
    if not root.exists():
        return []
    return sorted(str(path.relative_to(base)) for path in root.rglob('*.json') if path not in visible)


def _diff_values(path: str, old: Any, new: Any) -> list[DiffEntry]:
    if type(old) is not type(new):
        return [DiffEntry('replace', path or '/', old=old, new=new)]
    if isinstance(old, dict):
        entries: list[DiffEntry] = []
        for key in sorted(set(old) | set(new)):
            child = f'{path}/{_escape_pointer(str(key))}'
            if key not in old:
                entries.append(DiffEntry('add', child, new=new[key]))
            elif key not in new:
                entries.append(DiffEntry('remove', child, old=old[key]))
            else:
                entries.extend(_diff_values(child, old[key], new[key]))
        return entries
    if isinstance(old, list):
        return _diff_lists(path, old, new)
    if isinstance(old, str) and '\n' in old + new and old != new:
        text = '\n'.join(unified_diff(old.splitlines(), new.splitlines(), lineterm=''))
        return [DiffEntry('replace', path or '/', old=old, new=text)]
    if old != new:
        return [DiffEntry('replace', path or '/', old=old, new=new)]
    return []


def _diff_lists(path: str, old: list[Any], new: list[Any]) -> list[DiffEntry]:
    if _list_has_ids(old) and _list_has_ids(new):
        return _diff_lists_by_id(path, old, new)
    if old != new:
        return [DiffEntry('replace', path or '/', old=old, new=new)]
    return []


def _diff_lists_by_id(path: str, old: list[dict[str, Any]], new: list[dict[str, Any]]) -> list[DiffEntry]:
    old_by_id = {str(item['id']): item for item in old}
    new_by_id = {str(item['id']): item for item in new}
    entries: list[DiffEntry] = []
    for item_id in sorted(set(old_by_id) | set(new_by_id)):
        child = f'{path}/{_escape_pointer(item_id)}'
        if item_id not in old_by_id:
            entries.append(DiffEntry('add', child, new=new_by_id[item_id]))
        elif item_id not in new_by_id:
            entries.append(DiffEntry('remove', child, old=old_by_id[item_id]))
        else:
            entries.extend(_diff_values(child, old_by_id[item_id], new_by_id[item_id]))
    return entries


def _list_has_ids(values: list[Any]) -> bool:
    return all(isinstance(item, dict) and 'id' in item for item in values)


def _escape_pointer(value: str) -> str:
    return value.replace('~', '~0').replace('/', '~1')
