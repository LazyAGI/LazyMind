'''Language-neutral Workflow v1 fixture reader used by contract tests.'''

from __future__ import annotations

import json
from dataclasses import dataclass
from pathlib import Path
from typing import Any

VERSION_V1 = 'workflow.v1'


@dataclass(frozen=True)
class GoldenScenario:
    scenario: str
    projection: dict[str, Any]
    attempts: tuple[dict[str, Any], ...]
    artifacts: tuple[dict[str, Any], ...]
    events: tuple[dict[str, Any], ...]


@dataclass(frozen=True)
class BaselineManifest:
    production_sources: tuple[str, ...]
    required_scenarios: tuple[str, ...]
    tool_semantics: dict[str, dict[str, str]]


def replay_projection(events: tuple[dict[str, Any], ...] | list[dict[str, Any]]) -> dict[str, Any]:
    projection: dict[str, Any] = {}
    for event in events:
        candidate = event.get('payload', {}).get('projection')
        if candidate is not None:
            projection = candidate
    if not projection.get('session_id'):
        raise ValueError('event stream contains no projection')
    return projection


def read_baseline_manifest(path: str | Path) -> BaselineManifest:
    payload = json.loads(Path(path).read_text(encoding='utf-8'))
    if (payload.get('contract_version') != VERSION_V1
            or payload.get('authority') != 'public_runtime'
            or not payload.get('production_sources')):
        raise ValueError('invalid Workflow baseline authority')
    tools = payload.get('tool_semantics', {})
    sync, handoff = tools.get('advance_step'), tools.get('advance_step_and_hand_off')
    if (not sync or not handoff or not sync.get('transition')
            or sync['transition'] != handoff.get('transition')
            or sync.get('wait') == handoff.get('wait')):
        raise ValueError('advance tools must share transition and use different waits')
    replay = payload.get('replay', {})
    if (replay.get('ordering') != 'cursor_strictly_increasing'
            or replay.get('initial_event') != 'workflow.snapshot'):
        raise ValueError('invalid Workflow replay rules')
    return BaselineManifest(
        production_sources=tuple(payload['production_sources']),
        required_scenarios=tuple(payload.get('required_scenarios', ())),
        tool_semantics=tools,
    )


def read_golden(path: str | Path) -> GoldenScenario:
    payload = json.loads(Path(path).read_text(encoding='utf-8'))
    if payload.get('contract_version') != VERSION_V1:
        raise ValueError('unsupported Workflow contract version')
    projection = payload.get('projection', {})
    if not payload.get('scenario') or not projection.get('session_id'):
        raise ValueError('invalid Workflow fixture identity')
    cursors = [event.get('cursor') for event in payload.get('events', ())]
    if not cursors or cursors != sorted(set(cursors)):
        raise ValueError('Workflow event cursors must be strictly increasing')
    events = payload.get('events', ())
    if events[0].get('type') != 'workflow.snapshot' or events[0].get('entity_id') != projection['session_id']:
        raise ValueError('event sequence must start with the session snapshot')
    attempts = payload.get('attempts', ())
    attempt_ids, attempt_nos = set(), {}
    for attempt in attempts:
        number, step_id = attempt.get('attempt_no'), attempt.get('step_id')
        if (not attempt.get('attempt_id') or not step_id or not isinstance(number, int)
                or number < 1 or number <= attempt_nos.get(step_id, 0)):
            raise ValueError('invalid Attempt sequence')
        attempt_ids.add(attempt['attempt_id'])
        attempt_nos[step_id] = number
        context = attempt.get('context', {})
        if (context.get('contract_version') != VERSION_V1
                or context.get('session_id') != projection['session_id']
                or context.get('step_id') != step_id
                or context.get('attempt_id') != attempt['attempt_id']
                or context.get('attempt_no') != number
                or context.get('operation') != attempt.get('operation')
                or not isinstance(context.get('input_bindings'), list)):
            raise ValueError('invalid Attempt Context')
    artifact_ids, slot_revisions = set(), {}
    for artifact in payload.get('artifacts', ()):
        slot, revision = artifact.get('slot'), artifact.get('revision')
        if (not artifact.get('artifact_id') or not slot or not isinstance(revision, int)
                or revision <= slot_revisions.get(slot, 0)
                or artifact.get('producer_attempt_id') not in attempt_ids):
            raise ValueError('invalid Artifact lineage')
        artifact_ids.add(artifact['artifact_id'])
        slot_revisions[slot] = revision
    for event in events:
        if (not isinstance(event.get('state_version'), int)
                or event['state_version'] < 1
                or not isinstance(event.get('payload'), dict)):
            raise ValueError('invalid durable Event envelope')
        if event['type'] == 'attempt.patch' and event['entity_id'] not in attempt_ids:
            raise ValueError('event references unknown Attempt')
        if event['type'] == 'artifact.upsert' and event['entity_id'] not in artifact_ids:
            raise ValueError('event references unknown Artifact')
    if replay_projection(events) != projection:
        raise ValueError('event replay does not reconstruct projection')
    return GoldenScenario(
        scenario=payload['scenario'],
        projection=projection,
        attempts=tuple(attempts),
        artifacts=tuple(payload.get('artifacts', ())),
        events=tuple(payload['events']),
    )
