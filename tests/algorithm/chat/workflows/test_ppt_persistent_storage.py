from __future__ import annotations

import importlib.util
import json
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import Mock

import pytest


def _load_ppt_tools():
    root = Path(__file__).resolve().parents[4]
    path = root / 'workflows' / 'ppt-workflow' / 'scripts' / 'tools.py'
    spec = importlib.util.spec_from_file_location('_test_ppt_workflow_tools', path)
    assert spec is not None and spec.loader is not None
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def _write_ready_style(deck):
    (deck / 'style_spec.json').write_text(json.dumps({
        'design_style': {'id': 1}, 'color_tone': {'id': 1},
        'primary_color': {'id': 1}, 'palette': {'primary': '#fff'},
    }), encoding='utf-8')


def test_restyle_pages_scope_and_persisted_global_instruction(monkeypatch, tmp_path):
    tools = _load_ppt_tools()
    monkeypatch.setattr(tools, '_resolve_deck_dir', lambda _: tmp_path)
    (tmp_path / 'pages').mkdir()
    for page in (1, 2):
        (tmp_path / 'pages' / f'page_{page:03d}.html').write_text(f'<html>{page}</html>')
    _write_ready_style(tmp_path)
    calls = []
    monkeypatch.setattr(tools, 'ppt_run_stage', lambda *a, **kw: calls.append(kw) or {'status': 'ok'})
    result = tools.ppt_restyle_pages(str(tmp_path), '第2页留白少一点，不改文字', pages=[2])
    assert result['updated_pages'] == [2]
    assert calls[0]['edit_instruction'] == '第2页留白少一点，不改文字'
    assert 'visual_revision_instructions' not in json.loads((tmp_path / 'style_spec.json').read_text(encoding='utf-8'))
    result = tools.ppt_restyle_pages(str(tmp_path), '全部米白和橄榄绿，禁用蓝色')
    assert result['updated_pages'] == [1, 2]
    assert '全部米白和橄榄绿，禁用蓝色' in json.loads(
        (tmp_path / 'style_spec.json').read_text(encoding='utf-8'))['visual_revision_instructions']


def test_restyle_reports_partial_failure_without_claiming_global_success(monkeypatch, tmp_path):
    import pytest
    tools = _load_ppt_tools()
    monkeypatch.setattr(tools, '_resolve_deck_dir', lambda _: tmp_path)
    (tmp_path / 'pages').mkdir()
    for page in (1, 2, 3):
        (tmp_path / 'pages' / f'page_{page:03d}.html').write_text(f'<html>{page}</html>')
    _write_ready_style(tmp_path)
    calls = []

    def stage(*args, **kwargs):
        calls.append(kwargs['page'])
        if kwargs['page'] == 2:
            raise tools.ToolExecutionError('unchanged HTML')
        return {'status': 'ok'}
    monkeypatch.setattr(tools, 'ppt_run_stage', stage)
    with pytest.raises(tools.ToolExecutionError, match=r'updated_pages=\[1\]'):
        tools.ppt_restyle_pages(str(tmp_path), '改成绿色')
    assert calls == [1, 2]
    assert 'visual_revision_instructions' not in json.loads((tmp_path / 'style_spec.json').read_text(encoding='utf-8'))


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
    original = ('<html><style>#bg{background:url("../images/big image.png")}</style>'
                '<img src="../images/big image.png"></html>')
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


def test_authored_outline_retry_preserves_deck_assets_without_model_calls(monkeypatch, tmp_path):
    tools = _load_ppt_tools()
    monkeypatch.setattr(tools, '_conversation_root', lambda: tmp_path)
    monkeypatch.setattr(tools, '_attach_material_images_to_deck', lambda _: {'attached': 0})
    monkeypatch.setattr(tools, 'ppt_run_stage', lambda *a, **kw: pytest.fail('outline publication called a model'))
    saves = []

    def save(**kwargs):
        saves.append(kwargs)
        return {'ok': False, 'value': 'temporary publication failure'} if len(saves) == 1 else {'status': 'ok'}

    monkeypatch.setattr(tools, '_save_artifact', save)
    outline = '## 1. Background\n\n1. Keep this list.\n'
    with pytest.raises(tools.ToolExecutionError, match='temporary publication failure'):
        tools.ppt_build_outline('Retry this presentation', outline, page_count=1)
    checkpoint = next((tmp_path / '.outline_builds').glob('*.json'))
    deck = Path(json.loads(checkpoint.read_text())['deck_dir'])
    image = deck / 'images' / 'preserved.png'
    image.write_bytes(b'original image')
    _write_ready_style(deck)
    style = (deck / 'style_spec.json').read_bytes()
    result = tools.ppt_build_outline('Retry this presentation', outline, page_count=1)
    assert result['deck_dir'] == str(deck) and result['page_count'] == 1
    assert len(list((tmp_path / 'ppt_decks').iterdir())) == 1
    assert image.read_bytes() == b'original image'
    assert (deck / 'style_spec.json').read_bytes() == style
    assert not list((tmp_path / '.outline_builds').glob('*.json'))
    assert not (deck / 'outline.json').exists()
    assert saves[-1]['value'] == '## Background\n\n1. Keep this list.'


def test_ordered_publication_inserts_only_one_new_stable_index(monkeypatch):
    tools = _load_ppt_tools()
    order = [10, 11, 12, 13]
    reserve = Mock(return_value=99)
    save = Mock(return_value={'status': 'ok'})
    monkeypatch.setattr(tools, '_reserve_ui_slot_position', reserve)
    monkeypatch.setattr(tools, '_save_artifact', save)
    for _ in range(2):
        result = tools._publish_ordered_ppt_artifact(
            slot='slide_outline', page_no=4, value='## 4. Inserted\n\n1. Keep this point.',
            content_type='text', source_tool='ppt_publish_outline', caption='Inserted',
            order_list=order, insert_before=4, expected_total=5,
        )
        assert result['status'] == 'ok'
        assert order == [10, 11, 12, 99, 13]
        assert save.call_args.kwargs['publisher_list_index'] == 99
        assert save.call_args.kwargs['value'] == '## Inserted\n\n1. Keep this point.'
    reserve.assert_called_once()
    assert reserve.call_args.kwargs['value'] == save.call_args.kwargs['value']


def test_outline_publication_reuses_prepared_backgrounds(monkeypatch, tmp_path):
    tools = _load_ppt_tools()
    monkeypatch.setattr(tools, '_conversation_root', lambda: tmp_path)
    monkeypatch.setattr(tools, '_attach_material_images_to_deck', lambda _: {'attached': 0})
    initial = tools.ppt_init_deck('A short presentation', page_count=1, style_flow='auto',
                                  generate_background_images=True)
    deck = Path(initial['deck_dir'])
    image = deck / 'images' / 'background.png'
    image.write_bytes(b'preserved background')
    monkeypatch.setattr(tools, 'ppt_run_stage', lambda *a, **kw: pytest.fail('publication must not generate'))
    monkeypatch.setattr(tools, '_save_artifact', lambda **kw: {'ok': True})
    result = tools.ppt_build_outline('A short presentation', '## Background\nEvidence',
                                     page_count=1, deck_dir=str(deck), generate_background_images=None)
    assert result['deck_dir'] == str(deck)
    assert result['background_images_enabled'] is True
    assert image.read_bytes() == b'preserved background'


def test_workflow_deck_binding_survives_reworded_retry_and_other_workflows(monkeypatch, tmp_path):
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
    saves = []

    def save(**kwargs):
        saves.append(kwargs)
        return {'ok': False, 'value': 'temporary publication failure'} if len(saves) == 1 else {'ok': True}

    monkeypatch.setattr(tools, 'ppt_run_stage', lambda *a, **kw: pytest.fail('publication must not generate'))
    monkeypatch.setattr(tools, '_save_artifact', save)
    with pytest.raises(tools.ToolExecutionError, match='temporary publication failure'):
        tools.ppt_build_outline('First wording', '## First page\nApproved content', page_count=1)
    result = tools.ppt_build_outline('Completely different recovery wording',
                                     '## First page\nApproved content', page_count=1, style_hint='green')
    assert Path(result['deck_dir']) == deck
    assert (deck / 'images' / 'background.png').read_bytes() == b'keep'
    assert len(list((tmp_path / 'ppt_decks').iterdir())) == 2


@pytest.mark.parametrize('explicit_deck', [False, True])
def test_auto_background_prompts_publish_and_generate_without_style_model(monkeypatch, tmp_path, explicit_deck):
    from PIL import Image

    tools = _load_ppt_tools()
    monkeypatch.setattr(tools, '_conversation_root', lambda: tmp_path)
    monkeypatch.setattr(tools, '_workflow_session_id', lambda: 'background-only')
    monkeypatch.setattr(tools, '_attach_material_images_to_deck', lambda _: {'attached': 0})
    monkeypatch.setattr(tools, '_ui_slot_order_list', lambda _: [])
    monkeypatch.setattr(tools, '_save_artifact', lambda **_: {'stored': True})

    def unexpected_stage(*args, **kwargs):
        raise AssertionError('Background planning must not run a style/outline model')

    monkeypatch.setattr(tools, 'ppt_run_stage', unexpected_stage)
    initialized = tools.ppt_init_deck(
        'Two slides about weekend rest', page_count=2, style_flow='auto',
        style_hint='light and fresh', generate_background_images=True,
    )
    deck = Path(initialized['deck_dir'])
    prompts = [
        {'page_no': 1, 'prompt': '16:9 soft cream morning balcony. Calm left side, no text.'},
        {'page_no': 2, 'prompt': '16:9 soft cream park scene. Calm left side, no text.'},
    ]
    published = tools.ppt_publish_background_prompts(str(deck), prompts)
    assert published['count'] == 2
    retried = tools.ppt_init_deck('Continue the existing request', page_count=2, style_flow='auto')
    assert retried['reused'] is True
    assert retried['deck_dir'] == str(deck)
    assert json.loads((deck / 'background_prompts.json').read_text(encoding='utf-8'))['pages'] == prompts
    source = tmp_path / 'generated.png'
    Image.new('RGB', (128, 72), '#eef4e8').save(source)
    generated_prompts = []

    def generate(**kwargs):
        generated_prompts.append(kwargs['prompt'])
        return {'local_path': str(source)}

    monkeypatch.setattr(tools, 'image_generator', generate)
    result = tools.ppt_generate_background_images(str(deck) if explicit_deck else None, prompts_json=prompts)
    assert result['count'] == 2
    assert generated_prompts == [item['prompt'] for item in prompts]
    assert not (deck / 'style_spec.json').exists()
    assert not (deck / 'outline.json').exists()
