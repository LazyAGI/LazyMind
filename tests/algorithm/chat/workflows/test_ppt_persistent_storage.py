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

    assert result['deck_id'] == 'deck-1'
    assert result['deck_dir'] == str(deck.resolve())


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


def test_find_deck_empty_conversation_is_normal_result(monkeypatch, tmp_path):
    tools = _load_ppt_tools()
    monkeypatch.setattr(tools, '_conversation_root', lambda: tmp_path)
    result = tools.ppt_find_deck()
    assert result['found'] is False
    assert result['deck_dir'] is None
    assert result['next_tool'] == 'ppt_init_deck'


def test_init_deck_accepts_model_key_points_alias(monkeypatch, tmp_path):
    import json
    tools = _load_ppt_tools()
    monkeypatch.setattr(tools, '_conversation_root', lambda: tmp_path)
    monkeypatch.setattr(tools, '_attach_material_images_to_deck', lambda _: {'attached': 0})
    result = tools.ppt_init_deck(user_query='Test deck', key_points='["Keep this point"]')
    info = json.loads((Path(result['deck_dir']) / 'info_pack.json').read_text(encoding='utf-8'))
    assert info['query_normalized']['key_points'] == ['Keep this point']
    result = tools.ppt_init_deck(user_query='Canonical deck', key_points=['alias'], key_points_json=['canonical'])
    info = json.loads((Path(result['deck_dir']) / 'info_pack.json').read_text(encoding='utf-8'))
    assert info['query_normalized']['key_points'] == ['canonical']


def test_preview_images_use_small_durable_urls_but_disk_stays_relative(monkeypatch, tmp_path):
    tools = _load_ppt_tools()
    monkeypatch.setenv('LAZYMIND_UPLOAD_ROOT', str(tmp_path))
    deck = tmp_path / 'deck'
    (deck / 'images').mkdir(parents=True)
    (deck / 'pages').mkdir()
    image = deck / 'images' / 'big image.png'
    image.write_bytes(b'x' * 2_000_000)
    original = '<html><style>#bg{background:url("../images/big image.png")}</style><img src="../images/big image.png"></html>'
    page = deck / 'pages' / 'page_001.html'
    page.write_text(original, encoding='utf-8')
    preview, count = tools._inline_preview_images(original, deck, page)
    assert count == 2
    assert preview.count('/static-files/deck/images/big%20image.png') == 2
    assert len(preview) < 300
    assert 'base64' not in preview
    assert page.read_text(encoding='utf-8') == original


def test_preview_never_links_files_outside_the_deck(monkeypatch, tmp_path):
    tools = _load_ppt_tools()
    monkeypatch.setenv('LAZYMIND_UPLOAD_ROOT', str(tmp_path))
    deck = tmp_path / 'deck'
    (deck / 'pages').mkdir(parents=True)
    (tmp_path / 'private.png').write_bytes(b'secret')
    html = '<img src="../../private.png">'
    assert tools._inline_preview_images(html, deck, deck / 'pages' / 'page.html') == (html, 0)


def test_outline_retry_reuses_deck_style_and_completed_outline(monkeypatch, tmp_path):
    import json
    import pytest
    tools = _load_ppt_tools()
    monkeypatch.setattr(tools, '_conversation_root', lambda: tmp_path)
    monkeypatch.setattr(tools, '_attach_material_images_to_deck', lambda _: {'attached': 0})
    calls = []
    fail_outline = True
    fail_publish = True
    deck_paths = []

    def stage(deck_dir, stage):
        nonlocal fail_outline
        calls.append(stage)
        deck = Path(deck_dir)
        deck_paths.append(deck)
        if stage == 'style':
            (deck / 'style_spec.json').write_text(json.dumps({
                'design_style': {'id': 1}, 'palette': {'primary': '#fff'},
                'typography': {'heading_font': 'Arial'},
            }), encoding='utf-8')
        if stage in ('outline', 'content-outline'):
            if fail_outline:
                fail_outline = False
                return {'ok': False, 'value': 'upstream timeout'}
            (deck / 'outline.json').write_text('{"pages":[{"page_no":1}]}', encoding='utf-8')
        return {'status': 'ok'}

    def publish(deck):
        nonlocal fail_publish
        if fail_publish:
            fail_publish = False
            return {'ok': False, 'error': 'temporary publication failure'}
        return {'ok': True, 'page_count': 1}

    monkeypatch.setattr(tools, 'ppt_run_stage', stage)
    monkeypatch.setattr(tools, '_publish_deck_outline', publish)
    with pytest.raises(Exception, match='outline failed'):
        tools.ppt_build_outline('Retry this presentation', page_count=1)
    deck = deck_paths[0]
    (deck / 'images' / 'preserved.png').write_bytes(b'original image')
    with pytest.raises(Exception, match='publish deck outline failed'):
        tools.ppt_build_outline('Retry this presentation', page_count=1)
    # Creating the visual contract later must not invalidate the content checkpoint.
    (deck / 'style_spec.json').write_text('{"palette": {"primary": "#fff"}}', encoding='utf-8')
    result = tools.ppt_build_outline('Retry this presentation', page_count=1)
    assert result['deck_dir'] == str(deck)
    assert calls == ['preflight', 'content-outline', 'preflight', 'content-outline', 'preflight']
    assert len(set(deck_paths)) == 1
    assert (deck / 'images' / 'preserved.png').read_bytes() == b'original image'
    assert not list((tmp_path / '.outline_builds').glob('*.json'))


def test_legacy_standard_deck_recovers_without_recreating_assets(monkeypatch, tmp_path):
    import json
    tools = _load_ppt_tools()
    monkeypatch.setattr(tools, '_conversation_root', lambda: tmp_path)
    monkeypatch.setattr(tools, '_attach_material_images_to_deck', lambda _: {'attached': 0})
    initial = tools.ppt_init_deck('A short presentation', page_count=1, ppt_mode='standard')
    deck = Path(initial['deck_dir'])
    assert json.loads((deck / 'task_pack.json').read_text(encoding='utf-8'))['ppt_mode'] == 'fast'
    pack = json.loads((deck / 'task_pack.json').read_text(encoding='utf-8'))
    pack['ppt_mode'] = 'standard'  # A deck created by an older workflow revision.
    (deck / 'task_pack.json').write_text(json.dumps(pack), encoding='utf-8')
    image = deck / 'images' / 'background.png'
    image.write_bytes(b'preserved background')
    calls = []

    def stage(deck_dir, stage):
        calls.append(stage)
        assert Path(deck_dir) == deck
        if stage == 'content-outline':
            (deck / 'outline.json').write_text('{"pages":[{"page_no":1}]}', encoding='utf-8')
        return {'status': 'ok'}

    monkeypatch.setattr(tools, 'ppt_run_stage', stage)
    monkeypatch.setattr(tools, '_publish_deck_outline', lambda _: {'ok': True, 'page_count': 1})
    result = tools.ppt_build_outline('A short presentation', page_count=1, deck_dir=str(deck))
    assert result['ppt_mode'] == 'fast'
    assert result['deck_dir'] == str(deck)
    assert calls == ['preflight', 'content-outline']
    assert image.read_bytes() == b'preserved background'


def test_workflow_deck_binding_survives_reworded_retry_and_other_workflows(monkeypatch, tmp_path):
    import json
    import pytest
    tools = _load_ppt_tools()
    monkeypatch.setattr(tools, '_conversation_root', lambda: tmp_path)
    monkeypatch.setattr(tools, '_attach_material_images_to_deck', lambda _: {'attached': 0})
    session = 'workflow-a'
    monkeypatch.setattr(tools, '_workflow_session_id', lambda: session)
    deck = Path(tools.ppt_init_deck('First presentation', page_count=1)['deck_dir'])
    (deck / 'images' / 'background.png').write_bytes(b'keep')
    repeated = tools.ppt_init_deck('Reworded recovery request', page_count=1)
    assert repeated['reused'] is True
    assert Path(repeated['deck_dir']) == deck
    session = 'workflow-b'
    other = Path(tools.ppt_init_deck('Separate presentation', page_count=1)['deck_dir'])
    assert other != deck
    session = 'workflow-a'
    assert Path(tools.ppt_find_deck()['deck_dir']) == deck
    calls = []
    failed = False

    def stage(deck_dir, stage):
        nonlocal failed
        assert Path(deck_dir) == deck
        calls.append(stage)
        if stage == 'content-outline':
            if not failed:
                failed = True
                return {'ok': False, 'value': 'temporary timeout'}
        if stage == 'content-outline':
            (deck / 'outline.json').write_text('{"pages":[{"page_no":1}]}', encoding='utf-8')
        return {'status': 'ok'}

    monkeypatch.setattr(tools, 'ppt_run_stage', stage)
    monkeypatch.setattr(tools, '_publish_deck_outline', lambda _: {'ok': True, 'page_count': 1})
    with pytest.raises(Exception, match='outline failed'):
        tools.ppt_build_outline('First wording', page_count=1)
    result = tools.ppt_build_outline('Completely different recovery wording', page_count=1, style_hint='green')
    assert Path(result['deck_dir']) == deck
    assert calls == ['preflight', 'content-outline', 'preflight', 'content-outline']
    assert (deck / 'images' / 'background.png').read_bytes() == b'keep'
    assert len(list((tmp_path / 'ppt_decks').iterdir())) == 2
