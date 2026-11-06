"""Paper Writer material adaptation, drafting and scoped revision regressions."""
import copy
import json
from types import SimpleNamespace
from unittest.mock import Mock

import pytest

from lazyllm.tools.writer.data_models.task import WritingTask
from lazymind.document_tools.paper import contracts, peer_review
from lazymind.document_tools.paper.writing import common as document, revise_document as revision_entry
from lazymind.document_tools.paper.review.schema import text_digest
from lazymind.document_tools.toolkits import WriterRevisionToolkit


OUTLINE = '# 方法比较\n\n## 方法\n\n## 结果\n\n## 局限'
BODY = '仅比较单次得分。新方法得分高 15 分；不能推断统计显著性。'


@pytest.mark.parametrize('word_target', [1, 10000])
@pytest.mark.parametrize('defect', [None, 'structure', 'reference', 'chart'])
def test_draft_defers_semantic_review_but_keeps_hard_checks(monkeypatch, defect, word_target):
    from lazymind.document_tools.paper import shared_facts, workflow
    from lazymind.document_tools.paper.writing import write_document

    inputs = contracts.validate_input('write_document', {
        'topic': '方法比较', 'outline': OUTLINE, 'constraints': {'word_target': word_target}})
    prepared = dict(writing_task={'constraints': inputs['constraints']},
                    writing_context={}, outline=OUTLINE,
                    charts=[], reference_candidates=[], shared_facts=[shared_facts._fact(
                        'source:1', 'status', 'The evidence still needs review.', 'writing_materials')])
    forbidden_review = Mock(side_effect=AssertionError('Source review belongs to F6'))
    monkeypatch.setattr(shared_facts, 'AutoModel', forbidden_review)
    monkeypatch.setattr(write_document, 'prepare_writer_context', lambda *a, **k: prepared)
    toolkit = Mock()
    monkeypatch.setattr(write_document, 'WriterCreateToolkit', lambda: toolkit)
    toolkit.check_consistency.side_effect = AssertionError('Semantic review belongs to F6')
    toolkit.generate_section_instructions.return_value = json.dumps({'section_instructions': {
        'instructions': [dict(instruction_id=str(i), content_ref={'heading_path': ['方法比较', title]},
                              section_title=title, section_goal='说明', required_points=[],
                              expected_blocks=[], fact_constraints=[], meta={})
                         for i, title in enumerate(['方法', '结果', '局限'])]}})
    toolkit.stream_draft_blocks_markdown.return_value = json.dumps([OUTLINE])
    body = OUTLINE
    if defect == 'structure':
        body = body.replace('## 方法', '## 未计划章节')
    elif defect == 'reference':
        body += '\n\n[文献:SRC-UNKNOWN]'
    elif defect == 'chart':
        body += '\n\n[图表:unknown]'
    toolkit.generate_draft_document_markdown.return_value = json.dumps({'draft_document': body})
    pool = workflow.pool_from_business(dict(
        topic=inputs['topic'], outline=inputs['outline'], constraints=inputs['constraints']))
    if defect:
        with pytest.raises(ValueError):
            workflow.execute_step('write_document', pool)
    else:
        completed = workflow.execute_step('write_document', pool)['pool']
        assert workflow.material(completed, 'manuscript')['content'] == body
        assert workflow.routes_after(completed) == ['F6']
        assert workflow.business_input('peer_review', completed)['manuscript'] == body
    forbidden_review.assert_not_called()
    toolkit.check_consistency.assert_not_called()
    toolkit.stream_draft_blocks_markdown.assert_called_once()


@pytest.mark.parametrize('language,target', [('zh-CN', None), ('zh-CN', 1000), ('en', 1000)])
@pytest.mark.parametrize('unit', ['characters', 'words'])
def test_outline_relative_weights_do_not_leak_as_character_instructions(language, target, unit):
    descriptions = ['讨论60个问题与0.7权重（约3字）。', f'Discuss latency (about 2 {unit}).']
    outline = '# T\n\n' + '\n\n'.join(
        f'<a id="block-sec-{i}"></a>\n## {title}\n<!-- writer:outline '
        + json.dumps({'node_id': f'sec-{i}', 'target_chars': weight, 'outline_description': description})
        + ' -->'
        for i, (title, weight, description) in enumerate(zip(['方法', '结果'], [3, 2], descriptions), 1))
    plan = {'instructions': [
        {'instruction_id': str(i), 'content_ref': {'heading_path': ['T', title]},
         'section_title': title, 'section_goal': description,
         'required_points': [description, '原始记录：60个问题，延迟128ms。'],
         'expected_blocks': [description], 'fact_constraints': ['用户引语：“约3字”是界面提示。'],
         'meta': {'target_chars': weight, 'max_chars': weight + 1},
         'heading_structure': [{'node_id': f'child-{i}', 'level': 2, 'title': '细节', 'target_chars': 1}]}
        for i, (title, weight, description) in enumerate(zip(['方法', '结果'], [3, 2], descriptions), 1)]}
    before = copy.deepcopy(plan)
    result = document._prepare_sections(plan, outline, {'paper_language': language, 'word_target': target})
    assert plan == before
    for i, item in enumerate(result['instructions']):
        assert item['section_goal'] == ['讨论60个问题与0.7权重。', 'Discuss latency.'][i]
        assert item['expected_blocks'] == [item['section_goal']]
        assert item['required_points'][:2] == [item['section_goal'], '原始记录：60个问题，延迟128ms。']
        assert item['fact_constraints'][0] == '用户引语：“约3字”是界面提示。'
        assert 'target_chars' not in item['heading_structure'][0]
        if target is None:
            assert 'target_chars' not in item['meta'] and 'word_target' not in item['meta']
            assert document.SECTION_NO_TARGET_INSTRUCTIONS in item['required_points']
        else:
            assert item['meta']['word_target'] == [600, 400][i]
            assert ('target_chars' in item['meta']) == (language == 'zh-CN')


@pytest.mark.parametrize('marker, error', [
    ('', 'every supplied'),
    ('<!-- paper-charts: ["chart-999"] -->', 'unknown'),
    ('<!-- paper-charts: ["chart-001", "chart-001"] -->', 'more than once'),
    ('<!-- paper-charts: ["chart-001"] -->', None),
])
def test_chart_ownership_is_unique_and_subsection_maps_to_parent(marker, error):
    outline = '# 论文\n## 方法\n## 结果\n### 命中率\n' + marker + '\n## 讨论\n'
    plan = {'instructions': [
        {'content_ref': {'heading_path': ['论文', title]}, 'meta': {}}
        for title in ['方法', '结果', '讨论']
    ]}
    prepared = {'charts': [{'id': 'chart-001', 'title': '命中率', 'columns': [], 'finding_ids': [], 'caption': '合成数据'}]}
    if error:
        with pytest.raises(ValueError, match=error):
            document._bind_chart_references(plan, prepared, outline)
        return
    document._bind_chart_references(plan, prepared, outline)
    assert [item['meta']['cross_references'][0]['must_create'] for item in plan['instructions']] == [False, True, False]
    assert '命中率' in plan['instructions'][1]['meta']['cross_references'][0]['guidance']


def test_chart_ownership_survives_public_writer_outline_normalization():
    from lazyllm.tools.writer.tools.planning_tools import WriterPlanningTools
    source = '# 论文\n## 结果\n<!-- paper-charts: ["chart-001"] -->\n'
    task = WritingTask(task_id='ownership', query='write', task_type='write')
    normalized = WriterPlanningTools._complete_markdown_outline_fields(
        WriterPlanningTools._normalize_markdown_outline(source), task,
    )
    assert document._outline_chart_owners(normalized, [{'id': 'chart-001'}]) == {
        'chart-001': (['论文', '结果'], 1),
    }


# Current upstream input contracts and preparation.
@pytest.fixture
def revision():
    manuscript = '# 论文\n\n## 方法\n\n来源[SRC-001]。'
    reviewed = peer_review.build_output(peer_review.ReviewResult.model_validate(dict(
        summary='修改表述', limitations=[], issues=[dict(severity='minor', category='language',
                                                     location='方法', quote='来源[SRC-001]。', problem='表述不清',
                                                     suggestion='澄清', action_type='text_revision')]
    )), dict(topic='论文', manuscript=manuscript, evidence=None, analysis=None, request=''))
    return dict(topic='论文', manuscript=manuscript, selected_issue_ids=['R1'],
                papers=[dict(id='SRC-001', title='来源', text='来源正文', citation='来源文献[Z].')],
                review=reviewed['review'], review_data=reviewed['review_data'])


def test_revision_preserves_reviewed_source_and_quote_before_hash_check(revision):
    before = copy.deepcopy(revision)
    normalized = contracts.validate_input('revise_document', revision)
    document.validate_inputs(normalized)
    assert normalized['manuscript'] == revision['manuscript']
    assert text_digest(normalized['manuscript']) == revision['review_data']['manuscript_sha256']
    assert normalized['review_data']['issues'][0]['quote'] in normalized['manuscript']
    assert revision == before


def test_direct_revision_normalizes_explicit_source_ids(revision):
    normalized = contracts.validate_input('revise_document', {
        key: revision[key] for key in ('topic', 'manuscript', 'papers')})
    assert '[文献:SRC-001]' in normalized['manuscript']


@pytest.mark.parametrize('change', ['object_review', 'missing_review', 'missing_review_data'])
def test_revision_requires_current_f6_pair(revision, change):
    if change == 'object_review':
        revision['review'] = revision.pop('review_data')
    else:
        revision.pop(change.removeprefix('missing_'))
    with pytest.raises(ValueError):
        contracts.validate_input('revise_document', revision)


@pytest.mark.parametrize('field,value', [
    ('question', {'candidates': []}), ('analysis', '旧分析文本'),
    ('analysis', {'summary': '不完整的分析'}), ('chart', '<svg/>'),
    ('charts', [dict(id='fig1', title='图', svg='<svg/>', columns=[], finding_ids=[], description='旧图注')]),
])
def test_current_f5_contract_rejects_other_shapes(field, value):
    with pytest.raises(ValueError):
        contracts.validate_input('write_document', dict(topic='论文', manuscript='# 论文\n\n## 方法', **{field: value}))


@pytest.mark.parametrize('mode', ['direct', 'review'])
def test_existing_manuscript_does_not_validate_input_outline_structure(revision, mode):
    source = revision if mode == 'review' else {key: revision[key] for key in ('topic', 'manuscript', 'papers')}
    source = dict(source, outline='过期大纲，仅供背景参考', request='修改方法')
    normalized = contracts.validate_input('revise_document', source)
    document.validate_inputs(normalized)


def test_new_draft_requires_valid_input_outline():
    inputs = contracts.validate_input('write_document', dict(topic='论文', outline='没有标题结构'))
    with pytest.raises(ValueError):
        document.validate_inputs(inputs)


def test_missing_review_section_is_preserved_for_public_writer(revision):
    normalized = contracts.validate_input('revise_document', revision)
    normalized['review_data']['issues'][0].update(location='缺失章节：讨论', quote=None)
    prepared = document.prepare_writer_context(normalized)
    assert prepared['selected_issues'][0]['location'] == '缺失章节：讨论'
    assert 'target' not in prepared['selected_issues'][0]


def test_chart_directory_reuses_one_index_and_keeps_exact_associations(monkeypatch):
    source = contracts.validate_input('write_document', {'topic': '论文', 'question': '比较'})
    source['analysis'] = {'findings': [{'id': 'F1', 'statement': '结论一'}, {'id': 'F2', 'statement': '结论二'}]}
    source['charts'] = [dict(id=f'Fig{i}', title=f'图{i}', caption='说明', columns=[],
                             finding_ids=refs, svg='<svg/>')
                        for i, refs in enumerate([['F2'], ['F1', 'F2']], 1)]
    before = copy.deepcopy(source)
    indexes = []
    original = document._chart_directory

    def record(chart, findings):
        indexes.append(findings)
        return original(chart, findings)
    monkeypatch.setattr(document, '_chart_directory', record)
    prepared = document.prepare_writer_inputs(source)
    assert indexes[0] is indexes[1]
    assert [[f['id'] for f in c['findings']] for c in prepared['chart_directory']] == [['F2'], ['F1', 'F2']]
    assert source == before


# Scoped revision through the public Writer toolkit.
def test_reader_material_registry_preserves_study_names_and_tracks_internal_ids():
    title = 'ImageNet-1K'
    inputs = contracts.validate_input('write_document', {
        'topic': '方法比较', 'outline': OUTLINE, 'papers': [dict(
            id='upload-test-records', title=title, text='Thirty observed tasks.',
            review_role='background', attachment_key='test-attachment', content_type='excerpt')],
    })
    prepared = document.prepare_writer_inputs(inputs)
    registry = prepared['material_identifiers']
    assert 'upload-test-records' in registry
    assert '[文献:upload-test-records]' in registry
    assert title not in registry and '`' + title + '`' not in registry


SOURCE = '# 论文\n\n## 方法\n\n原方法。\n\n## 结果\n\n原结果。\n\n## 局限\n\n保留原局限。'


def instruction(section='方法', ids=None, **overrides):
    return dict(content_ref={'heading_path': ['论文', section]}, modify_type='update',
                target_scope='section', instruction='按意见修改',
                meta={'issue_ids': ['R1'] if ids is None else ids}, **overrides)


def replacement(old='原方法。', new='修订方法。', section='方法'):
    return dict(old_string=old, new_string=new, content_ref={'heading_path': ['论文', section]})


def prepared(issues=None):
    issues = issues if issues is not None else [('R1', '方法', 'text_revision')]
    return dict(manuscript=SOURCE, outline='# 论文\n\n## 方法\n\n## 结果\n\n## 局限',
                writing_task={'query': '只修改选中意见', 'meta': {'mode': 'revise', 'review_driven': True}},
                writing_context={'context_id': 'scope-test'}, charts=[], reference_candidates=[],
                selected_issues=[dict(id=key, action_type=action,
                                      location=section, quote=None)
                                 for key, section, action in issues])


def editor(operations, changes=()):
    return SimpleNamespace(
        locate_revision_target=Mock(return_value='{}'),
        generate_modify_plan=Mock(return_value=json.dumps({'scope': 'document', 'instructions': operations})),
        generate_string_replace_set=Mock(return_value=json.dumps({'replacements': changes})),
        apply_string_replace=Mock(wraps=WriterRevisionToolkit().apply_string_replace),
    )


def test_replacements_reject_ambiguous_original_and_accept_explicit_section():
    source = SOURCE.replace('原结果。', '原方法。')
    item = replacement()
    item['content_ref'] = {'document_root': True}
    with pytest.raises(ValueError, match='exactly once'):
        revision_entry._bounded_replacements(source, [(0, len(source))], {'replacements': [item]})
    item['content_ref'] = {'heading_path': ['论文', '方法']}
    _, located = revision_entry._bounded_replacements(source, [(0, len(source))], {'replacements': [item]})
    # Coverage reports the changed span; the full matching string is preserved for application.
    assert source[located[0][0]:located[0][1]] == '原'
    assert located[0][2].old_string == '原方法。'


def test_replacements_cannot_escape_planned_scope():
    allowed = [document.markdown_section_range(SOURCE, ['论文', '方法'], 1)]
    with pytest.raises(ValueError, match='exceeds the planned'):
        revision_entry._bounded_replacements(SOURCE, allowed, {
            'replacements': [replacement('原结果。', '篡改结果。', '结果')]})


def test_generated_citation_repair_precedes_effective_scope_validation():
    change = replacement(new=r'修订方法。[\[1\](#paper-ref-ref-1)')
    changes, located = revision_entry._validated_changes(
        SOURCE, None, [(0, len(SOURCE))], {'replacements': [change]}, allow_repair=False,
        candidates=[{'id': 'ref-1', 'citation': 'Author. Study. 2026.'}],
    )
    assert changes['replacements'][0]['new_string'] == r'修订方法。[\[1\]](#paper-ref-ref-1)'
    assert located[0][2].new_string == changes['replacements'][0]['new_string']


def test_replacements_reject_overlap():
    with pytest.raises(ValueError, match='overlapping'):
        revision_entry._bounded_replacements(SOURCE, [(0, len(SOURCE))], {'replacements': [
            replacement(), replacement('方法。', '新方法。')]})


def test_reverse_application_preserves_child_references_when_title_changes():
    changes, _ = revision_entry._bounded_replacements(SOURCE, [(0, len(SOURCE))], {'replacements': [
        dict(old_string='# 论文', new_string='# 新论文', content_ref={'heading_path': ['论文']}),
        replacement(), replacement('原结果。', '修订结果。', '结果')]})
    result = json.loads(WriterRevisionToolkit().apply_string_replace(
        markdown_document=SOURCE, string_replace_set_json=json.dumps(changes),
        writing_context_json='{"context_id":"scope-test"}'))
    assert result['string_replace_result']['success']
    assert result['revised_document'] == SOURCE.replace('原方法。', '修订方法。').replace(
        '原结果。', '修订结果。').replace('# 论文', '# 新论文')


@pytest.mark.parametrize('new_title', ['# 论文的实证研究', '# 新论文'])
def test_planned_title_revision_accepts_suffix_and_replacement(new_title):
    changes = [dict(old_string='# 论文', new_string=new_title,
                    content_ref={'heading_path': ['论文']}), replacement()]
    tools = editor([instruction()], changes)
    tools.generate_modify_plan.return_value = json.dumps({
        'scope': 'document', 'title_instruction': '修改论文题目', 'instructions': [instruction()],
    })
    result = revision_entry._modify_manuscript(prepared(), {'word_target': None}, Mock(), editor=tools)
    assert result['manuscript'] == SOURCE.replace('# 论文', new_title).replace('原方法。', '修订方法。')


def test_planned_title_revision_keeps_other_edits_when_title_is_missing():
    tools = editor([instruction()], [replacement()])
    tools.generate_modify_plan.return_value = json.dumps({
        'scope': 'document', 'title_instruction': '修改论文题目', 'instructions': [instruction()],
    })
    result = revision_entry._modify_manuscript(prepared(), {'word_target': None}, Mock(), editor=tools)
    assert result['manuscript'] == SOURCE.replace('原方法。', '修订方法。')
    assert any('标题' in item for item in result['_pending_edits'])


@pytest.mark.parametrize('ids', [[], ['R2'], [1]])
def test_review_plan_requires_selected_issue_ids(ids):
    data = prepared()
    tools = editor([instruction(ids=ids)])
    result = revision_entry._modify_manuscript(data, {'word_target': None}, Mock(), editor=tools)
    assert result['manuscript'] == SOURCE and result['_pending_edits']
    tools.apply_string_replace.assert_not_called()


def test_partial_review_plan_saves_executable_edits():
    data = prepared([('R1', '方法', 'text_revision'), ('R2', '结果', 'text_revision')])
    result = revision_entry._modify_manuscript(
        data, {'word_target': None}, Mock(), editor=editor([instruction()], [replacement()]))
    assert result['manuscript'] == SOURCE.replace('原方法。', '修订方法。')
    assert any('R2' in item for item in result['_pending_edits'])


def test_move_ranges_include_public_writer_destination():
    from lazyllm.tools.writer.data_models.revision import MarkdownModifyPlan
    operation = instruction()
    operation.update(modify_type='move', destination_ref={'heading_path': ['论文', '结果']},
                     destination_scope='section', position='after')
    plan = MarkdownModifyPlan.model_validate({'scope': 'document', 'instructions': [operation]})
    ranges = revision_entry._operation_ranges(SOURCE, plan)
    assert [SOURCE[start:end].strip() for start, end in ranges[0]] == ['## 方法\n\n原方法。', '## 结果\n\n原结果。']


def test_unresolved_issue_does_not_discard_other_effective_edits():
    data = prepared([('R1', '方法', 'text_revision'), ('R2', '结果', 'text_revision')])
    tools = editor([instruction(), instruction('结果', ['R2'])], [replacement()])
    result = revision_entry._modify_manuscript(data, {'word_target': None}, Mock(), editor=tools)
    assert result['manuscript'] == SOURCE.replace('原方法。', '修订方法。')
    assert any('R2' in item for item in result['_pending_edits'])
    tools.apply_string_replace.assert_called_once()


def test_invalid_generated_edit_is_rejected_before_application():
    tools = editor([instruction()], [replacement('原结果。', '篡改结果。', '结果')])
    result = revision_entry._modify_manuscript(prepared(), {'word_target': None}, Mock(), editor=tools)
    assert result['manuscript'] == SOURCE and result['_pending_edits']
    tools.apply_string_replace.assert_not_called()


@pytest.mark.parametrize('old,new', [
    ('前文。原方法。后文。', '前文。修订方法。后文。'),
    ('前文。原方法。后文。', '前文。后文。'),
])
def test_replacement_context_outside_scope_is_allowed_only_if_unchanged(old, new):
    source = '# 论文\n\n## 方法\n\n' + old
    start = source.index('原方法。')
    allowed = [(start, start + len('原方法。'))]
    change = replacement(old, new)
    result, located = revision_entry._bounded_replacements(source, allowed, {'replacements': [change]})
    assert result['replacements'][0]['old_string'] == old
    assert result['replacements'][0]['new_string'] == new
    assert all(revision_entry._within_range(a, b, allowed) for a, b, _ in located)
    with pytest.raises(ValueError, match='exceeds the planned'):
        revision_entry._bounded_replacements(source, allowed, {'replacements': [
            replacement(old, new.replace('后文', '额外修改'))]})


def test_overlapping_unchanged_context_is_still_rejected():
    source = '# 论文\n\n## 方法\n\n前文。原方法。后文。'
    changes = [replacement('前文。原方法。', '前文。新方法。'),
               replacement('原方法。后文。', '原方法。新后文。')]
    with pytest.raises(ValueError, match='overlapping'):
        revision_entry._bounded_replacements(source, [(0, len(source))], {'replacements': changes})


@pytest.mark.parametrize('word_target', [None, 10000])
def test_selected_revision_preserves_unselected_sections_and_inputs(word_target):
    data = prepared([('R1', '方法', 'text_revision'), ('R2', '结果', 'needs_experiment')])
    before = copy.deepcopy(data)
    tools = editor([instruction()], [replacement()])
    quality = Mock()
    result = revision_entry._modify_manuscript(
        data, {'word_target': word_target, 'paper_language': 'zh-CN'}, quality, editor=tools)
    assert result == dict(manuscript=SOURCE.replace('原方法。', '修订方法。'), is_demo=False, _pending_edits=[])
    assert data == before
    tools.apply_string_replace.assert_called_once()
    quality.check_consistency.assert_not_called()


@pytest.mark.parametrize('mode', ['direct', 'review'])
@pytest.mark.parametrize('old,new,outline', [
    ('## 方法', '## 研究方法', None),
    ('原方法。', '原方法。\n\n### 参数\n\n补充说明。', '旧大纲说明，不再约束当前稿件'),
    ('## 方法\n\n原方法。\n\n', '', None),
    ('# 论文', '# 新论文', '旧大纲说明，不再约束当前稿件'),
])
def test_edits_only_update_manuscript_without_enforcing_old_outline(mode, outline, old, new):
    data = prepared()
    data['outline'] = outline
    data['writing_task']['meta']['mode'] = 'revise'
    data['writing_task']['meta']['review_driven'] = mode == 'review'
    operation = instruction()
    operation.update(target_scope='fragment', locator_text=old)
    change = replacement(old, new)
    if old == '# 论文':
        operation['content_ref'] = change['content_ref'] = {'heading_path': ['论文']}
    tools = editor([operation], [change])
    tools.locate_revision_target = Mock(return_value='{}')
    quality = Mock()
    quality.check_consistency.return_value = json.dumps({
        'review_report': {'result': {'is_passed': True, 'score': 100, 'summary': '通过'}}})
    result = revision_entry._modify_manuscript(data, {'word_target': None}, quality, editor=tools)
    expected = SOURCE.replace(old, new)
    assert result['manuscript'] == expected
    assert 'outline' not in result
    assert data['outline'] == outline
    document.validate_result(result, dict(mode='revise', manuscript=SOURCE, papers=[], charts=[], outline=outline))
    quality.check_consistency.assert_not_called()


def test_revision_result_does_not_require_output_outline():
    inputs = dict(mode='revise', manuscript=SOURCE, papers=[], charts=[], outline=None)
    document.validate_result(dict(manuscript=SOURCE), inputs)


def test_new_draft_still_checks_planned_headings():
    with pytest.raises(ValueError, match='headings do not match'):
        document._validate_manuscript_structure(
            SOURCE.replace('## 方法', '## 研究方法'), signature=document._heading_signature(SOURCE))


def test_inline_trace_failure_preserves_business_result_and_error(monkeypatch):
    def broken(*args, **kwargs):
        raise RuntimeError('trace unavailable')
    for name in ('get_trace_context', 'start_span', 'set_span_attributes', 'set_span_error', 'finish_span'):
        monkeypatch.setattr(document.tracing, name, broken)
    assert document._trace_call('prepare', lambda: 'result') == 'result'
    with pytest.raises(ValueError, match='business failure'):
        document._trace_call('assemble', lambda: (_ for _ in ()).throw(ValueError('business failure')))


@pytest.mark.parametrize('mode', ['direct', 'review'])
@pytest.mark.parametrize('failure', [None, 'scope', 'validation'])
def test_completed_revision_streams_full_text_once_after_validation(monkeypatch, mode, failure):
    from lazymind.document_tools.paper.runtime_events import bind_runtime_events
    data = prepared()
    data['writing_task']['meta']['mode'] = 'revise'
    data['writing_task']['meta']['review_driven'] = mode == 'review'
    changes = [replacement('原结果。', '越界。', '结果')] if failure == 'scope' else [replacement()]
    tools = editor([instruction()], changes)
    tools.locate_revision_target = Mock(return_value='{}')
    emitted = []
    candidate = SOURCE.replace('原方法。', '修订方法。')
    expected = SOURCE if failure else candidate

    def validate(body, *args, **kwargs):
        assert body == candidate
        assert not any(e['type'].startswith('artifact_stream') for e in emitted)
        if failure == 'validation':
            raise ValueError('invalid candidate')
    monkeypatch.setattr(revision_entry, '_validate_manuscript', validate)
    slot = 'revise_document_document'

    def run():
        with bind_runtime_events(emitted.append, slot) as events:
            result = revision_entry._modify_manuscript(data, {'word_target': None}, Mock(), editor=tools)
            assert ''.join(e['delta'] for e in emitted if 'delta' in e) == expected
            assert [e['type'] for e in emitted if e['type'].startswith('artifact_stream')] == [
                'artifact_stream_start', 'artifact_stream', 'artifact_stream_end',
            ]
            assert [e['delta'] for e in emitted if 'delta' in e] == [expected]
            events.complete()
            return result
    result = run()
    assert result['manuscript'] == expected
    assert bool(result['_pending_edits']) == bool(failure)
    assert len([e for e in emitted if e['type'] == 'artifact_stream_start']) == 1
    assert not any(e['type'] == 'artifact_stream_abort' for e in emitted)
    assert any(e['type'] == 'artifact_stream_end' for e in emitted)
    assert all(e['slot'] == slot for e in emitted if 'slot' in e)
    assert data['manuscript'] == SOURCE


@pytest.mark.parametrize('language', ['zh-CN', 'en'])
@pytest.mark.parametrize('target', [1, 10000])
def test_manuscript_length_is_a_generation_target_not_a_failure(language, target):
    toolkit = Mock()
    document._validate_manuscript(
        SOURCE, None, [], {'word_target': target, 'paper_language': language}, toolkit, '{}',
        candidates=[], check_quality=False)
    toolkit.check_consistency.assert_not_called()


@pytest.mark.parametrize('action', ['needs_material', 'needs_experiment'])
def test_review_without_executable_edits_stops_before_replacement(action):
    data = prepared([('R1', '方法', action)])
    tools = editor([])
    tools.generate_modify_plan.side_effect = ValueError(
        'modify_plan contains no operations for the located revision scope.')
    result = revision_entry._modify_manuscript(data, {'word_target': None}, Mock(), editor=tools)
    assert result['manuscript'] == SOURCE and result['_pending_edits']
    tools.generate_string_replace_set.assert_not_called()
    tools.apply_string_replace.assert_not_called()


@pytest.mark.parametrize('action', ['needs_material', 'needs_experiment'])
def test_pending_issue_stays_in_report_instead_of_inserting_a_manuscript_marker(action):
    data = prepared([('R1', '缺失内容：验证', action)])
    marker = '原结果。\n\n[待补实验：独立验证尚未完成。]'
    tools = editor([instruction('结果')], [replacement('原结果。', marker, '结果')])
    result = revision_entry._modify_manuscript(data, {'word_target': None}, Mock(), editor=tools)
    assert result['manuscript'] == SOURCE and result['_pending_edits']
    tools.locate_revision_target.assert_not_called()
    tools.generate_string_replace_set.assert_not_called()
    tools.apply_string_replace.assert_not_called()
    assert data['selected_issues'][0]['action_type'] == action


def test_pending_only_without_writer_target_keeps_manuscript():
    data = prepared([('R1', '缺少实验', 'needs_experiment')])
    before = copy.deepcopy(data)
    tools = editor([])
    tools.locate_revision_target.side_effect = ValueError('locate_result contains no revision targets.')
    result = revision_entry._modify_manuscript(data, {'word_target': None}, Mock(), editor=tools)
    assert result['manuscript'] == SOURCE and result['_pending_edits']
    assert data == before
    tools.generate_modify_plan.assert_not_called()
    tools.apply_string_replace.assert_not_called()


@pytest.mark.parametrize('action', ['needs_material', 'needs_experiment'])
@pytest.mark.parametrize('clarify_pending', [False, True])
def test_selected_pending_issues_allow_optional_text_clarification(action, clarify_pending):
    data = prepared([('R1', '方法', 'text_revision'), ('R2', '局限', action)])
    before = copy.deepcopy(data)
    operations, changes = [instruction()], [replacement()]
    expected = SOURCE.replace('原方法。', '修订方法。')
    if clarify_pending:
        operations.append(instruction('局限', ids=['R2']))
        clarification = '保留原局限；仍需补充材料或实验，未声称完成。'
        changes.append(replacement('保留原局限。', clarification, '局限'))
        expected = expected.replace('保留原局限。', clarification)
    tools = editor(operations, changes)
    quality = Mock()
    result = revision_entry._modify_manuscript(
        data, {'word_target': None, 'paper_language': 'zh-CN'}, quality, editor=tools)
    assert result['manuscript'] == expected
    assert data == before
    assert data['selected_issues'][1]['action_type'] == action
    quality.check_consistency.assert_not_called()
    assert tools.apply_string_replace.call_count == len(changes)


def test_failed_locator_keeps_successful_revision(monkeypatch):
    data = prepared([('R1', '方法', 'text_revision'), ('R2', '结果', 'text_revision')])
    bad = instruction('不存在的章节', ids=['R2'])
    tools = editor([instruction(), bad], [replacement()])
    monkeypatch.setattr(revision_entry, 'repair_locators', Mock(side_effect=ValueError('cannot locate R2')))
    result = revision_entry._modify_manuscript(data, {'word_target': None}, Mock(), editor=tools)
    assert result['manuscript'] == SOURCE.replace('原方法。', '修订方法。')
    assert any('R2' in note for note in result['_pending_edits'])
    tools.apply_string_replace.assert_called_once()


def test_one_apply_failure_preserves_other_completed_edits():
    data = prepared([('R1', '方法', 'text_revision'), ('R2', '结果', 'text_revision')])
    tools = editor([instruction(), instruction('结果', ['R2'])], [
        replacement(), replacement('原结果。', '修订结果。', '结果')])
    apply = WriterRevisionToolkit().apply_string_replace

    def fail_method_only(**kwargs):
        if json.loads(kwargs['string_replace_set_json'])['replacements'][0]['old_string'] == '原方法。':
            return json.dumps({'string_replace_result': {'success': False}})
        return apply(**kwargs)

    tools.apply_string_replace.side_effect = fail_method_only
    result = revision_entry._modify_manuscript(data, {'word_target': None}, Mock(), editor=tools)
    assert result['manuscript'] == SOURCE.replace('原结果。', '修订结果。')
    assert result['_pending_edits']
    assert tools.apply_string_replace.call_count == 2
