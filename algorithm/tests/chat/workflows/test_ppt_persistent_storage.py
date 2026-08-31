from __future__ import annotations

import importlib.util
from pathlib import Path
from types import SimpleNamespace


def _load_ppt_tools():
    root = Path(__file__).resolve().parents[4]
    path = root / 'workflows' / 'ppt-workflow' / 'scripts' / 'tools.py'
    spec = importlib.util.spec_from_file_location('_test_ppt_workflow_tools', path)
    assert spec is not None and spec.loader is not None
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def test_conversation_root_uses_durable_upload_storage(monkeypatch, tmp_path):
    tools = _load_ppt_tools()
    attempt = tmp_path / 'tmp' / 'lazymind-workflow-attempt-1'
    attempt.mkdir(parents=True)
    ctx = SimpleNamespace(
        conversation_id='conversation-1',
        workspace_path=str(attempt),
        params={'user_id': 'user-1'},
    )
    upload_root = tmp_path / 'uploads'
    monkeypatch.setattr(tools, 'require_context', lambda: ctx)
    monkeypatch.setattr(tools, '_upload_root', lambda: str(upload_root))

    root = tools._conversation_root()

    assert root == (
        upload_root / 'workflow-workspaces' / 'ppt-workflow' / 'user-1'
        / 'ppt_sessions' / 'conversation-1'
    )
    assert attempt not in root.parents


def test_find_deck_survives_disposable_attempt_removal(monkeypatch, tmp_path):
    tools = _load_ppt_tools()
    upload_root = tmp_path / 'uploads'
    first_attempt = tmp_path / 'tmp' / 'lazymind-workflow-attempt-1'
    first_attempt.mkdir(parents=True)
    ctx = SimpleNamespace(
        conversation_id='conversation-1',
        workspace_path=str(first_attempt),
        params={'user_id': 'user-1'},
    )
    monkeypatch.setattr(tools, 'require_context', lambda: ctx)
    monkeypatch.setattr(tools, '_upload_root', lambda: str(upload_root))

    deck = tools._conversation_root() / 'ppt_decks' / 'deck-1'
    (deck / 'pages').mkdir(parents=True)
    (deck / 'task_pack.json').write_text(
        '{"deck_id":"deck-1","params":{"page_count":1}}', encoding='utf-8')
    (deck / 'info_pack.json').write_text('{}', encoding='utf-8')

    first_attempt.rmdir()
    second_attempt = tmp_path / 'tmp' / 'lazymind-workflow-attempt-2'
    second_attempt.mkdir()
    ctx.workspace_path = str(second_attempt)

    result = tools.ppt_find_deck()

    assert result['success'] is True
    assert result['result']['deck_id'] == 'deck-1'
    assert result['result']['deck_dir'] == str(deck.resolve())


def test_legacy_tmp_deck_is_copied_without_overwriting_persistent_state(
    monkeypatch, tmp_path,
):
    tools = _load_ppt_tools()
    upload_root = tmp_path / 'uploads'
    temp_root = tmp_path / 'executor-tmp'
    attempt = temp_root / 'lazymind-workflow-attempt-1'
    attempt.mkdir(parents=True)
    legacy_deck = (
        temp_root / 'ppt_sessions' / 'conversation-1' / 'ppt_decks' / 'legacy-deck'
    )
    legacy_deck.mkdir(parents=True)
    (legacy_deck / 'task_pack.json').write_text('legacy', encoding='utf-8')
    ctx = SimpleNamespace(
        conversation_id='conversation-1',
        workspace_path=str(attempt),
        params={'user_id': 'user-1'},
    )
    monkeypatch.setattr(tools, 'require_context', lambda: ctx)
    monkeypatch.setattr(tools, '_upload_root', lambda: str(upload_root))
    monkeypatch.setattr(tools.tempfile, 'gettempdir', lambda: str(temp_root))

    root = tools._conversation_root()
    migrated = root / 'ppt_decks' / 'legacy-deck' / 'task_pack.json'
    assert migrated.read_text(encoding='utf-8') == 'legacy'

    migrated.write_text('persistent', encoding='utf-8')
    (legacy_deck / 'task_pack.json').write_text('stale', encoding='utf-8')
    tools._conversation_root()

    assert migrated.read_text(encoding='utf-8') == 'persistent'
