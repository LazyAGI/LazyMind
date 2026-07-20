from __future__ import annotations

import lazymind.chat.engine.subagent.tools as attachment_tools
import lazymind.chat.engine.tools.attachment_edit as attachment_edit
import lazymind.chat.service.chat_service as chat_service


def test_attachment_tools_register_string_replace_only_when_files_exist():
    assert chat_service._build_user_attachment_tools(False) == []
    names = {tool.__name__ for tool in chat_service._build_user_attachment_tools(True)}
    assert names == {'find_user_attachment', 'read_user_attachment', 'string_replace'}


def test_string_replace_publishes_edited_copy_and_preserves_upload(monkeypatch, tmp_path):
    source = tmp_path / 'deepseek2.txt'
    source.write_text('before\ntarget value\nafter\n', encoding='utf-8')
    published = {}

    monkeypatch.setattr(
        attachment_tools,
        '_resolve_attachment',
        lambda filename, turn=None: (str(source), None),
    )
    monkeypatch.setattr(attachment_edit.lazyllm, 'globals', {
        'agentic_config': {
            'user_id': 'user-1',
            'conversation_id': 'conversation-1',
            'session_id': 'conversation-1_turn-1',
        },
    })
    monkeypatch.setattr(
        attachment_edit,
        'chat_agent_workspace',
        lambda user_id, conversation_id: str(tmp_path / 'workspace'),
    )

    def fake_save(filename, content, caption=None, artifact_id=None, replace_existing=False):
        published.update(filename=filename, content=content, caption=caption)
        return {
            'success': True,
            'result': {
                'artifact_id': artifact_id,
                'filename': filename,
                'message': f"Saved downloadable artifact '{filename}'.",
            },
        }

    monkeypatch.setattr(attachment_edit, 'save_chat_file_bytes', fake_save)

    preview = attachment_tools.string_replace(
        'deepseek2.txt',
        'target value',
        'updated value',
    )
    result = attachment_tools.string_replace(
        'deepseek2.txt', action='apply', preview_id=preview['result']['preview_id'],
    )

    assert preview['result']['status'] == 'preview'
    assert preview['result']['requires_apply'] is True
    assert 'target value' in preview['result']['diff']
    assert result['success'] is True
    assert result['result']['status'] == 'ok'
    assert result['result']['replacements'] == 1
    assert result['result']['artifact_id']
    assert result['result']['original_unchanged'] is True
    assert published['filename'] == 'deepseek2.txt'
    assert published['content'] == b'before\nupdated value\nafter\n'
    assert source.read_text(encoding='utf-8') == 'before\ntarget value\nafter\n'


def test_string_replace_does_not_publish_when_match_count_is_ambiguous(monkeypatch, tmp_path):
    source = tmp_path / 'duplicate.txt'
    source.write_text('same\nsame\n', encoding='utf-8')
    monkeypatch.setattr(
        attachment_tools,
        '_resolve_attachment',
        lambda filename, turn=None: (str(source), None),
    )
    monkeypatch.setattr(attachment_edit.lazyllm, 'globals', {
        'agentic_config': {
            'user_id': 'user-1',
            'conversation_id': 'conversation-1',
            'session_id': 'conversation-1_turn-2',
        },
    })
    monkeypatch.setattr(
        attachment_edit,
        'chat_agent_workspace',
        lambda user_id, conversation_id: str(tmp_path / 'workspace'),
    )
    monkeypatch.setattr(
        attachment_edit,
        'save_chat_file_bytes',
        lambda *args, **kwargs: (_ for _ in ()).throw(AssertionError('must not publish')),
    )

    result = attachment_tools.string_replace('duplicate.txt', 'same', 'changed')

    assert result['success'] is True
    assert result['result']['status'] == 'error'
    assert 'found 2' in result['result']['message']
    assert source.read_text(encoding='utf-8') == 'same\nsame\n'


def test_repeated_string_replace_composes_one_draft_and_one_artifact(monkeypatch, tmp_path):
    source = tmp_path / 'paper.txt'
    source.write_text('first line\nsecond line\n', encoding='utf-8')
    monkeypatch.setattr(
        attachment_tools,
        '_resolve_attachment',
        lambda filename, turn=None: (str(source), None),
    )
    monkeypatch.setattr(attachment_edit.lazyllm, 'globals', {
        'agentic_config': {
            'user_id': 'user-1',
            'conversation_id': 'conversation-1',
            'session_id': 'conversation-1_turn-3',
        },
    })
    monkeypatch.setattr(
        attachment_edit,
        'chat_agent_workspace',
        lambda user_id, conversation_id: str(tmp_path / 'workspace'),
    )
    published = []

    def fake_save(filename, content, caption=None, artifact_id=None, replace_existing=False):
        published.append((artifact_id, content, replace_existing))
        return {
            'success': True,
            'result': {'artifact_id': artifact_id, 'filename': filename, 'message': 'saved'},
        }

    monkeypatch.setattr(attachment_edit, 'save_chat_file_bytes', fake_save)

    first_preview = attachment_tools.string_replace('paper.txt', 'first', '第一')
    first = attachment_tools.string_replace(
        'paper.txt', action='apply', preview_id=first_preview['result']['preview_id'],
    )
    attachment_edit.lazyllm.globals['agentic_config']['session_id'] = 'conversation-1_turn-4'
    second_preview = attachment_tools.string_replace('paper.txt', 'second', '第二')
    second = attachment_tools.string_replace(
        'paper.txt', action='apply', preview_id=second_preview['result']['preview_id'],
    )

    assert first['result']['continues_previous_edit'] is False
    assert second['result']['continues_previous_edit'] is True
    assert len(published) == 2
    assert published[0][0] == published[1][0]
    assert all(item[2] is True for item in published)
    assert published[-1][1] == '第一 line\n第二 line\n'.encode()
    assert source.read_text(encoding='utf-8') == 'first line\nsecond line\n'

    undone = attachment_tools.string_replace('paper.txt', action='undo')
    assert undone['result']['action'] == 'undo'
    assert undone['result']['revision'] == 1
    assert published[-1][1] == '第一 line\nsecond line\n'.encode()


def test_literal_multiline_preview_matches_crlf_and_returns_locations(monkeypatch, tmp_path):
    source = tmp_path / 'crlf.txt'
    source.write_bytes(b'header\r\nfirst line\r\nsecond line\r\ntail\r\n')
    monkeypatch.setattr(
        attachment_tools,
        '_resolve_attachment',
        lambda filename, turn=None: (str(source), None),
    )
    monkeypatch.setattr(attachment_edit.lazyllm, 'globals', {
        'agentic_config': {
            'user_id': 'user-1',
            'conversation_id': 'conversation-1',
            'session_id': 'conversation-1_turn-crlf',
        },
    })
    monkeypatch.setattr(
        attachment_edit,
        'chat_agent_workspace',
        lambda user_id, conversation_id: str(tmp_path / 'workspace'),
    )

    preview = attachment_tools.string_replace(
        'crlf.txt', 'first line\nsecond line', '合并行', action='preview',
    )

    assert preview['result']['status'] == 'preview'
    assert preview['result']['replacements'] == 1
    assert preview['result']['matches'] == [{
        'index': 1,
        'start_line': 2,
        'end_line': 3,
        'matched_text': 'first line\r\nsecond line',
        'matched_text_truncated': False,
    }]
    assert '-first line\r\n' in preview['result']['diff']
    assert '+\u5408\u5e76\u884c\r\n' in preview['result']['diff']


def test_regex_preview_is_bounded_by_expected_count(monkeypatch, tmp_path):
    source = tmp_path / 'regex.txt'
    source.write_text('BEGIN\nalpha\nEND\nBEGIN\nbeta\nEND\n', encoding='utf-8')
    monkeypatch.setattr(
        attachment_tools,
        '_resolve_attachment',
        lambda filename, turn=None: (str(source), None),
    )
    monkeypatch.setattr(attachment_edit.lazyllm, 'globals', {
        'agentic_config': {
            'user_id': 'user-1',
            'conversation_id': 'conversation-1',
            'session_id': 'conversation-1_turn-regex',
        },
    })
    monkeypatch.setattr(
        attachment_edit,
        'chat_agent_workspace',
        lambda user_id, conversation_id: str(tmp_path / 'workspace'),
    )

    unsafe = attachment_tools.string_replace(
        'regex.txt', r'BEGIN.*?END', 'block', mode='regex', regex_flags='MULTILINE,DOTALL',
    )
    safe = attachment_tools.string_replace(
        'regex.txt', r'BEGIN\nalpha\nEND', 'block', mode='regex', expected_replacements=1,
    )

    assert unsafe['result']['status'] == 'error'
    assert 'found 2' in unsafe['result']['message']
    assert safe['result']['status'] == 'preview'
    assert safe['result']['matches'][0]['start_line'] == 1


def test_apply_rejects_stale_preview(monkeypatch, tmp_path):
    source = tmp_path / 'stale.txt'
    source.write_text('alpha beta', encoding='utf-8')
    monkeypatch.setattr(
        attachment_tools,
        '_resolve_attachment',
        lambda filename, turn=None: (str(source), None),
    )
    monkeypatch.setattr(attachment_edit.lazyllm, 'globals', {
        'agentic_config': {
            'user_id': 'user-1',
            'conversation_id': 'conversation-1',
            'session_id': 'conversation-1_turn-stale',
        },
    })
    monkeypatch.setattr(
        attachment_edit,
        'chat_agent_workspace',
        lambda user_id, conversation_id: str(tmp_path / 'workspace'),
    )
    monkeypatch.setattr(
        attachment_edit,
        'save_chat_file_bytes',
        lambda filename, content, **kwargs: {
            'success': True,
            'result': {'artifact_id': kwargs['artifact_id'], 'filename': filename},
        },
    )

    stale = attachment_tools.string_replace('stale.txt', 'alpha', 'A')
    current = attachment_tools.string_replace('stale.txt', 'beta', 'B')
    attachment_tools.string_replace(
        'stale.txt', action='apply', preview_id=current['result']['preview_id'],
    )
    rejected = attachment_tools.string_replace(
        'stale.txt', action='apply', preview_id=stale['result']['preview_id'],
    )

    assert rejected['result']['status'] == 'error'
    assert 'stale' in rejected['result']['message'].lower()
