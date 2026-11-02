import json

import pytest

from lazymind.chat.engine.agent_runtime.compactors import (
    compact_or_spill_tool_result,
    plan_tool_result_compaction,
)


@pytest.mark.parametrize('tool_name', ['image_editor', 'image_generator'])
@pytest.mark.parametrize('spill', [False, True])
def test_long_image_result_keeps_every_exact_output_reference(tmp_path, tool_name, spill):
    images = [
        {'local_path': f'/uploads/edited-{i}.png',
         'image_url': f'/static-files/edited-{i}.png?expires=123&sig=exact-{i}',
         'image_markdown': f'![edit {i}](/static-files/edited-{i}.png?expires=123&sig=exact-{i})'}
        for i in range(3)
    ]
    payload = {'prompt': 'long editing instructions ' * 400, 'images': images,
               **images[0], 'metadata': 'long provider metadata ' * 400}
    content = json.dumps(payload)
    options = {'workspace': str(tmp_path), 'threshold': 100 if spill else 100000}
    compacted, *_ = compact_or_spill_tool_result(tool_name, content, **options)
    planned = plan_tool_result_compaction(tool_name, content, **options)
    for result in (compacted, planned.content):
        for image in images:
            for value in image.values():
                assert value in result
        assert len(result) < len(content)
        assert 'file_id:' not in result


@pytest.mark.parametrize('spill', [False, True])
def test_image_failure_is_not_replaced_by_success_observation(tmp_path, spill):
    failure = {'details': 'diagnostic ' * 400, 'ok': False,
               'error': {'code': 'UNAVAILABLE', 'message': 'provider rejected the edit'},
               'footer': 'diagnostic ' * 400}
    content = json.dumps(failure)
    result, *_ = compact_or_spill_tool_result(
        'image_editor', content, workspace=str(tmp_path), threshold=100 if spill else 100000,
        observation={'version': 1, 'ok': False, 'value': {'image_url': '/stale-image.png'}},
    )
    assert 'UNAVAILABLE' in result
    assert 'provider rejected the edit' in result
    assert '/stale-image.png' not in result


def test_structured_observation_keeps_failure_hidden_by_rendered_text(tmp_path):
    result = plan_tool_result_compaction(
        'image_editor', 'progress ' * 400, workspace=str(tmp_path), threshold=100,
        observation={'version': 1, 'ok': False, 'error': 'UNAVAILABLE: provider HTTP 502', 'value': None},
    )
    assert 'UNAVAILABLE: provider HTTP 502' in result.content
    assert 'no successful output is confirmed' in result.content
