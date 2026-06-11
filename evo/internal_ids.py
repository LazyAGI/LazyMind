from __future__ import annotations

from typing import Any

SYNTHETIC_OPERATION_PREFIX = 'message.response.'


def is_synthetic_operation(value: Any) -> bool:
    operation_id = value if isinstance(value, str) else (value.get('operation_run_id') if isinstance(value, dict) else '')
    return str(operation_id or '').startswith(SYNTHETIC_OPERATION_PREFIX)


def stage_group(value: str) -> str:
    value = str(value or '')
    if value in {'dataset', 'eval', 'analysis', 'repair', 'abtest'}: return value
    if value in {'dataset_gen', 'dataset_corpus'}: return 'dataset'
    if value == 'candidate_eval': return 'abtest'
    if value.startswith('eval.'): return 'eval'
    if value.startswith('analysis.'): return 'analysis'
    if value.startswith(('repair.', 'repair_', 'opencode_', 'candidate_workspace')): return 'repair'
    if value.startswith(('abtest.', 'abtest_', 'candidate_service', 'candidate_cutover')): return 'abtest'
    return ''


def latest_failed_stage_from_events(events: list[Any]) -> str:
    succeeded: set[str] = set()
    for event in reversed(events):
        event_type = getattr(event, 'event_type', '') if not isinstance(event, dict) else event.get('event_type', '')
        if event_type != 'evo_flow.progress': continue
        payload = getattr(event, 'payload', {}) if not isinstance(event, dict) else event.get('payload', {})
        payload = payload or {}
        stage = stage_group(str(payload.get('stage') or payload.get('phase') or ''))
        if not stage: continue
        status = str(payload.get('status') or '')
        if status == 'success':
            succeeded.add(stage)
        elif status == 'failed' and stage not in succeeded:
            return stage
    return ''
