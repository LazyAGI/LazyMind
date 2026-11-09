"""Offline regression checks for F6's independently computed fact findings."""
import copy

import pytest

from lazymind.document_tools.paper.fact_checks import check_facts, review_issues
from lazymind.document_tools.paper.review.schema import LiteratureReading, ReviewResult, SavedReview, text_digest


def claim(category='numbers', quote='结果为 93.33%。', asserted='93.33', operation='percentage', sources=None):
    return dict(category=category, location='结果', quote=quote, asserted_text=asserted, operation=operation,
                sources=sources or [{'path': '/analysis/correct'}, {'path': '/analysis/n'}])


def evaluate(item, inputs=None):
    inputs = inputs or {'manuscript': item['quote'], 'analysis': {'correct': 14, 'n': 15}}
    result = ReviewResult(summary='审查', issues=[], limitations=[], fact_claims=[item])
    coverage = check_facts(result, inputs)
    return result, coverage, next(group for group in coverage if group.category == item['category']).checks[0]


def test_fraction_recalculation_preserves_input_and_reports_all_five_categories():
    inputs = {'manuscript': '结果为 93.33%。', 'analysis': {'correct': 14, 'n': 15}}
    before = copy.deepcopy(inputs)
    _, coverage, checked = evaluate(claim(), inputs)
    assert checked.status == 'verified'
    assert len(coverage) == 5
    assert checked.evidence_locations == ['manuscript:L1', '/analysis/correct', '/analysis/n']
    assert inputs == before


@pytest.mark.parametrize('asserted,expected', [('100', 'conflict'), ('93.33', 'verified')])
def test_ir_node_matching_uses_14_of_15_instead_of_scope_success_15_of_15(asserted, expected):
    _, _, checked = evaluate(claim(quote=f'IR目标节点匹配为 {asserted}%。', asserted=asserted))
    assert checked.status == expected
    assert '93.333' in checked.calculation


@pytest.mark.parametrize('operation,asserted,expected', [
    ('percentage_points', '29.23', 'conflict'), ('percentage_points', '11.4', 'verified'),
    ('relative_change', '29.23', 'verified'),
])
def test_percentage_points_and_relative_changes_differ(operation, asserted, expected):
    unit = '%' if operation == 'relative_change' else '个百分点'
    item = claim(quote=f'缓存命中变化为 {asserted}{unit}。', asserted=asserted, operation=operation,
                 sources=[{'path': '/analysis/after'}, {'path': '/analysis/before'}])
    _, _, checked = evaluate(item, {'manuscript': item['quote'], 'analysis': {'after': .504, 'before': .39}})
    assert checked.status == expected


@pytest.mark.parametrize('left,right,word', [
    (34840, 31576, '下降'), (137.286, 113.395, '低于'), (.8368333333333, .8042083333333, '低于')])
def test_token_increase_time_regression_and_baseline_manuscript_reversal(left, right, word):
    item = claim(category='comparison', quote='该值' + word + '基线。', asserted=word, operation='direction',
                 sources=[{'path': '/analysis/left'}, {'path': '/analysis/right'}])
    inputs = {'manuscript': item['quote'], 'analysis': {'left': left, 'right': right}}
    result, coverage, checked = evaluate(item, inputs)
    assert checked.status == 'conflict'
    issue, disposition = review_issues(result, coverage)[0]
    assert disposition == 'fact' and issue.action_type == 'text_revision'
    assert issue.quote == item['quote'] and 'higher' in issue.problem


def test_correct_direction_control_generates_no_factual_accusation():
    item = claim(category='comparison', quote='目标落实率高于基线。', asserted='高于', operation='direction',
                 sources=[{'path': '/analysis/left'}, {'path': '/analysis/right'}])
    inputs = {'manuscript': item['quote'], 'analysis': {'left': .8368333333333, 'right': .8042083333333}}
    result, coverage, checked = evaluate(item, inputs)
    assert checked.status == 'verified'
    assert review_issues(result, coverage) == []


@pytest.mark.parametrize('value', [None, 0, 'timeout', True])
def test_missing_timeout_boolean_and_zero_denominator_are_not_verified(value):
    item = claim()
    _, _, checked = evaluate(item, {'manuscript': item['quote'], 'analysis': {'correct': 14, 'n': value}})
    assert checked.status == 'insufficient'


def test_legacy_text_sources_require_exact_excerpts_and_whole_numeric_tokens():
    item = claim(quote='总token下降。', asserted='下降', category='comparison', operation='direction', sources=[
        {'path': '/evidence', 'quote': '总token 31,576 → 34,840', 'value_text': '34,840'},
        {'path': '/evidence', 'quote': '总token 31,576 → 34,840', 'value_text': '31,576'},
    ])
    inputs = {'manuscript': item['quote'], 'evidence': '历史总token 31,576 → 34,840，含失败。'}
    assert evaluate(item, inputs)[2].status == 'conflict'
    item['sources'][0]['value_text'] = '34'
    assert evaluate(item, inputs)[2].status == 'insufficient'
    item['sources'][0]['quote'] = '模型编造的总token 31,576 → 34,840'
    assert evaluate(item, inputs)[2].status == 'insufficient'


@pytest.mark.parametrize('path', ['/manuscript', '/analysis/nonexistent', '/fact_checks/0/status'])
def test_sources_cannot_be_the_manuscript_model_verdicts_or_missing_paths(path):
    item = claim(sources=[{'path': path}])
    assert evaluate(item)[2].status == 'insufficient'


def test_unsupported_source_and_asserted_quotations_cannot_be_fact_findings():
    item = claim(quote='结果为 93.33%。', asserted='3.33')
    assert evaluate(item)[2].status == 'insufficient'
    inputs = {'manuscript': '结果为 100%。', 'analysis': {'correct': 14, 'n': 15}}
    assert evaluate(claim(), inputs)[2].status == 'insufficient'


def test_repeated_excerpt_needs_unique_location_and_large_counts_keep_integer_precision():
    inputs = {'manuscript': '结果为 93.33%。\n结果为 93.33%。', 'analysis': {'correct': 14, 'n': 15}}
    assert evaluate(claim(), inputs)[2].status == 'insufficient'
    item = claim(quote='总量为 10000000000000000个。', asserted='10000000000000000', operation='value',
                 sources=[{'path': '/analysis/count'}])
    inputs = {'manuscript': item['quote'], 'analysis': {'count': 10000000000000001}}
    assert evaluate(item, inputs)[2].status == 'conflict'


@pytest.mark.parametrize('label,expected', [('实测', 'conflict'), ('模拟', 'verified')])
def test_explicit_simulation_label_cannot_become_real_measurement(label, expected):
    item = claim(category='data_status', quote=f'本实验使用{label}数据。', asserted=label, operation='status',
                 sources=[{'path': '/result_meta/data_origin', 'quote': 'synthetic'}])
    _, _, checked = evaluate(item, {'manuscript': item['quote'], 'result_meta': {'data_origin': 'synthetic'}})
    assert checked.status == expected


def test_execution_and_non_demo_flags_do_not_establish_measurements():
    item = claim(category='data_status', quote='本实验使用实测数据。', asserted='实测', operation='status',
                 sources=[{'path': '/analysis/executed'}])
    _, _, checked = evaluate(item, {'manuscript': item['quote'], 'analysis': {'executed': True, 'is_demo': False}})
    assert checked.status == 'insufficient'


@pytest.mark.parametrize('field', ['abstract', 'text'])
def test_abstract_or_full_text_does_not_mechanically_prove_semantic_citation_support(field):
    item = claim(category='literature', quote='文献[P1]证明所有任务更快。', asserted='证明',
                 operation='citation_support', sources=[{'path': f'/papers/0/{field}', 'quote': '研究局部编辑。'}])
    _, coverage, checked = evaluate(item, {'manuscript': item['quote'], 'papers': [{field: '研究局部编辑。'}]})
    assert checked.status == 'insufficient'
    assert next(group for group in coverage if group.category == 'literature').status == 'insufficient'
    assert '全文' in checked.calculation and '支持' in checked.calculation


@pytest.mark.parametrize('displayed,expected', [(24, 'conflict'), (19, 'verified')])
def test_conditional_figure_denominator_checks_actual_svg_text(displayed, expected):
    item = claim(category='figure', quote=f'图3计划正确子集 n={displayed}。', asserted=str(displayed),
                 operation='figure_value', sources=[{'path': '/analysis/conditional_n'},
                 {'path': '/charts/0/svg', 'quote': f'n={displayed}', 'value_text': str(displayed)}])
    inputs = {'manuscript': item['quote'], 'analysis': {'conditional_n': 19},
              'charts': [{'svg': f'<svg xmlns="http://www.w3.org/2000/svg"><text>n={displayed}</text></svg>'}]}
    _, _, checked = evaluate(item, inputs)
    assert checked.status == expected


def test_absent_chart_or_provenance_only_cannot_verify_rendered_values():
    item = claim(category='figure', quote='图3子集 n=24。', asserted='24', operation='figure_value',
                 sources=[{'path': '/analysis/n'}, {'path': '/charts/0/n'}])
    inputs = {'manuscript': item['quote'], 'analysis': {'n': 24}, 'charts': [{'n': 24}]}
    assert evaluate(item, inputs)[2].status == 'insufficient'
    inputs['charts'] = []
    assert evaluate(item, inputs)[2].status == 'insufficient'


def test_svg_coordinate_or_attribute_is_not_a_rendered_numeric_label():
    item = claim(category='figure', quote='图3子集 n=24。', asserted='24', operation='figure_value', sources=[
        {'path': '/analysis/n'}, {'path': '/charts/0/svg', 'quote': 'n=24', 'value_text': '24'}])
    inputs = {'manuscript': item['quote'], 'analysis': {'n': 24},
              'charts': [{'svg': '<svg><text data-note="n=24">Conditional correctness</text></svg>'}]}
    assert evaluate(item, inputs)[2].status == 'insufficient'
    inputs['charts'][0]['svg'] = '<svg><metadata>n=24</metadata><text>Conditional correctness</text></svg>'
    assert evaluate(item, inputs)[2].status == 'insufficient'


def test_suggestion_remains_advice_while_unresolved_numerical_allegations_stay_unverified():
    issue = dict(severity='major', category='language', location='全文', quote=None,
                 problem='句子过长。', suggestion='拆分长句。', action_type='text_revision')
    result = ReviewResult(summary='审查', issues=[issue, dict(issue, category='analysis')], limitations=[])
    coverage = check_facts(result, {'manuscript': '结果为 100%，实验见图1和文献[P1]，速度提高。'})
    assert {group.category for group in coverage} == {'numbers', 'comparison', 'literature', 'data_status', 'figure'}
    assert all(group.status == 'not_applicable' for group in coverage)
    assert [kind for _, kind in review_issues(result, coverage)] == ['advice', 'unverified']


def test_legacy_saved_review_remains_loadable_with_safe_defaults():
    review = SavedReview(summary='审查', issues=[], limitations=[],
                         manuscript_sha256=text_digest('M'), review_sha256=text_digest('R'))
    assert review.fact_claims == [] and review.fact_checks == []


@pytest.mark.parametrize('word,after,asserted,status', [
    ('下降', 19.25, '13.79', 'verified'), ('降幅', 19.25, '13.79', 'verified'),
    ('增加', 19.25, '13.79', 'conflict'), ('下降', 25.41, '13.79', 'conflict'),
])
def test_positive_change_magnitude_preserves_its_explicit_direction(word, after, asserted, status):
    item = claim(quote=f'耗时从22.33秒变为{after}秒，{word}{asserted}%。', asserted=asserted,
                 operation='relative_change', sources=[{'path': '/analysis/after'}, {'path': '/analysis/before'}])
    inputs = {'manuscript': item['quote'], 'analysis': {'before': 22.33, 'after': after, 'unit': 's'}}
    assert evaluate(item, inputs)[2].status == status


@pytest.mark.parametrize('source_value,source_unit,asserted,unit,status', [
    (10, 's', '10', '毫秒', 'conflict'), (10, 's', '10000', '毫秒', 'verified'),
    (10000, 'ms', '10', '秒', 'verified'), (10, 's', '10', '秒', 'verified'),
    (10, None, '10', '毫秒', 'insufficient'), (10, 's', '10', '', 'insufficient'),
    (10, 's', '10', '%', 'conflict'),
])
def test_numeric_values_verify_units_and_time_conversions(source_value, source_unit, asserted, unit, status):
    item = claim(quote=f'耗时为{asserted}{unit}。', asserted=asserted, operation='value',
                 sources=[{'path': '/analysis/value'}])
    inputs = {'manuscript': item['quote'], 'analysis': {'value': source_value, 'unit': source_unit}}
    assert evaluate(item, inputs)[2].status == status


def test_equal_magnitude_percentage_and_percentage_points_are_not_interchangeable():
    item = claim(quote='提高11.4%。', asserted='11.4', operation='percentage_points',
                 sources=[{'path': '/analysis/after'}, {'path': '/analysis/before'}])
    inputs = {'manuscript': item['quote'], 'analysis': {'after': .504, 'before': .39}}
    assert evaluate(item, inputs)[2].status == 'conflict'


@pytest.mark.parametrize('status', ['verified', 'conflict', 'insufficient'])
def test_independent_fulltext_reading_requires_bound_actual_passages(status):
    item = claim(category='literature', quote='文献[P1]指出方法A只在受控样本中更快。', asserted='指出',
                 operation='citation_support', sources=[{'path': '/papers/0/text', 'quote': '仅在受控样本中更快。'}])
    inputs = {'manuscript': item['quote'], 'papers': [{'content_type': 'full_text',
              'text': '方法A仅在受控样本中更快。其他环境尚不确定。'}]}
    result = ReviewResult(summary='审查', issues=[], limitations=[], fact_claims=[item])
    reading = LiteratureReading(claim_index=0, status=status, reason='具体范围核读结果。',
                                passages=[{'path': '/papers/0/text', 'quote': '方法A仅在受控样本中更快。'}])
    checked = next(group for group in check_facts(result, inputs, [reading]) if group.category == 'literature').checks[0]
    assert checked.status == status and checked.verification_method == 'semantic_read'
    assert '不是确定性证明' in checked.calculation
    reading.passages[0].quote = '源文不存在的支持证据。'
    checked = next(group for group in check_facts(result, inputs, [reading]) if group.category == 'literature').checks[0]
    assert checked.status == 'insufficient'


def test_abstract_truncation_and_unknown_coverage_cannot_gain_fulltext_verdict():
    item = claim(category='literature', quote='文献[P1]支持该结论。', asserted='支持', operation='citation_support',
                 sources=[{'path': '/papers/0/text', 'quote': '受控样本。'}])
    result = ReviewResult(summary='审查', issues=[], limitations=[], fact_claims=[item])
    reading = LiteratureReading(claim_index=0, status='verified', reason='声称核实。',
                                passages=[{'path': '/papers/0/text', 'quote': '受控样本。'}])
    for metadata in ({}, {'content_type': 'abstract'}, {'content_type': 'full_text', 'truncated': True}):
        inputs = {'manuscript': item['quote'], 'papers': [{'text': '受控样本。', **metadata}]}
        group = next(g for g in check_facts(result, inputs, [reading]) if g.category == 'literature')
        assert group.status == 'insufficient'


@pytest.mark.parametrize('reverse', [False, True])
@pytest.mark.parametrize('verb,expected', [('增加', 'verified'), ('下降', 'conflict')])
def test_change_operands_are_bound_to_manuscript_endpoints_not_model_order(reverse, verb, expected):
    quote = f'总token从31,576增至34,840，{verb}3,264。'
    row = '| 全部题目总token | 31,576 | 34,840 |'
    sources = [dict(path='/writing_materials', quote=row, value_text=value) for value in ('31,576', '34,840')]
    item = claim(quote=quote, asserted='3,264', operation='difference', sources=sources[::-1] if reverse else sources)
    result, coverage, checked = evaluate(item, {'manuscript': quote, 'writing_materials': row})
    assert checked.status == expected
    assert ('fact' in [kind for _, kind in review_issues(result, coverage)]) == (expected == 'conflict')
    assert '3264' in checked.calculation


def test_unbound_operands_do_not_turn_model_order_into_factual_error():
    item = claim(quote='增加10。', asserted='10', operation='difference',
                 sources=[{'path': '/analysis/a'}, {'path': '/analysis/b'}])
    checked = evaluate(item, {'manuscript': item['quote'], 'analysis': {'a': 10, 'b': 20}})[2]
    assert checked.status == 'insufficient' and '操作数' in checked.calculation


@pytest.mark.parametrize('token,source,status', [
    ('93.33%', '93.33%', 'verified'), ('10秒', '10000毫秒', 'verified'),
    ('14/15', '14/15', 'verified'), ('2/4', '1/2', 'conflict'),
    ('3.33%', '93.33%', 'insufficient'),
])
def test_complete_quantities_accept_percent_fraction_units_without_losing_denominators(token, source, status):
    operation = 'denominator' if '/' in token else 'value'
    quote = '对应结果为' + ('93.33%' if status == 'insufficient' else token) + '。'
    evidence = '对应结果为' + source
    item = claim(quote=quote, asserted=token, operation=operation,
                 sources=[dict(path='/evidence', quote=evidence, value_text=source)])
    assert evaluate(item, {'manuscript': quote, 'evidence': evidence})[2].status == status


@pytest.mark.parametrize('reverse', [False, True])
def test_complete_relative_and_percentage_point_quantities_bind_both_roles(reverse):
    quote = '缓存率从39.0%提高到50.4%，增加11.4个百分点，相对增加29.23%。'
    row = '缓存率从39.0%提高到50.4%'
    sources = [dict(path='/evidence', quote=row, value_text=t) for t in ('39.0%', '50.4%')]
    for operation, token in [('percentage_points', '11.4个百分点'), ('relative_change', '29.23%')]:
        item = claim(quote=quote, asserted=token, operation=operation, sources=sources[::-1] if reverse else sources)
        assert evaluate(item, {'manuscript': quote, 'evidence': row})[2].status == 'verified'


def test_raw_json_table_mean_uses_all_rows_and_explicit_group_and_metric_binding():
    import json
    quote = 'A组 with_binding 的范围外修改率均值为0.061125。'
    rows = [['A', 'with_binding', v] for v in [.125, .222, .120, 1.0] + [0.] * 20]
    table = {'columns': ['method', 'binding', 'out_of_scope_change'],
             'rows': rows + [['B', 'with_binding', .8]]}
    source = dict(path='/evidence/experiment/results', column='out_of_scope_change',
                  where={'method': 'A', 'binding': 'with_binding'})
    item = claim(quote=quote, asserted='0.061125', operation='mean', sources=[source])
    inputs = dict(manuscript=quote, evidence=json.dumps({'experiment': {'results': table}}),
                  result_meta={'metrics': [dict(column='out_of_scope_change', description='范围外修改率：比例', unit='比例')]})
    assert evaluate(item, inputs)[2].status == 'verified'
    table['rows'][2][2] = None
    inputs['evidence'] = json.dumps({'experiment': {'results': table}})
    assert evaluate(item, inputs)[2].status == 'insufficient'


def test_table_filters_and_record_count_do_not_establish_guessed_groups_or_independent_samples():
    table = {'columns': ['method', 'value'], 'rows': [['A', 1], ['A', 2], ['B', 1]]}
    source = dict(path='/analysis/table', column='value', where={'method': 'A'})
    for quote, token, status in [('A组 value有2条记录。', '2', 'verified'),
                                 ('A组 value有2名独立样本。', '2', 'insufficient'),
                                 ('甲组 value均值为1.5。', '1.5', 'insufficient')]:
        item = claim(quote=quote, asserted=token, operation='denominator' if token == '2' else 'mean',
                     sources=[source])
        checked = evaluate(item, dict(manuscript=quote, analysis={'table': table}))[2]
        assert checked.status == status


@pytest.mark.parametrize('label', ['数据来自模拟实验', 'synthetic data'])
def test_state_phrases_and_structured_origin_are_checked_but_negation_is_not_measured(label):
    item = claim(category='data_status', quote=label + '。', asserted=label, operation='status',
                 sources=[{'path': '/result_meta'}])
    checked = evaluate(item, dict(manuscript=item['quote'], result_meta={'data_origin': 'synthetic'}))[2]
    assert checked.status == 'verified'
    item.update(quote='本次不是实测数据。', asserted_text='实测')
    checked = evaluate(item, dict(manuscript=item['quote'], result_meta={'data_origin': 'measured'}))[2]
    assert checked.status == 'insufficient'


@pytest.mark.parametrize('asserted,status,kind', [('0.061125', 'verified', 'advice'), ('0.062125', 'conflict', 'fact')])
def test_decimal_precision_complaint_yields_only_to_independently_recomputed_mean(asserted, status, kind):
    from lazymind.document_tools.paper.peer_review import build_output
    quote = f'A组 value均值为{asserted}。'
    # Separate scalar paths retain duplicate zeros; no model may silently drop records.
    sources = [dict(path=f'/analysis/values/{i}') for i in range(24)]
    item = claim(quote=quote, asserted=asserted, operation='mean', sources=sources)
    issue = dict(severity='minor', category='evidence', location='结果', quote=quote,
                 problem='结果行只有三位小数，该六位均值超过精度，没有对应记录，无法核验。',
                 suggestion='补充新数据和汇总表。', action_type='needs_material')
    result = ReviewResult(summary='均值精度不足。', issues=[issue], limitations=[], fact_claims=[item])
    output = build_output(result, dict(topic='T', manuscript=quote, analysis={
        'values': [.125, .222, .120, 1.] + [0.] * 20}))
    checks = output['review_data']['fact_checks'][0]['checks']
    assert checks[0]['status'] == status
    assert kind in [i['disposition'] for i in output['review_data']['issues']]
    if status == 'verified':
        assert all(i['action_type'] != 'needs_material' for i in output['review_data']['issues'])
        assert '程序复核更正' in output['review_data']['summary']
    else:
        assert any(i['action_type'] == 'needs_material' for i in output['review_data']['issues'])


@pytest.mark.parametrize('extra', ['mean_missing', 'other_conflict', 'mixed_problem'])
def test_verified_mean_does_not_erase_unverified_or_mixed_fact_allegations(extra):
    from lazymind.document_tools.paper.peer_review import build_output
    quote = '结果均值为2，分母为3。'
    facts = [claim(quote=quote, asserted='2', operation='mean', sources=[{'path': '/analysis/a'}])]
    if extra == 'mean_missing':
        facts.append(claim(quote=quote, asserted='2', operation='mean', sources=[{'path': '/analysis/missing'}]))
    elif extra == 'other_conflict':
        facts.append(claim(quote=quote, asserted='3', operation='value', sources=[{'path': '/analysis/b'}]))
    problem = '均值小数精度超过原始记录，无法核验。'
    if extra == 'mixed_problem':
        problem += '而且分母和方向也错误。'
    issue = dict(severity='major', category='evidence', location='结果', quote=quote, problem=problem,
                 suggestion='补充分母与计算记录。', action_type='needs_material')
    result = ReviewResult(summary='待核对', issues=[issue], limitations=[], fact_claims=facts)
    output = build_output(result, dict(topic='T', manuscript=quote, analysis={'a': 2, 'b': 4}))
    saved = output['review_data']['issues']
    assert any(i['problem'] == problem and i['action_type'] == 'needs_material' for i in saved)


def test_fraction_denominator_cannot_be_inferred_from_a_ratio_scalar():
    item = claim(quote='正确率为1/2。', asserted='1/2', operation='denominator',
                 sources=[dict(path='/analysis/value')])
    checked = evaluate(item, dict(manuscript=item['quote'], analysis={'value': .5, 'unit': '比例'}))[2]
    assert checked.status == 'insufficient'


@pytest.mark.parametrize('predicate,after,before', [('增到', 34840, 31576), ('降到', 19.25, 22.33)])
@pytest.mark.parametrize('reverse', [False, True])
def test_actual_chinese_to_predicates_bind_operands_without_sign_sorting(predicate, after, before, reverse):
    unit = 'token' if after > 100 else '秒'
    quote = f'数值从{before}{unit}{predicate}{after}{unit}。'
    item = claim(category='comparison', quote=quote, asserted=predicate, operation='direction',
                 sources=[{'path': '/analysis/before'}, {'path': '/analysis/after'}][::(-1 if reverse else 1)])
    inputs = dict(manuscript=quote, analysis={'after': after, 'before': before, 'unit': unit})
    assert evaluate(item, inputs)[2].status == 'verified'


@pytest.mark.parametrize('source,expected', [
    ('仿真代码已实际运行；结果来自所述仿真假设，不是现实实测数据。', 'conflict'),
    ('数据不是实测，而是模拟数据。', 'conflict'),
    ('synthetic data, not measured', 'conflict'),
    ('非模拟数据。', 'insufficient'),
    ('后续将开展真实模型受控实验。', 'insufficient'),
    ('真实模型受控实验尚未进行。', 'insufficient'),
    ('包括模拟数据和实测数据。', 'insufficient'),
])
def test_local_negation_keeps_explicit_simulation_but_never_infers_measurement(source, expected):
    item = claim(category='data_status', quote='这些是实测数据。', asserted='实测', operation='status',
                 sources=[{'path': '/evidence', 'quote': source}])
    checked = evaluate(item, dict(manuscript=item['quote'], evidence=source))[2]
    assert checked.status == expected


def test_actual_model_quotient_proposals_are_bound_to_change_semantics_without_false_conflicts():
    import json
    from pathlib import Path
    fixture = json.loads((Path(__file__).resolve().parents[4]
                          / 'tests/fixtures/paper-writing/fact_change_claims.json').read_text())
    for item in fixture['claims']:
        _, _, checked = evaluate(item, dict(manuscript=item['quote'], evidence=fixture['evidence']))
        assert checked.status == 'verified', checked.calculation
        assert 'relative_change(' in checked.calculation
        # Genuine wrong increase/decrease and wrong amounts remain factual conflicts.
        wrong = copy.deepcopy(item)
        if '10.34' in item['asserted_text']:
            wrong['quote'] = wrong['quote'].replace('增加 3,264，约 10.34%', '减少 3,264，下降约 10.34%')
            if wrong['quote'] == item['quote']:
                wrong['quote'] = item['quote'].replace('增加', '下降')
            checked = evaluate(wrong, dict(manuscript=wrong['quote'], evidence=fixture['evidence']))[2]
            assert checked.status == 'conflict'


def test_regular_proportion_is_not_automatically_changed_to_relative_increase():
    assert evaluate(claim())[2].status == 'verified'
    item = claim(quote='耗时变化，比例为21.07%。', asserted='21.07', operation='percentage',
                 sources=[{'path': '/analysis/after'}, {'path': '/analysis/before'}])
    checked = evaluate(item, dict(manuscript=item['quote'], analysis={'after': 137.286, 'before': 113.395}))[2]
    assert checked.status == 'insufficient'


@pytest.mark.parametrize('asserted,expected', [('93.33%', 'verified'), ('100%', 'conflict')])
def test_manuscript_arithmetic_uses_its_explicit_operands(asserted, expected):
    quote = f'15次测试中答对14次，准确率为{asserted}。'
    item = claim(quote=quote, asserted=asserted, sources=[
        {'path': '/manuscript', 'quote': quote, 'value_text': '14'},
        {'path': '/manuscript', 'quote': quote, 'value_text': '15'},
    ])
    checked = evaluate(item, {'manuscript': quote})[2]
    assert checked.status == expected
    assert '未证明来源本身真实' in checked.calculation


@pytest.mark.parametrize('operation,category,asserted', [
    ('value', 'numbers', '24'), ('status', 'data_status', '实测'),
])
def test_manuscript_statement_cannot_verify_itself(operation, category, asserted):
    quote = '本实验为实测，共24次。'
    item = claim(category=category, quote=quote, asserted=asserted, operation=operation,
                 sources=[{'path': '/manuscript', 'quote': quote, 'value_text': '24'}])
    checked = evaluate(item, {'manuscript': quote})[2]
    assert checked.status == 'insufficient'
    assert '自身' in checked.calculation


def test_manuscript_figure_check_uses_the_submitted_table_and_rendered_label():
    quote = '图1报告样本量为24个。'
    source = '结果表记录样本量为19个。'
    item = claim(category='figure', quote=quote, asserted='24', operation='figure_value', sources=[
        {'path': '/manuscript', 'quote': source, 'value_text': '19'},
        {'path': '/charts/0/svg', 'quote': 'n=24', 'value_text': '24'},
    ])
    checked = evaluate(item, {'manuscript': quote + '\n' + source,
                              'charts': [{'svg': '<svg><text>n=24</text></svg>'}]})[2]
    assert checked.status == 'conflict'


def test_absent_upstream_artifacts_do_not_create_missing_evidence_rows():
    result = ReviewResult(summary='审阅成稿。', issues=[], limitations=[])
    coverage = check_facts(result, {'manuscript': '本实验样本量24，方法优于基线，见图1和文献[P1]。'})
    assert all(group.status == 'not_applicable' for group in coverage)
    assert all('需补充材料' not in group.reason for group in coverage)
