from pathlib import Path


ROOT = Path(__file__).resolve().parents[4]
SKILL = ROOT / 'skills' / 'vocabulary' / 'vocabulary-learning' / 'SKILL.md'


def test_vocabulary_training_requires_interactive_audited_review() -> None:
    content = SKILL.read_text(encoding='utf-8')

    assert '不得直接调用 `ask_user`' in content
    assert '`ask_words` 内部复用 ask-user 的 SSE 事件和 panel 展示' in content
    assert '不得使用 `while` 或其他循环等待用户' in content
    assert '`e2c+choice`、`c2e+choice`、`c2e+fill`' in content
    assert 'Core hook 会直接判分、调用单词服务并逐题登记' in content
    assert '`complete=true` 和 `remaining=0`' in content
    assert '`finish_review_session`' in content
    assert '模型不得再次调用 `get_review_words`' in content
    assert '工具还会直接完成 session 并返回后端生成的 `report`' in content
    assert '不得重新计算、改写或补造报告主体数据' in content
    assert '用户忽略题卡 / 转向其他事项时不得持续追问' in content
    assert '`get_review_words(count=20..200)`' in content
    assert '`type=cloze`' in content
    assert '干扰词及其他未使用候选不会被签发' in content
