from __future__ import annotations

from collections.abc import Mapping
from contextlib import contextmanager
from contextvars import ContextVar
from dataclasses import dataclass
from pathlib import Path
import sqlite3
import uuid
from typing import Any, Literal

from evo.artifact_runtime import (
    ArtifactKey,
    ArtifactPayload,
    ArtifactRef,
    ControllerEvent,
    EventLog,
    EvoRuntime,
    EvoRuntimeConfig,
    IntentCommandRequest,
    MaterializeIntent,
    RetryFailedIntent,
    RunControlIntent,
    RunUntilIdleIntent,
    SubmitPlanIntent,
    intent_request_fingerprint,
    open_evo_runtime,
)
from evo.artifact_runtime.store import ArtifactStoreVersionResolver

from .contract import STEP_ROOTS, StepName, case_ids
from .graph import build_evo_graph

GateStatus = Literal['idle', 'active', 'paused', 'completed', 'cancelled', 'stale']
STEPS: tuple[StepName, ...] = ('dataset', 'eval', 'analysis', 'repair', 'abtest')
DEFAULT_DATASET_STEP_RUN_ID = str(uuid.UUID(int=1))
FOLLOW_UP_STEP_RUN_KEY = '__follow_up__'
FOLLOW_UP_STEP_RUN_PREFIX = '__follow_up__:'


@dataclass(frozen=True)
class FlowStepState:
    run_id: str
    current_step: StepName | str
    completed_steps: tuple[StepName, ...] = ()
    stale_steps: tuple[StepName, ...] = ()
    active_step_plan_version: int = 0
    gate_status: GateStatus = 'idle'
    gate_artifact_ref: ArtifactRef | None = None

    @property
    def next_step(self) -> StepName | None:
        if self.current_step not in STEPS:
            return STEPS[0]
        index = STEPS.index(self.current_step) + 1
        return STEPS[index] if index < len(STEPS) else None


class SQLiteFlowStepStore:
    def __init__(self, path: str | Path) -> None:
        self.path = str(path)
        self._connection = sqlite3.connect(self.path, check_same_thread=False)
        self._connection.row_factory = sqlite3.Row
        self._connection.execute(
            """
            CREATE TABLE IF NOT EXISTS flow_step_state (
                run_id TEXT PRIMARY KEY,
                current_step TEXT NOT NULL,
                completed_steps TEXT NOT NULL,
                stale_steps TEXT NOT NULL,
                active_step_plan_version INTEGER NOT NULL,
                gate_status TEXT NOT NULL,
                gate_artifact_id TEXT NOT NULL,
                gate_partition TEXT NOT NULL,
                gate_version INTEGER NOT NULL
            )
            """
        )
        self._connection.execute(
            """
            CREATE TABLE IF NOT EXISTS flow_step_run_ids (
                run_id TEXT NOT NULL,
                step TEXT NOT NULL,
                step_run_id TEXT NOT NULL,
                next_step_run_id TEXT NOT NULL DEFAULT '',
                PRIMARY KEY (run_id, step)
            )
            """
        )
        columns = {
            str(row['name'])
            for row in self._connection.execute('PRAGMA table_info(flow_step_run_ids)').fetchall()
        }
        if 'next_step_run_id' not in columns:
            self._connection.execute(
                "ALTER TABLE flow_step_run_ids ADD COLUMN next_step_run_id TEXT NOT NULL DEFAULT ''"
            )
        self._connection.commit()

    def get(self, run_id: str) -> FlowStepState | None:
        row = self._connection.execute('SELECT * FROM flow_step_state WHERE run_id = ?', (run_id,)).fetchone()
        return None if row is None else _state_from_row(row)

    def put(self, state: FlowStepState) -> FlowStepState:
        ref = state.gate_artifact_ref
        self._connection.execute(
            """
            INSERT INTO flow_step_state(
                run_id, current_step, completed_steps, stale_steps, active_step_plan_version,
                gate_status, gate_artifact_id, gate_partition, gate_version
            )
            VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
            ON CONFLICT(run_id) DO UPDATE SET
                current_step = excluded.current_step,
                completed_steps = excluded.completed_steps,
                stale_steps = excluded.stale_steps,
                active_step_plan_version = excluded.active_step_plan_version,
                gate_status = excluded.gate_status,
                gate_artifact_id = excluded.gate_artifact_id,
                gate_partition = excluded.gate_partition,
                gate_version = excluded.gate_version
            """,
            (
                state.run_id,
                state.current_step,
                ','.join(state.completed_steps),
                ','.join(state.stale_steps),
                state.active_step_plan_version,
                state.gate_status,
                '' if ref is None else ref.key.artifact_id,
                '' if ref is None else ref.key.partition,
                0 if ref is None else ref.version,
            ),
        )
        self._connection.commit()
        return state

    def close(self) -> None:
        self._connection.close()

    def ensure_step_run_id(self, run_id: str, step: str, step_run_id: str) -> str:
        self._connection.execute(
            """
            INSERT OR IGNORE INTO flow_step_run_ids(run_id, step, step_run_id)
            VALUES (?, ?, ?)
            """,
            (run_id, step, step_run_id),
        )
        self._connection.commit()
        row = self._connection.execute(
            'SELECT step_run_id FROM flow_step_run_ids WHERE run_id = ? AND step = ?',
            (run_id, step),
        ).fetchone()
        if row is None:
            raise ValueError(f'step_run_id missing for {run_id}:{step}')
        return str(row['step_run_id'])

    def set_next_step_run_id(self, run_id: str, step: str, next_step_run_id: str) -> str:
        self._connection.execute(
            """
            UPDATE flow_step_run_ids
            SET next_step_run_id = ?
            WHERE run_id = ? AND step = ?
            """,
            (next_step_run_id, run_id, step),
        )
        self._connection.commit()
        return next_step_run_id

    def replace_step_run_id(self, run_id: str, step: str, step_run_id: str) -> str:
        self._connection.execute(
            """
            UPDATE flow_step_run_ids
            SET step_run_id = ?, next_step_run_id = ''
            WHERE run_id = ? AND step = ?
            """,
            (step_run_id, run_id, step),
        )
        self._connection.commit()
        return step_run_id

    def next_step_run_id_for(self, run_id: str, step_run_id: str) -> str:
        row = self._connection.execute(
            """
            SELECT next_step_run_id
            FROM flow_step_run_ids
            WHERE run_id = ? AND step_run_id = ?
            ORDER BY CASE WHEN step LIKE ? THEN 0 ELSE 1 END
            LIMIT 1
            """,
            (run_id, step_run_id, f'{FOLLOW_UP_STEP_RUN_PREFIX}%'),
        ).fetchone()
        return '' if row is None else str(row['next_step_run_id'])

    def step_for_step_run_id(self, run_id: str, step_run_id: str) -> str:
        row = self._connection.execute(
            """
            SELECT step
            FROM flow_step_run_ids
            WHERE run_id = ? AND step_run_id = ?
            ORDER BY CASE WHEN step LIKE ? THEN 0 ELSE 1 END
            LIMIT 1
            """,
            (run_id, step_run_id, f'{FOLLOW_UP_STEP_RUN_PREFIX}%'),
        ).fetchone()
        return '' if row is None else str(row['step'])


class StepRunEventLog:
    def __init__(self, inner: EventLog) -> None:
        self.inner = inner
        self.path = getattr(inner, 'path', '')
        self._context: ContextVar[dict[str, str] | None] = ContextVar('evo_step_run_context', default=None)

    @contextmanager
    def bind(self, *, run_id: str, step_run_id: str, next_step_run_id: str = ''):
        token = self._context.set({
            'run_id': run_id,
            'step_run_id': step_run_id,
            'next_step_run_id': next_step_run_id,
        })
        try:
            yield
        finally:
            self._context.reset(token)

    def append(self, event: ControllerEvent) -> int:
        context = self._context.get()
        if context is None or context.get('run_id') != event.run_id:
            return self.inner.append(event)
        payload = dict(event.payload)
        payload.setdefault('step_run_id', context['step_run_id'])
        if context.get('next_step_run_id'):
            payload.setdefault('next_step_run_id', context['next_step_run_id'])
        return self.inner.append(ControllerEvent(event.event_type, event.run_id, payload, event.seq))

    def scan(self, run_id: str):
        return self.inner.scan(run_id)

    def scan_since(self, seq: int = 0, *, limit: int = 1000):
        return self.inner.scan_since(seq, limit=limit)

    def max_seq(self) -> int:
        return self.inner.max_seq()


class EvoFlowRuntime:
    def __init__(self, runtime: EvoRuntime, step_store: SQLiteFlowStepStore) -> None:
        self.runtime = runtime
        self.step_store = step_store
        self.event_log = _step_run_event_log(runtime.controller.event_log)
        self.runtime.controller.event_log = self.event_log

    @classmethod
    def open(cls, path: str | Path, *, case_count: int,
             llm_config: Mapping[str, Any] | None = None) -> 'EvoFlowRuntime':
        graph = build_evo_graph(case_ids(case_count))
        runtime = open_evo_runtime(path, graph=graph, config=EvoRuntimeConfig(
            path, llm_config=dict(llm_config or {})))
        return cls(runtime, SQLiteFlowStepStore(path))

    def start_full_flow(self, *, command_id: str, run_id: str, config: Mapping[str, Any]) -> FlowStepState:
        self.set_llm_config(_llm_config(config))
        self._put_sources_once(command_id, _artifact_config(config))
        return self._submit_step(command_id=f'{command_id}:dataset', run_id=run_id, step='dataset')

    def set_llm_config(self, llm_config: Mapping[str, Any] | None) -> None:
        self.runtime.set_llm_config(dict(llm_config or {}))

    def continue_flow(self, *, command_id: str, run_id: str) -> FlowStepState:
        state = self.step_store.get(run_id)
        if state is None:
            raise ValueError('flow has not started')
        step = state.next_step
        if step is None:
            return state
        if self.runtime.controller.state(run_id).run.status == 'paused':
            step_run_id, next_step_run_id = self._prepare_step_run_ids(run_id, step)
            with self.event_log.bind(run_id=run_id, step_run_id=step_run_id, next_step_run_id=next_step_run_id):
                self.runtime.execute_intent(IntentCommandRequest(
                    f'{command_id}:resume', run_id, RunControlIntent('resume')))
        return self._submit_step(command_id=command_id, run_id=run_id, step=step)

    def continue_flow_command_id(self, *, turn_id: str, intent_index: int, run_id: str) -> str:
        state = self.step_store.get(run_id)
        if state is None:
            raise ValueError('flow has not started')
        step = state.next_step
        if step is None:
            intent = RunUntilIdleIntent(reason='continue_flow_noop')
            advance_until_idle = False
        else:
            intent = SubmitPlanIntent((STEP_ROOTS[step],), reason=f'step:{step}')
            advance_until_idle = True
        request = IntentCommandRequest('msg:pending', run_id, intent, advance_until_idle=advance_until_idle)
        fingerprint = intent_request_fingerprint(request)
        return f'msg:{turn_id}:{intent_index}:continue_flow:{fingerprint}'

    def pause_flow(self, *, command_id: str, run_id: str) -> FlowStepState:
        with self._current_step_event_scope(run_id):
            self.runtime.execute_intent(IntentCommandRequest(command_id, run_id, RunControlIntent('pause')))
        return self._mark_status(run_id, 'paused')

    def cancel_flow(self, *, command_id: str, run_id: str) -> FlowStepState:
        with self._current_step_event_scope(run_id):
            self.runtime.execute_intent(IntentCommandRequest(command_id, run_id, RunControlIntent('cancel')))
        return self._mark_status(run_id, 'cancelled')

    def run_until_idle(self, *, command_id: str, run_id: str) -> None:
        with self._current_step_event_scope(run_id):
            self.runtime.execute_intent(IntentCommandRequest(
                command_id, run_id, RunUntilIdleIntent()))
        self._complete_active_step_if_ready(run_id)

    def retry_failed_flow(self, *, command_id: str, run_id: str) -> FlowStepState:
        with self._operation_event_scope(run_id):
            self.runtime.execute_intent(IntentCommandRequest(
                command_id, run_id, RetryFailedIntent(), advance_until_idle=True))
        state = self._complete_active_step_if_ready(run_id)
        if state is None:
            raise ValueError('flow has not started')
        return state

    def materialize_flow(self, *, command_id: str, run_id: str, artifacts: tuple[ArtifactKey, ...]) -> FlowStepState:
        with self._operation_event_scope(run_id):
            self.runtime.execute_intent(
                IntentCommandRequest(command_id, run_id, MaterializeIntent(artifacts), advance_until_idle=True)
            )
        state = self._complete_active_step_if_ready(run_id)
        if state is None:
            raise ValueError('flow has not started')
        return state

    def preview_reconcile(self, artifact: ArtifactKey) -> dict[str, Any]:
        affected = tuple(sorted(self.runtime.graph.affected_artifacts_of(artifact)))
        return {
            'changed_artifact': _artifact_key_payload(artifact),
            'affected_artifacts': [_artifact_key_payload(item) for item in affected],
            'affected_count': len(affected),
        }

    def latest_ref(self, artifact: ArtifactKey) -> ArtifactRef | None:
        return self.runtime.stores.artifact_store.latest(artifact)

    def close(self) -> None:
        self.step_store.close()
        self.runtime.close()

    def _put_sources_once(self, command_id: str, config: Mapping[str, Any]) -> None:
        payloads = {
            'run.config': ArtifactPayload('RunConfig', dict(config)),
            'corpus.source_config': ArtifactPayload('CorpusSourceConfig', dict(config.get('corpus') or config)),
            'eval.target_config': ArtifactPayload('EvalTargetConfig', dict(config.get('target') or config)),
            'eval.policy': ArtifactPayload('EvalPolicy', dict(config.get('eval_policy') or {})),
            'repair.policy': ArtifactPayload('RepairPolicy', dict(config.get('repair_policy') or {})),
            'abtest.candidate_config': ArtifactPayload('CandidateConfig', _candidate_config(config)),
        }
        for artifact_id, payload in payloads.items():
            outcome = self.runtime.stores.artifact_store.put_source_once(
                f'{command_id}:source:{artifact_id}',
                ArtifactKey.of(artifact_id),
                payload,
                create_only=True,
                metadata={'bootstrap_command_id': command_id},
            )
            if outcome.status != 'committed':
                raise ValueError(f'bootstrap source write failed for {artifact_id}: {outcome.reason}')

    def _submit_step(self, *, command_id: str, run_id: str, step: StepName) -> FlowStepState:
        step_run_id, next_step_run_id = self._prepare_step_run_ids(run_id, step)
        root = STEP_ROOTS[step]
        resolver = ArtifactStoreVersionResolver(self.runtime.stores.artifact_store)
        plan = self.runtime.graph.build_plan_for_selected_artifacts(
            resolver,
            flow=step,
        )
        self._activate_step(run_id, step)
        with self.event_log.bind(run_id=run_id, step_run_id=step_run_id, next_step_run_id=next_step_run_id):
            instance = self.runtime.controller.submit_plan(
                run_id,
                plan,
                targets={root},
                reason=f'step:{step}',
                command_id=f'{command_id}:submit_plan',
            )
            self._activate_step(run_id, step, instance.plan_version)
            driver_result = self.runtime.driver.run_until_idle(run_ids=(run_id,))
        if driver_result.status != 'idle':
            raise ValueError(f'step execution did not reach idle: {driver_result.status}')
        state = self.runtime.controller.state(run_id)
        if state.run.active_plan_version != instance.plan_version:
            raise ValueError('step plan was superseded before completion')
        if state.run.status != 'completed':
            raise ValueError(f'step execution did not complete: {state.run.status}')
        ref = self.runtime.stores.artifact_store.latest(root)
        if ref is None:
            raise ValueError(f'step root was not materialized: {root}')
        return self._complete_step(run_id, step, instance.plan_version, ref)

    def _activate_step(self, run_id: str, step: StepName, plan_version: int = 0) -> FlowStepState:
        current = self.step_store.get(run_id)
        return self.step_store.put(
            FlowStepState(
                run_id,
                step,
                () if current is None else current.completed_steps,
                () if current is None else current.stale_steps,
                plan_version,
                'active',
                None if current is None else current.gate_artifact_ref,
            )
        )

    def _complete_step(
            self, run_id: str, step: StepName, plan_version: int, ref: ArtifactRef) -> FlowStepState:
        completed = tuple(item for item in STEPS if STEPS.index(item) <= STEPS.index(step))
        status: GateStatus = 'completed' if step == 'abtest' else 'paused'
        return self.step_store.put(
            FlowStepState(
                run_id,
                step,
                completed,
                (),
                plan_version,
                status,
                ref,
            )
        )

    def _complete_active_step_if_ready(self, run_id: str) -> FlowStepState | None:
        current = self.step_store.get(run_id)
        if current is None or current.gate_status != 'active' or current.current_step not in STEPS:
            return current
        controller_state = self.runtime.controller.state(run_id)
        if not controller_state.run_exists or controller_state.run.status != 'completed':
            return current
        step = current.current_step
        ref = self.runtime.stores.artifact_store.latest(STEP_ROOTS[step])
        if ref is None:
            return current
        return self._complete_step(
            run_id,
            step,
            controller_state.run.active_plan_version or current.active_step_plan_version,
            ref,
        )

    def _prepare_step_run_ids(self, run_id: str, step: StepName) -> tuple[str, str]:
        step_run_id = self.step_store.ensure_step_run_id(
            run_id,
            step,
            DEFAULT_DATASET_STEP_RUN_ID if step == 'dataset' else str(uuid.uuid4()),
        )
        next_step = _next_step(step)
        next_step_run_id = self.step_store.ensure_step_run_id(
            run_id,
            next_step or FOLLOW_UP_STEP_RUN_KEY,
            str(uuid.uuid4()),
        )
        self.step_store.set_next_step_run_id(run_id, step, next_step_run_id)
        return step_run_id, next_step_run_id

    def _prepare_follow_up_step_run_ids(self, run_id: str) -> tuple[str, str]:
        step_run_id = self.step_store.ensure_step_run_id(run_id, FOLLOW_UP_STEP_RUN_KEY, str(uuid.uuid4()))
        next_step_run_id = str(uuid.uuid4())
        self.step_store.ensure_step_run_id(
            run_id,
            f'{FOLLOW_UP_STEP_RUN_PREFIX}{step_run_id}',
            step_run_id,
        )
        self.step_store.set_next_step_run_id(
            run_id,
            f'{FOLLOW_UP_STEP_RUN_PREFIX}{step_run_id}',
            next_step_run_id,
        )
        self.step_store.replace_step_run_id(run_id, FOLLOW_UP_STEP_RUN_KEY, next_step_run_id)
        return step_run_id, next_step_run_id

    @contextmanager
    def _operation_event_scope(self, run_id: str):
        state = self.step_store.get(run_id)
        if state is not None and state.current_step == 'abtest' and state.gate_status == 'completed':
            step_run_id, next_step_run_id = self._prepare_follow_up_step_run_ids(run_id)
            with self.event_log.bind(run_id=run_id, step_run_id=step_run_id, next_step_run_id=next_step_run_id):
                yield
            return
        with self._current_step_event_scope(run_id):
            yield

    @contextmanager
    def _current_step_event_scope(self, run_id: str):
        state = self.step_store.get(run_id)
        if state is None or state.current_step not in STEPS:
            yield
            return
        step_run_id = self.step_store.ensure_step_run_id(
            run_id,
            state.current_step,
            DEFAULT_DATASET_STEP_RUN_ID if state.current_step == 'dataset' else str(uuid.uuid4()),
        )
        next_step_run_id = self.step_store.next_step_run_id_for(run_id, step_run_id)
        with self.event_log.bind(run_id=run_id, step_run_id=step_run_id, next_step_run_id=next_step_run_id):
            yield

    def _mark_status(self, run_id: str, status: GateStatus) -> FlowStepState:
        current = self.step_store.get(run_id) or FlowStepState(run_id, '', gate_status=status)
        return self.step_store.put(
            FlowStepState(
                current.run_id,
                current.current_step,
                current.completed_steps,
                current.stale_steps,
                current.active_step_plan_version,
                status,
                current.gate_artifact_ref,
            )
        )


def _state_from_row(row: sqlite3.Row) -> FlowStepState:
    version = int(row['gate_version'])
    ref = None if version < 1 else ArtifactRef(ArtifactKey(
        str(row['gate_artifact_id']), str(row['gate_partition'])), version)
    return FlowStepState(
        str(row['run_id']),
        str(row['current_step']),
        tuple(item for item in str(row['completed_steps']).split(',') if item),
        tuple(item for item in str(row['stale_steps']).split(',') if item),
        int(row['active_step_plan_version']),
        str(row['gate_status']),
        ref,
    )


def _artifact_key_payload(key: ArtifactKey) -> dict[str, str]:
    return {'artifact_id': key.artifact_id, 'partition': key.partition}


def _next_step(step: StepName) -> StepName | None:
    index = STEPS.index(step) + 1
    return STEPS[index] if index < len(STEPS) else None


def _step_run_event_log(event_log: EventLog) -> StepRunEventLog:
    return event_log if isinstance(event_log, StepRunEventLog) else StepRunEventLog(event_log)


def _llm_config(config: Mapping[str, Any]) -> Mapping[str, Any]:
    value = config.get('llm_config') or {}
    return value if isinstance(value, Mapping) else {}


def _artifact_config(config: Mapping[str, Any]) -> dict[str, Any]:
    return {str(key): value for key, value in config.items() if key != 'llm_config'}


def _candidate_config(config: Mapping[str, Any]) -> dict[str, Any]:
    candidate = dict(config.get('candidate') or {})
    for key in ('target_chat_url', 'router_admin_url', 'candidate_chat_url', 'dataset_id', 'kb_id'):
        if key in config and key not in candidate:
            candidate[key] = config[key]
    return candidate
