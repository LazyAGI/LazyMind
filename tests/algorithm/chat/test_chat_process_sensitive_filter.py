import sys
import types

from lazymind.chat.service.utils.sensitive_filter import SensitiveFilter


class FakeAutomaton:
    def __init__(self):
        self.words = []

    def add_word(self, word, payload):
        self.words.append((word, payload))

    def make_automaton(self):
        return None

    def iter(self, text):
        for word, payload in self.words:
            pos = text.find(word)
            if pos >= 0:
                yield pos + len(word) - 1, payload


def test_sensitive_filter_returns_pass_when_not_loaded():
    filter_ = SensitiveFilter()

    assert filter_.check('anything') == (False, '')
    assert filter_.check('') == (False, '')


def test_sensitive_filter_loads_keywords_and_returns_first_match(monkeypatch, tmp_path):
    monkeypatch.setitem(sys.modules, 'ahocorasick', types.SimpleNamespace(Automaton=FakeAutomaton))
    keyword_file = tmp_path / 'keywords.txt'
    keyword_file.write_text('\nblocked\nignored\n', encoding='utf-8')

    filter_ = SensitiveFilter(str(keyword_file))

    assert filter_.loaded is True
    assert filter_.keyword_count == 2
    assert filter_.check('this text is blocked') == (True, 'blocked')
    assert filter_.check('clean text') == (False, '')


def test_sensitive_filter_handles_missing_and_directory_keyword_paths(monkeypatch, tmp_path):
    monkeypatch.setitem(sys.modules, 'ahocorasick', types.SimpleNamespace(Automaton=FakeAutomaton))

    missing_filter = SensitiveFilter(str(tmp_path / 'missing.txt'))
    directory_filter = SensitiveFilter(str(tmp_path))

    assert missing_filter.loaded is False
    assert missing_filter.keyword_count == 0
    assert directory_filter.loaded is False
    assert directory_filter.keyword_count == 0


def test_sensitive_filter_handles_import_error(monkeypatch, tmp_path):
    keyword_file = tmp_path / 'keywords.txt'
    keyword_file.write_text('blocked\n', encoding='utf-8')
    monkeypatch.delitem(sys.modules, 'ahocorasick', raising=False)

    import builtins

    real_import = builtins.__import__

    def fake_import(name, *args, **kwargs):
        if name == 'ahocorasick':
            raise ImportError('missing')
        return real_import(name, *args, **kwargs)

    monkeypatch.setattr(builtins, '__import__', fake_import)

    filter_ = SensitiveFilter(str(keyword_file))

    assert filter_.loaded is False
    assert filter_.check('blocked') == (False, '')


def test_sensitive_filter_handles_load_and_check_exceptions(monkeypatch, tmp_path):
    class FailingAddAutomaton(FakeAutomaton):
        def add_word(self, word, payload):
            raise RuntimeError('load failure')

    keyword_file = tmp_path / 'keywords.txt'
    keyword_file.write_text('blocked\n', encoding='utf-8')
    monkeypatch.setitem(sys.modules, 'ahocorasick', types.SimpleNamespace(Automaton=FailingAddAutomaton))

    filter_ = SensitiveFilter(str(keyword_file))

    assert filter_.loaded is False
    assert filter_.actree is None

    class FailingIterAutomaton(FakeAutomaton):
        def iter(self, text):
            raise RuntimeError('check failure')

    filter_.actree = FailingIterAutomaton()
    filter_.loaded = True

    assert filter_.check('blocked') == (False, '')
