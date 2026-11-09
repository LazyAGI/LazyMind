"""F5 regressions from the saved 2026-10-29 outline (27 relative-weight hints).

Source: workflow 7d33ca77d81f46d4bfcdc7f0f0859bb2, step
pss_38f25d7fa3964782ada55d32fe3c4611. The original outline is kept verbatim.
"""
import copy
import json
from pathlib import Path
import re
from unittest.mock import Mock

import pytest

from lazyllm.tools.writer.utils.serialization import get_markdown_outline_targets, parse_markdown_outline_instructions
from lazymind.document_tools.paper import workflow
from lazymind.document_tools.paper.runtime_events import bind_runtime_events
from lazymind.document_tools.paper.writing import write_outline
from lazymind.document_tools.paper.writing.common import _heading_signature, _outline_chart_owners, _prepare_sections


@pytest.fixture
def recorded_outline():
    path = Path(__file__).resolve().parents[4] / 'tests/fixtures/paper-writing/outline-budget-20261029.md'
    return path.read_text(encoding='utf-8')


def prepared_outline(outline, constraints):
    return {'writing_task': {'constraints': constraints}, 'writing_context': {},
            'outline': outline, 'charts': [{'id': f'fig{i}'} for i in range(1, 6)]}


def without_sidecars(outline):
    return re.sub(r'<!-- writer:outline .*? -->', '', outline)


@pytest.mark.parametrize('language,requested,target', [
    ('zh-CN', None, 8000), ('en', None, 5000), ('zh-CN', 6600, 6600), ('en', 6600, 6600),
])
def test_recorded_outline_saves_length_in_preview_pool_and_next_draft(
    recorded_outline, monkeypatch, language, requested, target,
):
    original = parse_markdown_outline_instructions(recorded_outline)
    assert len(original) == 27
    assert original['sec-001-001']['target_chars'] == 2
    assert original['sec-001-001']['outline_description'].endswith('（约2字）。')
    toolkit = Mock()

    def stream_outline(**kwargs):
        kwargs['on_delta'](recorded_outline)
        return recorded_outline

    toolkit.stream_outline.side_effect = stream_outline
    monkeypatch.setattr(write_outline, 'WriterCreateToolkit', lambda: toolkit)
    monkeypatch.setattr(write_outline, 'prepare_writer_context', lambda inputs, **kw: {
        'writing_task': {'constraints': inputs['constraints']}, 'writing_context': {},
        'outline': None, 'charts': [],
    })
    pool = workflow.pool_from_business({
        'topic': '论文写作 Agent', 'question': '比较分阶段与人工介入',
        'constraints': {'paper_language': language, 'word_target': requested},
    })
    before = copy.deepcopy(pool)
    emitted = []
    with bind_runtime_events(emitted.append, 'write_outline_document') as events:
        result = workflow.execute_step('write_outline', pool)
        events.complete()
    assert pool == before
    assert result['pool']['constraints']['word_target'] == target
    outline = workflow.material(result['pool'], 'outline')['content']
    assert result['document'] == outline
    assert outline != recorded_outline
    assert without_sidecars(outline) == without_sidecars(recorded_outline)
    cleaned = parse_markdown_outline_instructions(outline)
    chapter_budgets = [cleaned[f'sec-{i:03}']['target_chars'] for i in range(1, 8)]
    assert sum(chapter_budgets) == target
    for node_id, payload in cleaned.items():
        budget = payload['target_chars']
        hint = f'(about {budget} words)' if language == 'en' else f'（约{budget}字）'
        assert hint in payload['outline_description']
        assert {k: v for k, v in payload.items() if k not in {'target_chars', 'outline_description'}} == {
            k: v for k, v in original[node_id].items() if k not in {'target_chars', 'outline_description'}
        }
        children = [child['target_chars'] for key, child in cleaned.items()
                    if key.startswith(node_id + '-') and key.count('-') == node_id.count('-') + 1]
        if children:
            assert sum(children) == budget
    # A serialized material pool is the boundary between outline approval and drafting.
    saved = json.loads(json.dumps(result['pool'], ensure_ascii=False))
    inputs = workflow.business_input('write_document', saved)
    assert inputs['constraints']['word_target'] == target
    assert inputs['outline'] == outline
    _, targets = get_markdown_outline_targets(outline)
    plan = {'instructions': [dict(
        instruction_id=str(i), content_ref={'heading_path': path, 'occurrence': occurrence},
        section_title=path[-1], section_goal='展开论证', required_points=[], expected_blocks=[],
        fact_constraints=[], meta={'target_chars': 1},
    ) for i, (_, path, occurrence, _) in enumerate(targets)]}
    sections = _prepare_sections(plan, outline, inputs['constraints'])['instructions']
    assert [item['meta']['word_target'] for item in sections] == chapter_budgets
    assert all(item['meta']['count_unit'] == ('words' if language == 'en' else 'chinese_characters')
               for item in sections)
    # Reusing the confirmed outline must preserve both the allocation and the prose.
    confirmed = prepared_outline(outline, inputs['constraints'])
    assert write_outline.prepare_outline(confirmed, Mock()) == outline
    starts = [event for event in emitted if event['type'] == 'artifact_stream_start']
    assert len(starts) == 2  # Replace the raw Writer preview instead of appending to it.
    latest = [event for event in emitted if event.get('stream_id') == starts[-1]['stream_id']]
    assert ''.join(event.get('delta', '') for event in latest) == outline
    assert latest[-1]['type'] == 'artifact_stream_end'
    assert any(event['type'] == 'artifact_stream_abort' for event in emitted)
    assert [call[0] for call in toolkit.method_calls] == ['stream_outline']


@pytest.mark.parametrize('language', ['zh-CN', 'en'])
def test_recorded_explicit_length_displays_real_hierarchical_budgets(recorded_outline, language):
    # The seven recorded section weights total 66; 6600 gives inspectable budgets.
    prepared = prepared_outline(recorded_outline, {'paper_language': language, 'word_target': 6600})
    before = copy.deepcopy(prepared)
    toolkit = Mock()
    outline = write_outline.prepare_outline(prepared, toolkit)
    assert prepared == before
    toolkit.generate_outline.assert_not_called()
    payloads = parse_markdown_outline_instructions(outline)
    sections = [payloads[f'sec-{i:03}']['target_chars'] for i in range(1, 8)]
    assert sections == [600, 1200, 1200, 1200, 1000, 800, 600]
    assert sum(sections) == 6600
    assert payloads['sec-001-001']['target_chars'] == 200
    for node_id, payload in payloads.items():
        budget = payload['target_chars']
        hint = f'(about {budget} words)' if language == 'en' else f'（约{budget}字）'
        assert hint in payload['outline_description']
        assert ('characters' if language == 'en' else '（约2字）') not in payload['outline_description']
        children = [child['target_chars'] for key, child in payloads.items()
                    if key.startswith(node_id + '-') and key.count('-') == node_id.count('-') + 1]
        if children:
            assert sum(children) == budget
    assert without_sidecars(outline) == without_sidecars(recorded_outline)
    assert _heading_signature(outline) == _heading_signature(recorded_outline)
    assert _outline_chart_owners(outline, prepared['charts']) == _outline_chart_owners(
        recorded_outline, prepared['charts'])
    prepared['outline'] = outline
    assert write_outline.prepare_outline(prepared, toolkit) == outline


@pytest.mark.parametrize('target', [None, 6600])
def test_confirmed_recorded_outline_is_cleaned_without_regeneration(recorded_outline, target):
    prepared = prepared_outline(recorded_outline, {'paper_language': 'zh-CN', 'word_target': target})
    toolkit = Mock()
    outline = write_outline.prepare_outline(prepared, toolkit)
    prepared['outline'] = outline
    assert write_outline.prepare_outline(prepared, toolkit) == outline
    assert without_sidecars(outline) == without_sidecars(recorded_outline)
    toolkit.generate_outline.assert_not_called()
    toolkit.stream_outline.assert_not_called()


@pytest.mark.parametrize('description,expected', [
    ('解释60个问题、128ms与0.7权重（约3字）。', '解释60个问题、128ms与0.7权重。'),
    ('Discuss 60 tasks and latency 128ms (about 3 characters).', 'Discuss 60 tasks and latency 128ms.'),
    ('Discuss 60 tasks (about 3 words).', 'Discuss 60 tasks.'),
    ('引用用户原话“约3字”，再讨论60个问题。', '引用用户原话“约3字”，再讨论60个问题。'),
    ('用户要求本节约200字（约200字）。', '用户要求本节约200字（约200字）。'),
])
def test_only_matching_metadata_hints_change_not_user_prose_or_fenced_examples(description, expected):
    payload = {'node_id': 'method', 'target_chars': 3, 'outline_description': description,
               'custom': {'preserve': True}, 'context_relations': [], 'subtasks': []}
    sidecar = '<!-- writer:outline ' + json.dumps(payload, ensure_ascii=False) + ' -->'
    source = ('# Paper\n\n<a id="block-method"></a>\n## Method\n' + sidecar
              + '\n\n原始说明（约2字）。[文献:SRC-1]\n\n```markdown\n' + sidecar + '\n```\n'
              + '\n<!-- writer:outline {invalid JSON} -->\n')
    prepared = {'writing_task': {'constraints': {'word_target': None}}, 'writing_context': {},
                'outline': source, 'charts': []}
    result = write_outline.prepare_outline(prepared, Mock())
    changed = parse_markdown_outline_instructions(result)['method']
    assert changed == {**payload, 'outline_description': expected}
    assert source.split('\n\n原始说明', 1)[1] == result.split('\n\n原始说明', 1)[1]
    assert without_sidecars(source) == without_sidecars(result)


def test_explicit_length_without_writer_metadata_keeps_user_outline_verbatim():
    source = '# 用户确认的论文\n\n## 方法\n比较60个问题与128ms延迟。\n\n## 结果\n'
    prepared = {'writing_task': {'constraints': {'word_target': 8000, 'paper_language': 'zh-CN'}},
                'writing_context': {}, 'outline': source, 'charts': []}
    assert write_outline.prepare_outline(prepared, Mock()) == source


def test_identical_descriptions_are_cleaned_only_for_the_matching_heading_budget():
    description = '讨论研究结果（约3字）。'
    source = '# Paper\n\n' + '\n\n'.join(
        f'<a id="block-{node}"></a>\n## {node}\n<!-- writer:outline '
        + json.dumps({'node_id': node, 'target_chars': budget, 'outline_description': description}) + ' -->'
        for node, budget in [('method', 3), ('results', 2)])
    prepared = {'writing_task': {'constraints': {}}, 'writing_context': {}, 'outline': source, 'charts': []}
    result = write_outline.prepare_outline(prepared, Mock())
    payloads = parse_markdown_outline_instructions(result)
    assert payloads['method']['outline_description'] == '讨论研究结果。'
    assert payloads['results']['outline_description'] == description
