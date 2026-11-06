"""Dynamic SDK F6 integration, plus direct schema and report-rendering checks."""
import copy
import json
import re
from pathlib import Path

import pytest
from pydantic import ValidationError

from lazymind.document_tools.paper import api, peer_review, workflow

EXAMPLE = Path(__file__).resolve().parents[4] / 'tests/fixtures/paper-writing/peer_review.json'


@pytest.fixture
def example():
    return json.loads(EXAMPLE.read_text())


def test_review_instructions_are_english_with_explicit_output_language():
    for prompt in (peer_review.REVIEW_PROMPT, peer_review.REPAIR_PROMPT):
        assert not re.search(r'[\u4e00-\u9fff]', prompt)
        assert 'Simplified Chinese unless the request' in prompt
    for schema in (peer_review.ReviewResult, peer_review.RepairedIssue, peer_review.ReviewRepair):
        assert not re.search(r'[\u4e00-\u9fff]', json.dumps(schema.model_json_schema(), ensure_ascii=False))


def test_dynamic_model_review_preserves_input_and_output_contract(example, offline_paper_model):
    offline_paper_model(example['model_response'])
    before = copy.deepcopy(example['input'])
    output = api.run_algorithm('peer_review', example['input'])
    assert example['input'] == before
    assert set(output) == {'review', 'review_data', 'is_demo'}
    assert output['is_demo'] is False
    assert all(section in output['review'] for section in ('## 总体评价', '## 逐条意见', '## 检查范围与限制'))
    assert '未提供分析材料' in output['review']
    assert output['review_data']['manuscript_sha256'] == peer_review.text_digest(before['manuscript'])


def test_dynamic_review_reaches_revision_contract(example, offline_paper_model):
    offline_paper_model(example['model_response'])
    pool = workflow.pool_from_business(example['input'])
    result = workflow.execute_step('peer_review', pool, '只检查结论是否超出证据')
    assert result['status'] == 'completed'
    assert result['mock'] is False
    result['pool']['selected_issue_ids'] = [
        issue['id'] for issue in workflow.material(result['pool'], 'review_data')['content']['issues']
    ]
    revision = workflow.business_input('revise_document', result['pool'], '')
    assert revision['manuscript'] == example['input']['manuscript']
    assert revision['review'] == workflow.material(result['pool'], 'review')['content']
    assert '## 逐条意见' in revision['review']
    assert revision['review_data'] == workflow.material(result['pool'], 'review_data')['content']
    api.contracts.validate_input('revise_document', revision)


def test_render_example_without_mutating_input(example):
    before = copy.deepcopy(example)
    result = peer_review.ReviewResult.model_validate(example['model_response'])
    assert peer_review.render_review(result, example['input']) == example['output']['review']
    assert peer_review.build_output(result, example['input']) == example['output']
    assert example == before


def test_priority_numbering_and_all_action_types(example):
    response = example['model_response']
    issue = response['issues'][0]
    response['issues'] = [
        dict(issue, severity='suggestion', action_type='text_revision'),
        dict(issue, severity='major', action_type='needs_experiment'),
        dict(issue, severity='minor', action_type='needs_material'),
    ]
    before = copy.deepcopy(response)
    result = peer_review.ReviewResult.model_validate(response)
    text = peer_review.render_review(result, example['input'])
    assert 'R1｜重要问题' in text and 'R2｜一般问题' in text and 'R3｜建议' in text
    assert all(label in text for label in peer_review.ACTION_LABELS.values())
    assert response == before


def test_no_findings_is_not_publication_approval():
    result = peer_review.ReviewResult(summary='未识别出明确问题。', issues=[], limitations=[])
    text = peer_review.render_review(result, {'topic': 'T', 'manuscript': '# 稿件\n正文'})
    assert '不代表论文已达到发表标准' in text
    assert '未提供文献证据材料' in text
    assert '未提供分析材料' in text


def test_missing_content_and_multiline_quote_rendering(example):
    issue = example['model_response']['issues'][0]
    issue.update(location='全文（缺少数据说明）', quote=None)
    result = peer_review.ReviewResult.model_validate(example['model_response'])
    assert '全文或缺失内容建议' in peer_review.render_review(result, example['input'])
    issue.update(location='方法', quote='## 方法\n比较两种证据组织方法。')
    result = peer_review.ReviewResult.model_validate(example['model_response'])
    assert '> ## 方法\n> 比较两种证据组织方法。' in peer_review.render_review(result, example['input'])


@pytest.mark.parametrize('change', [
    {'severity': 'critical'}, {'category': 'invented'}, {'action_type': 'auto_experiment'},
    {'location': ''}, {'problem': '  '}, {'suggestion': 42}, {'extra': 'unknown'},
])
def test_invalid_issue_schema_is_rejected(example, change):
    example['model_response']['issues'][0].update(change)
    with pytest.raises(ValidationError):
        peer_review.ReviewResult.model_validate(example['model_response'])


def test_empty_manuscript_is_rejected():
    with pytest.raises(ValueError):
        peer_review.run({'topic': 'T', 'manuscript': ' '})


def test_whitespace_only_quote_difference_restores_exact_source(example):
    result = peer_review.ReviewResult.model_validate(example['model_response'])
    result.issues[0].quote = '本文方法 20 16 90% 3.2'
    inputs = {**example['input'], 'manuscript': '结果：\n本文方法   20\t16  90%\n3.2\n结束'}
    before = result.model_dump()
    output = peer_review.build_output(result, inputs)
    quote = output['review_data']['issues'][0]['quote']
    assert quote == '本文方法   20\t16  90%\n3.2'
    assert quote in inputs['manuscript']
    assert result.model_dump() == before


@pytest.mark.parametrize('quote', ['本文方法 20 18 90% 3.2', '本文方法 2016 90% 3.2', '本文方法……3.2'])
def test_changed_numbers_and_stitched_quotes_are_not_accepted(example, quote):
    result = peer_review.ReviewResult.model_validate(example['model_response'])
    result.issues[0].quote = quote
    with pytest.raises(peer_review.ReviewQuoteError, match='论文已收到') as error:
        peer_review.build_output(result, {**example['input'], 'manuscript': '本文方法 20 16 90% 3.2'})
    assert quote in str(error.value)


@pytest.mark.parametrize('field', ['manuscript', 'review'])
def test_revision_rejects_stale_structured_review(example, field):
    result = peer_review.ReviewResult.model_validate(example['model_response'])
    output = peer_review.build_output(result, example['input'])
    inputs = {**example['input'], 'review': output['review'], 'review_data': output['review_data']}
    api.contracts.validate_input('revise_document', inputs)
    inputs[field] += '\n用户编辑'
    with pytest.raises(ValueError, match='已过期'):
        api.contracts.validate_input('revise_document', inputs)


def test_structured_ids_match_markdown_and_reject_duplicate_ids(example):
    issue = example['model_response']['issues'][0]
    result = peer_review.ReviewResult.model_validate({**example['model_response'], 'issues': [
        dict(issue, severity='suggestion'), dict(issue, severity='major'),
    ]})
    output = peer_review.build_output(result, example['input'])
    issues = output['review_data']['issues']
    assert [(i['id'], i['severity']) for i in issues] == [('R1', 'major'), ('R2', 'suggestion')]
    assert 'R1｜重要问题' in output['review'] and 'R2｜建议' in output['review']
    issues[1]['id'] = 'R1'
    with pytest.raises(ValueError, match='编号不能重复'):
        api.contracts.validate_output('peer_review', output)


def test_edited_report_from_pool_cannot_use_old_structured_advice(example):
    result = peer_review.ReviewResult.model_validate(example['model_response'])
    output = peer_review.build_output(result, example['input'])
    pool = workflow.pool_from_business({**example['input'], 'review': output['review'],
                                       'review_data': output['review_data']})
    pool['history'].append({'node': 'F6', 'request': '', 'inputs': [], 'outputs': []})
    pool['sequence'] = 1
    edited = workflow.pool_from_inputs({'peer_review_pool': pool,
                                       'peer_review_document': output['review'] + '\n编辑后的意见'})
    edited['selected_issue_ids'] = [output['review_data']['issues'][0]['id']]
    with pytest.raises(ValueError, match='已过期'):
        api.contracts.validate_input('revise_document', workflow.business_input('revise_document', edited))


def test_legacy_markdown_only_revision_requires_structured_review(example):
    # F5 now consumes the paired report/review_data produced by F6.
    with pytest.raises(api.contracts.MissingMaterials, match='结构化意见'):
        api.contracts.validate_input('revise_document', {
            **example['input'], 'review': example['output']['review'],
        })


def test_partial_repair_keeps_valid_issues_and_input_unchanged(example):
    issue = example['model_response']['issues'][0]
    candidate = {**example['model_response'], 'issues': [copy.deepcopy(issue), copy.deepcopy(issue)]}
    del candidate['issues'][1]['suggestion']
    candidate['issues'][1]['正文被误当字段'] = '错误值'
    before = copy.deepcopy(candidate)
    targets = peer_review.repair_targets(candidate, example['input']['manuscript'])
    assert set(targets) == {'issues.1'}
    repair = peer_review.ReviewRepair(replacements={
        'issues.1': dict(issue, quote={'start_line': 3, 'end_line': 3})})
    result = peer_review.apply_repair(candidate, targets, repair, example['input']['manuscript'])
    assert result.issues[0].model_dump() == candidate['issues'][0]
    assert result.issues[1].model_dump() == issue
    assert candidate == before
    peer_review.build_output(result, example['input'])


def test_quote_and_format_failures_share_one_repair_batch(example):
    issue = example['model_response']['issues'][0]
    candidate = {**example['model_response'], 'issues': [copy.deepcopy(issue), copy.deepcopy(issue)]}
    del candidate['issues'][0]['action_type']
    candidate['issues'][1]['quote'] = '不在原稿中的一句话'
    targets = peer_review.repair_targets(candidate, example['input']['manuscript'])
    assert set(targets) == {'issues.0', 'issues.1'}


@pytest.mark.parametrize('replacements', [{}, {'summary': '擅自改动有效字段'}, {'issues.0': {}}])
def test_repair_cannot_omit_targets_or_change_valid_fields(example, replacements):
    candidate = example['model_response']
    del candidate['issues'][0]['suggestion']
    targets = peer_review.repair_targets(candidate, example['input']['manuscript'])
    with pytest.raises(ValueError):
        peer_review.apply_repair(candidate, targets, peer_review.ReviewRepair(replacements=replacements),
                                 example['input']['manuscript'])


def test_summary_and_limitations_can_be_repaired_without_regenerating_issues(example):
    candidate = {**example['model_response'], 'summary': '', 'limitations': '不是数组'}
    targets = peer_review.repair_targets(candidate, example['input']['manuscript'])
    assert set(targets) == {'summary', 'limitations'}
    result = peer_review.apply_repair(candidate, targets, peer_review.ReviewRepair(
        replacements={'summary': '已有审查结果', 'limitations': []}), example['input']['manuscript'])
    assert [issue.model_dump() for issue in result.issues] == candidate['issues']


@pytest.mark.parametrize('candidate', [None, [], {}, {'issues': '坏列表'},
                                       {'summary': 'S', 'issues': [], 'limitations': [], 'unknown': 1}])
def test_broken_envelope_stops_instead_of_regenerating_review(candidate):
    with pytest.raises(ValueError, match='未自动重新审稿'):
        peer_review.repair_targets(candidate, '稿件')


def test_valid_result_needs_no_repair(example):
    assert peer_review.repair_targets(example['model_response'], example['input']['manuscript']) == {}


def test_review_computes_fact_conflicts_and_keeps_language_advice_separate(example, monkeypatch):
    inputs = {**example['input'], 'manuscript': '总token从 31,576 增至 34,840，成本下降。句子过长。',
              'evidence': '总token，含失败：31,576 → 34,840。'}
    issue = dict(example['model_response']['issues'][0], category='language', quote='句子过长。',
                 problem='表达可精简。', suggestion='精简句子。', action_type='text_revision')
    candidate = {'summary': '核对成本方向；另有表达建议。', 'issues': [issue], 'limitations': [], 'fact_claims': [{
        'category': 'comparison', 'location': '结果', 'quote': inputs['manuscript'].split('句子')[0],
        'asserted_text': '下降', 'operation': 'direction', 'sources': [
            {'path': '/evidence', 'quote': inputs['evidence'], 'value_text': '34,840'},
            {'path': '/evidence', 'quote': inputs['evidence'], 'value_text': '31,576'},
        ],
    }]}
    monkeypatch.setattr(peer_review, 'AutoModel', lambda **kw: object())
    monkeypatch.setattr(peer_review, 'WriterToolBase', lambda **kw: object())
    monkeypatch.setattr(peer_review, 'call_once', lambda *args: candidate)
    output = peer_review.run(inputs)
    data = output['review_data']
    assert len(data['fact_checks']) == 5
    assert next(item for item in data['fact_checks'] if item['category'] == 'comparison')['status'] == 'conflict'
    assert {item['disposition'] for item in data['issues']} == {'fact', 'advice'}
    assert '### 事实问题' in output['review'] and '论证与语言建议（不阻断交付）' in output['review']
    assert data['review_sha256'] == peer_review.text_digest(output['review'])
    assert all(item['quote'] in inputs['manuscript'] for item in data['issues'])
    api.contracts.validate_output('peer_review', output)


def test_fact_claim_format_repair_preserves_existing_opinions(example):
    candidate = {**example['model_response'], 'fact_claims': 'invalid'}
    targets = peer_review.repair_targets(candidate, example['input']['manuscript'])
    assert set(targets) == {'fact_claims'}
    prompt = peer_review.repair_prompt(example['input'], targets)
    assert 'fact_claim_schema' in prompt
    result = peer_review.apply_repair(
        candidate, targets, peer_review.ReviewRepair(replacements={'fact_claims': []}), example['input']['manuscript'])
    assert [issue.model_dump() for issue in result.issues] == example['model_response']['issues']


@pytest.mark.parametrize('start,end', [(0, 1), (3, 2), (1, 999), (True, 2)])
def test_repair_rejects_invalid_quote_lines(example, start, end):
    issue = example['model_response']['issues'][0]
    repair = peer_review.ReviewRepair(replacements={
        'issues.0': dict(issue, quote={'start_line': start, 'end_line': end})})
    with pytest.raises(ValueError):
        peer_review.apply_repair(example['model_response'], {'issues.0': {}}, repair,
                                 example['input']['manuscript'])


def test_repair_copies_source_lines_without_rewriting_whitespace(example):
    manuscript = '方法\r\n数据  16\t20\r\n准确率 90%\r\n'
    issue = example['model_response']['issues'][0]
    repair = peer_review.ReviewRepair(replacements={
        'issues.0': dict(issue, quote={'start_line': 2, 'end_line': 3})})
    result = peer_review.apply_repair(example['model_response'], {'issues.0': {}}, repair, manuscript)
    assert result.issues[0].quote == '数据  16\t20\r\n准确率 90%'
    assert result.issues[0].quote in manuscript


def test_format_repair_contains_only_bad_issues_without_manuscript(example):
    candidate = copy.deepcopy(example['model_response'])
    candidate['issues'].append(dict(candidate['issues'][0], problem='只用于有效意见的标记'))
    del candidate['issues'][0]['suggestion']
    targets = peer_review.repair_targets(candidate, example['input']['manuscript'])
    prompt = peer_review.repair_prompt(example['input'], targets)
    assert '只用于有效意见的标记' not in prompt
    payload = json.loads(prompt.split('Input JSON:\n', 1)[1])
    assert 'manuscript' not in payload['inputs']
    assert 'manuscript_lines' not in payload['inputs']
    assert payload['quote_mode'] == 'preserve'


def test_repeated_field_tag_does_not_split_or_expand_issues(example):
    candidate = example['model_response']
    raw = json.dumps(candidate, ensure_ascii=False, separators=(',', ':'))
    damaged = raw.replace('"location":', '"category":"location":', 1)
    assert damaged != raw
    parsed = peer_review.parse_response(damaged)
    assert parsed == candidate
    assert peer_review.repair_targets(parsed, example['input']['manuscript']) == {}


def test_json_parser_preserves_quoted_manuscript_syntax(example):
    candidate = example['model_response']
    candidate['issues'][0]['quote'] = '"category":"analysis","category":"location":"方法"'
    raw = json.dumps(candidate, ensure_ascii=False)
    assert peer_review.parse_response('```json\n' + raw + '\n```') == candidate


@pytest.mark.parametrize('raw', [
    '{"summary":"A","summary":"B","issues":[],"limitations":[]}',
    '{"summary":"A","issues":[{"problem":"unfinished"}',
    '{"summary":"A","issues":[{"problem":"a"} {"suggestion":"b"}],"limitations":[]}',
])
def test_ambiguous_json_is_not_guessed_or_expanded(raw):
    with pytest.raises(ValueError):
        peer_review.parse_response(raw)


def test_multiple_invalid_issues_are_independent_targets(example):
    candidate = copy.deepcopy(example['model_response'])
    candidate['issues'] = [dict(candidate['issues'][0], severity='invalid') for _ in range(6)]
    assert len(peer_review.repair_targets(candidate, example['input']['manuscript'])) == 6


def test_repair_payload_limit_does_not_silently_truncate_source(example):
    inputs = {**example['input'], 'request': '审查要求' * peer_review.MAX_REPAIR_CHARS}
    with pytest.raises(ValueError, match='超过限制'):
        peer_review.repair_prompt(inputs, {'summary': {'value': '', 'errors': []}})


def test_quote_repair_does_not_resend_large_analysis_or_figures(example):
    inputs = copy.deepcopy(example['input'])
    inputs.update(analysis={'report': 'analysis-only-marker' * peer_review.MAX_REPAIR_CHARS},
                  charts=[{'svg': 'figure-only-marker' * peer_review.MAX_REPAIR_CHARS}])
    before = copy.deepcopy(inputs)
    candidate = copy.deepcopy(example['model_response'])
    candidate['issues'][0]['quote'] = 'An inexact quotation'
    targets = peer_review.repair_targets(candidate, inputs['manuscript'])
    prompt = peer_review.repair_prompt(inputs, targets)
    payload = json.loads(prompt.split('Input JSON:\n', 1)[1])
    assert set(payload['inputs']) == {'topic', 'request', 'manuscript_lines'}
    for line in payload['inputs']['manuscript_lines'].splitlines():
        number, text = line.split(': ', 1)
        assert inputs['manuscript'].splitlines()[int(number) - 1] == text
    assert payload['targets'] == targets
    assert 'analysis-only-marker' not in prompt and 'figure-only-marker' not in prompt
    assert inputs == before


def test_long_manuscript_quote_repair_only_sends_related_source(example):
    manuscript = '# 背景\n' + '\n\n'.join('无关背景材料。' * 25 for _ in range(300))
    manuscript += '\n\n## 方法\n比较两种证据组织方法。\n\n## 附录\n不应发送的附录。'
    inputs = {**example['input'], 'manuscript': manuscript}
    issue = dict(example['model_response']['issues'][0], quote='比较三种证据组织方法。')
    prompt, allowed = peer_review.repair_request(inputs, {'issues.0': {
        'value': issue, 'errors': ['quote not found']}})
    payload = json.loads(prompt.split('Input JSON:\n', 1)[1])
    assert '比较两种证据组织方法。' in payload['inputs']['manuscript_lines']
    assert '无关背景材料' not in prompt and '不应发送的附录' not in prompt
    assert len(manuscript) > peer_review.MAX_REPAIR_CHARS
    assert allowed
    assert len(prompt) < peer_review.MAX_REPAIR_CHARS


@pytest.mark.parametrize('quote', [
    None, {'start_line': 1, 'end_line': 3}, {'start_line': 2, 'end_line': 4}, '编造的引用'])
def test_local_repair_cannot_evade_source_checks(example, quote):
    issue = example['model_response']['issues'][0]
    target = {'value': dict(issue, quote='未匹配的原文'), 'errors': ['quote not found']}
    with pytest.raises(ValueError):
        peer_review.apply_repair(example['model_response'], {'issues.0': target},
                                 peer_review.ReviewRepair(replacements={'issues.0': dict(issue, quote=quote)}),
                                 example['input']['manuscript'], allowed_lines={1, 3, 5})


def test_format_repair_preserves_original_quote(example):
    issue = example['model_response']['issues'][0]
    target = {'value': dict(issue, severity='invalid'),
              'errors': [{'field': ['issues', 0, 'severity'], 'message': 'invalid severity'}]}
    result = peer_review.apply_repair(
        example['model_response'], {'issues.0': target}, peer_review.ReviewRepair(replacements={'issues.0': issue}),
        example['input']['manuscript'], allowed_lines=set())
    assert result.issues[0].model_dump() == issue


def test_repair_runs_separate_batches_and_preserves_valid_issues(example, monkeypatch):
    candidate = copy.deepcopy(example['model_response'])
    issue = candidate['issues'][0]
    candidate['issues'] = [copy.deepcopy(issue), dict(issue, severity='invalid'),
                           dict(issue, quote='比较三种证据组织方法。')]
    before = copy.deepcopy(candidate)
    calls = []

    def repair(_reviewer, prompt, schema, trace_id):
        payload = json.loads(prompt.split('Input JSON:\n')[1])
        calls.append(payload)
        key, = payload['targets']
        value = dict(issue)
        if payload['quote_mode'] == 'source_lines':
            assert 'manuscript_lines' in payload['inputs']
            value['quote'] = {'start_line': 3, 'end_line': 3}
        else:
            assert 'manuscript_lines' not in payload['inputs']
        return {'replacements': {key: value}}

    monkeypatch.setattr(peer_review, 'call_once', repair)
    targets = peer_review.repair_targets(candidate, example['input']['manuscript'])
    result = peer_review.repair_result(None, example['input'], candidate, targets, 'offline')
    assert len(calls) == 2 and all(len(p['targets']) == 1 for p in calls)
    assert [i.model_dump() for i in result.issues] == [issue] * 3
    assert candidate == before
    peer_review.build_output(result, example['input'])


def test_unverifiable_issue_is_disclosed_and_excluded_from_auto_revision(example, monkeypatch):
    candidate = copy.deepcopy(example['model_response'])
    candidate['issues'].append(dict(candidate['issues'][0], location='不存在的章节', quote='xyzzzz'))
    before = copy.deepcopy(candidate)
    monkeypatch.setattr(peer_review, 'call_once', lambda *a: pytest.fail('No source, no model call'))
    result = peer_review.repair_result(
        None, example['input'], candidate,
        peer_review.repair_targets(candidate, example['input']['manuscript']), 'offline')
    output = peer_review.build_output(result, example['input'])
    assert len(output['review_data']['issues']) == 1
    assert '待核验意见' in output['review'] and 'xyzzzz' in output['review']
    assert output['review_data']['issues'][0]['quote'] == candidate['issues'][0]['quote']
    assert candidate == before


def test_bad_quote_repair_keeps_valid_review_and_cannot_publish_fake_quote(example, monkeypatch):
    candidate = copy.deepcopy(example['model_response'])
    issue = candidate['issues'][0]
    candidate['issues'].append(dict(issue, quote='比较三种证据组织方法。'))
    monkeypatch.setattr(peer_review, 'call_once', lambda *a: {
        'replacements': {'issues.1': dict(issue, quote=None)}})
    result = peer_review.repair_result(
        None, example['input'], candidate,
        peer_review.repair_targets(candidate, example['input']['manuscript']), 'offline')
    assert len(result.issues) == 1
    assert any('不能将需要核验的原文引用改为 null' in text for text in result.limitations)


def test_quote_repair_cannot_change_supported_judgment(example):
    issue = example['model_response']['issues'][0]
    target = {'value': dict(issue, quote='比较三种证据组织方法。'), 'errors': ['quote not found']}
    replacement = dict(issue, problem='擅自替换原来的判断', quote={'start_line': 3, 'end_line': 3})
    with pytest.raises(ValueError, match='已经有效的字段'):
        peer_review.apply_repair(example['model_response'], {'issues.0': target},
                                 peer_review.ReviewRepair(replacements={'issues.0': replacement}),
                                 example['input']['manuscript'], {3})


def test_full_review_repairs_long_manuscript_in_bounded_batches(example, monkeypatch):
    issue = example['model_response']['issues'][0]
    manuscript = '# 背景\n' + ('无关内容。' * 100 + '\n\n') * 150
    manuscript += '\n## 方法\n' + issue['quote'] + '\n'
    line_number = manuscript.splitlines().index(issue['quote']) + 1
    inputs = {**example['input'], 'manuscript': manuscript}
    candidate = copy.deepcopy(example['model_response'])
    candidate['issues'] = [copy.deepcopy(issue)] + [dict(issue, quote='比较三种证据组织方法。') for _ in range(6)]
    before = copy.deepcopy(candidate)
    calls = []

    def respond(_reviewer, prompt, schema, trace_id):
        calls.append(schema)
        if schema is peer_review.ReviewResult:
            return copy.deepcopy(candidate)
        assert len(prompt) < peer_review.MAX_REPAIR_CHARS
        payload = json.loads(prompt.split('Input JSON:\n')[1])
        assert '无关内容' not in prompt
        key, = payload['targets']
        return {'replacements': {key: dict(issue, quote={'start_line': line_number, 'end_line': line_number})}}

    monkeypatch.setattr(peer_review, 'AutoModel', lambda **kw: object())
    monkeypatch.setattr(peer_review, 'WriterToolBase', lambda **kw: object())
    monkeypatch.setattr(peer_review, 'call_once', respond)
    output = peer_review.run(inputs)
    assert calls == [peer_review.ReviewResult] + [peer_review.ReviewRepair] * 6
    assert len(output['review_data']['issues']) == 7
    assert all(item['quote'] == issue['quote'] for item in output['review_data']['issues'])
    assert output['review_data']['manuscript_sha256'] == peer_review.text_digest(manuscript)
    assert candidate == before
    api.contracts.validate_output('peer_review', output)


def test_parse_failure_logs_metadata_without_response_content(caplog, monkeypatch):
    import logging
    monkeypatch.setattr(peer_review, 'LOG', logging.getLogger(__name__))
    from unittest.mock import Mock
    reviewer = Mock()
    reviewer._build_structured_llm.return_value = lambda prompt: '{"summary":"PRIVATE_MANUSCRIPT",'
    with pytest.raises(ValueError, match='无法解析的 JSON'):
        peer_review.call_once(reviewer, 'PRIVATE_PROMPT', peer_review.ReviewResult)
    assert 'paper_review parse_failure' in caplog.text
    assert 'position' in caplog.text and 'sha256' in caplog.text
    assert 'PRIVATE_MANUSCRIPT' not in caplog.text
    assert 'PRIVATE_PROMPT' not in caplog.text


def test_model_transport_failure_records_stage_without_payload(monkeypatch, caplog):
    import logging
    from unittest.mock import Mock
    monkeypatch.setattr(peer_review, 'LOG', logging.getLogger(__name__))
    caplog.set_level(logging.INFO)
    model = Mock(side_effect=TimeoutError('PRIVATE_PROVIDER_DETAIL'))
    reviewer = Mock()
    reviewer._build_structured_llm.return_value = model
    with pytest.raises(TimeoutError):
        peer_review.call_once(reviewer, 'PRIVATE_PROMPT', peer_review.ReviewResult, 'review-test')
    events = [json.loads(record.message.split(' ', 2)[2]) for record in caplog.records]
    assert any(event.get('stage') == 'model_response' and event.get('cause_types') == ['TimeoutError']
               and event['trace_id'] == 'review-test' for event in events)
    assert not any(event.get('stage') == 'json_parse' for event in events)
    assert 'PRIVATE_' not in caplog.text


def test_silent_review_stream_distinguishes_runtime_and_content(monkeypatch, caplog):
    import logging
    from unittest.mock import Mock
    monkeypatch.setattr(peer_review, 'LOG', logging.getLogger(__name__))
    caplog.set_level(logging.INFO)
    reviewer = Mock()

    def build(_prompt, *, stream_output, apply_formatter):
        assert apply_formatter is False
        sink = stream_output['_stream_sink']

        def model(_input):
            sink({'tag': 'runtime_event', 'runtime_event': {
                'type': 'model_call_started', 'data': {'model_call_id': 'provider-test', 'secret': 'PRIVATE'}}})
            sink({'tag': 'think', 'delta': 'PRIVATE_REASONING'})
            sink({'tag': 'text', 'delta': 'PRIVATE_CONTENT'})
            return '{"summary":"ok"}'
        return model

    reviewer._build_structured_llm.side_effect = build
    assert peer_review.call_once(reviewer, 'PRIVATE_PROMPT', peer_review.ReviewResult) == {'summary': 'ok'}
    assert 'model_runtime' in caplog.text and 'provider-test' in caplog.text
    assert caplog.text.count('model_first_delta') == 1
    assert '"delta_type": "think"' in caplog.text
    assert 'PRIVATE' not in caplog.text


@pytest.mark.parametrize('content', ['{"summary":"ok"}', '```json\n{"summary":"ok"}\n```'])
def test_parse_review_reuses_writer_thinking_cleanup(content):
    assert peer_review.parse_response('<think>not JSON { PRIVATE_REASONING</think>\n' + content) == {'summary': 'ok'}


def test_unclosed_thinking_or_duplicate_json_is_still_rejected():
    with pytest.raises(ValueError):
        peer_review.parse_response('<think>unfinished {"summary":"ok"}')
    with pytest.raises(ValueError, match='重复字段'):
        peer_review.parse_response('<think>reasoning</think>{"summary":"a","summary":"b"}')


@pytest.mark.parametrize('verdict', ['verified', 'conflict', 'insufficient'])
def test_review_runs_independent_fulltext_reading_and_rerender_preserves_it(monkeypatch, verdict):
    inputs = {'topic': 'T', 'manuscript': '# T\n文献[P1]指出仅受控样本更快。',
              'papers': [{'id': 'P1', 'title': 'Study', 'content_type': 'full_text',
                          'text': '受控样本更快。其他环境尚不确定。'}]}
    candidate = dict(summary='核对文献。', issues=[], limitations=[], fact_claims=[dict(
        category='literature', location='T', quote='文献[P1]指出仅受控样本更快。', asserted_text='指出',
        operation='citation_support', sources=[{'path': '/papers/0/text', 'quote': '受控样本更快。'}])])
    calls = []

    def response(_reviewer, prompt, schema, _trace_id):
        calls.append(schema)
        if schema is peer_review.ReviewResult:
            return candidate
        assert schema is peer_review.LiteratureReadings
        assert '其他环境尚不确定' in prompt
        return {'readings': [{'claim_index': 0, 'status': verdict, 'reason': '仅支持受控样本；没有推广到全部环境。',
                              'passages': [{'path': '/papers/0/text', 'quote': '受控样本更快。'}]}]}

    monkeypatch.setattr(peer_review, 'AutoModel', lambda **kw: object())
    monkeypatch.setattr(peer_review, 'WriterToolBase', lambda **kw: object())
    monkeypatch.setattr(peer_review, 'call_once', response)
    output = peer_review.run(inputs)
    assert calls == [peer_review.ReviewResult, peer_review.LiteratureReadings]
    assert '文献全文' in output['review']
    assert '未提供文献证据材料' not in output['review']
    group = next(group for group in output['review_data']['fact_checks'] if group['category'] == 'literature')
    assert group['status'] == verdict
    assert group['checks'][0]['verification_method'] == 'semantic_read'
    result = peer_review.ReviewResult.model_validate(candidate)
    result.limitations.append('外部材料仍待补。')
    rerender = peer_review.build_output(result, inputs, fact_checks=output['review_data']['fact_checks'])
    new = next(group for group in rerender['review_data']['fact_checks'] if group['category'] == 'literature')
    assert new == group
    assert '外部材料仍待补' in rerender['review']
    assert len(calls) == 2
    api.contracts.validate_output('peer_review', rerender)


def test_invalid_semantic_passage_and_failed_reader_keep_evidence_insufficient(monkeypatch):
    result = peer_review.ReviewResult(summary='核对', issues=[], limitations=[], fact_claims=[dict(
        category='literature', location='T', quote='文献[P1]支持结论。', asserted_text='支持', operation='citation_support',
        sources=[{'path': '/papers/0/text', 'quote': '受控样本。'}])])
    inputs = {'topic': 'T', 'manuscript': result.fact_claims[0].quote,
              'papers': [{'content_type': 'full_text', 'text': '受控样本。'}]}
    monkeypatch.setattr(peer_review, 'call_once', lambda *args: {'readings': [{
        'claim_index': 0, 'status': 'verified', 'reason': '假定支持。',
        'passages': [{'path': '/papers/0/text', 'quote': '不存在的原文。'}]}]})
    readings = peer_review.read_literature(None, result, inputs, 'test')
    output = peer_review.build_output(result, inputs, readings=readings)
    group = next(g for g in output['review_data']['fact_checks'] if g['category'] == 'literature')
    assert group['status'] == 'insufficient'

    def failed(*args):
        raise TimeoutError('reader unavailable')

    monkeypatch.setattr(peer_review, 'call_once', failed)
    assert peer_review.read_literature(None, result, inputs, 'test') == []
    assert any('未完成' in text for text in result.limitations)


@pytest.mark.parametrize('paper,label,limitation', [
    ({'abstract': 'Only an abstract.'}, '文献摘要', '仅提供摘要'),
    ({'text': 'Abstract text.', 'content_type': 'abstract'}, '文献摘要', '仅提供摘要'),
    ({'text': 'Partial text.', 'content_type': 'full_text', 'truncated': True}, '文献文本（节选或覆盖不明）', '截断'),
    ({'text': 'Unclear coverage.'}, '文献文本（节选或覆盖不明）', '覆盖不明'),
    ({'title': 'Bibliography only'}, '文献题录', '仅有题录'),
])
def test_report_scope_distinguishes_abstract_excerpt_and_bibliography(paper, label, limitation):
    result = peer_review.ReviewResult(summary='检查已提供材料。', issues=[], limitations=[])
    inputs = {'topic': 'T', 'manuscript': '# T\n引用[P1]。', 'papers': [{'id': 'P1', **paper}]}
    report = peer_review.render_review(result, inputs)
    assert label in report and limitation in report
    assert '本次使用：稿件、文献全文' not in report
    assert '未提供文献证据材料' not in report


def test_single_complete_json_with_plain_trailing_comment_is_unambiguous(example):
    candidate = example['model_response']
    assert peer_review.parse_response(json.dumps(candidate) + '\n以上为审稿结果。') == candidate
    with pytest.raises(ValueError):
        peer_review.parse_response(json.dumps(candidate) + json.dumps(candidate))


def test_extra_data_structural_repair_preserves_every_scalar_and_records_original_failure(example, monkeypatch, caplog):
    import logging
    from unittest.mock import Mock
    monkeypatch.setattr(peer_review, 'LOG', logging.getLogger(__name__))
    caplog.set_level(logging.INFO)
    candidate = example['model_response']
    correct = json.dumps(candidate, ensure_ascii=False)
    # Model closed its outer object early; all literal content remains present.
    damaged = correct.replace(', "limitations":', '}, "limitations":', 1)
    reviewer = Mock()
    model = Mock(side_effect=[damaged, correct])
    reviewer._build_structured_llm.return_value = model
    assert peer_review.call_once(reviewer, 'input', peer_review.ReviewResult) == candidate
    assert model.call_count == 2
    assert 'parse_failure' in caplog.text and 'structure_repair_accepted' in caplog.text
    changed = dict(candidate, summary='A new scientific judgment is forbidden')
    reviewer._build_structured_llm.return_value = Mock(side_effect=[damaged, json.dumps(changed)])
    with pytest.raises(ValueError, match='无法解析的 JSON'):
        peer_review.call_once(reviewer, 'input', peer_review.ReviewResult)
    assert 'structure_repair_rejected' in caplog.text


def test_review_scope_keeps_research_notes_separate_from_literature_fulltext():
    result = peer_review.ReviewResult(summary='检查', issues=[], limitations=[])
    supplied, limitations = peer_review.review_scope(result, {'writing_materials': '历史素材，非论文全文。'})
    assert '研究素材（非文献全文）' in supplied
    assert any('未核验外部引用' in item for item in limitations)


def _contract_candidate():
    valid_quote = 'VALID_SENTINEL记录为19。'
    change_quote = '总token从31,576增到34,840，增加3,264。'
    row = '| 总token | 31,576 | 34,840 |'
    valid = dict(category='numbers', location='T', quote=valid_quote, asserted_text='19', operation='value',
                 sources=[{'path': '/analysis/value'}])
    invalid = dict(category='numbers', location='T', quote=change_quote, asserted_text='增加3,264',
                   operation='difference', sources=[dict(path='/evidence', quote=row, value_text=v)
                                                    for v in ('31,576', '34,840')])
    candidate = dict(summary='审查', issues=[], limitations=[], fact_claims=[valid, invalid])
    inputs = dict(topic='T', manuscript=valid_quote + '\n' + change_quote,
                  analysis={'value': 19}, evidence=row)
    return candidate, inputs


def test_generation_contract_repair_sends_only_invalid_items_and_preserves_valid_sources(monkeypatch):
    from unittest.mock import Mock
    candidate, inputs = _contract_candidate()
    before = copy.deepcopy(candidate)
    targets = peer_review.repair_targets(candidate, inputs['manuscript'], inputs)
    assert targets['fact_claims']['indices'] == [1]
    prompt = peer_review.repair_prompt(inputs, targets)
    assert 'VALID_SENTINEL' not in prompt
    replacement = dict(candidate['fact_claims'][1], asserted_text='3,264')
    call = Mock(return_value={'replacements': {'fact_claims': [replacement]}})
    monkeypatch.setattr(peer_review, 'call_once', call)
    result = peer_review.repair_result(None, inputs, candidate, targets, 'T')
    assert call.call_count == 1
    assert candidate == before
    assert result.fact_claims[0].model_dump(exclude_defaults=True) == peer_review.FactClaim.model_validate(
        before['fact_claims'][0]).model_dump(exclude_defaults=True)
    output = peer_review.build_output(result, inputs)
    assert all(c['status'] == 'verified' for c in output['review_data']['fact_checks'][0]['checks'])
    assert output['review_data']['issues'] == []


@pytest.mark.parametrize('failure', ['transport', 'unchanged', 'new_source', 'valid_field_changed'])
def test_one_failed_fact_format_repair_keeps_unverified_item_and_still_delivers(monkeypatch, failure):
    candidate, inputs = _contract_candidate()
    replacement = dict(candidate['fact_claims'][1], asserted_text='3,264')
    if failure == 'unchanged':
        replacement = candidate['fact_claims'][1]
    elif failure == 'new_source':
        replacement = copy.deepcopy(replacement)
        replacement['sources'][0]['path'] = '/analysis/invented'
    elif failure == 'valid_field_changed':
        replacement = dict(replacement, quote='另一个稿件版本')
    calls = []

    def respond(*args):
        calls.append(args)
        if failure == 'transport':
            raise TimeoutError('one bounded format attempt failed')
        return {'replacements': {'fact_claims': [replacement]}}

    monkeypatch.setattr(peer_review, 'call_once', respond)
    targets = peer_review.repair_targets(candidate, inputs['manuscript'], inputs)
    result = peer_review.repair_result(None, inputs, candidate, targets, 'T')
    output = peer_review.build_output(result, inputs)
    assert len(calls) == 1
    assert result.fact_claims[1].asserted_text == candidate['fact_claims'][1]['asserted_text']
    assert any(c['status'] == 'insufficient' for c in output['review_data']['fact_checks'][0]['checks'])
    assert any('格式修复未完成' in item for item in output['review_data']['limitations'])
    assert output['review_data']['fact_checks'][0]['checks'][0]['status'] == 'verified'


def test_arity_category_and_scalar_state_quotation_are_generation_errors_without_breaking_saved_reader(monkeypatch):
    candidate, inputs = _contract_candidate()
    quote = '数据来自实测。'
    inputs.update(manuscript=quote, result_meta={'data_origin': 'synthetic'})
    fact = dict(category='data_status', location='T', quote=quote, asserted_text='实测', operation='status',
                sources=[dict(path='/result_meta/data_origin', quote='data_origin="synthetic"')])
    candidate['fact_claims'] = [fact]
    targets = peer_review.repair_targets(candidate, quote, inputs)
    assert targets['fact_claims']['source_snippets'] == [[{'path': '/result_meta/data_origin', 'text': 'synthetic'}]]
    replacement = copy.deepcopy(fact)
    replacement['sources'][0]['quote'] = 'synthetic'
    monkeypatch.setattr(peer_review, 'call_once', lambda *args: {'replacements': {'fact_claims': [replacement]}})
    result = peer_review.repair_result(None, inputs, candidate, targets, 'T')
    output = peer_review.build_output(result, inputs)
    state = next(g for g in output['review_data']['fact_checks'] if g['category'] == 'data_status')
    assert state['checks'][0]['status'] == 'conflict'
    malformed = peer_review.FactClaim.model_validate(dict(fact, category='numbers', operation='direction'))
    errors, _ = peer_review.fact_claim_errors(malformed)
    assert {'category', 'operation', 'sources'} <= set(errors)
    # Compatibility: older saved selections remain readable; only generation is repaired.
    from lazymind.document_tools.paper.review.schema import SavedReview, text_digest
    saved = SavedReview(summary='历史审稿', issues=[], limitations=[], fact_claims=[malformed],
                        manuscript_sha256=text_digest(quote), review_sha256=text_digest('历史审稿'))
    assert saved.fact_claims[0].category == 'numbers'


def test_missing_source_units_are_not_treated_as_format_errors_or_invented():
    item = peer_review.FactClaim(category='numbers', location='T', quote='耗时10秒。', asserted_text='10',
                                 operation='value', sources=[{'path': '/analysis/value'}])
    errors, _ = peer_review.fact_claim_errors(item, {'analysis': {'value': 10}})
    assert errors == {}
    result = peer_review.ReviewResult(summary='T', issues=[], limitations=[], fact_claims=[item])
    output = peer_review.build_output(result, dict(topic='T', manuscript=item.quote, analysis={'value': 10}))
    assert output['review_data']['fact_checks'][0]['checks'][0]['status'] == 'insufficient'


@pytest.mark.parametrize('failure', ['unchanged', 'adds_invented_figure', 'transport'])
def test_schema_invalid_missing_figure_proposal_cannot_block_valid_facts_or_become_conflict(monkeypatch, failure):
    candidate, inputs = _contract_candidate()
    candidate['fact_claims'] = candidate['fact_claims'][:1]
    invalid = dict(category='figure', location='缺失图', quote='', asserted_text='图1',
                   operation='figure_value', sources=[])
    candidate['fact_claims'].append(invalid)
    targets = peer_review.repair_targets(candidate, inputs['manuscript'], inputs)
    assert targets['fact_claims']['indices'] == [1]
    calls = []

    def respond(*args):
        calls.append(args)
        if failure == 'transport':
            raise TimeoutError('one bounded attempt')
        replacement = invalid
        if failure == 'adds_invented_figure':
            replacement = dict(invalid, quote='图1', sources=[dict(path='/charts/0/svg')])
        return {'replacements': {'fact_claims': [replacement]}}

    monkeypatch.setattr(peer_review, 'call_once', respond)
    result = peer_review.repair_result(None, inputs, candidate, targets, 'T')
    output = peer_review.build_output(result, inputs)
    assert len(calls) == 1
    assert len(result.fact_claims) == 1
    notes = result.limitations
    assert any('待核验事实提案' in note and '"sources": []' in note for note in notes)
    figure = next(g for g in output['review_data']['fact_checks'] if g['category'] == 'figure')
    assert figure['status'] == 'insufficient' and figure['checks'] == []
    assert not any(i['disposition'] == 'fact' for i in output['review_data']['issues'])


def test_real_unverified_numerical_correction_cannot_enter_automatic_revision(monkeypatch):
    from decimal import Decimal
    from lazymind.document_tools.paper.writing import revise_document
    fixture = json.loads((EXAMPLE.parent / 'fact_unverified_correction.json').read_text())
    table = fixture['table']
    rows = [dict(zip(table['columns'], row)) for row in table['rows']]
    selected = [r for r in rows if r['method'] == 'structured_local' and r['binding'] == 'with_binding']
    assert len(rows) == 96 and len(selected) == 24
    mean = sum(Decimal(str(r['target_compliance'])) for r in selected) / len(selected)
    assert mean.quantize(Decimal('0.000001')) == Decimal('0.836833')
    # The fixed short review sample has no H2; wrap only for the revision API's structural contract.
    manuscript = fixture['manuscript'].replace('\n\n', '\n\n## 结果\n\n', 1)
    inputs = dict(topic='论文写作 Agent 的真实评估', manuscript=manuscript,
                  evidence=json.dumps({'experiment': {'results': table}}, ensure_ascii=False),
                  result_meta=fixture['result_meta'])
    result = peer_review.ReviewResult(
        summary='审查', issues=[fixture['issue']], limitations=[], fact_claims=fixture['fact_claims'])
    before = result.model_dump()
    review = peer_review.build_output(result, inputs)
    issue = review['review_data']['issues'][0]
    assert issue['disposition'] == 'unverified' and issue['action_type'] == 'needs_material'
    assert all(c['status'] == 'insufficient' for c in review['review_data']['fact_checks'][0]['checks'])
    notes = [n for n in review['review_data']['limitations'] if n.startswith('未核实事实更正')]
    assert json.loads(notes[0].split('：', 1)[1])['original_issue'] == fixture['issue']
    assert result.model_dump() == before
    monkeypatch.setattr(revise_document, 'WriterRevisionToolkit', lambda: pytest.fail('Pending R3 reached Writer'))
    monkeypatch.setattr(peer_review, 'run', lambda current: peer_review.build_output(result, current))
    output = api.run_algorithm('revise_document', {
        **inputs, 'review': review['review'], 'review_data': review['review_data'],
        'selected_issue_ids': [issue['id']], 'request': '只修改该条意见'})
    assert output['manuscript'] == manuscript
    assert any(item['action_type'] == 'needs_material' for item in output['review_data']['issues'])


@pytest.mark.parametrize('binding', ['missing', 'verified', 'insufficient'])
def test_unverified_numerical_correction_never_inherits_verification_from_the_manuscript(binding):
    quote = '准确率为93.33%。'
    issue = dict(severity='major', category='analysis', location='结果', quote=quote,
                 problem='准确率应为100%，现数字错误。', suggestion='改为100%。', action_type='text_revision')
    facts = [] if binding == 'missing' else [dict(
        category='numbers', location='结果', quote=quote, asserted_text='93.33%', operation='percentage',
        sources=[{'path': '/analysis/correct'},
                 {'path': '/analysis/n' if binding == 'verified' else '/analysis/missing'}])]
    output = peer_review.build_output(peer_review.ReviewResult(
        summary='审查', issues=[issue], limitations=[], fact_claims=facts),
        dict(topic='论文', manuscript=quote, analysis={'correct': 14, 'n': 15}))
    saved = output['review_data']['issues'][0]
    assert saved['disposition'] == 'unverified' and saved['action_type'] == 'needs_material'
    assert any('original_issue' in n for n in output['review_data']['limitations'])


@pytest.mark.parametrize('kind', ['fact', 'advice'])
def test_checked_conflict_and_pure_advice_still_reach_atomic_revision(monkeypatch, kind):
    from types import SimpleNamespace
    from unittest.mock import Mock
    from lazymind.document_tools.paper.writing import revise_document
    from lazymind.document_tools.toolkits import WriterRevisionToolkit
    quote = '准确率为100%。' if kind == 'fact' else '表达很冗长。'
    new = '准确率为93.33%。' if kind == 'fact' else '表达简洁。'
    body = '# 论文\n\n## 结果\n' + quote
    issue = dict(severity='minor', category='analysis' if kind == 'fact' else 'language', location='结果',
                 quote=quote, problem='准确率需要更正。' if kind == 'fact' else '表达可精简。',
                 suggestion='更正准确率。' if kind == 'fact' else '精简表达。', action_type='text_revision')
    facts = [dict(category='numbers', location='结果', quote=quote, asserted_text='100%',
                  operation='percentage', sources=[{'path': '/evidence/correct'}, {'path': '/evidence/n'}])]
    inputs = dict(topic='论文', manuscript=body, evidence=json.dumps({'correct': 14, 'n': 15}))
    review = peer_review.build_output(peer_review.ReviewResult(
        summary='审查', issues=[issue], limitations=[], fact_claims=facts if kind == 'fact' else []), inputs)
    saved = review['review_data']['issues'][0]
    assert saved['disposition'] == kind and saved['action_type'] == 'text_revision'
    operation = dict(content_ref={'heading_path': ['论文', '结果']}, modify_type='update', target_scope='fragment',
                     locator_text=quote, instruction='采用已选修订', meta={'issue_ids': [saved['id']]})
    shared = WriterRevisionToolkit()
    editor = SimpleNamespace(
        locate_revision_target=Mock(return_value='{"target_title":false,"targets":[]}'),
        generate_modify_plan=Mock(return_value=json.dumps({'scope': 'document', 'instructions': [operation]})),
        generate_string_replace_set=Mock(return_value=json.dumps({'replacements': [dict(
            content_ref={'heading_path': ['论文', '结果']}, old_string=quote, new_string=new)]})),
        apply_string_replace=Mock(wraps=shared.apply_string_replace))
    monkeypatch.setattr(revise_document, 'WriterRevisionToolkit', lambda: editor)
    monkeypatch.setattr(peer_review, 'run', lambda current: peer_review.build_output(
        peer_review.ReviewResult(summary='修订后检查', issues=[], limitations=[]), current))
    output = api.run_algorithm('revise_document', {
        **inputs, 'review': review['review'], 'review_data': review['review_data'],
        'selected_issue_ids': [saved['id']], 'request': '只修改所选意见'})
    assert output['manuscript'] == body.replace(quote, new)
    assert editor.apply_string_replace.call_count == 1


@pytest.mark.parametrize('ids,message', [
    (['R2'], None), (['R1', 'R3'], 'selected issue_ids'),
])
def test_revision_plan_allows_partial_coverage_and_stays_within_selected_ids(ids, message):
    from lazymind.document_tools.paper.writing import revise_document
    plan = revise_document.MarkdownModifyPlan.model_validate(dict(scope='document', instructions=[dict(
        content_ref={'heading_path': ['论文']}, modify_type='update', target_scope='section',
        instruction='不能借合法修改执行待核纠错', meta={'issue_ids': ids})]))
    issues = [dict(id='R1', action_type='text_revision'), dict(id='R2', action_type='needs_material')]
    if message:
        with pytest.raises(ValueError, match=message):
            revise_document._validate_review_plan(plan, issues)
    else:
        revise_document._validate_review_plan(plan, issues)
