"""Public state, model identity and request lifecycle contracts (B1/B2/B4/B5)."""

import asyncio
from copy import deepcopy

import pytest

from evo import artifacts as A
from evo.artifact_flow import ArtifactFlow, FlowDefinition, FlowSnapshot, FlowStage, StageProgress
from evo.artifact_runtime import (
    ArtifactCommit, ArtifactDraft, ArtifactKey, ArtifactRef, DefinitionError,
    OperationResult, RuntimeSnapshot, one, operation, scalar,
)
from evo.message_intent.actions import ActionExecutor, PreparedAction
from evo.message_intent.config_guard import ConfigValidationError
from evo.message_intent.schemas import ArtifactAction, ConfigPatchAction, MessageRequest
from evo.service.core import EvoService
from evo.service.public import public_thread_state


def model_config():
    role = {'source': 'openai', 'model': 'example-model',
            'base_url': 'https://example.test/v1', 'api_key': 'test-only-key',
            'opencode': {'provider': 'openai', 'provider_model': 'example-model',
                         'model': 'openai/example-model', 'npm': '@ai-sdk/openai',
                         'base_url': 'https://example.test/v1'}}
    return {'llm': deepcopy(role), 'evo_llm': role,
            'embed_main': {'source': 'openai', 'model': 'example-embed',
                           'base_url': 'https://example.test/v1', 'api_key': 'test-only-key'}}


@operation(op_id='test.evolution.copy', inputs={'config': one(A.RUN_CONFIG),
           **{f'seed_{i}': one(name) for i, name in enumerate(A.SEEDS) if name != A.RUN_CONFIG}},
           outputs={'result': scalar('result')}, execution='cooperative')
async def copy_config(ctx, config, seed_1, seed_2, seed_3, seed_4, seed_5):
    return OperationResult({'result': {'num_case': config['num_case']}})


def definition():
    return FlowDefinition((copy_config,), (FlowStage('dataset', ArtifactKey.scalar('result')),))


def request():
    return {'mode': 'interactive', 'llm_config': model_config(),
            'inputs': {'kb_id': ['test-kb'], 'num_case': 1, 'algorithm_id': 'test-baseline',
                       'router_chat_url': 'http://example.test/chat',
                       'router_admin_url': 'http://example.test/admin'}}


GATES = {}


@pytest.mark.asyncio
@pytest.mark.parametrize('mode', ['interactive', 'auto'])
async def test_explicit_pause_survives_auto_driver_and_reopen_until_resume(tmp_path, mode):
    flow_definition = FlowDefinition((blocked,), (FlowStage('dataset', ArtifactKey.scalar('result')),))
    service = await EvoService.open(tmp_path, flow_definition)
    release = asyncio.Event()
    release.set()
    try:
        payload = request()
        payload['mode'] = mode
        thread_id = (await service.create_thread(payload))['thread_id']
        entered, cancelled = asyncio.Event(), asyncio.Event()
        GATES[thread_id] = (entered, cancelled, release)
        await service.start(thread_id, {})
        await asyncio.wait_for(entered.wait(), 2)
        await service.pause(thread_id, {'command_id': 'pause'})
        driver = service._auto_tasks.get(thread_id)
        if driver is not None:
            await asyncio.wait_for(asyncio.shield(driver), 2)
        assert (await service.public_thread(thread_id))['runtime_status'] == 'paused'
        await service.close()
        service = await EvoService.open(tmp_path, flow_definition)
        assert (await service.public_thread(thread_id))['runtime_status'] == 'paused'
        assert thread_id not in service._auto_tasks
        entered.clear()
        await service.resume(thread_id, {'command_id': 'resume'})
        await asyncio.wait_for(entered.wait(), 2)
        assert (await service.public_thread(thread_id))['runtime_status'] == 'running'
    finally:
        release.set()
        await service.close()
        GATES.clear()


@operation(op_id='test.evolution.blocked', inputs={'config': one(A.RUN_CONFIG),
           **{f'seed_{i}': one(name) for i, name in enumerate(A.SEEDS) if name != A.RUN_CONFIG}},
           outputs={'result': scalar('result')}, execution='cooperative')
async def blocked(ctx, config, seed_1, seed_2, seed_3, seed_4, seed_5):
    entered, cancelled, release = GATES[ctx.run_id]
    entered.set()
    try:
        await asyncio.Event().wait()
    except asyncio.CancelledError:
        cancelled.set()
        await release.wait()
        return OperationResult({'result': 'late result must not be committed'})


@pytest.mark.asyncio
@pytest.mark.parametrize('stage', A.STEPS)
async def test_cancel_each_stage_exposes_cleanup_and_discards_late_result(tmp_path, stage):
    flow_definition = FlowDefinition((blocked,), (FlowStage(stage, ArtifactKey.scalar('result')),))
    service = await EvoService.open(tmp_path, flow_definition, terminate_timeout=2)
    release = asyncio.Event()
    try:
        thread_id = (await service.create_thread(request()))['thread_id']
        entered, cancelled = asyncio.Event(), asyncio.Event()
        GATES[thread_id] = (entered, cancelled, release)
        await service.start(thread_id, {})
        await asyncio.wait_for(entered.wait(), 2)
        cancel = asyncio.create_task(service.cancel(thread_id, {'command_id': 'cancel-once'}))
        await asyncio.wait_for(cancelled.wait(), 2)
        state = await service.public_thread(thread_id)
        assert state['runtime_status'] == 'cancelling'
        assert state['cleanup_pending'] is True
        release.set()
        await asyncio.wait_for(cancel, 2)
        await service.cancel(thread_id, {'command_id': 'cancel-once'})
        assert (await service.public_thread(thread_id))['runtime_status'] == 'cancelled'
        assert await service.flow.head(thread_id, ArtifactKey.scalar('result')) is None
        assert await service.flow.head(thread_id, ArtifactKey.scalar(A.RUN_CONFIG)) is not None
        with pytest.raises(DefinitionError):
            await service.flow.resume(thread_id)
    finally:
        release.set()
        await service.close()
        GATES.clear()


@pytest.mark.asyncio
async def test_cleanup_timeout_is_visible_and_retryable(tmp_path):
    flow_definition = FlowDefinition((blocked,), (FlowStage('dataset', ArtifactKey.scalar('result')),))
    service = await EvoService.open(tmp_path, flow_definition, terminate_timeout=0.02)
    release = asyncio.Event()
    try:
        thread_id = (await service.create_thread(request()))['thread_id']
        entered, cancelled = asyncio.Event(), asyncio.Event()
        GATES[thread_id] = (entered, cancelled, release)
        await service.start(thread_id, {})
        await asyncio.wait_for(entered.wait(), 2)
        with pytest.raises(Exception, match='cleanup'):
            await asyncio.wait_for(service.cancel(thread_id, {}), 1)
        state = await service.public_thread(thread_id)
        assert state['runtime_status'] == 'failed'
        assert state['cleanup_pending'] is True
        release.set()
        await service.cancel(thread_id, {})
        assert (await service.public_thread(thread_id))['runtime_status'] == 'cancelled'
        assert await service.flow.head(thread_id, ArtifactKey.scalar('result')) is None
    finally:
        release.set()
        await service.close()
        GATES.clear()


@pytest.mark.asyncio
async def test_failed_worker_with_unverified_children_keeps_cleanup_visible(tmp_path, monkeypatch):
    from evo.artifact_runtime import session
    from evo.artifact_runtime.execution import ExecutionCleanupError
    failed, cleanup_attempted = asyncio.Event(), asyncio.Event()
    cleanup_allowed = False
    class Handle:
        async def wait(self):
            await failed.wait()
            raise ExecutionCleanupError('worker descendants unverified', unverified=True)
        async def terminate(self):
            cleanup_attempted.set()
            if not cleanup_allowed:
                raise ExecutionCleanupError('worker descendants unverified', unverified=True)
    async def start(*args, **kwargs):
        return Handle()
    monkeypatch.setattr(session, 'start_execution', start)
    service = await EvoService.open(tmp_path, definition())
    try:
        thread_id = (await service.create_thread(request()))['thread_id']
        await service.start(thread_id, {})
        failed.set()
        await asyncio.wait_for(cleanup_attempted.wait(), 2)
        state = await service.public_thread(thread_id)
        assert state['status'] == 'failed'
        assert state['cleanup_pending'] is True
        cleanup_allowed = True
        await service.cancel(thread_id, {})
        assert (await service.public_thread(thread_id))['cleanup_pending'] is False
    finally:
        cleanup_allowed = True
        await service.close()


@pytest.mark.asyncio
async def test_accepted_create_survives_disconnect_and_persists(tmp_path, monkeypatch):
    service = await EvoService.open(tmp_path, definition())
    entered, release, persisted = asyncio.Event(), asyncio.Event(), asyncio.Event()
    original = service.flow.create
    async def create(*args):
        entered.set()
        await release.wait()
        result = await original(*args)
        persisted.set()
        return result
    monkeypatch.setattr(service.flow, 'create', create)
    try:
        task = asyncio.create_task(service.create_thread(request()))
        await asyncio.wait_for(entered.wait(), 2)
        task.cancel()
        with pytest.raises(asyncio.CancelledError):
            await task
        release.set()
        await asyncio.wait_for(persisted.wait(), 2)
        assert len(await service.flow.run_ids()) == 1
    finally:
        release.set()
        await service.close()
    reopened = await EvoService.open(tmp_path, definition())
    try:
        assert len((await reopened.list_threads(10, ''))['items']) == 1
    finally:
        await reopened.close()


@pytest.mark.parametrize('status,legacy', [('cancelling', 'running'), ('pausing', 'running'),
                                          ('cancelled', 'cancelled'), ('completed', 'ended')])
def test_public_state_keeps_legacy_and_exposes_runtime(status, legacy):
    snapshot = FlowSnapshot(RuntimeSnapshot('test', status=status),
                            (StageProgress('dataset', ArtifactKey.scalar('result'), status=status),))
    public = public_thread_state(snapshot)
    assert public['status'] == legacy
    assert public['runtime_status'] == status
    assert public['cleanup_pending'] is False


@pytest.mark.asyncio
@pytest.mark.parametrize('kind', ['config', 'patch', 'replace', 'rollback', 'prepared'])
@pytest.mark.parametrize('field,value', [('model', 'other-model'), ('source', 'other-provider'),
                                        ('base_url', 'https://other.test'), ('api_key', 'other-key'),
                                        ('opencode', {})])
async def test_all_mutation_paths_reject_model_changes(tmp_path, kind, field, value):
    service = await EvoService.open(tmp_path, definition())
    try:
        thread_id = (await service.create_thread(request()))['thread_id']
        executor = ActionExecutor(service.flow, thread_id)
        config = await service._run_config(thread_id)
        changed = deepcopy(config)
        changed['llm_config']['evo_llm'][field] = value
        pointer = '/llm_config/evo_llm/' + field
        key = ArtifactKey.scalar(A.RUN_CONFIG)
        if kind == 'config':
            action = ConfigPatchAction(kind='config_patch', target='run_config', pointer=pointer, value=value)
        elif kind == 'patch':
            action = ArtifactAction(kind='artifact', command='patch', artifact_id=A.RUN_CONFIG,
                                    pointer=pointer, value=value)
        else:
            action = ArtifactAction(kind='artifact', command='replace', artifact_id=A.RUN_CONFIG, value=changed)
        if kind == 'rollback':
            # Legacy history may contain a model change. Rolling back to it must not revive it.
            await service.flow.commit(thread_id, ArtifactCommit('legacy', 'user:legacy',
                (ArtifactDraft(key, changed),), {key: ArtifactRef(key, 1)}))
            await service.flow.commit(thread_id, ArtifactCommit('restore', 'user:legacy',
                (ArtifactDraft(key, config),), {key: ArtifactRef(key, 2)}))
            action = ArtifactAction(kind='artifact', command='rollback', artifact_id=A.RUN_CONFIG, version=2)
        with pytest.raises(ConfigValidationError):
            if kind == 'prepared':
                commit = ArtifactCommit('bypass', 'user:test', (ArtifactDraft(key, changed),),
                                         {key: ArtifactRef(key, 1)})
                await executor.execute(PreparedAction(action, 'bypass', 'test', True, commit))
            else:
                await executor.prepare(action, 'test-message')
        assert (await service._run_config(thread_id))['llm_config'] == config['llm_config']
    finally:
        await service.close()


@pytest.mark.asyncio
async def test_non_model_config_change_remains_available(tmp_path):
    service = await EvoService.open(tmp_path, definition())
    try:
        thread_id = (await service.create_thread(request()))['thread_id']
        executor = ActionExecutor(service.flow, thread_id)
        action = ArtifactAction(kind='artifact', command='patch', artifact_id=A.RUN_CONFIG,
                                pointer='/num_case', value=2)
        await executor.execute(await executor.prepare(action, 'change-size'))
        assert (await service._run_config(thread_id))['num_case'] == 2
    finally:
        await service.close()


@pytest.mark.asyncio
async def test_cancel_completed_is_idempotent_and_retains_results(tmp_path):
    service = await EvoService.open(tmp_path, definition())
    try:
        thread_id = (await service.create_thread(request()))['thread_id']
        await service.start(thread_id, {})
        await service.flow.wait_until_boundary(thread_id)
        for _ in range(2):
            await service.cancel(thread_id, {'command_id': 'same-cancel'})
        assert (await service.public_thread(thread_id))['status'] == 'ended'
        assert await service.flow.head(thread_id, ArtifactKey.scalar('result')) is not None
    finally:
        await service.close()


@pytest.mark.asyncio
async def test_accepted_message_survives_disconnected_request_and_replays(tmp_path, monkeypatch):
    from evo.message_intent import planner
    from evo.message_intent.schemas import TurnPlan
    import threading
    entered, finish = threading.Event(), threading.Event()
    calls = []
    def plan(*args):
        calls.append(1)
        entered.set()
        assert finish.wait(5)
        return TurnPlan.model_validate({'turn_decision': 'next_action', 'next_action': {'kind': 'query', 'query': 'progress'}})
    monkeypatch.setattr(planner, 'plan_next_turn', plan)
    service = await EvoService.open(tmp_path, definition())
    try:
        thread_id = (await service.create_thread(request()))['thread_id']
        message = MessageRequest(message_id='same-message', text='查看进度')
        task = asyncio.create_task(service.message(thread_id, message))
        assert await asyncio.to_thread(entered.wait, 5)
        task.cancel()
        with pytest.raises(asyncio.CancelledError):
            await task
        finish.set()
        result = await service.message(thread_id, message)
        assert result.message_id == 'same-message'
        assert len(calls) == 1
        assert len((await service.message_history(thread_id, 10, ''))['items']) == 1
    finally:
        finish.set()
        await service.close()
