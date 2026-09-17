import json

from lazymind.rewrite import base


class _Model:
    def __init__(self, response):
        self.response = response

    def __call__(self, _prompt):
        return self.response


def test_learning_accepts_capability_schema_without_rewrite_wrapper(monkeypatch):
    monkeypatch.setattr(base, 'AutoModel', lambda **_kwargs: _Model(
        '{"meaning_in_context":"急剧转弯的路段","pinyin":"jí wān"}'
    ))

    generated = base.rewrite_content('learning', '急弯', 'Return the capability JSON schema')

    assert json.loads(generated) == {
        'meaning_in_context': '急剧转弯的路段',
        'pinyin': 'jí wān',
    }


def test_learning_keeps_accepting_legacy_content_wrapper(monkeypatch):
    monkeypatch.setattr(base, 'AutoModel', lambda **_kwargs: _Model(
        '{"content":"{\\"meaning_in_context\\":\\"急剧转弯的路段\\"}"}'
    ))

    generated = base.rewrite_content('learning', '急弯', 'Return the capability JSON schema')

    assert json.loads(generated) == {'meaning_in_context': '急剧转弯的路段'}
