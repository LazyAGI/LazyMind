"""Explicit user budgets and required sections survive the public writing path."""
import pytest

from lazymind.document_tools.paper.writing.requirements import resolve_constraints, validate_required_sections


@pytest.mark.parametrize('user_request,target,language', [
    ('生成约4000字中文初稿，含摘要、方法、结果、讨论和结论。', 4000, 'zh-CN'),
    ('Write a 2.5k word paper, including Abstract, Methods and Results.', 2500, 'en'),
    ('英文论文约4千words，含中文摘要。', 4000, 'en'),
    ('全文1万字，摘要约300字。', 10000, 'zh-CN'),
])
def test_explicit_whole_paper_budget(user_request, target, language):
    result = resolve_constraints({}, user_request)
    assert result['word_target'] == target and result['paper_language'] == language


def test_required_sections_are_explicit_and_not_a_full_template():
    result = resolve_constraints({}, '约4000字中文初稿（含摘要、方法、实验/评估、结果、讨论、限制和结论）。')
    assert result['required_sections'] == ['abstract', 'methods', 'evaluation', 'results',
                                           'discussion', 'limitations', 'conclusion']
    assert 'introduction' not in result['required_sections']


def test_structured_constraints_win_initially_and_current_update_wins_later():
    source = dict(word_target=5000, paper_language='en', required_sections=['methods'])
    result = resolve_constraints(source, '中文论文约4000字，含摘要。')
    assert result['word_target'] == 5000 and result['paper_language'] == 'en'
    assert result['required_sections'] == ['methods']
    updated = resolve_constraints(result, '原始要求', current_request='改为3000字中文初稿，含摘要、方法。')
    assert updated['word_target'] == 3000 and updated['paper_language'] == 'zh-CN'
    assert updated['required_sections'] == ['methods', 'abstract']
    assert source == dict(word_target=5000, paper_language='en', required_sections=['methods'])


@pytest.mark.parametrize('user_request', ['只把摘要改为200字', '论文摘要约300字', '实验结果200字', '请写3000–4000字论文', '参考2026年的论文'])
def test_local_or_ambiguous_numbers_are_not_whole_paper_targets(user_request):
    assert resolve_constraints({}, user_request)['word_target'] is None


def test_current_section_budget_does_not_overwrite_full_paper_budget():
    result = resolve_constraints(dict(word_target=4000, required_sections=['abstract', 'methods']),
                                 '已有要求', current_request='只把摘要缩为200字')
    assert result['word_target'] == 4000 and result['required_sections'] == ['abstract', 'methods']


def test_current_removal_changes_requirement_without_rewriting_a_manuscript():
    assert resolve_constraints(dict(required_sections=['abstract', 'methods']), '',
                               current_request='不要摘要')['required_sections'] == ['methods']


def test_required_section_must_be_a_heading_not_a_future_promise():
    with pytest.raises(ValueError, match='abstract'):
        validate_required_sections('# Paper\n\n## Introduction\nWe will add an abstract.',
                                   dict(required_sections=['abstract']))
    validate_required_sections('# Paper\n\n## Abstract\nSummary.\n\n## Materials and Methods\nDetails.',
                               dict(required_sections=['abstract', 'methods']))


def test_current_section_addition_preserves_original_abstract():
    result = resolve_constraints(dict(required_sections=['abstract', 'methods']), '',
                                 current_request='还需包含讨论。')
    assert result['required_sections'] == ['abstract', 'methods', 'discussion']
