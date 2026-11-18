"""Selection previews survive platform newline conversion without losing guards."""
import importlib.util
import json
from pathlib import Path

import pytest


HTML = ('<!doctype html>\n<html><head><title>Title</title></head>\n<body>\n'
        '<img data-el="picture" src="data:image/png;base64,iVBORw0KGgo=">\n'
        '<p data-el="other">Keep</p>\n</body></html>\n')


@pytest.fixture
def tools(monkeypatch, tmp_path):
    monkeypatch.setenv('LAZYMIND_FONT_DIR', str(tmp_path / 'font-library'))
    monkeypatch.setenv('LAZYMIND_UPLOAD_ROOT', str(tmp_path / 'uploads'))
    path = Path(__file__).resolve().parents[4] / 'workflows/ppt-workflow/scripts/tools.py'
    spec = importlib.util.spec_from_file_location('ppt_selection_newlines_test', path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    generated = tmp_path / 'generated.png'
    generated.write_bytes(b'\x89PNG\r\n\x1a\nnew image')
    monkeypatch.setattr(module, 'is_model_role_available', lambda _: True)
    monkeypatch.setattr(module, 'image_editor', lambda **_: {'local_path': str(generated)})
    return module


@pytest.fixture
def windows_text_writes(monkeypatch):
    """Exercise Windows text-mode translation on every platform."""
    original_open = Path.open

    def translated_open(path, mode='r', buffering=-1, encoding=None, errors=None, newline=None):
        if 'b' not in mode and any(flag in mode for flag in ('w', 'a', 'x')) and newline is None:
            newline = '\r\n'
        return original_open(path, mode, buffering, encoding, errors, newline)

    monkeypatch.setattr(Path, 'open', translated_open)


def preview_image(tools, tmp_path, html):
    return tools.ppt_preview_selection_edit(
        artifact=html, instruction='把图片改成蓝色',
        selection={'type': 'ppt_html', 'el': 'picture', 'page': 1},
        artifact_store=str(tmp_path), slot='preview_html',
    )


@pytest.mark.parametrize('newline', ['\n', '\r\n', '\r'])
def test_image_preview_can_be_saved_and_retried_on_windows(tools, tmp_path, windows_text_writes, newline):
    html = HTML.replace('\n', newline)
    preview = preview_image(tools, tmp_path, html)
    token = preview['commit']['token']
    for _ in range(2):
        saved = tools.ppt_apply_selection_edit(
            token, artifact=html, artifact_store=str(tmp_path), slot='preview_html',
        )
        assert saved['artifact']['value'] == preview['candidate_html']
        assert '>Keep</p>' in saved['artifact']['value']
    candidate = tmp_path / 'ppt-selection-actions' / f'{token}.html'
    assert b'\r' not in candidate.read_bytes()
    assert saved['html_sha256'] == tools._html_sha256(preview['candidate_html'])


@pytest.mark.parametrize('changed_file', ['candidate', 'artifact'])
def test_newline_handling_still_rejects_changed_content(tools, tmp_path, windows_text_writes, changed_file):
    html = HTML.replace('\n', '\r\n')
    preview = preview_image(tools, tmp_path, html)
    token = preview['commit']['token']
    if changed_file == 'candidate':
        manifest = json.loads((tmp_path / 'ppt-selection-actions' / f'{token}.json').read_text())
        candidate = Path(manifest['candidate_path'])
        candidate.write_bytes(candidate.read_bytes().replace(b'Keep', b'Tampered'))
    else:
        html = html.replace('Keep', 'New revision')
    expected_error = 'candidate was modified' if changed_file == 'candidate' else 'slide changed'
    with pytest.raises(ValueError, match=expected_error):
        tools.ppt_apply_selection_edit(token, artifact=html, artifact_store=str(tmp_path), slot='preview_html')


@pytest.mark.parametrize('source_changed', [False, True])
def test_source_page_save_uses_lf_and_preserves_stale_guard(tools, tmp_path, windows_text_writes, source_changed):
    pages = tmp_path / 'deck' / 'pages'
    pages.mkdir(parents=True)
    source = pages / 'page_001.html'
    source.write_bytes(HTML.replace('\n', '\r\n').encode('utf-8'))
    (pages.parent / 'task_pack.json').write_text('{}', encoding='utf-8')
    public, _ = tools._inline_preview_images(tools._sanitize_page_html(HTML), pages.parent, source)
    artifact = tools._with_ppt_source_meta(public, source, tools._html_sha256(HTML)).replace('\n', '\r\n')
    preview = preview_image(tools, tmp_path, artifact)
    token = preview['commit']['token']
    if source_changed:
        source.write_bytes(source.read_bytes().replace(b'Keep', b'New revision'))
        with pytest.raises(ValueError, match='slide changed'):
            tools.ppt_apply_selection_edit(token, artifact=artifact, artifact_store=str(tmp_path), slot='preview_html')
        return
    saved = tools.ppt_apply_selection_edit(
        token, artifact=artifact, artifact_store=str(tmp_path), slot='preview_html',
    )
    assert b'\r' not in source.read_bytes()
    assert tools._read_ppt_source_meta(saved['artifact']['value'])['sha256'] == saved['html_sha256']
    assert '>Keep</p>' in source.read_text(encoding='utf-8')
    # The saved preview can itself be edited without a false stale-source error.
    (tmp_path / 'generated.png').write_bytes(b'\x89PNG\r\n\x1a\nnext image')
    assert preview_image(tools, tmp_path, saved['artifact']['value'])['commit']['token']


@pytest.mark.parametrize('change', [None, 'artifact_title', 'source', 'hash', 'source_style', 'extra_css'])
def test_legacy_injected_font_preview_matches_only_unchanged_source(tools, tmp_path, monkeypatch, change):
    from lazymind.chat.engine.tools.file_resources import font_library
    pages = tmp_path / 'deck' / 'pages'
    pages.mkdir(parents=True)
    source = pages / 'page_001.html'
    (pages.parent / 'task_pack.json').write_text('{}', encoding='utf-8')
    old_css = ('@font-face{font-family:"Test Font";font-weight:400;font-style:normal;'
               'src:url(data:font/woff;base64,Zm9udA==)}')
    old_block = '<style id="lazymind-local-fonts">' + old_css + '</style>'
    new_css = ('@font-face{font-family:"Test Font";font-weight:400;font-style:normal;'
               'src:url(/static-files/presentation-fonts/' + 'a' * 64 + '.ttf)}')
    monkeypatch.setattr(font_library, 'local_font_css', lambda _: new_css)
    original = HTML.replace('</title>', '</title><style>p{font-family:"Test Font"}</style>')
    if change == 'source_style':
        original = original.replace('</head>', old_block + '</head>')
    source.write_text(original, encoding='utf-8', newline='\n')
    legacy = original.replace('</head>', old_block + '</head>') if change != 'source_style' else original
    expected_hash = '0' * 64 if change == 'hash' else tools._html_sha256(original)
    artifact = tools._with_ppt_source_meta(legacy, source, expected_hash)
    if change == 'artifact_title':
        artifact = artifact.replace('<title>Title</title>', '<title>Changed</title>')
    elif change == 'source':
        source.write_text(original.replace('Keep', 'Changed'), encoding='utf-8', newline='\n')
    elif change == 'source_style':
        artifact = artifact.replace(old_css, new_css)
    elif change == 'extra_css':
        artifact = artifact.replace(old_css, old_css + 'p{display:none}')
    if change:
        with pytest.raises(ValueError) as rejected:
            preview_image(tools, tmp_path, artifact)
        assert rejected.value.error_code == 'SELECTION_STALE'
        assert not (tmp_path / 'ppt-selection-actions').exists()
        return
    preview = preview_image(tools, tmp_path, artifact)
    assert old_css not in preview['candidate_html']
    assert new_css in preview['candidate_html']
    saved = tools.ppt_apply_selection_edit(
        preview['commit']['token'], artifact=artifact, artifact_store=str(tmp_path), slot='preview_html',
    )
    assert saved['artifact']['value'] == preview['candidate_html']
    assert 'lazymind-local-fonts' not in source.read_text(encoding='utf-8')
