from __future__ import annotations

from collections.abc import Iterable, Mapping
from pathlib import Path

from evo.artifact_runtime import (
    ArtifactCommit,
    ArtifactDraft,
    ArtifactKey,
    ArtifactRecord,
    ArtifactRef,
    ArtifactRetryRequest,
    ArtifactRuntime,
    AttemptSnapshot,
    DefinitionError,
    ProgressEvent,
    RuntimeSnapshot,
)

from .definition import FlowDefinition
from .projection import project_flow
from .state import FlowSnapshot, StageProgress


class ArtifactFlow:
    def __init__(self, runtime: ArtifactRuntime, definition: FlowDefinition) -> None:
        if not isinstance(runtime, ArtifactRuntime):
            raise TypeError('runtime must be ArtifactRuntime')
        if not isinstance(definition, FlowDefinition):
            raise TypeError('definition must be FlowDefinition')
        self._runtime = runtime
        self._definition = definition
        self._approval_keys = frozenset(
            stage.approval_key
            for stage in definition.stages
            if stage.approval_key is not None
        )

    @classmethod
    async def open(cls, root: str | Path, definition: FlowDefinition, *,
                   max_concurrency: int = 4, terminate_timeout: float = 1.0
                   ) -> ArtifactFlow:
        if not isinstance(definition, FlowDefinition):
            raise TypeError('definition must be FlowDefinition')
        runtime = await ArtifactRuntime.open(
            root,
            definition.operations,
            max_concurrency=max_concurrency,
            terminate_timeout=terminate_timeout,
        )
        return cls(runtime, definition)

    async def create(self, run_id: str, initial_commit: ArtifactCommit | None = None
                     ) -> FlowSnapshot:
        if initial_commit is not None:
            self._validate_user_commit(initial_commit)
        return await self._project(await self._runtime.create(run_id, initial_commit))

    async def start(self, run_id: str) -> FlowSnapshot:
        return await self._project(await self._runtime.start(run_id))

    async def approve(self, run_id: str, stage: str) -> FlowSnapshot:
        current = await self.snapshot(run_id)
        progress = self._approval_target(current, stage)
        if progress.approved:
            return current
        result_ref = progress.result_ref
        approval_key = progress.approval_key
        if result_ref is None or approval_key is None:
            raise RuntimeError('approval target is incomplete')

        approval = await self._runtime.head(run_id, approval_key)
        commit = ArtifactCommit(
            _approval_commit_id(progress.stage, result_ref),
            'user:approval',
            (ArtifactDraft(
                approval_key,
                {
                    'stage': progress.stage,
                    'result': {
                        'artifact_id': result_ref.key.artifact_id,
                        'version': result_ref.version,
                    },
                },
                (result_ref,),
            ),),
            {
                result_ref.key: result_ref,
                approval_key: None if approval is None else approval.ref,
            },
        )
        return await self._project(await self._runtime.commit(run_id, commit))

    async def commit(self, run_id: str, commit: ArtifactCommit) -> FlowSnapshot:
        self._validate_user_commit(commit)
        return await self._project(await self._runtime.commit(run_id, commit))

    async def retry_artifact(self, run_id: str, artifact_key: ArtifactKey, *,
                             request_id: str
                             ) -> FlowSnapshot:
        return await self._project(await self._runtime.retry_artifact(
            run_id,
            artifact_key,
            request_id=request_id,
        ))

    async def pause(self, run_id: str) -> FlowSnapshot:
        return await self._project(await self._runtime.pause(run_id))

    async def resume(self, run_id: str) -> FlowSnapshot:
        return await self._project(await self._runtime.resume(run_id))

    async def retry(self, run_id: str) -> FlowSnapshot:
        return await self._project(await self._runtime.retry(run_id))

    async def cancel(self, run_id: str) -> FlowSnapshot:
        return await self._project(await self._runtime.cancel(run_id))

    async def wait_until_boundary(self, run_id: str, *, timeout: float = 10.0
                                  ) -> FlowSnapshot:
        snapshot = await self._runtime.wait_until_settled(run_id, timeout=timeout)
        return await self._project(snapshot)

    async def snapshot(self, run_id: str) -> FlowSnapshot:
        return await self._project(await self._runtime.snapshot(run_id))

    async def read(self, run_id: str, ref: ArtifactRef) -> object:
        return await self._runtime.read(run_id, ref)

    async def read_many(self, run_id: str, refs: Iterable[ArtifactRef]
                        ) -> Mapping[ArtifactRef, object]:
        return await self._runtime.read_many(run_id, refs)

    async def record(self, run_id: str, ref: ArtifactRef) -> ArtifactRecord | None:
        return await self._runtime.record(run_id, ref)

    async def head(self, run_id: str, key: ArtifactKey) -> ArtifactRecord | None:
        return await self._runtime.head(run_id, key)

    async def history(self, run_id: str, key: ArtifactKey) -> tuple[ArtifactRecord, ...]:
        return await self._runtime.history(run_id, key)

    async def attempts(self, run_id: str) -> tuple[AttemptSnapshot, ...]:
        return await self._runtime.attempts(run_id)

    async def progress_events(self, run_id: str, attempt_id: str | None = None
                              ) -> tuple[ProgressEvent, ...]:
        return await self._runtime.progress_events(run_id, attempt_id)

    async def retry_requests(self, run_id: str) -> tuple[ArtifactRetryRequest, ...]:
        return await self._runtime.retry_requests(run_id)

    async def run_ids(self) -> tuple[str, ...]:
        return await self._runtime.run_ids()

    async def has_run(self, run_id: str) -> bool:
        return await self._runtime.has_run(run_id)

    async def release(self, run_id: str) -> None:
        await self._runtime.release(run_id)

    async def delete_run(self, run_id: str) -> None:
        await self._runtime.delete_run(run_id)

    async def close(self) -> None:
        await self._runtime.close()

    async def _project(self, runtime: RuntimeSnapshot) -> FlowSnapshot:
        retries = await self._runtime.retry_requests(runtime.run_id)
        return project_flow(self._definition, runtime, retries)

    def _validate_user_commit(self, commit: ArtifactCommit) -> None:
        if not isinstance(commit, ArtifactCommit):
            raise TypeError('commit must be ArtifactCommit')
        forbidden = sorted(
            (write.key for write in commit.writes if write.key in self._approval_keys),
            key=lambda key: (key.artifact_id, key.partition_key),
        )
        if forbidden:
            names = ', '.join(key.artifact_id for key in forbidden)
            raise DefinitionError(f'approval artifacts require approve(): {names}')

    def _approval_target(self, snapshot: FlowSnapshot, stage: str) -> StageProgress:
        target = next(
            (progress for progress in snapshot.stages if progress.stage == stage),
            None,
        )
        if target is None:
            raise DefinitionError(f'unknown flow stage: {stage}')
        if target.approval_key is None:
            raise DefinitionError(f'flow stage does not require approval: {stage}')
        if not target.has_result:
            raise DefinitionError(f'flow stage is not complete: {stage}')
        if target.approved:
            return target
        pending = snapshot.pending_approval
        if pending is None or pending.stage != stage:
            raise DefinitionError(f'flow is not awaiting approval for: {stage}')
        return target


def _approval_commit_id(stage: str, result_ref: ArtifactRef) -> str:
    return f'approval:{stage}:{result_ref.key.artifact_id}:{result_ref.version}'


__all__ = ['ArtifactFlow']
