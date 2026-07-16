import pytest

from lazymind.chat.engine.tools import chat_artifact
from lazymind.chat.service.component.event_translator import AgentEventFrameTranslator


def test_save_chat_artifact_emits_downloadable_event(monkeypatch):
    emitted = []
    monkeypatch.setattr(
        chat_artifact,
        '_write_agent_data',
        lambda tag, **payload: emitted.append({'tag': tag, **payload}),
    )

    result = chat_artifact.save_chat_artifact('hello.txt', '你好')

    assert result['success'] is True
    assert emitted[0]['tag'] == 'artifact_created'
    assert emitted[0]['filename'] == 'hello.txt'
    assert emitted[0]['value'] == {'text': '你好'}


@pytest.mark.parametrize('filename', ['../escape.txt', 'dir/file.txt', '', '..'])
def test_save_chat_artifact_rejects_unsafe_filename(filename):
    with pytest.raises(ValueError):
        chat_artifact.save_chat_artifact(filename, 'x')


def test_artifact_event_translator_preserves_structured_payload():
    translator = AgentEventFrameTranslator(query='创建一个 txt')
    frames = translator.feed({
        'tag': 'artifact_created',
        'artifact_id': 'artifact-1',
        'filename': 'a.txt',
        'content_type': 'text',
        'value': {'text': 'a'},
    })
    assert frames == [{
        'think': None,
        'text': None,
        'sources': [],
        'artifact_created': {
            'artifact_id': 'artifact-1',
            'filename': 'a.txt',
            'content_type': 'text',
            'value': {'text': 'a'},
        },
    }]
