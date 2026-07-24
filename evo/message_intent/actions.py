from __future__ import annotations

import hashlib
import json
from collections.abc import Mapping
from dataclasses import dataclass
from types import MappingProxyType

from evo import artifacts as A
from evo.artifact_flow import ArtifactFlow
from evo.artifact_runtime import (
    ArtifactCommit,
    ArtifactDraft,
    ArtifactKey,
    ArtifactRecord,
    ArtifactRef,
    PartitionGuard,
    PartitionSet,
)

from .config_guard import patch_value, validate_config_patch
from .schemas import (
    ArtifactAction,
    CaseAction,
    ConfigPatchAction,
    FlowAction,
    PlannedAction,
    QueryAction,
)


CONFIG_ARTIFACTS = MappingProxyType({
    'run_config': A.RUN_CONFIG,
    'source_config': A.CORPUS_SOURCE_CONFIG,
    'target_config': A.EVAL_TARGET_CONFIG,
    'eval_policy': A.EVAL_POLICY,
    'repair_policy': A.REPAIR_POLICY,
    'candidate_config': A.ABTEST_CANDIDATE_CONFIG,
})


@dataclass(frozen=True)
class PreparedAction:
    action: PlannedAction
    command_id: str
    summary: str
    needs_confirmation: bool
    payload: object = None


class ActionExecutor:
    def __init__(self, flow: ArtifactFlow, thread_id: str) -> None:
        if not isinstance(flow, ArtifactFlow):
            raise TypeError('flow must be ArtifactFlow')
        if not isinstance(thread_id, str) or not thread_id.strip():
            raise ValueError('thread_id must be non-empty')
        self.flow = flow
        self.thread_id = thread_id

    async def prepare(self, action: PlannedAction, source_message_id: str
                      ) -> PreparedAction:
        command_id = _command_id(self.thread_id, source_message_id, action)
        summary = _summary(action)
        if isinstance(action, FlowAction):
            if action.stage and action.stage not in A.STEPS:
                raise ValueError(f'unknown flow stage: {action.stage}')
            return PreparedAction(
                action,
                command_id,
                summary,
                action.command == 'cancel',
            )
        if isinstance(action, QueryAction):
            _validate_query(action)
            return PreparedAction(action, command_id, summary, False)
        if isinstance(action, ArtifactAction):
            payload = await self._prepare_artifact(action, command_id)
            return PreparedAction(action, command_id, summary, True, payload)
        if isinstance(action, CaseAction):
            payload = await self._prepare_case(action, command_id)
            return PreparedAction(action, command_id, summary, True, payload)
        if isinstance(action, ConfigPatchAction):
            payload = await self._prepare_config(action, command_id)
            return PreparedAction(action, command_id, summary, True, payload)
        raise ValueError(f'action cannot be executed: {action.kind}')

    async def execute(self, prepared: PreparedAction) -> object:
        action = prepared.action
        if isinstance(action, FlowAction):
            return await self._execute_flow(action)
        if isinstance(action, QueryAction):
            return await self._execute_query(action)
        if isinstance(action, ArtifactAction) and action.command == 'retry':
            if not isinstance(prepared.payload, ArtifactKey):
                raise TypeError('prepared retry must contain ArtifactKey')
            return await self.flow.retry_artifact(
                self.thread_id,
                prepared.payload,
                request_id=prepared.command_id,
            )
        if isinstance(prepared.payload, ArtifactCommit):
            return await self._execute_commit(prepared.payload)
        raise TypeError('prepared action has no executable payload')

    async def _execute_commit(self, commit: ArtifactCommit) -> object:
        snapshot = await self.flow.commit(self.thread_id, commit)
        for write in commit.writes:
            expected = commit.expected_heads[write.key]
            version = 1 if expected is None else expected.version + 1
            record = await self.flow.head(self.thread_id, write.key)
            if (
                record is None
                or record.ref.version != version
                or record.producer != commit.producer
            ):
                raise ValueError(
                    f'artifact changed before commit completed: {write.key.artifact_id}'
                )
        return snapshot

    async def _execute_flow(self, action: FlowAction) -> object:
        if action.command == 'start':
            return await self.flow.start(self.thread_id)
        if action.command == 'approve':
            return await self.flow.approve(self.thread_id, action.stage)
        if action.command == 'pause':
            return await self.flow.pause(self.thread_id)
        if action.command == 'resume':
            return await self.flow.resume(self.thread_id)
        if action.command == 'retry':
            return await self.flow.retry(self.thread_id)
        return await self.flow.cancel(self.thread_id)

    async def _execute_query(self, action: QueryAction) -> object:
        if action.query == 'progress':
            return await self.flow.snapshot(self.thread_id)
        if action.query == 'stage_result':
            return await self._read_artifact(ArtifactKey.scalar(A.ROOTS[action.stage]), None)
        key = ArtifactKey(action.artifact_id, action.partition_key)
        if action.query == 'artifact':
            return await self._read_artifact(key, action.version)
        records = await self.flow.history(self.thread_id, key)
        return {
            'artifact': _key_data(key),
            'versions': [_record_data(record) for record in records],
        }

    async def _read_artifact(self, key: ArtifactKey,
                             version: int | None
                             ) -> Mapping[str, object]:
        record = (
            await self.flow.head(self.thread_id, key)
            if version is None
            else await self.flow.record(self.thread_id, ArtifactRef(key, version))
        )
        if record is None:
            target = f'{key.artifact_id}[{key.partition_key}]' if key.partition_key else key.artifact_id
            raise ValueError(f'artifact version not found: {target}@{version or "head"}')
        return {
            **_record_data(record),
            'value': await self.flow.read(self.thread_id, record.ref),
        }

    async def _prepare_artifact(self, action: ArtifactAction,
                                command_id: str
                                ) -> ArtifactCommit | ArtifactKey:
        key = ArtifactKey(action.artifact_id, action.partition_key)
        head = await self.flow.head(self.thread_id, key)
        if head is None:
            raise ValueError(f'artifact is not available: {action.artifact_id}')
        guards = await self._partition_guards(key)
        if action.command == 'retry':
            return key
        if action.command == 'rollback':
            version = action.version
            if version is None:
                raise ValueError('rollback requires version')
            source = await self.flow.record(
                self.thread_id,
                ArtifactRef(key, version),
            )
            if source is None:
                raise ValueError(f'artifact version not found: {action.version}')
            value = await self.flow.read(self.thread_id, source.ref)
        elif action.command == 'patch':
            current = await self.flow.read(self.thread_id, head.ref)
            value = patch_value(current, action.pointer, action.value)
        else:
            value = action.value
        return ArtifactCommit(
            command_id,
            f'user:message_intent:{action.command}',
            (ArtifactDraft(key, value),),
            {key: head.ref},
            guards,
        )

    async def _partition_guards(self, key: ArtifactKey
                                ) -> tuple[PartitionGuard, ...]:
        if not key.partition_key:
            return ()
        partition_set_id = A.PARTITION_SET_BY_ARTIFACT.get(key.artifact_id)
        if partition_set_id is None:
            raise ValueError(
                f'artifact is not a declared partitioned artifact: {key.artifact_id}'
            )
        partition_set_key = ArtifactKey.scalar(partition_set_id)
        record = await self.flow.head(self.thread_id, partition_set_key)
        if record is None:
            raise ValueError(f'partition set is not available: {partition_set_id}')
        partitions = await self.flow.read(self.thread_id, record.ref)
        if not isinstance(partitions, PartitionSet) or key.partition_key not in partitions:
            raise ValueError(f'partition is not active: {key.partition_key}')
        return (PartitionGuard(partition_set_key, key.partition_key),)

    async def _prepare_config(self, action: ConfigPatchAction,
                              command_id: str
                              ) -> ArtifactCommit:
        key = ArtifactKey.scalar(CONFIG_ARTIFACTS[action.target])
        head = await self.flow.head(self.thread_id, key)
        if head is None:
            raise ValueError(f'config artifact is not available: {action.target}')
        current = await self.flow.read(self.thread_id, head.ref)
        _, patched = validate_config_patch(self.thread_id, action, head.ref, current)
        return ArtifactCommit(
            command_id,
            'user:message_intent:config_patch',
            (ArtifactDraft(key, patched),),
            {key: head.ref},
        )

    async def _prepare_case(self, action: CaseAction,
                            command_id: str
                            ) -> ArtifactCommit:
        partition_key = ArtifactKey.scalar(A.EVAL_CASE_REQUESTS)
        partition_head = await self.flow.head(self.thread_id, partition_key)
        if partition_head is None:
            raise ValueError('case partition set is not available')
        partitions = await self.flow.read(self.thread_id, partition_head.ref)
        if not isinstance(partitions, PartitionSet):
            raise TypeError('case partition artifact must contain PartitionSet')
        active = action.case_id in partitions
        if action.command == 'delete':
            if not active:
                raise ValueError(f'case is not active: {action.case_id}')
            updated = PartitionSet(tuple(key for key in partitions.keys if key != action.case_id))
            return ArtifactCommit(
                command_id,
                'user:message_intent:delete_case',
                (ArtifactDraft(partition_key, updated),),
                {partition_key: partition_head.ref},
            )
        if active:
            raise ValueError(f'case already exists: {action.case_id}')
        updated = PartitionSet((*partitions.keys, action.case_id))
        if action.case is not None:
            value = dict(action.case)
            existing_id = str(value.get('id') or '')
            if existing_id and existing_id != action.case_id:
                raise ValueError('case.id must match case_id')
            value['id'] = action.case_id
            item_key = ArtifactKey.partition(A.EVAL_CASE, action.case_id)
        else:
            value = {
                'case_id': action.case_id,
                'instruction': action.instruction,
                'required_chunks': list(action.required_chunks),
            }
            item_key = ArtifactKey.partition(A.EVAL_CASE_REQUEST, action.case_id)
        item_head = await self.flow.head(self.thread_id, item_key)
        return ArtifactCommit(
            command_id,
            'user:message_intent:add_case',
            (
                ArtifactDraft(partition_key, updated),
                ArtifactDraft(item_key, value),
            ),
            {
                partition_key: partition_head.ref,
                item_key: None if item_head is None else item_head.ref,
            },
        )


def intent_catalog() -> Mapping[str, object]:
    artifact_ids = sorted({
        value
        for name in A.__all__
        if isinstance((value := getattr(A, name)), str)
    })
    return MappingProxyType({
        'stages': tuple({
            'name': stage,
            'result_artifact': A.ROOTS[stage],
            'requires_approval': stage in A.APPROVALS,
        } for stage in A.STEPS),
        'config_targets': dict(CONFIG_ARTIFACTS),
        'artifact_ids': tuple(artifact_ids),
        'partitioned_artifacts': dict(A.PARTITION_SET_BY_ARTIFACT),
    })


def _validate_query(action: QueryAction) -> None:
    if action.stage and action.stage not in A.STEPS:
        raise ValueError(f'unknown flow stage: {action.stage}')


def _summary(action: PlannedAction) -> str:
    if isinstance(action, FlowAction):
        return {
            'start': '启动流程',
            'approve': f'批准 {action.stage} 阶段',
            'pause': '暂停流程',
            'resume': '恢复流程',
            'retry': '重试失败流程',
            'cancel': '终止流程',
        }[action.command]
    if isinstance(action, QueryAction):
        return '查询流程或产物'
    if isinstance(action, ArtifactAction):
        return {
            'patch': '修改产物',
            'replace': '替换产物',
            'retry': '重新执行产物',
            'rollback': f'回滚到版本 {action.version}',
        }[action.command]
    if isinstance(action, CaseAction):
        return f'{"新增" if action.command == "add" else "删除"} case {action.case_id}'
    if isinstance(action, ConfigPatchAction):
        return f'修改配置 {action.target}'
    raise ValueError(f'action has no summary: {action.kind}')


def _command_id(thread_id: str, message_id: str, action: PlannedAction) -> str:
    payload = json.dumps(
        action.model_dump(mode='json'),
        ensure_ascii=False,
        sort_keys=True,
        separators=(',', ':'),
    ).encode()
    digest = hashlib.sha256(payload).hexdigest()[:24]
    return f'message:{thread_id}:{message_id}:{digest}'


def _key_data(key: ArtifactKey) -> Mapping[str, str]:
    return {
        'artifact_id': key.artifact_id,
        'partition_key': key.partition_key,
    }


def _ref_data(ref: ArtifactRef) -> Mapping[str, object]:
    return {**_key_data(ref.key), 'version': ref.version}


def _record_data(record: ArtifactRecord) -> Mapping[str, object]:
    return {
        'ref': _ref_data(record.ref),
        'producer': record.producer,
        'input_refs': [_ref_data(ref) for ref in record.input_refs],
    }


__all__ = ['ActionExecutor', 'PreparedAction', 'intent_catalog']
