import json
import random
import subprocess
import sys
from pathlib import Path

from lazymind.chat.service.utils.sensitive_filter import SensitiveFilter


SCRIPT_PATH = Path(__file__).resolve().parents[3] / 'scripts/split_sensitive_words.py'
RESOURCES_DIR = (
    Path(__file__).resolve().parents[3] / 'algorithm/lazymind/chat/resources'
)


def test_split_script_generates_reviewable_tiered_resources(tmp_path):
    source = tmp_path / 'sensitive_words.txt'
    output_dir = tmp_path / 'resources'
    source.write_text(
        '屏蔽词库\n普通红词\n口交\nSB\nJB\njb\nsb\n傻逼\n含*号\n普通红词\n',
        encoding='utf-8',
    )

    subprocess.run(
        [
            sys.executable,
            str(SCRIPT_PATH),
            'split',
            '--source',
            str(source),
            '--output-dir',
            str(output_dir),
        ],
        check=True,
    )

    assert (output_dir / 'sensitive_red.txt').read_text(encoding='utf-8') == (
        '普通红词\n含*号\n'
    )
    assert (output_dir / 'sensitive_gray.txt').read_text(encoding='utf-8') == (
        '口交\nSB\nJB\njb\nsb\n傻逼\n'
    )
    whitelist = (output_dir / 'sensitive_whitelist.txt').read_text(encoding='utf-8')
    assert '路口交警\n' in whitelist
    assert '可验证\n' in whitelist

    report = json.loads(
        (output_dir / 'sensitive_words_report.json').read_text(encoding='utf-8')
    )
    assert report['removed_headings'] == ['屏蔽词库']
    assert report['special_character_candidates'] == ['含*号']
    assert report['duplicate_count'] == 1

    subprocess.run(
        [
            sys.executable,
            str(SCRIPT_PATH),
            'check',
            '--output-dir',
            str(output_dir),
        ],
        check=True,
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
