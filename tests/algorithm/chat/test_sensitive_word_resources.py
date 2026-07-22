import random
from pathlib import Path

from lazymind.chat.service.utils.sensitive_filter import SensitiveFilter


RESOURCES_DIR = (
    Path(__file__).resolve().parents[3] / 'algorithm/lazymind/chat/resources'
)


def test_committed_resources_preserve_red_blocks_and_remove_classic_false_positives():
    red_path = RESOURCES_DIR / 'sensitive_red.txt'
    gray_path = RESOURCES_DIR / 'sensitive_gray.txt'
    whitelist_path = RESOURCES_DIR / 'sensitive_whitelist.txt'
    filter_ = SensitiveFilter(red_path, gray_path, whitelist_path)

    for query in (
        '路口交警在指挥交通',
        '黑木耳炒肉的做法',
        '这个操作步骤有问题',
        '生日快乐',
        '这个链路跑通后可验证',
    ):
        assert filter_.evaluate(query) is None

    gray_words = gray_path.read_text(encoding='utf-8').splitlines()
    assert len(gray_words) == 50
    for word in gray_words:
        match = filter_.evaluate(word)
        assert match is not None
        assert (match.word, match.tier) == (word, 'gray')

    red_words = red_path.read_text(encoding='utf-8').splitlines()
    for word in random.Random(0).sample(red_words, 50):
        match = filter_.evaluate(word)
        assert match is not None
        assert match.tier == 'red'
