import builtins

import pytest

from lazymind.chat.service.utils.sensitive_filter import SensitiveFilter


def _word_files(
    tmp_path,
    *,
    red: str = 'blocked',
    gray: str = 'gray',
    whitelist: str = 'allowed',
):
    red_file = tmp_path / 'red.txt'
    gray_file = tmp_path / 'gray.txt'
    whitelist_file = tmp_path / 'whitelist.txt'
    red_file.write_text(f'{red}\n', encoding='utf-8')
    gray_file.write_text(f'{gray}\n', encoding='utf-8')
    whitelist_file.write_text(f'{whitelist}\n', encoding='utf-8')
    return red_file, gray_file, whitelist_file


def test_sensitive_filter_reports_red_match_with_span(tmp_path):
    filter_ = SensitiveFilter(*_word_files(tmp_path))

    match = filter_.evaluate('this is blocked text')

    assert match is not None
    assert match.word == 'blocked'
    assert match.tier == 'red'
    assert (match.start, match.end) == (8, 15)
    assert filter_.evaluate('clean text') is None
    assert filter_.evaluate('') is None


def test_sensitive_filter_whitelist_span_exempts_nested_red_match(tmp_path):
    filter_ = SensitiveFilter(*_word_files(
        tmp_path,
        red='黑木耳\n木耳',
        whitelist='黑木耳',
    ))

    assert filter_.evaluate('黑木耳炒肉的做法') is None
    match = filter_.evaluate('木耳')
    assert match is not None
    assert match.word == '木耳'


def test_sensitive_filter_exempts_multiple_whitelist_spans(tmp_path):
    filter_ = SensitiveFilter(*_word_files(
        tmp_path,
        red='交警\n木耳',
        whitelist='路口交警\n黑木耳',
    ))

    assert filter_.evaluate('路口交警推荐黑木耳炒肉') is None


def test_sensitive_filter_continues_after_whitelisted_match(tmp_path):
    filter_ = SensitiveFilter(*_word_files(
        tmp_path,
        red='敏感',
        whitelist='合法敏感词',
    ))

    match = filter_.evaluate('合法敏感词之后仍有敏感')

    assert match is not None
    assert (match.word, match.start, match.end) == ('敏感', 9, 11)


def test_sensitive_filter_matches_injected_gray_words_as_whole_tokens(tmp_path):
    filter_ = SensitiveFilter(*_word_files(
        tmp_path,
        red='redword',
        gray='口交\n傻逼\nSB\nJB\nAV\nSEX\nDICK',
        whitelist='路口交警',
    ))

    assert filter_.evaluate('路口交警在指挥交通') is None
    match = filter_.evaluate('你是傻逼')
    assert match is not None
    assert (match.word, match.tier, match.start, match.end) == ('傻逼', 'gray', 2, 4)
    assert filter_.evaluate('SB/JB').word == 'SB'
    for query in ('USB接口', 'JAVA教程', 'UNISEX', 'DICKENS'):
        assert filter_.evaluate(query) is None


@pytest.mark.parametrize('missing_tier', ['red', 'gray', 'whitelist'])
def test_sensitive_filter_rejects_missing_word_files(tmp_path, missing_tier):
    paths = dict(zip(
        ('red', 'gray', 'whitelist'),
        _word_files(tmp_path),
    ))
    paths[missing_tier].unlink()

    with pytest.raises(RuntimeError, match=missing_tier):
        SensitiveFilter(paths['red'], paths['gray'], paths['whitelist'])


@pytest.mark.parametrize('missing_dependency', ['ahocorasick', 'jieba'])
def test_sensitive_filter_rejects_missing_dependencies(
    monkeypatch,
    tmp_path,
    missing_dependency,
):
    real_import = builtins.__import__

    def fake_import(name, *args, **kwargs):
        if name == missing_dependency:
            raise ImportError('missing for test')
        return real_import(name, *args, **kwargs)

    monkeypatch.setattr(builtins, '__import__', fake_import)

    with pytest.raises(RuntimeError, match=missing_dependency):
        SensitiveFilter(*_word_files(tmp_path))
