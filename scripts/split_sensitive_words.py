"""Split the legacy sensitive-word list into reviewed runtime tiers."""

from __future__ import annotations

import argparse
import hashlib
import json
from pathlib import Path


CATEGORY_HEADINGS = frozenset({
    '屏蔽词库',
    '犯罪词汇',
    '恐怖主义敏感词汇',
})

# These context-sensitive entries have been explicitly approved for whole-token
# matching. All unreviewed candidates remain in the red tier.
APPROVED_GRAY_WORDS = frozenset({
    'AIDS',
    'AV',
    'DICK',
    'FUCK',
    'Fuck',
    'ISIL',
    'ISIS',
    'JB',
    'J8',
    'RAPE',
    'SB',
    'SEX',
    'SHIT',
    'TMD',
    'fuck',
    'j8',
    'jb',
    'sb',
    'sex',
    'tmd',
    '乱伦',
    '二货',
    '二逼',
    '人渣',
    '仆街',
    '你妈',
    '你妹',
    '你爸',
    '傻逼',
    '傻比',
    '傻叉',
    '傻子',
    '几吧',
    '刹笔',
    '卧槽',
    '卧艹',
    '口交',
    '去死',
    '坑爹',
    '妓女',
    '妓院',
    '妈的',
    '妈批',
    '妈逼',
    '婊子',
    '嫖娼',
    '嫖客',
    '屁民',
    '尼玛',
    '强奸',
})

WHITELIST_WORDS = (
    '路口交警',
    '黑木耳',
    '操作步骤',
    '生日快乐',
    '可验证',
)

RED_FILENAME = 'sensitive_red.txt'
GRAY_FILENAME = 'sensitive_gray.txt'
WHITELIST_FILENAME = 'sensitive_whitelist.txt'
REPORT_FILENAME = 'sensitive_words_report.json'


def _sha256(content: str) -> str:
    return hashlib.sha256(content.encode('utf-8')).hexdigest()


def _render_words(words: list[str] | tuple[str, ...]) -> str:
    return ''.join(f'{word}\n' for word in words)


def split_words(source: Path, output_dir: Path) -> dict:
    raw_content = source.read_text(encoding='utf-8')
    seen: set[str] = set()
    red_words: list[str] = []
    gray_words: list[str] = []
    removed_headings: list[str] = []
    special_candidates: list[str] = []
    duplicate_count = 0

    for line in raw_content.splitlines():
        word = line.strip()
        if not word:
            continue
        if word in seen:
            duplicate_count += 1
            continue
        seen.add(word)
        if word in CATEGORY_HEADINGS:
            removed_headings.append(word)
            continue
        if '*' in word or '-' in word:
            special_candidates.append(word)
        if word in APPROVED_GRAY_WORDS:
            gray_words.append(word)
        else:
            red_words.append(word)

    output_dir.mkdir(parents=True, exist_ok=True)
    rendered = {
        RED_FILENAME: _render_words(red_words),
        GRAY_FILENAME: _render_words(gray_words),
        WHITELIST_FILENAME: _render_words(WHITELIST_WORDS),
    }
    for filename, content in rendered.items():
        (output_dir / filename).write_text(content, encoding='utf-8')

    report = {
        'schema_version': 1,
        'source_sha256': _sha256(raw_content),
        'source_nonempty_count': sum(bool(line.strip()) for line in raw_content.splitlines()),
        'unique_source_count': len(seen),
        'duplicate_count': duplicate_count,
        'red_count': len(red_words),
        'gray_count': len(gray_words),
        'approved_gray_words': gray_words,
        'whitelist_count': len(WHITELIST_WORDS),
        'removed_headings': removed_headings,
        'special_character_candidates': special_candidates,
        'output_sha256': {
            filename: _sha256(content)
            for filename, content in rendered.items()
        },
    }
    (output_dir / REPORT_FILENAME).write_text(
        json.dumps(report, ensure_ascii=False, indent=2, sort_keys=True) + '\n',
        encoding='utf-8',
    )
    return report


def check_resources(output_dir: Path) -> dict:
    report = json.loads((output_dir / REPORT_FILENAME).read_text(encoding='utf-8'))
    contents = {
        filename: (output_dir / filename).read_text(encoding='utf-8')
        for filename in (RED_FILENAME, GRAY_FILENAME, WHITELIST_FILENAME)
    }
    words = {
        filename: [line.strip() for line in content.splitlines() if line.strip()]
        for filename, content in contents.items()
    }

    for filename, values in words.items():
        if len(values) != len(set(values)):
            raise ValueError(f'Duplicate entries found in {filename}')
        expected_hash = report['output_sha256'][filename]
        if _sha256(contents[filename]) != expected_hash:
            raise ValueError(f'Checksum mismatch for {filename}')

    red_words = set(words[RED_FILENAME])
    gray_words = set(words[GRAY_FILENAME])
    if red_words.intersection(gray_words):
        raise ValueError('Red and gray resources must be disjoint')
    if CATEGORY_HEADINGS.intersection(red_words | gray_words):
        raise ValueError('Category headings must not be runtime keywords')
    approved_gray_words = set(report['approved_gray_words'])
    if gray_words != approved_gray_words:
        raise ValueError('Gray resource differs from the approved gray-word set')
    if not gray_words.issubset(APPROVED_GRAY_WORDS):
        raise ValueError('Gray resource contains an unreviewed gray word')
    if tuple(words[WHITELIST_FILENAME]) != WHITELIST_WORDS:
        raise ValueError('Whitelist resource differs from the reviewed whitelist')

    expected_counts = {
        RED_FILENAME: report['red_count'],
        GRAY_FILENAME: report['gray_count'],
        WHITELIST_FILENAME: report['whitelist_count'],
    }
    for filename, expected_count in expected_counts.items():
        if len(words[filename]) != expected_count:
            raise ValueError(f'Count mismatch for {filename}')
    return report


def _parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description=__doc__)
    subparsers = parser.add_subparsers(dest='command', required=True)

    split_parser = subparsers.add_parser('split')
    split_parser.add_argument('--source', type=Path, required=True)
    split_parser.add_argument('--output-dir', type=Path, required=True)

    check_parser = subparsers.add_parser('check')
    check_parser.add_argument('--output-dir', type=Path, required=True)
    return parser


def main() -> None:
    args = _parser().parse_args()
    if args.command == 'split':
        report = split_words(args.source, args.output_dir)
    else:
        report = check_resources(args.output_dir)
    print(json.dumps({
        'red_count': report['red_count'],
        'gray_count': report['gray_count'],
        'whitelist_count': report['whitelist_count'],
    }, ensure_ascii=False, sort_keys=True))


if __name__ == '__main__':
    main()
