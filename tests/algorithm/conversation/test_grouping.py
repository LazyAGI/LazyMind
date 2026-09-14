from __future__ import annotations

import asyncio
import json

import pytest

from lazymind.conversation.conversation_grouping.grouping import organize_step
from lazymind.conversation.model_client import ConversationCallError
from lazymind.conversation.conversation_grouping.schemas import GroupingRequest
from lazymind.conversation.schemas import ConversationResult
from lazymind.conversation.api import grouping_routes


def _request(conversations, groups=None, cap=64_000, **data):
    return GroupingRequest(
        input={'task_id': 'task-1', 'snapshot_id': 'snap-1',
               'snapshot_hash': 'frozen-snapshot', 'cursor': 0, 'phase': 'batch',
               'conversations': conversations, 'directory': groups or [], **data},
        llm_config={'llm': {'max_input_tokens': cap}},
    )


def _payload(prompt):
    return json.loads(prompt.split('输入：\n', 1)[1])


def _response(items, group_id='cand_shared', operations=None):
    return {'candidate_operations': operations or [],
            'assignments': [{'id': item['id'], 'group_id': group_id} for item in items]}


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
        'new_1', 'free'}


def test_bad_ids_fail_atomically_after_two_repairs():
    request = _request([{'id': 'c1', 'title': 'A', 'summary': 'B'}])
    calls = []

    def bad(_request, _prompt, **_):
        calls.append(1)
        return {'candidate_operations': [], 'assignments': [{'id': 'not-c1', 'group_id': 'free'}]}

    with pytest.raises(ConversationCallError, match='invalid_output') as caught:
        organize_step(request, call=bad)
    assert len(calls) == 3
    assert caught.value.usage['model_calls'] == 3


def test_model_request_preserves_full_input_without_output_cap():
    items = [{'id': 'c1', 'title': '主题', 'summary': '摘要' * 4000}]
    seen = []

    def model(_request, prompt, **options):
        payload = _payload(prompt)
        seen.append(payload['conversations'])
        assert 'max_tokens' not in options
        return _response(payload['conversations'], group_id='free')

    output, _ = organize_step(_request(items, cap=1000), call=model)
    assert output['processed'] == 1
    assert seen == [items]


def test_http_route_preserves_structured_organizer_failure(monkeypatch):
    failed = ConversationResult(status='failed', task_id='t1', error='invalid_output',
                                error_code='invalid_output', retryable=False,
                                usage={'model_calls': 3})
    monkeypatch.setattr(grouping_routes, 'run_grouping', lambda _request: failed)
    result = asyncio.run(grouping_routes.grouping_run(_request([])))
    assert result == failed
    assert result.error_code == 'invalid_output'
    assert result.retryable is False


def test_transport_failure_does_not_enter_output_repair():
    import requests

    calls = []

    def disconnected(*_args, **_kwargs):
        calls.append(1)
        raise requests.ConnectionError('connection lost')

    with pytest.raises(ConversationCallError, match='connection_error'):
        organize_step(_request([{'id': 'c1', 'title': 'A', 'summary': 'B'}]), call=disconnected)
    assert len(calls) == 1


def test_oversized_directory_is_sharded():
    modes = []

    def model(_request, prompt, **_):
        payload = _payload(prompt)
        modes.append(payload['mode'])
        return _response(payload['conversations'], group_id='free')
    groups = [{'id': f'group-{i}', 'short_id': f'g{i}', 'kind': 'existing',
               'name': f'组{i}', 'scope': '范围', 'version': 1, 'examples': []}
              for i in range(51)]
    output, _ = organize_step(_request([{'id': 'c1'}], groups=groups), call=model)
    assert output['assignments'] == [{'id': 'c1', 'group_id': 'free'}]
    assert modes == ['directory_scan', 'directory_scan', 'compare_directory_shards']


@pytest.mark.parametrize('code', ['input_too_large', 'output_too_large'])
def test_size_failure_is_returned_to_core_for_batch_reduction(code):
    calls = []

    def model(*_args, **_kwargs):
        calls.append(1)
        raise ConversationCallError(code)
    with pytest.raises(ConversationCallError, match=code):
        organize_step(_request([{'id': 'c1'}]), call=model)
    assert len(calls) == 1


@pytest.mark.parametrize('verdict,accepted', [
    ({'keep': ['c1'], 'reject': [], 'reason': 'accepted'}, True),
    ({'keep': [], 'reject': ['c1'], 'reason': 'coverage_gap'}, False),
    ({'keep': ['c1'], 'reject': [], 'reason': 'no_shared_scenario'}, False),
    ({'keep': ['c1'], 'reject': [], 'reason': 'boundary_too_broad'}, False),
])
def test_scope_audit_uses_complete_partition(verdict, accepted):
    output, _ = organize_step(_request([{'id': 'c1'}], phase='audit', scope='范围', scope_change={}),
                              call=lambda *_a, **_k: verdict)
    assert output['accepted'] is accepted
    assert output['audit_reason'] == verdict['reason']
    assert output['rejected_ids'] == verdict['reject']
    assert output['processed'] == 1


def test_incomplete_scope_audit_is_rejected():
    with pytest.raises(ConversationCallError, match='invalid_output'):
        organize_step(_request([{'id': 'c1'}], phase='audit', scope='范围', scope_change={}),
                      call=lambda *_a, **_k: {'keep': [], 'reject': [], 'reason': 'accepted'})


@pytest.mark.parametrize('verdict', [
    {'keep': ['c1'], 'reject': [], 'reason': 'unknown'},
    {'keep': [], 'reject': ['c1'], 'reason': 'accepted'},
    {'keep': ['c1'], 'reject': [], 'reason': 'coverage_gap'},
])
def test_scope_audit_rejects_inconsistent_reason(verdict):
    with pytest.raises(ConversationCallError, match='invalid_output'):
        organize_step(_request([{'id': 'c1'}], phase='audit', scope='范围', scope_change={}),
                      call=lambda *_a, **_k: verdict)


def test_preserve_existing_candidates_only_allows_create():
    cards = [{'id': 'candidate-1', 'short_id': 'g1', 'kind': 'candidate',
              'name': '邮件处理', 'scope': '处理邮件'}]
    calls = []

    def model(_request, prompt, **_):
        payload = _payload(prompt)
        calls.append(payload)
        if len(calls) < 3:
            return _response(payload['conversations'], group_id='g1', operations=[
                {'op': 'update', 'id': 'g1', 'scope': '机械扩大范围'},
            ])
        return _response(payload['conversations'], group_id='new_1', operations=[
            {'op': 'create', 'id': 'new_1', 'name': '独立场景', 'scope': '独立任务场景'},
        ])

    output, _ = organize_step(_request(
        [{'id': 'c1', 'summary': '独立任务'}], groups=cards,
        preserve_existing_candidates=True,
        scope_repair={'reason': 'no_shared_scenario'},
    ), call=model)
    assert len(calls) == 3
    assert all(call['preserve_existing_candidates'] is True for call in calls)
    assert all(call['scope_repair']['reason'] == 'no_shared_scenario' for call in calls)
    assert all('已有候选组保持只读' in call['repair_instruction'] for call in calls)
    assert all('重写准确边界' not in call['repair_instruction'] for call in calls)
    assert '上一次输出未通过校验' not in calls[0]['repair_instruction']
    assert all('上一次输出未通过校验' in call['repair_instruction'] for call in calls[1:])
    assert output['operations'][0]['op'] == 'create'


def test_scope_repair_keeps_retry_feedback():
    cards = [{'id': 'candidate-1', 'short_id': 'g1', 'kind': 'candidate',
              'name': '邮件处理', 'scope': '处理邮件', 'count': 2}]
    seen = []

    def model(_request, prompt, **_):
        payload = _payload(prompt)
        seen.append(payload)
        return _response(payload['conversations'], group_id='g1')

    organize_step(_request(
        [{'id': 'c1', 'summary': '邮件任务'}], groups=cards, repair=1,
        scope_repair={'reason': 'coverage_gap'},
    ), call=model)
    assert '依据审核证据' in seen[0]['repair_instruction']
    assert '上一次输出未通过校验' in seen[0]['repair_instruction']


def test_candidate_merge_limits_are_validated_in_order():
    cards = [{'id': f'candidate-{i}', 'short_id': f'g{i}', 'kind': 'candidate',
              'name': f'候选{i}', 'scope': '任务', 'count': count}
             for i, count in enumerate([3, 3, 1, 1, 1, 1], 1)]
    valid = [{'op': 'merge', 'source_ids': ['g2', 'g3', 'g4', 'g5', 'g6'],
              'target_id': 'g2', 'name': '合并', 'scope': '共同任务'}]
    output, _ = organize_step(_request([{'id': 'c1'}], groups=cards), call=lambda *_a, **_k: _response(
        [{'id': 'c1'}], group_id='g2', operations=valid))
    assert output['operations'] == valid

    invalid_operations = [
        [{'op': 'merge', 'source_ids': [f'g{i}' for i in range(1, 7)],
          'target_id': 'g1', 'name': '过多', 'scope': '任务'}],
        [{'op': 'merge', 'source_ids': ['g1', 'g2'], 'target_id': 'g1', 'name': '第一次', 'scope': '任务'},
         {'op': 'merge', 'source_ids': ['g1', 'g3'], 'target_id': 'g1', 'name': '第二次', 'scope': '任务'}],
    ]
    for operations in invalid_operations:
        with pytest.raises(ConversationCallError, match='invalid_output'):
            organize_step(_request([{'id': 'c1'}], groups=cards), call=lambda *_a, _ops=operations, **_k: _response(
                [{'id': 'c1'}], group_id='g1', operations=_ops))

    oversized = [dict(cards[0], count=6), cards[1]]
    with pytest.raises(ConversationCallError, match='invalid_output'):
        organize_step(_request([{'id': 'c1'}], groups=oversized), call=lambda *_a, **_k: _response(
            [{'id': 'c1'}], group_id='g1', operations=[
                {'op': 'merge', 'source_ids': ['g1', 'g2'], 'target_id': 'g1',
                 'name': '过大', 'scope': '任务'},
            ]))
    output, _ = organize_step(_request([{'id': 'c1'}], groups=oversized), call=lambda *_a, **_k: _response(
        [{'id': 'c1'}], group_id='g1'))
    assert output['assignments'] == [{'id': 'c1', 'group_id': 'g1'}]


def test_new_candidate_can_be_merged_before_assignment():
    cards = [{'id': 'candidate-1', 'short_id': 'g1', 'kind': 'candidate',
              'name': '邮件', 'scope': '邮件任务', 'count': 5}]
    operations = [
        {'op': 'create', 'id': 'new_1', 'name': '发邮件', 'scope': '发送邮件'},
        {'op': 'merge', 'source_ids': ['g1', 'new_1'], 'target_id': 'g1',
         'name': '邮件处理', 'scope': '收发邮件'},
    ]
    output, _ = organize_step(_request([{'id': 'c1'}], groups=cards), call=lambda *_a, **_k: _response(
        [{'id': 'c1'}], group_id='g1', operations=operations))
    assert output['operations'] == operations
