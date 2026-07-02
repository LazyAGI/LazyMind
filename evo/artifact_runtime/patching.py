from __future__ import annotations

from collections.abc import Callable, Mapping
import hashlib
import json
from typing import Any

from jsonpointer import JsonPointer, JsonPointerException

from .artifact import ArtifactKey, ArtifactPayload, ArtifactRef
from .intent import IntentCommandRequest, PatchAndReconcileIntent, prepare_intent_payload
from .utils import canonical_json, normalize_json_value


def artifact_id_for_key(key: ArtifactKey) -> str:
    return key.artifact_id if not key.partition else f'{key.artifact_id}[{key.partition}]'


def parse_ref(value: str, fallback_key: ArtifactKey) -> ArtifactRef:
    key, version = parse_ref_parts(value)
    if version < 1:
        raise ValueError('artifact ref version required')
    return ArtifactRef(key if key.artifact_id else fallback_key, version)


def parse_ref_parts(value: str) -> tuple[ArtifactKey, int]:
    text = str(value or '').strip()
    version = 0
    if '@v' in text:
        text, raw_version = text.rsplit('@v', 1)
        version = int(raw_version)
    partition = ''
    if text.endswith(']') and '[' in text:
        text, partition = text[:-1].split('[', 1)
    return ArtifactKey(text, partition) if text else ArtifactKey('__missing__'), version


def prepare_json_pointer_patch(
    *,
    command_id: str,
    run_id: str,
    artifact_ref: str,
    json_pointer: str,
    patch_value: Any,
    row: Mapping[str, Any],
    provenance: Mapping[str, Any],
    preview_reconcile: Callable[[ArtifactKey], dict[str, Any]],
    patch_source: str,
    reason: str,
) -> dict[str, Any]:
    artifact, explicit_version = parse_ref_parts(artifact_ref)
    if artifact.artifact_id == '__missing__':
        raise ValueError('artifact_ref is required')
    expected_ref = parse_ref(str(row.get('ref') or ''), artifact)
    if explicit_version and expected_ref.version != explicit_version:
        raise ValueError('artifact_ref version does not match current artifact row')
    before = row.get('data')
    after = replace_json_pointer(before, json_pointer, patch_value)
    request = IntentCommandRequest(
        command_id,
        run_id,
        PatchAndReconcileIntent(
            artifact,
            ArtifactPayload(str(row.get('schema') or 'ManualPatch'), after, metadata=dict(provenance)),
            expected_ref,
            patch_source=patch_source,
            include_downstream=True,
            reason=reason,
        ),
        advance_until_idle=True,
        metadata=dict(provenance),
    )
    prepared = prepare_intent_payload(request)
    reconcile_preview = preview_reconcile(artifact)
    return {
        'command_id': request.command_id,
        'run_id': request.run_id,
        'intent_kind': request.kind,
        'prepared_payload': prepared.payload,
        'request_fingerprint': prepared.request_fingerprint,
        'preview': reconcile_preview,
        'preview_hash': preview_hash(reconcile_preview),
        'expected_refs': (str(expected_ref),),
        'patch_preview': patch_preview(artifact, expected_ref, before, after, json_pointer),
    }


def replace_json_pointer(value: Any, pointer: str, replacement: Any) -> Any:
    normalized = normalize_json_value(value, allow_tuple=True)
    clone = json.loads(canonical_json(normalized))
    parts = JsonPointer(pointer).parts
    if not parts:
        raise ValueError('root replacement is not allowed')
    parent = JsonPointer.from_parts(parts[:-1]).resolve(clone)
    key = parts[-1]
    if isinstance(parent, dict):
        if key not in parent:
            raise ValueError(f'path does not exist: {pointer}')
        parent[key] = normalize_json_value(replacement, allow_tuple=True)
        return clone
    if isinstance(parent, list):
        try:
            index = int(key)
        except ValueError as exc:
            raise ValueError('array index must be numeric') from exc
        if index < 0 or index >= len(parent):
            raise ValueError('array index out of bounds')
        parent[index] = normalize_json_value(replacement, allow_tuple=True)
        return clone
    raise ValueError('patch parent is not mutable')


def patch_preview(
    artifact: ArtifactKey,
    ref: ArtifactRef,
    before: Any,
    after: Any,
    pointer: str,
) -> dict[str, Any]:
    before_json = normalize_json_value(before, allow_tuple=True)
    after_json = normalize_json_value(after, allow_tuple=True)
    try:
        old_value = JsonPointer(pointer).resolve(before_json)
        new_value = JsonPointer(pointer).resolve(after_json)
    except JsonPointerException as exc:
        raise ValueError(f'path does not exist: {pointer}') from exc
    return {
        'target_artifact': artifact_id_for_key(artifact),
        'source_ref': str(ref),
        'json_pointer': pointer,
        'old_value': normalize_json_value(old_value, allow_tuple=True),
        'new_value': normalize_json_value(new_value, allow_tuple=True),
        'effective_changes': _top_level_changes(before_json, after_json),
    }


def preview_hash(preview: Mapping[str, Any]) -> str:
    normalized = normalize_json_value(dict(preview), allow_tuple=True)
    return hashlib.sha256(canonical_json(normalized).encode()).hexdigest()


def _top_level_changes(before: Any, after: Any) -> list[dict[str, Any]]:
    if not isinstance(before, dict) or not isinstance(after, dict):
        return []
    return [
        {'json_pointer': f'/{key}', 'old_value': before.get(key), 'new_value': after.get(key)}
        for key in sorted(set(before) | set(after))
        if before.get(key) != after.get(key)
    ]
