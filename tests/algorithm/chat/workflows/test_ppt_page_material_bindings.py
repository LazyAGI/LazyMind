from __future__ import annotations

import hashlib
import importlib.util
import json
import sys
from pathlib import Path, PureWindowsPath

import pytest
from PIL import Image

ROOT = Path(__file__).resolve().parents[4]
LIB = ROOT / 'workflows/ppt-workflow/runtime/lib'


@pytest.fixture
def runtime(monkeypatch):
    sys.path.insert(0, str(LIB))
    spec = importlib.util.spec_from_file_location(
        'ppt_material_binding_runtime', ROOT / 'workflows/ppt-workflow/runtime/scripts/run_stage.py')
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    monkeypatch.setattr(module, 'prompt_catalog', lambda _: '')
    yield module
    sys.path.remove(str(LIB))


@pytest.fixture
def deck(tmp_path):
    deck = tmp_path / 'uploads' / '演示稿 with spaces'
    (deck / 'images').mkdir(parents=True)
    (deck / 'pages').mkdir()
    for number, color in enumerate(['red', 'green', 'blue'], 1):
        Image.new('RGB', (8, 8), color).save(deck / f'images/material_{number:02d}.png')
    (deck / 'info_pack.json').write_text(json.dumps({'user_assets': {'reference_images': [
        str(deck / f'images/material_{n:02d}.png') for n in range(1, 4)
    ]}}))
    (deck / 'complete_page_prompts.json').write_text('{"version":1}')
    (deck / 'outline.json').write_text(json.dumps({'pages': [{'page_no': 1, 'title': 'Cover'}]}))
    return deck


def html(sources):
    return '<!doctype html><html><head></head><body><h1>封面</h1>' + ''.join(
        f'<img src="{source}">' for source in sources) + '</body></html>'


def old_input(deck, number, root='removed attempt'):
    file = deck / f'images/material_{number:02d}.png'
    digest = hashlib.sha256(file.read_bytes()).hexdigest()[:16]
    return str(deck.parent / root / 'inputs/material_images' / f'{number:04d}_cover-{digest}.png')


def generate(runtime, deck, brief, responses):
    calls = []

    def llm(system, query, **kwargs):
        calls.append(query)
        return responses[min(len(calls) - 1, len(responses) - 1)]
    code, result = runtime._capture_cmd(runtime.cmd_page_html_from_brief, deck, 1, brief, llm_call=llm)
    return code, result, calls


def test_complete_prompt_rebinds_removed_attempt_without_reading_it(runtime, deck):
    source = old_input(deck, 1)
    assert not Path(source).exists()
    code, result, calls = generate(runtime, deck, f'Use {source} on the cover', [html([source])])
    assert code == 0 and result['status'] == 'ok'
    saved = (deck / 'pages/page_001.html').read_text()
    assert '../images/material_01.png' in saved and source not in saved
    assert source not in calls[0]
    assert len(calls) == 1


def test_saved_prompt_binds_registered_basename_and_repairs_missing_image(runtime, deck):
    bare = 'material_01.png'
    canonical = '../images/' + bare
    brief = f'Use the panorama {bare}. Source https://example.org/{bare}'
    code, _, calls = generate(runtime, deck, brief, [html([]), html([bare])])
    assert code == 0 and len(calls) == 2
    assert calls[0] == f'Use the panorama {canonical}. Source https://example.org/{bare}'
    assert canonical in calls[1]
    assert f'src="{canonical}"' in (deck / 'pages/page_001.html').read_text()
    code, result, calls = generate(runtime, deck, f'Use /unregistered/{bare}', [html([bare])])
    assert code != 0 and not calls and 'Unregistered material image reference' in result['error']


def test_retry_with_new_input_paths_and_three_images_preserves_every_material(runtime, deck):
    for attempt in ['old attempt', 'new attempt']:
        sources = [old_input(deck, n, attempt) for n in range(1, 4)]
        code, _, calls = generate(runtime, deck, '\n'.join(sources), [html(sources)])
        assert code == 0 and len(calls) == 1
        saved = (deck / 'pages/page_001.html').read_text()
        assert saved.count('<img ') == 3
        assert all(f'../images/material_{n:02d}.png' in saved for n in range(1, 4))


def test_prose_slash_before_registered_image_is_preserved(runtime, deck):
    source = deck / 'images/material_01.png'
    canonical = '../images/material_01.png'
    prefix = 'Format landscape / 16:9, use '
    code, result, calls = generate(runtime, deck, f'{prefix}{source}', [html([canonical])])
    assert code == 0 and result['status'] == 'ok'
    assert calls == [f'{prefix}{canonical}']
    assert f'src="{canonical}"' in (deck / 'pages/page_001.html').read_text()


@pytest.mark.parametrize('wrap', ['{}', '`{}`'])
def test_missing_image_fails_after_one_repair_and_preserves_previous_page(runtime, deck, wrap):
    previous = html(['../images/material_01.png'])
    (deck / 'pages/page_001.html').write_text(previous)
    code, result, calls = generate(runtime, deck, wrap.format(old_input(deck, 1)), [html([])])
    assert code != 0 and result['status'] == 'failed'
    assert 'material_01.png' in result['error']
    assert len(calls) == 2 and 'MANDATORY CORRECTION' in calls[1]
    assert (deck / 'pages/page_001.html').read_text() == previous


def test_one_missing_member_of_three_is_repaired(runtime, deck):
    sources = [f'../images/material_{n:02d}.png' for n in range(1, 4)]
    code, _, calls = generate(runtime, deck, '\n'.join(sources), [html(sources[:2]), html(sources)])
    assert code == 0 and len(calls) == 2
    assert (deck / 'pages/page_001.html').read_text().count('<img ') == 3


def test_unknown_or_missing_material_fails_before_model_call(runtime, deck):
    for source in ['/not-authorized/cover.png', '../images/material_01.png']:
        if source.startswith('..'):
            (deck / 'images/material_01.png').unlink()
        code, result, calls = generate(runtime, deck, source, [html([])])
        assert code != 0 and calls == []
        assert 'material image' in result['error']
        assert not (deck / 'pages/page_001.html').exists()


def test_invalid_extra_image_is_not_silently_removed_and_reported_success(runtime, deck):
    code, result, calls = generate(runtime, deck, 'Text-only slide', [html(['/missing/decorative.png'])])
    assert code != 0 and result['status'] == 'failed' and len(calls) == 2
    assert not (deck / 'pages/page_001.html').exists()


def test_text_only_and_remote_text_links_are_unchanged(runtime, deck):
    brief = 'Text-only slide. Reference https://example.com/report.png'
    code, _, calls = generate(runtime, deck, brief, [html([])])
    assert code == 0 and len(calls) == 1
    assert calls[0] == brief


def test_trusted_input_alias_survives_deleted_input_and_encoded_file_uri(runtime, deck, tmp_path):
    from material_images import bind_prompt_materials, record_material_aliases
    inputs = tmp_path / 'planning' / 'inputs' / 'material_images'
    inputs.mkdir(parents=True)
    source = inputs / '封面 with spaces.png'
    source.write_bytes((deck / 'images/material_01.png').read_bytes())
    record_material_aliases(deck, [], inputs)
    uri = source.as_uri()
    source.unlink()
    rewritten, required = bind_prompt_materials(deck, uri)
    assert rewritten == '../images/material_01.png'
    assert required == ['images/material_01.png']


@pytest.mark.parametrize('origin', [
    r'C:\planning\inputs\material_images\封面 with spaces.png',
    'D:/planning/inputs/material_images/封面 with spaces.png',
])
def test_windows_material_alias_survives_deleted_source_as_file_uri(runtime, deck, tmp_path, origin):
    from material_images import bind_prompt_materials, record_material_aliases
    source = tmp_path / 'uploaded.png'
    source.write_bytes((deck / 'images/material_01.png').read_bytes())
    record_material_aliases(deck, [{'path': str(source), 'origin': origin}])
    source.unlink()

    # Exercise Windows drive URIs on every CI platform, not just the host's as_uri().
    for reference in (origin, PureWindowsPath(origin).as_uri()):
        code, result, calls = generate(runtime, deck, f'Use {reference} on the cover', [html([reference])])
        assert code == 0 and result['status'] == 'ok'
        assert len(calls) == 1 and reference not in calls[0]
        saved = (deck / 'pages/page_001.html').read_text(encoding='utf-8')
        assert '../images/material_01.png' in saved and reference not in saved

    unregistered = PureWindowsPath('Z:/unregistered/封面 with spaces.png').as_uri()
    with pytest.raises(ValueError, match='Unregistered'):
        bind_prompt_materials(deck, unregistered)


def test_material_alias_cannot_authorize_an_out_of_deck_target(runtime, deck):
    from material_images import bind_prompt_materials
    data = json.loads((deck / 'info_pack.json').read_text())
    data['user_assets']['reference_image_aliases'] = {'/inputs/secret.png': '../../secret.png'}
    (deck / 'info_pack.json').write_text(json.dumps(data))
    with pytest.raises(ValueError, match='Unregistered'):
        bind_prompt_materials(deck, '/inputs/secret.png')


def test_published_preview_retains_registered_images(runtime, deck, monkeypatch):
    source = old_input(deck, 1)
    assert generate(runtime, deck, source, [html([source])])[0] == 0
    spec = importlib.util.spec_from_file_location(
        'ppt_material_publication_tools', ROOT / 'workflows/ppt-workflow/scripts/tools.py')
    tools = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(tools)
    monkeypatch.setenv('LAZYMIND_UPLOAD_ROOT', str(deck.parent))
    import lazymind.chat.engine.tools.file_resources.font_library as fonts
    monkeypatch.setattr(fonts, 'local_font_css', lambda _: '')
    published = []
    monkeypatch.setattr(
        tools, '_publish_ordered_ppt_artifact', lambda **kw: published.append(kw) or {'ok': True})
    result = tools._publish_one_page(deck, 1, with_notes=False, slot_orders={'preview_html': []})
    assert result['ok']
    assert '<img ' in published[0]['value'] and '/static-files/' in published[0]['value']
    assert source not in published[0]['value']


def test_remote_image_hash_cannot_be_rebound_or_satisfy_required_material(runtime, deck):
    from material_images import normalize_material_sources
    digest = hashlib.sha256((deck / 'images/material_01.png').read_bytes()).hexdigest()[:16]
    remote = f'https://example.com/cover-{digest}.png'
    assert normalize_material_sources(html([remote]), deck) == html([remote])
    code, result, calls = generate(runtime, deck, '../images/material_01.png', [html([remote])])
    assert code != 0 and result['status'] == 'failed' and len(calls) == 2


def test_legacy_outline_uses_its_inherited_copy_without_requiring_both_copies(runtime, deck):
    (deck / 'complete_page_prompts.json').unlink()
    (deck / 'outline.json').write_text(json.dumps({'pages': [
        {'page_no': 1, 'use_image': {'reference_image_index': 0}}
    ]}))
    (deck / 'asset_plan.json').write_text(json.dumps({'pages': [{'page_no': 1, 'slots': []}]}))
    (deck / 'style_spec.json').write_text('{}')
    (deck / 'task_pack.json').write_text('{}')
    code, _, calls = generate(runtime, deck, 'Use ../images/material_01.png', [
        html(['../images/page_001_inherited.png'])
    ])
    assert code == 0 and len(calls) == 1
    assert (deck / 'pages/page_001.html').read_text().count('<img ') == 1
    assert (deck / 'images/page_001_inherited.png').read_bytes() == (deck / 'images/material_01.png').read_bytes()


def test_imported_material_origin_is_recorded_only_for_matching_bytes(runtime, deck, tmp_path):
    from material_images import bind_prompt_materials, record_material_aliases
    source = tmp_path / 'uploaded.png'
    source.write_bytes((deck / 'images/material_02.png').read_bytes())
    unrelated = tmp_path / 'unrelated.png'
    unrelated.write_bytes(b'unregistered content')
    record_material_aliases(deck, [
        {'path': str(source), 'origin': '/old-session/original.png'},
        {'path': str(unrelated), 'origin': '/old-session/unrelated.png'},
    ])
    source.unlink()
    rewritten, required = bind_prompt_materials(deck, '/old-session/original.png')
    assert rewritten == '../images/material_02.png' and required == ['images/material_02.png']
    with pytest.raises(ValueError, match='Unregistered'):
        bind_prompt_materials(deck, '/old-session/unrelated.png')


def set_backgrounds(deck, paths, *, enabled=True):
    (deck / 'background_images.json').write_text(json.dumps({
        'enabled': enabled,
        'pages': [{'page_no': page, 'local_path': path} for page, path in enumerate(paths, 1)],
    }))


def background_snapshot(deck, path):
    digest = hashlib.sha256((deck / path).read_bytes()).hexdigest()[:16]
    return f'inputs/background_images/0001_{Path(path).stem}-{digest}.png'


def test_four_registered_backgrounds_bind_without_foreground_images(runtime, deck):
    (deck / 'info_pack.json').write_text('{}')
    paths = [f'images/page_{page:03d}_background.png' for page in range(1, 5)]
    for page, path in enumerate(paths, 1):
        Image.new('RGB', (8, 8), (page, 0, 0)).save(deck / path)
    set_backgrounds(deck, paths)
    for page, path in enumerate(paths, 1):
        snapshot = background_snapshot(deck, path)
        calls = []

        def llm(_system, query, **_kwargs):
            calls.append(query)
            return ('<!doctype html><html><head></head><body>'
                    '<div id="bg"></div><h1>Slide content</h1></body></html>')

        code, result = runtime._capture_cmd(
            runtime.cmd_page_html_from_brief, deck, page, f'Use `{snapshot}` as the background', llm_call=llm,
        )
        assert code == 0 and result['background_restored']
        assert calls == [f'Use `../{path}` as the background']
        output = (deck / f'pages/page_{page:03d}.html').read_text()
        assert f'background-image:url("../{path}")' in output
        assert '<img' not in output


def test_background_snapshot_remains_distinct_from_identical_foreground(runtime, deck):
    from material_images import bind_prompt_materials

    background = 'images/page_001_background.png'
    (deck / background).write_bytes((deck / 'images/material_01.png').read_bytes())
    set_backgrounds(deck, [background])
    snapshot = background_snapshot(deck, background)
    win_uri = PureWindowsPath(f'C:/removed-attempt/{snapshot}').as_uri()
    resolved = runtime._resolve_background_image(deck, 1)
    brief = f'Use `{win_uri}` as background and `../images/material_01.png` as evidence'
    rewritten, required = bind_prompt_materials(deck, brief, background_image=resolved)
    assert rewritten == f'Use `../{background}` as background and `../images/material_01.png` as evidence'
    assert required == ['images/material_01.png']
    with pytest.raises(ValueError, match='Ambiguous material image reference'):
        bind_prompt_materials(deck, '/' + snapshot.split('/')[-1], background_image=resolved)

    css_only = ('<!doctype html><html><head></head><body>'
                '<div id="bg"></div><h1>Slide content</h1></body></html>')
    code, result, calls = generate(runtime, deck, brief, [css_only])
    assert code != 0 and 'material_01.png' in result['error']
    assert len(calls) == 2


@pytest.mark.parametrize('failure', ['disabled', 'wrong_page', 'missing', 'outside'])
def test_unavailable_background_snapshot_fails_before_model_call(runtime, deck, tmp_path, failure):
    background = 'images/page_001_background.png'
    Image.new('RGB', (8, 8), 'black').save(deck / background)
    snapshot = background_snapshot(deck, background)
    if failure == 'outside':
        outside = tmp_path / 'outside.png'
        outside.write_bytes((deck / background).read_bytes())
        set_backgrounds(deck, [str(outside)])
    elif failure == 'wrong_page':
        set_backgrounds(deck, ['images/other.png', background])
    else:
        set_backgrounds(deck, [background], enabled=failure != 'disabled')
        if failure == 'missing':
            (deck / background).unlink()

    code, result, calls = generate(runtime, deck, snapshot, [html([])])
    assert code != 0 and 'Unregistered material image reference' in result['error']
    assert calls == []
