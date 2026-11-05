"""Regressions from all three failed revisions in conversation 71eb9c6a-1618-47cf-a50e-4a213cab6413."""
import copy
import json
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import Mock

import pytest

from lazyllm.tools.writer.data_models.revision import MarkdownModifyPlan
from lazymind.document_tools.paper.runtime_events import bind_runtime_events
from lazymind.document_tools.paper.writing import matching, recovery, revise_document
from lazymind.document_tools.toolkits import WriterRevisionToolkit


@pytest.fixture
def record():
    path = Path(__file__).resolve().parents[4] / 'tests/fixtures/paper-writing/revision-20261029.json'
    return json.loads(path.read_text())


def test_recorded_third_plan_resolves_all_locators_without_another_model(record, monkeypatch):
    model = Mock(side_effect=AssertionError('Format-only locating must not call a model'))
    monkeypatch.setattr(recovery, 'AutoModel', lambda **kwargs: model)
    plan = MarkdownModifyPlan.model_validate(record['attempts'][2]['plan'])
    before = plan.model_dump()
    fixed, ranges, repaired = revise_document._validated_ranges(record['source'], plan)
    assert len(ranges) == len(plan.instructions) == 27
    assert not repaired
    assert plan.model_dump() == before
    for index in [6, 7, 8, 9, 21, 22]:
        operation = fixed.instructions[index]
        assert operation.target_scope == 'fragment'
        start, end = ranges[index][0]
        assert record['source'][start:end] == operation.locator_text
        assert '\n\n' not in operation.locator_text
        assert operation.content_ref == plan.instructions[index].content_ref
        assert operation.instruction == plan.instructions[index].instruction
    for start, end in ranges[23]:
        assert record['source'][start:end].count('#paper-ref-SRC-019') == 1
    model.assert_not_called()


def test_recorded_correction_recovers_the_missing_citation_bracket(record, monkeypatch):
    attempt = record['attempts'][2]
    correction = next(item for item in attempt['locator_correction']['repairs'] if item['index'] == 23)
    assert record['source'].count(correction['locator_text']) == 0
    model = Mock(return_value=json.dumps({'repairs': [correction]}))
    monkeypatch.setattr(recovery, 'AutoModel', lambda **kwargs: model)
    fixed = recovery.repair_locators(record['source'], MarkdownModifyPlan.model_validate(attempt['plan']), {23: 'stale'})
    assert record['source'].count(fixed.instructions[23].locator_text) == 1
    revise_document._operation_ranges(record['source'], fixed.model_copy(update={
        'instructions': [fixed.instructions[23]],
    }))
    model.assert_called_once()


@pytest.mark.parametrize('user_request', [
    '只针对 5 个重要问题进行落实，修改论文', '只改最重要的五条意见', '仅处理前5条问题',
    '前5条意见', 'Only the 5 most important issues', 'top 5 comments',
])
def test_counted_selection_cannot_expand_to_the_recorded_all_issues(record, monkeypatch, user_request):
    selected = ['R2', 'R4', 'R6', 'R8', 'R10']
    model = Mock(side_effect=[json.dumps(record['observed_selection']), json.dumps({
        'intent': 'review', 'selection': 'subset', 'issue_ids': selected,
    })])
    monkeypatch.setattr(revise_document, 'AutoModel', lambda **kwargs: model)
    before = copy.deepcopy(record['review'])
    assert revise_document.resolve_review_issues(user_request, record['review']) == selected
    assert model.call_count == 2
    assert json.loads(model.call_args.args[0].split('\n', 1)[1])['required_count'] == 5
    assert record['review'] == before


@pytest.mark.parametrize('result', [
    {'intent': 'review', 'selection': 'all'},
    {'intent': 'review', 'selection': 'subset', 'issue_ids': ['R1', 'R2', 'R3', 'R4']},
    {'intent': 'review', 'selection': 'subset', 'issue_ids': ['R1', 'R2', 'R3', 'R4', 'R4']},
    {'intent': 'review', 'selection': 'subset', 'issue_ids': ['R1', 'R2', 'R3', 'R4', 'R99']},
    {'intent': 'direct'},
])
def test_count_still_wrong_after_one_correction_requires_input(record, monkeypatch, result):
    model = Mock(return_value=json.dumps(result))
    monkeypatch.setattr(revise_document, 'AutoModel', lambda **kwargs: model)
    with pytest.raises(revise_document.RevisionInputRequired, match='只处理 5 条'):
        revise_document.resolve_review_issues(record['request'], record['review'])
    assert model.call_count == 2


@pytest.mark.parametrize('user_request', ['只改第5条', '只改语言类意见', '只改5个字'])
def test_issue_numbers_categories_and_text_length_are_not_issue_counts(monkeypatch, user_request):
    model = Mock(return_value='{"intent":"review","selection":"subset","issue_ids":["R5"]}')
    monkeypatch.setattr(revise_document, 'AutoModel', lambda **kwargs: model)
    assert revise_document.resolve_review_issues(user_request, {'issues': [{'id': 'R5'}]}) == ['R5']
    model.assert_called_once()


@pytest.mark.parametrize('locator', [
    'The **score** is 0.063 [\\[3\\](#paper-ref-SRC-019).',
    'The score is 0.063 [[3]](#paper-ref-SRC-019).',
    'The **score**\nis 0.063 [\\[3\\]](#paper-ref-SRC-019).',
])
def test_format_matching_returns_verbatim_source(locator):
    source = 'The **score** is 0.063 [\\[3\\]](#paper-ref-SRC-019).'
    exact, scope = matching.resolve_locator(source, locator, 'paragraph')
    assert exact == source
    assert scope == 'paragraph'


@pytest.mark.parametrize('locator', [
    'The score is 0.064 [3](#paper-ref-SRC-019).',
    'The score is 0.063 [3](#paper-ref-SRC-020).',
    'The rate is 0.063 [3](#paper-ref-SRC-019).',
])
def test_format_matching_does_not_fuzz_facts_or_citation_destinations(locator):
    with pytest.raises(ValueError):
        matching.resolve_locator('The **score** is 0.063 [3](#paper-ref-SRC-019).', locator, 'paragraph')


def test_format_matching_rejects_ambiguous_source_and_multiple_paragraphs():
    source = '**Same** result.\n\n__Same__ result.'
    with pytest.raises(ValueError, match='exactly once'):
        matching.resolve_locator(source, 'Same result.', 'paragraph')
    with pytest.raises(ValueError, match='one paragraph'):
        matching.resolve_locator('First paragraph.\n\nSecond paragraph.',
                                 'First paragraph. Second paragraph.', 'paragraph')


def test_list_item_permission_does_not_include_its_neighbor():
    source = '# Paper\n\n## Results\n\n1. **First** result.\n2. **Second** result.'
    plan = MarkdownModifyPlan.model_validate({'scope': 'document', 'instructions': [dict(
        content_ref={'heading_path': ['Paper', 'Results']}, modify_type='update', target_scope='paragraph',
        locator_text='First result.', instruction='Clarify the first item.',
    )]})
    fixed, ranges, repaired = revise_document._validated_ranges(source, plan)
    assert fixed.instructions[0].locator_text == '1. **First** result.'
    assert not repaired
    with pytest.raises(ValueError, match='exceeds the planned'):
        revise_document._bounded_replacements(source, ranges[0], {'replacements': [dict(
            old_string='Second** result.', new_string='Second** changed result.',
        )]})


def editor_for(plan, replacements):
    return SimpleNamespace(
        locate_revision_target=Mock(return_value='{}'),
        generate_modify_plan=Mock(return_value=json.dumps(plan)),
        generate_string_replace_set=Mock(return_value=json.dumps({'replacements': replacements})),
        apply_string_replace=Mock(wraps=WriterRevisionToolkit().apply_string_replace),
    )


@pytest.mark.parametrize('attempt', [0, 1])
@pytest.mark.parametrize('body_edit', [False, True])
def test_recorded_title_directive_can_apply_atomically_with_body_edits(record, attempt, body_edit):
    old_title = record['source'].splitlines()[0]
    source = old_title + '\n\n## Methods\n\nOriginal method.\n\n## Results\n\nKeep results.'
    reference = {'heading_path': [old_title[2:], 'Methods']}
    plan = {'scope': 'document', 'title_instruction': record['attempts'][attempt]['plan']['title_instruction'],
            'instructions': [dict(content_ref=reference, modify_type='update', target_scope='fragment',
                                  locator_text='Original method.', instruction='Clarify method.',
                                  meta={'issue_ids': ['R2']})] if body_edit else []}
    replacements = [dict(content_ref={'heading_path': [old_title[2:]]},
                         old_string=old_title, new_string='# Refined title')]
    if body_edit:
        replacements.append(dict(content_ref=reference, old_string='Original method.', new_string='Clarified method.'))
    editor = editor_for(plan, replacements)
    prepared = dict(manuscript=source, charts=[], reference_candidates=[], writing_context={'context_id': 'title-test'},
                    writing_task={'query': 'Revise the title and requested text.', 'meta': {'review_driven': body_edit}},
                    selected_issues=[{'id': 'R2', 'action_type': 'text_revision'}] if body_edit else [])
    before = copy.deepcopy(prepared)
    result = revise_document._modify_manuscript(prepared, {'word_target': None}, Mock(), editor=editor)
    expected = source.replace(old_title, '# Refined title', 1)
    if body_edit:
        expected = expected.replace('Original method.', 'Clarified method.')
    assert result['manuscript'] == expected
    assert prepared == before
    editor.apply_string_replace.assert_called_once()


@pytest.mark.parametrize('defect', ['outside', 'new_section', 'reference', 'empty_title', 'no_title_change'])
def test_title_permission_does_not_authorize_unrelated_or_invalid_changes(defect):
    source = '# Original\n\n## Methods\n\nKeep method.'
    plan = {'scope': 'document', 'title_instruction': 'Clarify title.', 'instructions': []}
    replacements = [dict(content_ref={'heading_path': ['Original']}, old_string='# Original', new_string='# Refined')]
    if defect == 'outside':
        replacements.append(dict(content_ref={'heading_path': ['Original', 'Methods']},
                                 old_string='Keep method.', new_string='Changed method.'))
    elif defect == 'new_section':
        replacements[0]['new_string'] += '\n\n## Unauthorized section'
    elif defect == 'reference':
        replacements[0]['new_string'] += ' [文献:unknown]'
    elif defect == 'empty_title':
        replacements[0]['new_string'] = ''
    else:
        replacements = [dict(old_string='Keep method.', new_string='Changed method.')]
    editor = editor_for(plan, replacements)
    prepared = dict(manuscript=source, charts=[], reference_candidates=[],
                    writing_context={'context_id': 'title-invalid'},
                    writing_task={'query': 'Clarify title.', 'meta': {}}, selected_issues=[])
    emitted = []
    with bind_runtime_events(emitted.append, 'revise_document_document'), pytest.raises(ValueError):
        revise_document._modify_manuscript(prepared, {'word_target': None}, Mock(), editor=editor)
    assert prepared['manuscript'] == source
    assert not any(event['type'].startswith('artifact_stream') for event in emitted)
    if defect not in {'reference', 'empty_title'}:
        editor.apply_string_replace.assert_not_called()
