from __future__ import annotations

import json
import asyncio

import pytest

from lazymind.chat.service.conversation_organizer import (
    OrganizerInput, _apply_response, _audit_scope_changes,
    _initial_state, organize_step,
)
from lazymind.chat.service.llm_task import LLMTaskCallError, LLMTaskRequest
from lazymind.chat.service.llm_task import LLMTaskResult
from lazymind.chat.api import llm_task_routes


def _request(conversations, groups=None, checkpoint=None, cap=64_000):
    return LLMTaskRequest(
        task_type='conversation.organize_step',
        input={'data': {'task_id': 'task-1', 'snapshot_id': 'snap-1',
                        'snapshot': {'conversations': conversations, 'groups': groups or []},
                        'checkpoint': checkpoint, 'request_cap_tokens': cap}},
        llm_config={'llm': {'max_input_tokens': cap}},
    )


def _payload(prompt):
    return json.loads(prompt.split('输入：\n', 1)[1])


def test_prompt_declares_complete_response_protocol():
    seen = []

    def model(_request, prompt, **_):
        payload = _payload(prompt)
        seen.append(payload)
        return _response(payload['conversations'], group_id='free')

    organize_step(_request([{'id': 'c1', 'title': 'A', 'summary': 'B'}]), call=model)
    schema = seen[0]['response_schema']
    assert schema['required_top_level_fields'] == ['candidate_operations', 'assignments']
    assert set(schema['assignments']['items']) == {'id', 'group_id'}
    assert schema['example']['candidate_operations'][0]['op'] == 'create'
    assert {item['group_id'] for item in schema['example']['assignments']} == {
        'cand_example_task', 'free'}


def _response(items, group_id='cand_shared', operations=None):
    return {'candidate_operations': operations or [],
            'assignments': [{'id': item['id'], 'group_id': group_id} for item in items]}


def test_cross_batch_candidate_reaches_min_three():
    items = [{'id': str(index), 'title': f'日志诊断 {index}', 'summary': '排查服务日志 ' + '细节' * 700}
             for index in range(51)]

    def model(_request, prompt, **_):
        data = _payload(prompt)
        operations = []
        if not any(group['id'] == 'cand_shared' for group in data['groups']):
            operations = [{'op': 'create', 'id': 'cand_shared', 'name': '服务日志诊断',
                           'scope': '服务运行日志的定位与诊断'}]
        return _response(data['conversations'], operations=operations)

    checkpoint = None
    result = None
    calls = 0
    while result is None or not result[0]['done']:
        result = organize_step(_request(items, checkpoint=checkpoint, cap=6000), call=model)
        checkpoint = result[0]['checkpoint']
        calls += 1
    assert calls >= 2
    assert result[0]['proposal']['new_groups'][0]['conversation_ids'] == [str(index) for index in range(51)]
    assert result[0]['proposal']['free_conversation_ids'] == []


def test_existing_group_metadata_is_immutable_and_can_receive_one_item():
    request = _request([{'id': 'c1', 'title': '周报', 'summary': '整理本周工作'}], groups=[
        {'id': 'g1', 'name': '工作记录', 'scope': '', 'version': 7, 'examples': []}])
    data = OrganizerInput.model_validate(request.input.data)
    state = _initial_state(data)
    original = json.loads(json.dumps(state))
    with pytest.raises(ValueError, match='candidate'):
        _apply_response(data, state, data.snapshot.conversations, {
            'candidate_operations': [{'op': 'rename', 'id': 'g1', 'name': '被改写'}],
            'assignments': [{'id': 'c1', 'group_id': 'g1'}]})
    assert state == original

    output, _ = organize_step(request, call=lambda _r, prompt, **_: _response(
        _payload(prompt)['conversations'], group_id='g1'))
    assert output['proposal']['existing_group_assignments'] == [
        {'group_id': 'g1', 'group_version': 7, 'conversation_ids': ['c1']}]


def test_bad_ids_fail_atomically_after_two_repairs():
    request = _request([{'id': 'c1', 'title': 'A', 'summary': 'B'}])
    calls = []

    def bad(_request, _prompt, **_):
        calls.append(1)
        return {'candidate_operations': [], 'assignments': [{'id': 'not-c1', 'group_id': 'free'}]}

    with pytest.raises(LLMTaskCallError, match='invalid_output') as caught:
        organize_step(request, call=bad)
    assert len(calls) == 3
    assert caught.value.usage['model_calls'] == 3
    assert request.input.data['checkpoint'] is None


def test_oversized_directory_is_sharded_and_output_truncation_shrinks_batch():
    items = [{'id': str(index), 'title': 'T', 'summary': 'S' * 180} for index in range(5)]
    groups = [{'id': f'g{index}', 'name': f'组{index}', 'scope': '范围' * 90, 'version': 1,
               'examples': [{'id': f'x{index}', 'title': '例', 'summary': '摘要' * 30}]}
              for index in range(51)]
    scan_modes = []

    def sharded(_request, prompt, **_):
        data = _payload(prompt)
        scan_modes.append(data['mode'])
        if data['mode'] == 'compare_directory_shards':
            return _response(data['conversations'], group_id='free')
        return _response(data['conversations'], group_id='free')

    output, _ = organize_step(_request(items[:1], groups=groups, cap=6000), call=sharded)
    assert output['done']
    assert scan_modes.count('directory_scan') > 1
    assert scan_modes[-1] == 'compare_directory_shards'

    sizes = []

    def truncated(_request, prompt, **_):
        data = _payload(prompt)
        sizes.append(len(data['conversations']))
        if len(sizes) <= 2:
            raise LLMTaskCallError('output_too_large')
        return _response(data['conversations'], group_id='free')

    output, _ = organize_step(_request(items), call=truncated)
    assert sizes[0] > sizes[1] > sizes[2]
    assert output['progress']['processed'] == sizes[2]


def test_done_checkpoint_replay_is_model_free_and_stable():
    request = _request([{'id': 'c1', 'title': 'A', 'summary': 'B'}])
    first, _ = organize_step(request, call=lambda _r, prompt, **_: _response(
        _payload(prompt)['conversations'], group_id='free'))
    replay = _request(request.input.data['snapshot']['conversations'], checkpoint=first['checkpoint'])
    second, usage = organize_step(replay, call=lambda *_a, **_k: pytest.fail('must not call model'))
    assert second == first
    assert usage['model_calls'] == 0


def test_model_request_preserves_full_input_without_output_cap():
    items = [{'id': 'c1', 'title': '主题', 'summary': '摘要' * 4000}]
    seen = []

    def model(_request, prompt, **options):
        payload = _payload(prompt)
        seen.append(payload['conversations'])
        assert 'max_tokens' not in options
        return _response(payload['conversations'], group_id='free')

    output, _ = organize_step(_request(items, cap=1000), call=model)
    assert output['done']
    assert seen == [items]


def test_scope_audit_simulates_merge_before_following_update():
    request = _request([
        {'id': 'c1', 'title': 'A', 'summary': 'A'},
        {'id': 'c2', 'title': 'B', 'summary': 'B'},
    ])
    data = OrganizerInput.model_validate(request.input.data)
    state = _initial_state(data, request)
    state.update(cursor=2, candidates={
        'cand_a': {'name': 'A', 'scope': 'A'}, 'cand_b': {'name': 'B', 'scope': 'B'}},
        assignments={'c1': 'cand_a', 'c2': 'cand_b'})
    audited_ids = []

    def audit(_request, prompt, **_):
        payload = _payload(prompt)
        ids = [item['id'] for item in payload['items']]
        audited_ids.append(ids)
        return {'keep': ids, 'reject': []}

    response, _ = _audit_scope_changes(request, data, state, {
        'candidate_operations': [
            {'op': 'merge', 'source_ids': ['cand_a', 'cand_b'], 'target_id': 'cand_a',
             'name': 'AB', 'scope': 'AB'},
            {'op': 'update', 'id': 'cand_a', 'scope': 'AB scope'},
        ], 'assignments': []}, audit)
    assert audited_ids == [['c1', 'c2'], ['c1', 'c2']]
    assert response['candidate_operations'][1]['reviewed_member_ids'] == ['c1', 'c2']


def test_legacy_final_checkpoint_finishes_without_model_calls():
    items = [{'id': f'c{i}', 'title': str(i), 'summary': str(i)} for i in range(4)]
    request = _request(items)
    data = OrganizerInput.model_validate(request.input.data)
    state = _initial_state(data, request)
    state.update(cursor=4, stage='final', assignments={f'c{i}': 'cand_shared' for i in range(4)},
                 candidates={'cand_shared': {'name': '共同场景', 'scope': '共同范围'}},
                 final_blocks=[['cand_shared']], final_pair_cursor=1)
    output, usage = organize_step(_request(items, checkpoint=state),
                                  call=lambda *_a, **_k: pytest.fail('must not call model'))
    assert output['done']
    assert usage['model_calls'] == 0
    assert output['proposal']['new_groups'][0]['conversation_ids'] == [item['id'] for item in items]


def test_checkpoint_rejects_dangling_alias():
    request = _request([{'id': 'c1', 'title': 'A', 'summary': 'B'}])
    data = OrganizerInput.model_validate(request.input.data)
    state = _initial_state(data, request)
    state['aliases'] = {'cand_old': 'cand_missing'}
    with pytest.raises(LLMTaskCallError, match='invalid_output'):
        organize_step(_request(request.input.data['snapshot']['conversations'], checkpoint=state),
                      call=lambda *_a, **_k: {})


def test_http_route_preserves_structured_organizer_failure(monkeypatch):
    failed = LLMTaskResult(status='failed', task_id='t1', error='invalid_output',
                           error_code='invalid_output', retryable=False,
                           usage={'model_calls': 3})
    monkeypatch.setattr(llm_task_routes, 'run_llm_task', lambda _request: failed)
    result = asyncio.run(llm_task_routes.llm_task_run(_request([])))
    assert result == failed
    assert result.error_code == 'invalid_output'
    assert result.retryable is False


def test_create_name_collision_with_formal_group_is_repaired():
    items = [{'id': 'c1', 'title': '设计周报', 'summary': '整理设计工作'}]
    groups = [{'id': 'g1', 'name': '工作记录', 'scope': '', 'version': 1, 'examples': []}]
    calls = 0

    def model(_request, prompt, **_):
        nonlocal calls
        calls += 1
        conversation = _payload(prompt)['conversations'][0]
        name = ' 工作记录 ' if calls == 1 else '设计工作记录'
        return _response([conversation], group_id='cand_design', operations=[{
            'op': 'create', 'id': 'cand_design', 'name': name, 'scope': '设计工作记录'}])

    output, usage = organize_step(_request(items, groups=groups), call=model)
    assert output['done']
    assert output['checkpoint']['candidates']['cand_design']['name'] == '设计工作记录'
    assert usage['model_calls'] == 2


def test_rename_name_collision_with_candidate_is_repaired_atomically():
    items = [
        {'id': 'c1', 'title': 'A1', 'summary': 'A'},
        {'id': 'c2', 'title': 'B1', 'summary': 'B'},
        {'id': 'c3', 'title': 'B2', 'summary': 'B'},
    ]
    request = _request(items)
    data = OrganizerInput.model_validate(request.input.data)
    checkpoint = _initial_state(data, request)
    checkpoint.update(cursor=2, assignments={'c1': 'cand_a', 'c2': 'cand_b'}, candidates={
        'cand_a': {'name': 'Alpha', 'scope': 'A'},
        'cand_b': {'name': 'Beta', 'scope': 'B'},
    })
    calls = 0

    def model(_request, prompt, **_):
        nonlocal calls
        calls += 1
        conversation = _payload(prompt)['conversations'][0]
        operations = ([{'op': 'rename', 'id': 'cand_b', 'name': ' alpha '}] if calls == 1
                      else [{'op': 'rename', 'id': 'cand_b', 'name': 'Beta Work'}])
        return _response([conversation], group_id='cand_b', operations=operations)

    output, usage = organize_step(_request(items, checkpoint=checkpoint), call=model)
    assert output['checkpoint']['candidates']['cand_a']['name'] == 'Alpha'
    assert output['checkpoint']['candidates']['cand_b']['name'] == 'Beta Work'
    assert usage['model_calls'] == 2


def test_transport_failure_does_not_enter_output_repair():
    import requests

    calls = []

    def disconnected(*_args, **_kwargs):
        calls.append(1)
        raise requests.ConnectionError('connection lost')

    with pytest.raises(LLMTaskCallError, match='connection_error'):
        organize_step(_request([{'id': 'c1', 'title': 'A', 'summary': 'B'}]), call=disconnected)
    assert len(calls) == 1


def test_cancel_execution_stops_worker_and_fences_late_start(monkeypatch):
    from fastapi import HTTPException
    from lazymind.chat.service import organizer_stream

    class Worker:
        alive = True
        terminated = False

        def is_alive(self):
            return self.alive

        def terminate(self):
            self.terminated = True
            self.alive = False

        def join(self, _timeout):
            pass

    async def scenario():
        worker = Worker()
        execution = organizer_stream.Execution(worker, None)
        monkeypatch.setattr(organizer_stream, '_executions', {'execution': execution})
        monkeypatch.setattr(organizer_stream, '_canceled', set())
        assert await organizer_stream.cancel_execution('execution') == {'settled': True}
        assert worker.terminated and execution.canceled
        assert await organizer_stream.cancel_execution('late') == {'settled': True}
        with pytest.raises(HTTPException) as caught:
            await organizer_stream.stream_execution('late', _request([]))
        assert caught.value.status_code == 409

    asyncio.run(scenario())
