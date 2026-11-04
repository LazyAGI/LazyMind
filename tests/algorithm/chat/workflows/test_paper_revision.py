"""Revision entries and current-request scope must not reuse historical review selections."""
import json
import pytest
from lazymind.document_tools.paper import api, contracts, workflow
from lazymind.document_tools.paper.writing import common, revise_document, write_document

BODY = '# Paper\n\n## Methods\nKeep methods.\n\n## Discussion\nShorten this paragraph.'

def checked_revision(body, inputs=None):
    from lazymind.document_tools.paper.peer_review import build_output
    from lazymind.document_tools.paper.review_schema import ReviewResult
    return {**build_output(ReviewResult(summary='Checked revised manuscript', issues=[], limitations=[]),
                           {'topic': 'Paper', 'manuscript': body}), 'manuscript': body}


@pytest.fixture(autouse=True)
def offline_revision_review(monkeypatch):
    from lazymind.document_tools.paper import peer_review
    monkeypatch.setattr(peer_review, 'run', lambda inputs: {
        key: value for key, value in checked_revision(inputs['manuscript']).items() if key != 'manuscript'})


CURRENT_REVIEW = {'current_step': 'revise_document', 'previous_completed_step': 'peer_review',
                  'review_matches_manuscript': True}


@pytest.mark.parametrize('user_request', ['继续，修订论文', '继续', '请修改稿件', '开始修订'])
def test_current_review_confirmation_selects_all_without_reclassifying(monkeypatch, user_request):
    monkeypatch.setattr(revise_document, 'AutoModel', lambda **kw: pytest.fail('Already confirmed current review'))
    review = {'issues': [{'id': 'R1'}, {'id': 'R2'}]}
    assert revise_document.resolve_review_issues(user_request, review, workflow_context=CURRENT_REVIEW) == ['R1', 'R2']


@pytest.mark.parametrize('change', [{'previous_completed_step': 'write_document'},
                                   {'review_matches_manuscript': False}, {'current_step': 'peer_review'}])
def test_confirmation_does_not_adopt_old_review_without_current_context(monkeypatch, change):
    monkeypatch.setattr(revise_document, 'AutoModel', lambda **kw: lambda prompt: '{"intent":"unclear"}')
    with pytest.raises(revise_document.RevisionInputRequired):
        revise_document.resolve_review_issues('继续，修订论文', {'issues': [{'id': 'R1'}]},
                                             workflow_context={**CURRENT_REVIEW, **change})


@pytest.mark.parametrize('user_request,response,expected', [
    ('缩短摘要', {'intent': 'direct'}, []),
    ('只改R1，R2先保留', {'intent': 'review', 'selection': 'subset', 'issue_ids': ['R1']}, ['R1']),
])
def test_explicit_scope_wins_over_current_review_default(monkeypatch, user_request, response, expected):
    def model(prompt):
        assert 'previous_completed_step' in prompt and user_request in prompt
        return json.dumps(response)
    monkeypatch.setattr(revise_document, 'AutoModel', lambda **kw: model)
    assert revise_document.resolve_review_issues(user_request, {'issues': [{'id': 'R1'}, {'id': 'R2'}]},
                                                workflow_context=CURRENT_REVIEW) == expected


def test_revision_scope_events_include_start_and_result(monkeypatch):
    from lazymind.document_tools.paper.runtime_events import bind_runtime_events
    events = []
    with bind_runtime_events(events.append, 'revise_document_document'):
        assert revise_document.resolve_review_issues('继续，修订论文', {'issues': [
            {'id': 'R1', 'action_type': 'text_revision'}, {'id': 'R2', 'action_type': 'needs_material'}]},
            workflow_context=CURRENT_REVIEW) == ['R1', 'R2']
    text = ''.join(e.get('text', '') for e in events)
    assert '正在确定修订范围' in text and '1 条可执行意见、1 条待补事项' in text
    assert any(s['status'] == 'succeeded' for e in events if e['type'] == 'progress'
               for s in e['public_display']['process_steps'])


def test_separate_entries_and_no_implicit_draft_revision():
    assert api.ALGORITHMS['write_document'] is write_document
    assert api.ALGORITHMS['revise_document'] is revise_document
    assert 'revise_document' not in contracts.ALIASES
    inputs = contracts.validate_input('write_document', {'topic': 'Paper', 'manuscript': BODY})
    with pytest.raises(ValueError, match='revise_document'):
        write_document.validate_inputs(inputs)


def test_direct_revision_needs_request_and_has_no_review_context():
    inputs = contracts.validate_input('revise_document', {'topic': 'Paper', 'manuscript': BODY})
    with pytest.raises(contracts.MissingMaterials):
        revise_document.validate_inputs(inputs)
    inputs['request'] = 'Shorten Discussion only.'
    revise_document.validate_inputs(inputs)
    prepared = common.prepare_writer_context(inputs)
    assert prepared['writing_task']['task_type'] == 'revise'
    assert prepared['writing_task']['meta']['review_driven'] is False
    assert prepared['selected_issues'] == []
    assert not any(r['meta']['material_kind'] == 'review' for r in prepared['resources'])


def test_direct_business_input_omits_historical_review():
    pool = workflow.pool_from_business({'topic': 'Paper', 'manuscript': BODY})
    workflow.add_material(pool, 'review', 'Historical advice: replace every section.', [], [])
    inputs = workflow.business_input('revise_document', pool, 'Shorten Discussion only.')
    assert inputs['review'] is None and inputs['review_data'] is None
    assert inputs['selected_issue_ids'] == []
    assert inputs['manuscript'] == BODY


@pytest.mark.parametrize('step', ['peer_review', 'revise_document'])
def test_updated_research_materials_do_not_block_existing_manuscript(step):
    import copy
    pool = workflow.pool_from_business({'topic': 'Paper', 'evidence': 'Original evidence'})
    original_evidence = copy.deepcopy(pool['current']['evidence'])
    workflow.add_material(pool, 'manuscript', BODY, [original_evidence], [])
    workflow.add_material(pool, 'evidence', 'Additional verified evidence', [original_evidence], [])
    before = copy.deepcopy(pool)
    inputs = contracts.validate_input(step, workflow.business_input(step, pool, 'Use the updated evidence.'))
    assert inputs['manuscript'] == BODY
    assert inputs['evidence'] == 'Additional verified evidence'
    assert pool == before


def test_initial_direct_revision_restores_launch_request_only_without_pool():
    pool = workflow.pool_from_business({'topic': 'Paper', 'manuscript': BODY})
    params = {'step_id': 'revise_document', 'user_input': 'Shorten Discussion only.'}
    assert workflow._step_request('revise_document', pool, params, {'manuscript_text': 'bound'}) == params['user_input']
    assert workflow._step_request('revise_document', pool, params, {'peer_review_pool': 'bound'}) == ''


def test_revision_keeps_existing_outline_material_version(monkeypatch):
    outline = '# Paper\n\n## Methods\n\n## Discussion'
    pool = workflow.pool_from_business({'topic': 'Paper', 'manuscript': BODY, 'outline': outline})
    revised = BODY.replace('## Discussion', '## Limitations')
    monkeypatch.setattr(revise_document, 'run', lambda inputs: checked_revision(revised))
    result = workflow.execute_step('revise_document', pool, 'Rename Discussion to Limitations.')
    assert result['pool']['current']['outline'] == pool['current']['outline']
    assert workflow.material(result['pool'], 'outline')['content'] == outline
    assert workflow.material(result['pool'], 'manuscript')['content'] == revised
    assert len(result['pool']['history'][-1]['outputs']) == 3


def test_direct_revision_without_review_does_not_call_model(monkeypatch):
    monkeypatch.setattr(revise_document, 'AutoModel', lambda **kw: pytest.fail('unnecessary scope model'))
    assert revise_document.resolve_review_issues('只缩短讨论段落', None) == []


@pytest.mark.parametrize('selected', ['R1', 'R9'])
def test_revision_intent_uses_writer_parser_and_keeps_selection_validation(monkeypatch, selected):
    response = json.dumps({'intent': 'review', 'selection': 'subset', 'issue_ids': [selected]})
    monkeypatch.setattr(revise_document, 'AutoModel', lambda **kw: lambda prompt:
                        '<think>selection</think>\n```json\n' + response + '\n```')
    review = {'issues': [{'id': 'R1'}]}
    if selected == 'R1':
        assert revise_document.resolve_review_issues('只改 R1', review) == ['R1']
    else:
        with pytest.raises(contracts.MissingMaterials):
            revise_document.resolve_review_issues('只改 R1', review)


@pytest.mark.parametrize('intent,expected', [
    ('direct', []),
    ('review', ['R1', 'R2']),
    ('unclear', None),
])
def test_review_intent_uses_all_current_issues(monkeypatch, intent, expected):
    monkeypatch.setattr(revise_document, 'AutoModel', lambda **kw: lambda prompt:
                        json.dumps({'intent': intent, **({'selection': 'all'} if intent == 'review' else {})}))
    review = {'issues': [{'id': 'R1'}, {'id': 'R2'}]}
    if expected is None:
        with pytest.raises(contracts.MissingMaterials):
            revise_document.resolve_review_issues('本轮修订要求', review)
    else:
        assert revise_document.resolve_review_issues('本轮修订要求', review) == expected


def test_review_without_issues_does_not_become_direct_revision(monkeypatch):
    monkeypatch.setattr(revise_document, 'AutoModel', lambda **kw: lambda prompt: '{"intent":"review","selection":"all"}')
    with pytest.raises(contracts.MissingMaterials, match='没有待处理意见'):
        revise_document.resolve_review_issues('按审稿修订', {'issues': []})


def test_intent_model_cannot_supply_selection_without_explicit_mode(monkeypatch):
    monkeypatch.setattr(revise_document, 'AutoModel', lambda **kw: lambda prompt:
                        '{"intent":"review","issue_ids":["R1"]}')
    with pytest.raises(ValueError, match='Invalid revision intent'):
        revise_document.resolve_review_issues('按审稿修订', {'issues': [{'id': 'R1'}]})


def test_missing_current_request_does_not_reuse_old_selection():
    with pytest.raises(contracts.MissingMaterials):
        revise_document.resolve_review_issues('', {'issues': [{'id': 'R1'}]})


@pytest.mark.parametrize('stale', [False, True])
def test_review_version_is_checked_independently_of_intent(monkeypatch, stale):
    from lazymind.document_tools.paper.peer_review import build_output
    from lazymind.document_tools.paper.review_schema import ReviewResult
    review = build_output(ReviewResult.model_validate({
        'summary': 'Shorten discussion.', 'limitations': [],
        'issues': [{'severity': 'minor', 'category': 'language', 'location': 'Discussion',
                    'quote': 'Shorten this paragraph.', 'problem': 'Verbose discussion.',
                    'suggestion': 'Shorten discussion.', 'action_type': 'text_revision'}],
    }), {'topic': 'Paper', 'manuscript': BODY})
    pool = workflow.pool_from_business({'topic': 'Paper', 'manuscript': BODY, **{
        key: review[key] for key in ('review', 'review_data')
    }})
    if stale:
        workflow.add_material(pool, 'manuscript', BODY + '\nNew content.', [], [])
    # Matching or stale review never leaks into direct editing context.
    direct = workflow.business_input('revise_document', pool, 'Shorten Discussion only.')
    assert direct['review_data'] is None
    monkeypatch.setattr(revise_document, 'AutoModel', lambda **kw: lambda prompt: '{"intent":"review","selection":"all"}')
    pool['selected_issue_ids'] = revise_document.resolve_review_issues('按当前审稿修订', review['review_data'])
    if stale:
        with pytest.raises(ValueError, match='STALE_MATERIAL'):
            contracts.validate_input('revise_document', workflow.business_input('revise_document', pool, '按审稿修订'))
    else:
        inputs = contracts.validate_input('revise_document', workflow.business_input('revise_document', pool, '按审稿修订'))
        assert inputs['selected_issue_ids'] == ['R1']


@pytest.mark.parametrize('user_request,selection,expected', [
    ('先改第1、3条，第2条要补实验，先留着', ['R1', 'R3'], ['R1', 'R3']),
    ('除了R2都改', ['R3', 'R1', 'R1'], ['R1', 'R3']),
    ('只改语言类意见', ['R3'], ['R3']),
])
def test_partial_review_selection_uses_current_report(monkeypatch, user_request, selection, expected):
    review = {'issues': [{'id': 'R1', 'category': 'structure'},
                         {'id': 'R2', 'action_type': 'needs_experiment'},
                         {'id': 'R3', 'category': 'language'}]}
    prompts = []
    def model(prompt):
        prompts.append(prompt)
        return json.dumps({'intent': 'review', 'selection': 'subset', 'issue_ids': selection})
    monkeypatch.setattr(revise_document, 'AutoModel', lambda **kw: model)
    assert revise_document.resolve_review_issues(user_request, review) == expected
    assert user_request in prompts[0] and 'needs_experiment' in prompts[0]


@pytest.mark.parametrize('selection', [[], ['R9'], [1], 'R1'])
def test_invalid_subset_never_becomes_all(monkeypatch, selection):
    monkeypatch.setattr(revise_document, 'AutoModel', lambda **kw: lambda prompt:
        json.dumps({'intent': 'review', 'selection': 'subset', 'issue_ids': selection}))
    with pytest.raises(contracts.MissingMaterials):
        revise_document.resolve_review_issues('只改指定意见', {'issues': [{'id': 'R1'}]})


@pytest.mark.parametrize('partial', [False, True])
@pytest.mark.parametrize('pending_action', ['needs_material', 'needs_experiment'])
def test_missing_section_and_partial_review_use_shared_writer(monkeypatch, partial, pending_action):
    """Real F6 output, pool/API adaptation, Writer application and version preservation."""
    from types import SimpleNamespace
    from unittest.mock import Mock
    from lazymind.document_tools.paper import peer_review
    from lazymind.document_tools.paper.review_schema import ReviewResult
    from lazymind.document_tools.toolkits import WriterRevisionToolkit

    issues = [
        dict(severity='minor', category='structure', location='缺失章节：局限性', quote=None,
             problem='缺少局限性章节', suggestion='在讨论后新增局限性，说明现有证据有限。', action_type='text_revision'),
        dict(severity='minor', category='language', location='Methods', quote='Keep methods.',
             problem='表达不够清晰', suggestion='重写方法说明。', action_type='text_revision'),
        dict(severity='minor', category='evidence', location='缺失内容：独立验证', quote=None,
             problem='缺少独立验证', suggestion='待作者补充验证，当前无可执行的文字修改。', action_type=pending_action),
    ]
    review = peer_review.build_output(ReviewResult(summary='必须重写方法说明', issues=issues, limitations=[]),
                                      {'topic': 'Paper', 'manuscript': BODY})
    pool = workflow.pool_from_business(dict(topic='Paper', manuscript=BODY,
                                           review=review['review'], review_data=review['review_data']))
    pool['selected_issue_ids'] = ['R1', 'R3'] if partial else ['R1', 'R2', 'R3']
    original_review = workflow.material(pool, 'review_data')['content']
    addition = '\n\n## Limitations\n现有证据有限，尚不能推广到其他场景。'
    operations = [dict(content_ref={'heading_path': ['Paper', 'Discussion']}, modify_type='create',
                       position='after', target_scope='section', instruction='新增局限性章节', meta={'issue_ids': ['R1']})]
    changes = [dict(content_ref={'heading_path': ['Paper', 'Discussion']},
                    old_string='Shorten this paragraph.', new_string='Shorten this paragraph.' + addition)]
    if not partial:
        operations.append(dict(content_ref={'heading_path': ['Paper', 'Methods']}, modify_type='update',
                               target_scope='section', instruction='澄清方法', meta={'issue_ids': ['R2']}))
        changes.append(dict(content_ref={'heading_path': ['Paper', 'Methods']},
                            old_string='Keep methods.', new_string='Clarified methods.'))
    locate_result = json.dumps({'target_title': False, 'targets': [
        {'content_ref': operation['content_ref'], 'reason': operation['instruction']} for operation in operations]})
    shared = WriterRevisionToolkit()
    editor = SimpleNamespace(
        locate_revision_target=Mock(return_value=locate_result),
        generate_modify_plan=Mock(return_value=json.dumps({'scope': 'document', 'instructions': operations})),
        generate_string_replace_set=Mock(return_value=json.dumps({'replacements': changes})),
        apply_string_replace=Mock(wraps=shared.apply_string_replace),
    )
    monkeypatch.setattr(revise_document, 'WriterRevisionToolkit', lambda: editor)
    result = workflow.execute_step('revise_document', pool,
        '先改第1、3条，第2条先留着' if partial else '按全部审稿意见修改，缺实验的先保留')
    expected = BODY + addition
    if not partial:
        expected = expected.replace('Keep methods.', 'Clarified methods.')
    assert workflow.material(result['pool'], 'manuscript')['content'] == expected
    assert workflow.material(pool, 'manuscript')['content'] == BODY
    refreshed = workflow.material(result['pool'], 'review_data')['content']
    assert refreshed['manuscript_sha256'] != original_review['manuscript_sha256']
    assert any(i['action_type'] == pending_action for i in refreshed['issues'])
    assert workflow.material(pool, 'review_data')['content'] == original_review
    locating = editor.locate_revision_target.call_args.kwargs
    assert '缺失章节：局限性' in locating['writing_task_json']
    assert editor.generate_modify_plan.call_args.kwargs['locate_result_json'] == locate_result
    if partial:
        assert '重写方法说明' not in locating['writing_context_json']
        assert '重写方法说明' not in locating['writing_task_json']
        assert 'R2' not in json.dumps(operations)
    assert editor.apply_string_replace.called


@pytest.mark.parametrize('selected', [['R1'], ['R1', 'R2']])
def test_revision_publication_reports_only_selected_pending_work(tmp_path, monkeypatch, selected):
    import lazyllm
    from lazymind.chat.engine.subagent.context import SubAgentContext, set_context
    from lazymind.document_tools.paper import peer_review
    from lazymind.document_tools.paper.review_schema import ReviewResult
    review = peer_review.build_output(ReviewResult.model_validate({
        'summary': 'Revise discussion', 'limitations': [], 'issues': [
            dict(severity='minor', category='language', location='Discussion', quote='Shorten this paragraph.',
                 problem='Verbose', suggestion='Shorten', action_type='text_revision'),
            dict(severity='minor', category='evidence', location='Missing validation', quote=None,
                 problem='Missing validation', suggestion='Author must run validation', action_type='needs_experiment'),
        ]}), {'topic': 'Paper', 'manuscript': BODY})
    pool = workflow.pool_from_business(dict(topic='Paper', manuscript=BODY,
        review=review['review'], review_data=review['review_data']))
    path = tmp_path / 'review-pool.json'
    path.write_text(json.dumps(pool))
    monkeypatch.setattr(revise_document, 'AutoModel', lambda **kw: lambda prompt:
        json.dumps({'intent': 'review', 'selection': 'subset', 'issue_ids': selected}))
    monkeypatch.setattr(revise_document, '_modify_manuscript', lambda *args, **kwargs:
        {'manuscript': BODY.replace('Shorten this paragraph.', 'Shortened.'), 'is_demo': False})
    previous = lazyllm.globals.get('subagent_ctx')
    events = []
    slots = ['revise_document_pool', 'revise_document_document']
    set_context(SubAgentContext(
        task_id='review-pending', conversation_id='paper-test', agent_type='workflow_step',
        objective='Revise selected issues', params={
            'step_id': 'revise_document', 'remote_inputs': {'peer_review_pool': str(path)},
            'current_user_input': '修改所选意见，待补实验先留着',
            'output_slot_types': {slot: 'file' for slot in slots},
        }, workspace_path=str(tmp_path), input_slots=['peer_review_pool'], output_slots=slots,
        db=None, emit=events.append,
    ))
    try:
        result = workflow.run_paper_step()
        assert result['status'] == 'completed'
        final = result['_agent_control']['final_text']
        assert '仍需补充材料或实验' in final
        assert '新报告中的问题编号' in final
        saved = next(e['value']['path'] for e in events if e.get('slot') == 'revise_document_pool'
                     and e['type'] == 'artifact')
        persisted = workflow._read_bound(saved)
        assert workflow.material(persisted, 'review_data')['content']['manuscript_sha256'] != review['review_data']['manuscript_sha256']
        assert persisted['selected_issue_ids'] == []
    finally:
        if previous is None:
            lazyllm.globals.pop('subagent_ctx', None)
        else:
            lazyllm.globals['subagent_ctx'] = previous
