"""Replay topic refinement from conversation 71eb9c6a-1618-47cf-a50e-4a213cab6413.

The fixture is the unmodified model response from the failed first attempt,
23d57b3c-771f-4df5-b794-ba2a221ee856, on 2026-10-29. Only model I/O is replaced.
"""
import copy
from pathlib import Path
import re
from unittest.mock import Mock

import pytest

from lazymind.document_tools.paper import research_topic, workflow


@pytest.fixture
def recorded_topic():
    path = Path(__file__).resolve().parents[4] / 'tests/fixtures/paper-writing/refine-topic-20261029.md'
    return path.read_text(encoding='utf-8')


@pytest.fixture
def topic_pool():
    # Bind all call-local markers in the recorded response without external paper retrieval.
    return workflow.pool_from_business({
        'topic': '分阶段论文写作与人工介入',
        'question': '# 原选题\n\n## 研究问题\n人工介入能否改善论文质量？',
        'evidence': '# 已确认的综述\n比较证据一致性、修订可靠性及成本，现有结论仍需核验。',
        'papers': [dict(id=f'source-{index}', title=f'参考论文{index}', abstract='测试绑定摘要。')
                   for index in range(1, 14)],
    })


@pytest.mark.parametrize('feedback_heading', ['', '## 文献反馈', '### 文献反馈与调整依据'])
def test_recorded_refinement_saves_question_without_fixed_feedback_heading(
    recorded_topic, topic_pool, feedback_heading, monkeypatch,
):
    response = recorded_topic
    if feedback_heading:
        response += '\n\n' + feedback_heading + '\n依据综述保留证据一致性目标，收紧修订可靠性的验证范围。'
    model = Mock(spec=lambda prompt: None, return_value=response)
    monkeypatch.setattr(research_topic, 'AutoModel', lambda **kwargs: model)
    before = copy.deepcopy(topic_pool)

    result = workflow.execute_step('refine_topic', topic_pool, '根据综述调整选题')

    expected = re.sub(r'\[M(\d+)\]', lambda match: f'[source-{match[1]}]', response)
    assert result['status'] == 'completed'
    assert result['document'] == expected
    assert workflow.material(result['pool'], 'question')['content'] == expected
    assert workflow.material(result['pool'], 'evidence') == workflow.material(topic_pool, 'evidence')
    assert topic_pool == before
    model.assert_called_once()
    prompt = model.call_args.args[0]
    assert 'explain what is retained and what is changed' in prompt
    assert 'what the review supports, contradicts, or cannot yet determine' in prompt
    assert 'exactly one second-level section titled `## 文献反馈与调整依据`' not in prompt


@pytest.mark.parametrize('invalid', [
    '',
    '# 选题\n\n## 其他章节\n缺少研究问题。',
    '# 选题\n\n## 研究问题\n\n## 证据状态\n尚待核验。',
    '# 选题\n\n## 研究问题\n问题一。\n\n## 研究问题\n问题二。',
    '```markdown\n# 选题\n\n## 研究问题\n问题。\n```',
    '<think>未闭合的思考内容',
    '# 选题\n\n## 研究问题\n未知来源声称有效。[M99]',
])
def test_refinement_keeps_required_question_and_citation_checks(topic_pool, invalid, monkeypatch):
    model = Mock(spec=lambda prompt: None, return_value=invalid)
    monkeypatch.setattr(research_topic, 'AutoModel', lambda **kwargs: model)
    before = copy.deepcopy(topic_pool)

    with pytest.raises(ValueError):
        workflow.execute_step('refine_topic', topic_pool, '根据综述调整选题')

    assert topic_pool == before
    model.assert_called_once()
