"""Offline regressions from the 2026-10-28 failed drafting/revision records."""
import copy
import json
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import Mock

import pytest

from lazyllm.tools.writer.data_models.revision import MarkdownModifyPlan, StringReplaceSet
from lazymind.document_tools.paper import contracts
from lazymind.document_tools.paper.runtime_events import bind_runtime_events
from lazymind.document_tools.paper.writing import recovery, revise_document, write_document
from lazymind.document_tools.toolkits import WriterRevisionToolkit


@pytest.fixture
def record():
    path = Path(__file__).resolve().parents[4] / 'tests/fixtures/paper-writing/recovery-20261028.json'
    return json.loads(path.read_text())


@pytest.fixture(autouse=True)
def no_external_correction(monkeypatch):
    monkeypatch.setattr(recovery, 'AutoModel', lambda **kw: pytest.fail('Unexpected recovery model call'))


def correction_model(monkeypatch, respond):
    requests = []

    def call(prompt):
        items = json.loads(prompt.split('no other keys or items.\n', 1)[1])
        requests.append(items)
        return json.dumps(respond(items), ensure_ascii=False)

    monkeypatch.setattr(recovery, 'AutoModel', lambda **kw: call)
    return requests


def draft(monkeypatch, body, candidates, material_identifiers=()):
    outline = '# Paper\n\n## Discussion'
    inputs = contracts.validate_input('write_document', {'topic': 'Paper', 'outline': outline})
    prepared = dict(writing_task={'constraints': contracts.normalize_constraints({})},
                    writing_context={}, outline=outline,
                    charts=[], reference_candidates=candidates, material_identifiers=material_identifiers)
    monkeypatch.setattr(write_document, 'prepare_writer_context', lambda *a, **kw: prepared)
    toolkit = Mock()
    toolkit.generate_section_instructions.return_value = json.dumps({'section_instructions': {'instructions': [dict(
        instruction_id='discussion', content_ref={'heading_path': ['Paper', 'Discussion']},
        section_title='Discussion', section_goal='Discuss evidence', required_points=[],
        expected_blocks=[], fact_constraints=[], meta={},
    )]}})
    toolkit.stream_draft_blocks_markdown.return_value = json.dumps([body])
    toolkit.generate_draft_document_markdown.return_value = json.dumps({'draft_document': body})
    result = write_document._write_draft(inputs, toolkit=toolkit)
    toolkit.check_consistency.assert_not_called()
    return result['manuscript']


@pytest.mark.parametrize('attempt', [0, 1])
def test_recorded_draft_converts_known_markers_before_strict_validation(record, monkeypatch, attempt):
    from lazymind.document_tools.paper.publication import normalize_source_citations
    source = '# Paper\n\n## Discussion\n\n' + record['draft_passages'][attempt]
    expected = normalize_source_citations(source, record['references'])
    assert source != expected  # These exact passages triggered both historical failures.
    assert draft(monkeypatch, source, record['references']) == expected


@pytest.mark.parametrize('marker', ['[SRC-UNKNOWN]', '[文献:SRC-UNKNOWN]', '[文献:upload-unknown]', '[1]'])
def test_draft_normalization_still_rejects_unknown_and_unmanaged_citations(record, monkeypatch, marker):
    with pytest.raises(ValueError):
        draft(monkeypatch, '# Paper\n\n## Discussion\n\n' + marker, record['references'])


def test_recorded_locator_typo_gets_one_verbatim_correction(record, monkeypatch):
    plan = MarkdownModifyPlan.model_validate(record['locator']['plan'])
    before = plan.model_dump()
    with pytest.raises(ValueError, match='no longer identifies'):
        revise_document._operation_ranges(record['source'], plan)
    locator = '人工介入更适合被理解为高风险中间工件上的选择性判断'
    requests = correction_model(monkeypatch, lambda items: {'repairs': [dict(index=0, locator_text=locator)]})
    fixed, ranges, repaired = revise_document._validated_ranges(record['source'], plan)
    assert repaired and len(requests) == 1
    assert '被理解成' in requests[0][0]['operation']['locator_text']
    assert locator in requests[0][0]['source_scope']
    assert locator in record['source'][ranges[0][0][0]:ranges[0][0][1]]
    expected = copy.deepcopy(before)
    expected['instructions'][0]['locator_text'] = locator
    assert fixed.model_dump() == expected
    assert plan.model_dump() == before


@pytest.mark.parametrize('reply', [
    {'repairs': []},
    {'repairs': [{'index': 99, 'locator_text': '原文'}]},
    {'repairs': [{'index': False, 'locator_text': '原文'}]},
    {'repairs': [{'index': 0, 'locator_text': '人工介入更适合被理解成高风险中间工件上的选择性判断'}]},
    {'repairs': [{'index': 0, 'locator_text': '不存在的原文', 'target_scope': 'section'}]},
    {'repairs': [{'index': 0, 'locator_text': '原文'}, {'index': 0, 'locator_text': '原文'}]},
])
def test_invalid_locator_correction_is_not_retried_or_allowed_to_expand_scope(record, monkeypatch, reply):
    plan = MarkdownModifyPlan.model_validate(record['locator']['plan'])
    before = plan.model_dump()
    requests = correction_model(monkeypatch, lambda items: reply)
    with pytest.raises(ValueError):
        revise_document._validated_ranges(record['source'], plan)
    assert len(requests) == 1
    assert plan.model_dump() == before


@pytest.mark.parametrize('attempt,bad_ids', [
    ('first', ['op-04', 'op-09', 'op-11']), ('third', ['R7-I3']),
])
def test_recorded_actual_scope_violations_remain_rejected(record, attempt, bad_ids):
    case = record[attempt]
    plan = MarkdownModifyPlan.model_validate(case['plan'])
    allowed = [pair for ranges in revise_document._operation_ranges(record['source'], plan) for pair in ranges]
    for change in case['replacements']['replacements']:
        if change['replacement_id'] in bad_ids:
            with pytest.raises(ValueError, match='exceeds the planned'):
                revise_document._bounded_replacements(record['source'], allowed, {'replacements': [change]})


def test_recorded_failed_patches_are_corrected_without_regenerating_valid_patch(record, monkeypatch):
    case = record['first']
    plan = MarkdownModifyPlan.model_validate(case['plan'])
    before = copy.deepcopy(case['replacements'])
    allowed = [pair for ranges in revise_document._operation_ranges(record['source'], plan) for pair in ranges]

    def respond(items):
        assert [item['replacement']['replacement_id'] for item in items] == ['op-04', 'op-09', 'op-11']
        repairs = []
        for item in items:
            # Simulate corrected model output restricted to the exact original plan passage.
            passage = item['allowed_passages'][0]
            repairs.append(dict(index=item['index'], old_string=passage,
                                new_string='以下为拟议修订，尚未完成真实验证。' + passage))
        return {'repairs': repairs}

    requests = correction_model(monkeypatch, respond)
    changes, _ = revise_document._validated_changes(
        record['source'], plan, allowed, case['replacements'], allow_repair=True,
    )
    assert len(requests) == 1
    valid = StringReplaceSet.model_validate(case['replacements']).replacements[0].model_dump()
    assert next(item for item in changes['replacements'] if item['replacement_id'] == 'op-01') == valid
    assert case['replacements'] == before
    revise_document._bounded_replacements(record['source'], allowed, changes)


def test_recorded_patch_still_failing_after_correction_stops_once(record, monkeypatch):
    case = record['third']
    plan = MarkdownModifyPlan.model_validate(case['plan'])
    allowed = [pair for ranges in revise_document._operation_ranges(record['source'], plan) for pair in ranges]
    failed = case['replacements']['replacements'][0]
    requests = correction_model(monkeypatch, lambda items: {'repairs': [dict(
        index=0, old_string=failed['old_string'], new_string=failed['new_string'],
    )]})
    with pytest.raises(ValueError, match='exceeds the planned'):
        revise_document._validated_changes(record['source'], plan, allowed, case['replacements'], allow_repair=True)
    assert len(requests) == 1


def revision_case():
    source = ('# Paper\n\n## Methods\n\nOriginal method. Keep background.'
              '\n\n## Results\n\nOriginal result. Keep evidence.')
    operations, changes = [], []
    for section, word, issue in [('Methods', 'method', 'R1'), ('Results', 'result', 'R2')]:
        reference = {'heading_path': ['Paper', section]}
        operations.append(dict(content_ref=reference, modify_type='update', target_scope='fragment',
                               locator_text=f'Original {word}.', instruction=f'Clarify {word}.',
                               meta={'issue_ids': [issue]}))
        changes.append(dict(content_ref=reference, old_string=f'Original {word}.', new_string=f'Clarified {word}.'))
    changes[1].update(old_string='Original result. Keep evidence.', new_string='Clarified result. Changed evidence.')
    data = dict(manuscript=source, charts=[], reference_candidates=[], writing_context={'context_id': 'recovery-test'},
                selected_issues=[dict(id=key, action_type='text_revision') for key in ['R1', 'R2']],
                writing_task={'query': 'Clarify selected text.', 'meta': {'review_driven': True}})
    tools = SimpleNamespace(
        locate_revision_target=Mock(return_value='{}'),
        generate_modify_plan=Mock(return_value=json.dumps({'scope': 'document', 'instructions': operations})),
        generate_string_replace_set=Mock(return_value=json.dumps({'replacements': changes})),
        apply_string_replace=Mock(wraps=WriterRevisionToolkit().apply_string_replace),
    )
    return data, tools, operations


@pytest.mark.parametrize('invalid', [False, True, 'citation', 'chart', 'noop'])
def test_recovery_applies_all_validated_edits_once_and_never_publishes_invalid_result(monkeypatch, invalid):
    data, tools, _ = revision_case()
    before = copy.deepcopy(data)
    old, new = ('Original result.', 'Clarified result.')
    if invalid is True:
        old, new = ('Original result. Keep evidence.', 'Clarified result. Changed evidence.')
    elif invalid == 'citation':
        new += '[文献:unknown]'
    elif invalid == 'chart':
        new += '\n\n[图表:unknown]\n\n'
    elif invalid == 'noop':
        new = old
    requests = correction_model(monkeypatch, lambda items: {'repairs': [dict(index=1, old_string=old, new_string=new)]})
    emitted = []
    with bind_runtime_events(emitted.append, 'revise_document_document'):
        result = revise_document._modify_manuscript(data, {'word_target': None}, Mock(), editor=tools)
        expected = data['manuscript'].replace('Original method.', 'Clarified method.')
        if not invalid:
            expected = expected.replace('Original result.', 'Clarified result.')
        assert result['manuscript'] == expected
        assert bool(result['_pending_edits']) == bool(invalid)
        assert tools.apply_string_replace.call_count == (1 if invalid else 2)
        assert len([event for event in emitted if event['type'] == 'artifact_stream_end']) == 1
    assert data == before
    assert len(requests) == 1
    tools.generate_modify_plan.assert_called_once()
    tools.generate_string_replace_set.assert_called_once()


def test_locator_and_patch_correction_share_one_attempt(monkeypatch):
    data, tools, operations = revision_case()
    operations[0]['locator_text'] = 'Paraphrased method.'
    tools.generate_modify_plan.return_value = json.dumps({'scope': 'document', 'instructions': operations})
    requests = correction_model(monkeypatch, lambda items: {'repairs': [dict(index=0, locator_text='Original method.')]})
    result = revise_document._modify_manuscript(data, {'word_target': None}, Mock(), editor=tools)
    assert result['manuscript'] == data['manuscript'].replace('Original method.', 'Clarified method.')
    assert result['_pending_edits']
    assert len(requests) == 1
    tools.apply_string_replace.assert_called_once()


def test_oversized_local_correction_stops_before_model_request(record):
    plan = MarkdownModifyPlan.model_validate(record['locator']['plan'])
    locator = '人工介入更适合被理解为高风险中间工件上的选择性判断'
    source = record['source'].replace(locator, '原始材料' * 40000 + locator, 1)
    with pytest.raises(ValueError, match='超过预算'):
        revise_document._validated_ranges(source, plan)


def test_reader_provenance_repairs_only_affected_blocks_and_preserves_code_and_citation_targets(monkeypatch):
    source = ('# Paper\n\nRecords from `write_document:analysis` contain 30 tasks.\n\n'
              '| Record | Source |\n| --- | --- |\n| Cache | `table-private` |\n\n'
              'Supported claim [1](#paper-ref-table-private) and [文献:table-private].\n\n'
              '```text\nwrite_document:analysis\n\nprivate example\n```\n\nRetained paragraph.')
    edits = ['Operational records contain 30 tasks.',
             '| Record | Source |\n| --- | --- |\n| Cache | Context-reuse record |']
    requests = correction_model(monkeypatch, lambda items: {
        'repairs': [dict(index=item['index'], new_string=edits[item['index']]) for item in items],
    })
    output = recovery.repair_reader_provenance([source], ['write_document:analysis', 'table-private'])[0]
    assert len(requests) == 1 and len(requests[0]) == 2
    assert output == source.replace(requests[0][0]['source_scope'], edits[0]).replace(
        requests[0][1]['source_scope'], edits[1])


def test_draft_corrects_registered_processing_identities_before_publication(monkeypatch):
    source = '# Paper\n\n## Discussion\n\nSource write_document:analysis contains 30 tasks.'
    requests = correction_model(monkeypatch, lambda items: {
        'repairs': [dict(index=0, new_string='Operational records contain 30 tasks.')],
    })
    result = draft(monkeypatch, source, [], ['write_document:analysis'])
    assert result == source.replace('Source write_document:analysis contains', 'Operational records contain')
    assert len(requests) == 1


@pytest.mark.parametrize('marker', ['[文献:M1]', '[文献:M1](#study-background)'])
def test_registered_background_markers_are_removed_only_from_visible_prose(marker):
    source = ('# Paper\n\nOur data ' + marker + ' supports this observation. Prior work [文献:M2].\n\n'
              '```text\n[文献:M1]\n```\n\n<!-- [文献:M1] -->\n\n'
              '[Public source](https://example.org/[文献:M1])')
    result = recovery.repair_reader_provenance([source], ['[文献:M1]'])[0]
    assert result == source.replace('Our data ' + marker, 'Our data ')
    assert '[文献:M2]' in result


def test_draft_and_result_boundary_accept_added_subsections(monkeypatch):
    body = '# Paper\n\n## Discussion\n\nStudy findings.\n\n### Resource Use\n\nResource observations.'
    assert draft(monkeypatch, body, []) == body
    write_document.validate_result(dict(manuscript=body), dict(
        mode='draft', manuscript=None, papers=[], charts=[], outline='# Paper\n\n## Discussion'))


@pytest.mark.parametrize('body', [
    '# Paper\n\n## Discussion\n\n### Second\n\n### First',
    '# Paper\n\n## Discussion\n\n### First',
    '# Paper\n\n## Discussion\n\n### First\n\n### Second\n\n## Unplanned Main Section',
])
def test_draft_outline_validation_keeps_planned_sections_and_order(body):
    inputs = dict(mode='draft', manuscript=None, papers=[], charts=[],
                  outline='# Paper\n\n## Discussion\n\n### First\n\n### Second')
    with pytest.raises(ValueError, match='大纲与稿件不一致'):
        write_document.validate_result(dict(manuscript=body), inputs)


@pytest.mark.parametrize('corrected', ['', 'Records from write_document:analysis.'])
def test_reader_provenance_rejects_empty_or_still_internal_text(monkeypatch, corrected):
    requests = correction_model(monkeypatch, lambda items: {
        'repairs': [dict(index=0, new_string=corrected)],
    })
    with pytest.raises(ValueError, match='Reader-facing source'):
        recovery.repair_reader_provenance(['Records from write_document:analysis.'], ['write_document:analysis'])
    assert len(requests) == 1


def test_reader_provenance_correction_keeps_the_original_revision_scope_guard(monkeypatch):
    source = '# Paper\n\n## Methods\n\nOriginal method. Source write_document:analysis.'
    plan = MarkdownModifyPlan.model_validate(dict(scope='document', instructions=[]))
    start = source.index('Original method.')
    allowed = [(start, start + len('Original method.'))]
    requests = correction_model(monkeypatch, lambda items: {
        'repairs': [dict(index=0, new_string='Clarified method. Source operational records.')],
    })
    generated = {'replacements': [dict(old_string='Original method. Source write_document:analysis.',
                                       new_string='Clarified method. Source write_document:analysis.')]}
    with pytest.raises(ValueError, match='exceeds the planned'):
        revise_document._validated_changes(source, plan, allowed, generated, allow_repair=False,
                                           material_identifiers=['write_document:analysis'])
    assert len(requests) == 1
