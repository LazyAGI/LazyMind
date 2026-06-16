from __future__ import annotations

from collections.abc import Mapping
from dataclasses import dataclass
from pathlib import Path
import sqlite3
from typing import Any, Literal

from evo.artifact_runtime import (
    ArtifactKey,
    ArtifactPayload,
    ArtifactRef,
    EvoRuntime,
    EvoRuntimeConfig,
    IntentCommandRequest,
    RunControlIntent,
    RunUntilIdleIntent,
    SubmitPlanIntent,
    open_evo_runtime,
)

from .contract import STEP_ROOTS, StepName, case_ids
from .graph import build_evo_graph

GateStatus = Literal["idle", "active", "paused", "completed", "cancelled", "stale"]
STEPS: tuple[StepName, ...] = ("dataset", "eval", "analysis", "repair", "abtest")


@dataclass(frozen=True)
class FlowStepState:
    run_id: str
    current_step: StepName | str
    completed_steps: tuple[StepName, ...] = ()
    stale_steps: tuple[StepName, ...] = ()
    active_step_plan_version: int = 0
    gate_status: GateStatus = "idle"
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
        self._connection.commit()

    def get(self, run_id: str) -> FlowStepState | None:
        row = self._connection.execute("SELECT * FROM flow_step_state WHERE run_id = ?", (run_id,)).fetchone()
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
                ",".join(state.completed_steps),
                ",".join(state.stale_steps),
                state.active_step_plan_version,
                state.gate_status,
                "" if ref is None else ref.key.artifact_id,
                "" if ref is None else ref.key.partition,
                0 if ref is None else ref.version,
            ),
        )
        self._connection.commit()
        return state

    def close(self) -> None:
        self._connection.close()


class EvoFlowRuntime:
    def __init__(self, runtime: EvoRuntime, step_store: SQLiteFlowStepStore) -> None:
        self.runtime = runtime
        self.step_store = step_store

    @classmethod
    def open(cls, path: str | Path, *, case_count: int) -> "EvoFlowRuntime":
        graph = build_evo_graph(case_ids(case_count))
        runtime = open_evo_runtime(path, graph=graph, config=EvoRuntimeConfig(path))
        return cls(runtime, SQLiteFlowStepStore(path))

    def start_full_flow(self, *, command_id: str, run_id: str, config: Mapping[str, Any]) -> FlowStepState:
        self._put_sources_once(command_id, config)
        return self._submit_step(command_id=f"{command_id}:dataset", run_id=run_id, step="dataset")

    def continue_flow(self, *, command_id: str, run_id: str) -> FlowStepState:
        state = self.step_store.get(run_id)
        if state is None:
            raise ValueError("flow has not started")
        step = state.next_step
        if step is None:
            return state
        if self.runtime.controller.state(run_id).run.status == "paused":
            self.runtime.execute_intent(IntentCommandRequest(f"{command_id}:resume", run_id, RunControlIntent("resume")))
        return self._submit_step(command_id=command_id, run_id=run_id, step=step)

    def pause_flow(self, *, command_id: str, run_id: str) -> FlowStepState:
        self.runtime.execute_intent(IntentCommandRequest(command_id, run_id, RunControlIntent("pause")))
        return self._mark_status(run_id, "paused")

    def cancel_flow(self, *, command_id: str, run_id: str) -> FlowStepState:
        self.runtime.execute_intent(IntentCommandRequest(command_id, run_id, RunControlIntent("cancel")))
        return self._mark_status(run_id, "cancelled")

    def run_until_idle(self, *, command_id: str, run_id: str) -> None:
        self.runtime.execute_intent(IntentCommandRequest(command_id, run_id, RunUntilIdleIntent(), advance_until_idle=True))

    def close(self) -> None:
        self.step_store.close()
        self.runtime.close()

    def _put_sources_once(self, command_id: str, config: Mapping[str, Any]) -> None:
        payloads = {
            "run.config": ArtifactPayload("RunConfig", dict(config)),
            "corpus.source_config": ArtifactPayload("CorpusSourceConfig", dict(config.get("corpus") or config)),
            "eval.target_config": ArtifactPayload("EvalTargetConfig", dict(config.get("target") or config)),
            "eval.policy": ArtifactPayload("EvalPolicy", dict(config.get("eval_policy") or {})),
            "repair.policy": ArtifactPayload("RepairPolicy", dict(config.get("repair_policy") or {})),
            "abtest.candidate_config": ArtifactPayload("CandidateConfig", dict(config.get("candidate") or {})),
        }
        for artifact_id, payload in payloads.items():
            outcome = self.runtime.stores.artifact_store.put_source_once(
                f"{command_id}:source:{artifact_id}",
                ArtifactKey.of(artifact_id),
                payload,
                create_only=True,
                metadata={"bootstrap_command_id": command_id},
            )
            if outcome.status != "committed":
                raise ValueError(f"bootstrap source write failed for {artifact_id}: {outcome.reason}")

    def _submit_step(self, *, command_id: str, run_id: str, step: StepName) -> FlowStepState:
        root = STEP_ROOTS[step]
        result = self.runtime.execute_intent(
            IntentCommandRequest(command_id, run_id, SubmitPlanIntent((root,), reason=f"step:{step}"), advance_until_idle=True)
        )
        if result.status != "applied" or result.plan_result is None:
            raise ValueError(result.reason or "step submit failed")
        ref = self.runtime.stores.artifact_store.latest(root)
        if ref is None:
            raise ValueError(f"step root was not materialized: {root}")
        completed = tuple(item for item in STEPS if STEPS.index(item) <= STEPS.index(step))
        status: GateStatus = "completed" if step == "abtest" else "paused"
        return self.step_store.put(
            FlowStepState(
                run_id,
                step,
                completed,
                (),
                result.plan_result.plan_version,
                status,
                ref,
            )
        )

    def _mark_status(self, run_id: str, status: GateStatus) -> FlowStepState:
        current = self.step_store.get(run_id) or FlowStepState(run_id, "", gate_status=status)
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
    version = int(row["gate_version"])
    ref = None if version < 1 else ArtifactRef(ArtifactKey(str(row["gate_artifact_id"]), str(row["gate_partition"])), version)
    return FlowStepState(
        str(row["run_id"]),
        str(row["current_step"]),
        tuple(item for item in str(row["completed_steps"]).split(",") if item),
        tuple(item for item in str(row["stale_steps"]).split(",") if item),
        int(row["active_step_plan_version"]),
        str(row["gate_status"]),
        ref,
    )
