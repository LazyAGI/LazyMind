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


def test_learning_reports_each_attempt_without_source_content(monkeypatch):
    from types import SimpleNamespace

    responses = iter(['not JSON', '{"summary":"result","symbols":[]}'])
    messages = []
    monkeypatch.setattr(base, 'AutoModel', lambda **_kwargs: _Model(next(responses)))
    monkeypatch.setattr(base, 'LOG', SimpleNamespace(info=messages.append))

    generated = base.rewrite_content('learning', 'private source text', 'private task instruction')

    assert json.loads(generated)['summary'] == 'result'
    assert len(messages) == 2
    assert 'attempt=1' in messages[0] and 'attempt=2' in messages[1]
    assert all('duration_ms=' in message and 'private' not in message for message in messages)


def test_single_attempt_disables_transport_and_json_retries(monkeypatch):
    import pytest
    calls = []

    class InvalidModel:
        def __call__(self, prompt, **options):
            calls.append(options)
            return 'not JSON'

    monkeypatch.setattr(base, 'AutoModel', lambda **_kwargs: InvalidModel())
    with pytest.raises(base.UnprocessableContentError):
        base.rewrite_content('learning', 'formula', 'Return JSON', single_attempt=True)
    assert calls == [{'max_retries': 1}]
