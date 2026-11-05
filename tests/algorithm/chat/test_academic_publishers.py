import copy
from pathlib import Path

import pytest
from lazyllm.tools.agent import ToolExecutionError, ToolManager
from lazymind.chat.engine.subagent import context, tools


@pytest.fixture
def publisher(tmp_path, monkeypatch):
    path = Path(__file__).resolve().parents[3] / 'workflows/paper_expansion/scripts/tools.py'
    from lazymind.workflow_toolkit import load_workflow_package_tools
    package = {'files': {'scripts/tools.py': path.read_bytes()}}
    functions = load_workflow_package_tools(
        package, ['academic_submit_relations', 'academic_validate_stage',
                  'academic_publish_stage'],
        'paper_expansion', 'test-revision',
    )
    namespace = functions['academic_submit_relations'].__globals__

    class Module:
        """Replace HTTP in the actual loaded namespace."""

    module = Module()
    # Keep attributes in the real namespace so monkeypatch can inspect and
    # restore them, including pytest versions that use static attribute lookup.
    module.__dict__ = namespace
    body = {'user_instruction': 'read actual evidence', 'candidate_directions': ['agents'],
            'selected_directions': ['agents'], 'papers': [
                {'paper': {'paper_id': 'a', 'title': 'model title'}, 'is_seed': False, 'is_important': False}]}
    events = []
    ctx = context.SubAgentContext('task', 'chat', 'workflow_step', 'analyze', {
        'step_id': 'analyze_relations', 'remote_inputs': {'candidate_set': body, 'expansion_id': 'exp'},
        'workflow_runtime': {'publisher_owned_slots': ['relation_analysis', 'expansion_result', 'expansion_report']},
    }, str(tmp_path), [], ['relation_analysis', 'expansion_result', 'expansion_report'], None, events.append)
    monkeypatch.setattr(context, 'require_context', lambda: ctx)
    monkeypatch.setattr(tools, 'require_context', lambda: ctx)
    resolved = {'papers': [{'paper': {'paper_id': 'a', 'title': 'server title', 'source_version': 'v2'},
                           'is_seed': True, 'is_important': True}],
                'contents': [{'paper_id': 'a', 'status': 'ready'}]}
    monkeypatch.setattr(module, '_resolve', lambda ids: copy.deepcopy(resolved))
    monkeypatch.setattr(module, '_read_expansion_context',
                        lambda _: {'record': {'result': copy.deepcopy(body), 'reading': {}}})
    monkeypatch.setattr(module, '_model_metrics', lambda: {})
    return module, ctx, events, resolved


def test_direction_entry_accepts_no_seeds_and_seed_entry_cannot_drop_them(publisher, monkeypatch):
    module, ctx, _, resolved = publisher
    ctx.params['step_id'] = 'expand_and_screen'
    body = ctx.params['remote_inputs']['candidate_set']
    monkeypatch.setattr(module, '_read_expansion_context', lambda _: {'record': {'result': {'papers': []}}})
    assert module.academic_validate_stage(body)['status'] == 'valid'
    monkeypatch.setattr(module, '_read_expansion_context', lambda _: {
        'record': {'result': {'papers': [{'paper': {'paper_id': 'seed'}, 'is_seed': True}]}}})
    with pytest.raises(ToolExecutionError, match='all selected seed'):
        module.academic_validate_stage(body)
    for invalid in [{'test': True}, {}]:
        with pytest.raises(ToolExecutionError):
            module.academic_validate_stage(invalid)


def test_candidates_publish_authoritative_papers_once(publisher, monkeypatch):
    module, ctx, events, resolved = publisher
    ctx.params['step_id'] = 'expand_and_screen'
    ctx.output_slots = ['candidate_set', 'expansion_list_ready']
    ctx.params['workflow_runtime']['publisher_owned_slots'].append('candidate_set')
    source = copy.deepcopy(ctx.params['remote_inputs']['candidate_set'])
    source['papers'] = []
    resolved['papers'][0]['is_seed'] = False
    monkeypatch.setattr(module, '_read_expansion_context', lambda _: {'record': {
        'result': source, 'budget_mode': 'balanced', 'preparation': {
            'directions': [{'name': 'agents', 'question': 'How do agents coordinate?'},
                           {'name': 'unselected', 'question': 'Other question'}],
            'research_questions': [{'direction': 'agents', 'question': 'Internal predecessor investigation'}]}}})

    args = (['a'], ['Keep the registered representative.'], [])
    assert module.academic_publish_stage(*args)['status'] == 'ok'
    assert events[0]['slot'] == 'candidate_set'
    result = events[0]['value']['data']
    assert result['papers'] == resolved['papers']
    assert result['decisions'] == args[1]
    assert result['directions'] == [{'name': 'agents', 'question': 'How do agents coordinate?'}]
    assert 'research_questions' not in result
    assert module.academic_publish_stage(*args)['already_published'] is True
    assert len(events) == 1


def test_invalid_task_papers_reports_context_error_without_publishing(publisher, monkeypatch):
    module, ctx, events, _ = publisher
    ctx.params['step_id'] = 'expand_and_screen'
    source = copy.deepcopy(ctx.params['remote_inputs']['candidate_set'])
    source['papers'] = None
    monkeypatch.setattr(module, '_read_expansion_context', lambda _: {'record': {'result': source}})
    with pytest.raises(RuntimeError, match='Invalid task context: papers must be an array'):
        module.academic_publish_stage(['a'], [], [])
    assert events == []


@pytest.mark.parametrize(('goal', 'mode', 'ends_with_list'), [
    ('expansion', 'fast', True), ('review', 'fast', True), ('review', 'balanced', False),
])
def test_paper_list_marker_is_only_published_for_lightweight_expansion(
        publisher, monkeypatch, goal, mode, ends_with_list):
    module, ctx, events, resolved = publisher
    ctx.params['step_id'] = 'expand_and_screen'
    ctx.output_slots[:] = ['candidate_set', 'expansion_list_ready']
    resolved['papers'].append({'paper': {'paper_id': 'b', 'title': 'Related'}, 'is_seed': False, 'is_important': False})
    monkeypatch.setattr(module, '_read_expansion_context', lambda _: {'record': {
        'goal': goal, 'budget_mode': mode, 'result': ctx.params['remote_inputs']['candidate_set'],
        'preparation': {'directions': [{'name': 'agents', 'question': 'How do they coordinate?'}]},
    }})
    recommendation = {'paper_id': 'b', 'reason': 'Directly studies coordination', 'direction': 'agents'}
    if ends_with_list:
        with pytest.raises(ToolExecutionError, match='cover all non-seed papers'):
            module.academic_publish_stage(['a', 'b'], [], [])
        assert events == []
    result = module.academic_publish_stage(
        ['a', 'b'], [], [], recommendations=[recommendation] if ends_with_list else None)
    assert result['status'] == 'ok'
    expected_slots = ['candidate_set']
    if ends_with_list:
        expected_slots.append('expansion_list_ready')
    assert [event['slot'] for event in events] == expected_slots
    if ends_with_list:
        assert events[0]['value']['data']['recommendations'] == [recommendation]


@pytest.mark.parametrize('mode', ['balanced', 'deep'])
@pytest.mark.parametrize('goal', ['expansion', 'review'])
def test_standard_and_deep_publish_the_list_only_after_screening(publisher, monkeypatch, mode, goal):
    module, ctx, events, resolved = publisher
    ctx.params['step_id'] = 'expand_and_screen'
    ctx.output_slots[:] = ['candidate_set', 'expansion_list_ready']
    resolved['papers'].append({'paper': {'paper_id': 'b', 'title': 'Related'}, 'is_seed': False, 'is_important': False})
    monkeypatch.setattr(module, '_read_expansion_context', lambda _: {'record': {
        'goal': goal, 'budget_mode': mode, 'result': ctx.params['remote_inputs']['candidate_set'],
    }})
    module.academic_publish_stage(['a', 'b'], [], [])
    assert [event['slot'] for event in events] == ['candidate_set']
    draft = events[0]['value']['data']
    assert draft['screening_pending'] and 'list_mode' not in draft

    ctx.params['remote_inputs']['candidate_set'] = draft
    ctx.params['step_id'] = 'screen_and_supplement'
    ctx.output_slots[:] = ['screened_candidates', 'screened_list_ready']
    recommendation = {'paper_id': 'b', 'reason': 'Directly studies coordination', 'direction': 'agents'}
    module.academic_publish_stage(['a', 'b'], [], [], recommendations=[recommendation])
    expected = ['candidate_set', 'screened_candidates']
    if mode == 'balanced':
        expected.append('screened_list_ready')
    assert [event['slot'] for event in events] == expected
    final = events[1]['value']['data']
    assert final['selection'] == {'before_count': 2, 'changes': []}
    assert final['list_mode'] == mode
    assert final['recommendations'] == [recommendation]
    ctx.params['remote_inputs'].update(candidate_set=draft, screened_candidates=final)
    assert module._analysis_candidates() == final


@pytest.mark.parametrize('mode', ['balanced', 'deep'])
def test_screening_saves_actual_additions_and_requires_removal_reasons(publisher, monkeypatch, mode):
    module, ctx, events, resolved = publisher
    ctx.params['step_id'] = 'screen_and_supplement'
    ctx.output_slots[:] = ['screened_candidates', 'screened_list_ready']
    before = ctx.params['remote_inputs']['candidate_set']
    removed = {'paper': {'paper_id': 'old', 'title': 'Unrelated'}, 'is_seed': False, 'is_important': False}
    before['papers'].append(removed)
    added = {'paper': {'paper_id': 'new', 'title': 'New source'}, 'is_seed': False, 'is_important': False}
    resolved['papers'].append(added)
    monkeypatch.setattr(module, '_read_expansion_context', lambda _: {'record': {
        'goal': 'expansion', 'budget_mode': mode, 'result': before,
    }})
    recommendations = [{'paper_id': 'new', 'reason': 'Adds the missing source', 'direction': 'agents'}]
    with pytest.raises(ToolExecutionError, match='cover exactly'):
        module.academic_publish_stage(['a', 'new'], [], [], recommendations=recommendations)
    assert events == []
    module.academic_publish_stage(['a', 'new'], [], [], recommendations=recommendations,
                                  removal_reasons=[{'paper_id': 'old', 'reason': 'Studies a different mechanism'}])
    final = events[0]['value']['data']
    assert final['selection'] == {'before_count': 2, 'changes': [
        {'action': 'removed', 'paper': removed, 'reason': 'Studies a different mechanism'},
        {'action': 'added', 'paper': added, 'reason': 'Adds the missing source'},
    ]}
    assert [paper['paper']['paper_id'] for paper in final['papers']] == ['a', 'new']


def test_deep_analysis_cannot_replace_the_frozen_list(publisher):
    module, ctx, _, _ = publisher
    ctx.params['remote_inputs']['candidate_set']['list_mode'] = 'deep'
    with pytest.raises(ToolExecutionError, match='frozen candidate paper IDs'):
        module._lineage_input([{'paper_id': 'new', 'key_contribution': 'A substitute'}])
    result, _, _, selection = module._lineage_input([{'paper_id': 'a', 'key_contribution': 'Based on the full text'}])
    assert [row['paper']['paper_id'] for row in result['papers']] == ['a']
    assert selection == {'before_count': 1, 'changes': []}


def test_claim_schema_requires_the_published_analysis_key(publisher):
    module, _, _, _ = publisher
    from lazymind.chat.lazyllm_tool_docs import ensure_lazyllm_tool_docs
    ensure_lazyllm_tool_docs([module.academic_submit_relations])
    tool = ToolManager([module.academic_submit_relations]).all_tools[0]
    args = {'analyses': [{'paper_id': 'a', 'key_contribution': 'Improves by 7%'}],
            'relations': [], 'investigation': [],
            'claims': [{'paper_id': 'a', 'field': 'key_contribution',
                        'positions': [0]}]}
    assert tool._validate_input(args)['claims'][0]['field'] == 'key_contribution'
    args['claims'][0]['field'] = 'method'
    with pytest.raises(Exception, match='seed_relation|key_contribution'):
        tool._validate_input(args)


def test_large_history_keeps_all_current_papers_visible_and_preserves_full_snapshot(publisher, monkeypatch):
    import json
    import lazyllm
    from lazymind.chat.engine.tools.file_resources.tools import build_resource_read_tools
    from lazymind.common.token_estimation import estimate_tokens
    module, ctx, _, _ = publisher
    papers = [{'paper': {'paper_id': f'paper-{index}', 'title': f'Paper {index}',
                         'abstract': 'Complete abstract.', 'authors': ['Author']},
               'is_seed': index == 0} for index in range(33)]
    full = {'record': {'id': 'exp', 'result': {'papers': papers, 'user_instruction': 'same scope'},
                       'reading': {'paper-0': {'positions': [0, 3], 'content_hash': 'hash-v1'}},
                       'preparation': {'directions': [{'name': 'route', 'paper_ids': ['paper-0'], 'papers': papers}]},
                       'report': {'source_queries': 'history marker ' * 20000, 'limitations': ['partial reading']},
                       'progress': {'old_result': papers}},
            'rounds': [{'id': 'round-1', 'version': 1, 'status': 'completed', 'result': papers}],
            'contents': [{'paper_id': 'paper-0', 'status': 'ready', 'content_hash': 'hash-v1'}],
            'source_queries': [{
                'id': 'actual-query', 'query': 'Original method', 'mode': 'citations',
                'paper_id': 'paper-0', 'paper_ids': ['paper-1'], 'created_at': '2026-11-02',
                'sources': [{'source': 'arxiv', 'status': 'rate_limited', 'retry_at': '2026-11-03',
                             'count': 1, 'has_more': True, 'next_cursor': 'exact-source-cursor', 'elapsed_ms': 123}]}]}
    monkeypatch.setattr(module, '_read_expansion_context', lambda _: copy.deepcopy(full))
    visible = module.academic_expansion_context()
    assert [row['paper']['paper_id'] for row in visible['record']['result']['papers']] == [
        f'paper-{index}' for index in range(33)]
    assert visible['record']['reading'] == full['record']['reading']
    query = visible['source_queries'][0]
    assert {key: value for key, value in query.items() if key != 'sources'} == {
        key: value for key, value in full['source_queries'][0].items() if key != 'sources'}
    assert query['sources'] == [{'source': 'arxiv', 'status': 'rate_limited', 'retry_at': '2026-11-03',
                                 'count': 1, 'has_more': True}]
    assert 'report' not in visible['record']
    assert estimate_tokens(json.dumps(visible, ensure_ascii=False)) < 28000
    path = Path(ctx.workspace_path) / visible['full_context_resource'].removeprefix('workspace://')
    assert json.loads(path.read_text()) == {**full, 'bound_inputs': visible['bound_inputs']}
    assert visible['full_context_resource'].startswith('workspace://tool_spills/')
    monkeypatch.setattr(lazyllm, 'globals', {'agentic_config': {
        'agent_type': 'workflow_step', 'workflow_workspace_path': ctx.workspace_path}})
    readers = {tool.__name__: tool for tool in build_resource_read_tools()}
    read = readers['read_file_resource'](visible['full_context_resource'])
    assert read['target'] == visible['full_context_resource']
    assert read['kind'] == 'workspace'
    assert 'paper-0' in read['text']
    assert read['next_offset'] is not None
    match = readers['search_file_resource'](visible['full_context_resource'], 'same scope')['matches'][0]
    page = readers['read_file_resource'](visible['full_context_resource'], offset=match['line'], limit=1)
    assert 'same scope' in page['text']


@pytest.mark.parametrize('status', [400, 422])
def test_core_argument_errors_preserve_actionable_code_and_message(publisher, monkeypatch, status):
    import httpx
    from types import SimpleNamespace
    module, _, _, _ = publisher
    message = 'paper must be a saved seed or an externally discovered candidate of this expansion'
    response = httpx.Response(status, json={'code': 'INVALID_SELECTION', 'message': message},
                              request=httpx.Request('POST', 'http://core/academic/paper-contents:read'))
    transport = SimpleNamespace(request=lambda *args, **kwargs: response)
    monkeypatch.setattr(module, '_client', lambda: SimpleNamespace(
        transport=transport, base_url='http://core', _headers=lambda: {}))
    with pytest.raises(ToolExecutionError, match='INVALID_SELECTION: ' + message):
        module.academic_read_paper(paper_id='not-registered')
    with pytest.raises(ToolExecutionError, match='INVALID_SELECTION: ' + message):
        module._call('POST', '/expansions/exp/papers:resolve', {'paper_ids': ['not-registered']})


def test_core_service_failure_keeps_original_http_error(publisher, monkeypatch):
    import httpx
    from types import SimpleNamespace
    module, _, _, _ = publisher
    response = httpx.Response(503, json={'code': 'UNAVAILABLE', 'message': 'service unavailable'},
                              request=httpx.Request('POST', 'http://core/academic/paper-contents:read'))
    monkeypatch.setattr(module, '_client', lambda: SimpleNamespace(
        transport=SimpleNamespace(request=lambda *args, **kwargs: response),
        base_url='http://core', _headers=lambda: {}))
    with pytest.raises(httpx.HTTPStatusError) as error:
        module.academic_read_paper(paper_id='a')
    assert error.value.response is response
