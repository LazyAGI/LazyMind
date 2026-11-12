from zipfile import ZipFile
from unittest.mock import Mock
from types import SimpleNamespace
import pytest
from pptx import Presentation
from pptx.util import Inches, Pt
from lazymind.chat.engine.tools.file_resources.pptx_content_edit import inspect_content
from lazymind.chat.engine.tools.file_resources.pptx_font_edit import edit_fonts


@pytest.fixture
def deck(tmp_path, monkeypatch):
    from lazymind.chat.engine.tools.file_resources import presentation_edit
    from lazymind.chat.engine.tools.file_resources.font_library import register_font
    monkeypatch.setenv('LAZYMIND_FONT_DIR', str(tmp_path / 'font-library'))
    monkeypatch.setenv('LAZYMIND_UPLOAD_ROOT', str(tmp_path / 'uploads'))
    register_font(make_font(tmp_path))
    monkeypatch.setattr(presentation_edit, 'available_font_families', lambda: {'Test Font'})
    prs = Presentation()
    for p in range(2):
        slide = prs.slides.add_slide(prs.slide_layouts[1])
        slide.shapes.title.text = f'Title {p}'
        box = slide.shapes.add_textbox(Inches(1), Inches(2), Inches(5), Inches(1))
        run = box.text_frame.paragraphs[0].add_run()
        run.text = '正文 text'
        run.font.size, run.font.bold = Pt(20), True
        slide.notes_slide.notes_text_frame.text = 'Keep notes'
    path = tmp_path / 'source.pptx'
    prs.save(path)
    return path


@pytest.mark.parametrize('scope', ['all', 'page', 'slot', 'title'])
def test_font_scope_preserves_unselected_parts(deck, tmp_path, scope, monkeypatch):
    from itertools import count
    from fontTools.ttLib.tables import _h_e_a_d
    # Exercise validation across a clock tick without a slow, flaky sleep.
    ticks = count(4000000000)
    monkeypatch.setattr(_h_e_a_d, 'timestampNow', lambda: next(ticks))
    info = inspect_content(deck)
    slot = next(s['id'] for s in info['pages'][0]['slots'] if s['text'] == '正文 text')
    kwargs = {'pages': [1]} if scope in {'page', 'title'} else {}
    if scope == 'slot':
        kwargs['slot_ids'] = [slot]
    if scope == 'title':
        kwargs['text_scope'] = 'titles'
    out = tmp_path / 'edited.pptx'
    stats = edit_fonts(deck, out, info['sha256'], 'Test Font', **kwargs)
    assert stats['updated_font_pages'] == ([1, 2] if scope == 'all' else [1])
    result = Presentation(out)
    for before, after in zip(Presentation(deck).slides, result.slides):
        assert [s.text for s in before.shapes if s.has_text_frame] == [s.text for s in after.shapes if s.has_text_frame]
        run = after.shapes[-1].text_frame.paragraphs[0].runs[0]
        assert run.font.size == Pt(20) and run.font.bold is True
    with ZipFile(deck) as before, ZipFile(out) as after:
        for name in before.namelist():
            metadata = {'ppt/presentation.xml', 'ppt/_rels/presentation.xml.rels', '[Content_Types].xml'}
            if name not in metadata | ({'ppt/slides/slide1.xml', 'ppt/slides/slide2.xml'}
                                       if scope == 'all' else {'ppt/slides/slide1.xml'}):
                assert before.read(name) == after.read(name), name
        assert any(n.startswith('ppt/fonts/') for n in after.namelist())
        assert b'embeddedFontLst' in after.read('ppt/presentation.xml')
    assert stats['regenerated_pages'] == []
    assert stats['embedded_fonts'][0]['family'] == 'Test Font'
    assert result.slides[0].shapes[-1].text_frame.paragraphs[0].runs[0].font.name == (
        None if scope == 'title' else 'Test Font')


@pytest.mark.parametrize('kwargs', [{'pages': [99]}, {'pages': [1.5]},
                         {'slot_ids': ['1:999']}, {'slot_ids': []}, {'text_scope': 'guess'}])
def test_invalid_scope_never_publishes(deck, tmp_path, kwargs):
    out = tmp_path / 'invalid.pptx'
    with pytest.raises(ValueError):
        edit_fonts(deck, out, inspect_content(deck)['sha256'], 'Test Font', **kwargs)
    assert not out.exists()


def test_missing_font_and_stale_hash_fail(deck, tmp_path):
    out = tmp_path / 'invalid.pptx'
    with pytest.raises(ValueError, match='不可用'):
        edit_fonts(deck, out, inspect_content(deck)['sha256'], 'Not Installed')
    with pytest.raises(ValueError, match='Deck changed'):
        edit_fonts(deck, out, 'stale', 'Test Font')
    assert not out.exists()


@pytest.mark.parametrize('formatting', [False, True])
def test_native_font_edit_publishes_compact_receipt(deck, tmp_path, monkeypatch, formatting):
    from lazymind.chat.engine.subagent import tools
    monkeypatch.setattr(tools, '_public_workflow_artifacts', lambda *a: [{
        'artifact_id': 'a1', 'revision': 3, 'draft_version': 2, 'selected': True,
        'validity': 'effective', 'value': {'path': str(deck)}}])
    monkeypatch.setattr(
        tools.AttachmentEditDraft,
        'for_current_conversation',
        lambda p: SimpleNamespace(
            root=str(tmp_path),
            effective_path=p))
    client = Mock()
    client.patch_artifact.return_value.result = {
        'artifact_id': 'a2', 'revision': 4, 'value': {
            'content_base64': 'HIDDEN'}}
    monkeypatch.setattr(tools, '_workflow_client', lambda: client)
    args = {'style_edits': [{'pages': [1], 'bold': False, 'italic': True, 'font_size_pt': 18}]
            } if formatting else {'target_font': 'Test Font', 'pages': [1]}
    result = tools.edit_presentation('native_pptx', 'format_text' if formatting else 'replace_fonts', **args,
                                     source_sha256=inspect_content(deck)['sha256'], workflow_session_id='session')
    assert result['updated_style_pages' if formatting else 'updated_font_pages'] == [1]
    assert result['workflow_artifact']['revision'] == 4
    assert 'HIDDEN' not in str(result)
    assert client.patch_artifact.call_args.kwargs['base_draft_version'] == 2
    if formatting:
        import base64
        from io import BytesIO
        payload = client.patch_artifact.call_args.args[2]['content_base64']
        generated = Presentation(BytesIO(base64.b64decode(payload)))
        run = generated.slides[0].shapes[-1].text_frame.paragraphs[0].runs[0]
        assert run.font.bold is False and run.font.italic is True and run.font.size == Pt(18)


def test_font_download_is_opt_in_and_allowlisted(monkeypatch):
    from lazymind.chat.engine.tools.file_resources import font_library, presentation_edit
    monkeypatch.setattr(presentation_edit, 'available_font_families', lambda: {'Test Font'})
    assert font_library.ensure_font('test font', True) == 'Test Font'
    for name, download in [('Noto Serif SC', False), ('宋体', True), ('https://localhost/font.ttf', True)]:
        with pytest.raises(ValueError, match='不可用'):
            font_library.ensure_font(name, download)


def test_native_format_properties_preserve_content_and_other_parts(deck, tmp_path, monkeypatch):
    from lazymind.chat.engine.tools.file_resources import pptx_font_edit
    info = inspect_content(deck)
    slot = next(s['id'] for s in info['pages'][0]['slots'] if s['text'] == '正文 text')
    monkeypatch.setattr(pptx_font_edit, 'ensure_font', lambda *a: pytest.fail('format-only must not resolve fonts'))
    out = tmp_path / 'formatted.pptx'
    pptx_font_edit.edit_style_batch(deck, out, info['sha256'], [{
        'slot_ids': [slot], 'font_size_pt': 18, 'bold': False,
        'italic': True, 'underline': True, 'text_color': '#123ABC'}])
    run = Presentation(out).slides[0].shapes[-1].text_frame.paragraphs[0].runs[0]
    assert run.text == '正文 text'
    assert run.font.size == Pt(18) and run.font.bold is False
    assert run.font.italic is True and run.font.underline is True
    assert str(run.font.color.rgb) == '123ABC'
    assert run.font.name is None
    with ZipFile(deck) as before, ZipFile(out) as after:
        for name in before.namelist():
            if name != 'ppt/slides/slide1.xml':
                assert before.read(name) == after.read(name), name


def test_style_batch_handles_multiple_scopes_and_explicit_false(deck, tmp_path):
    from lazymind.chat.engine.tools.file_resources.pptx_font_edit import edit_style_batch
    from types import MappingProxyType
    info = inspect_content(deck)
    slot = next(s['id'] for s in info['pages'][1]['slots'] if s['text'] == '正文 text')
    out = tmp_path / 'batch.pptx'
    stats = edit_style_batch(deck, out, info['sha256'], (
        MappingProxyType({'pages': (1,), 'text_scope': 'titles', 'font_size_pt': 24, 'bold': True}),
        MappingProxyType({'slot_ids': (slot,), 'italic': True, 'bold': False, 'underline': False}),
    ))
    result = Presentation(out)
    assert stats['updated_style_pages'] == [1, 2]
    title = result.slides[0].shapes.title.text_frame.paragraphs[0].runs[0]
    assert title.font.size == Pt(24) and title.font.bold is True
    body = result.slides[1].shapes[-1].text_frame.paragraphs[0].runs[0]
    assert body.font.italic is True and body.font.bold is False and body.font.underline is False
    assert body.font.size == Pt(20)


def test_style_batch_failure_is_atomic_and_size_overflow_is_blocked(deck, tmp_path):
    from lazymind.chat.engine.tools.file_resources.pptx_font_edit import edit_style_batch
    out = tmp_path / 'failed.pptx'
    digest = inspect_content(deck)['sha256']
    with pytest.raises(ValueError, match='not found'):
        edit_style_batch(deck, out, digest, [{'pages': [1], 'italic': True}, {'slot_ids': ['2:999'], 'bold': False}])
    assert not out.exists()
    with pytest.raises(ValueError, match='overflow'):
        edit_style_batch(deck, out, digest, [{'pages': [1], 'font_size_pt': 400}])
    assert not out.exists()
    assert inspect_content(deck)['sha256'] == digest


@pytest.mark.parametrize('values', [{'bold': 'false'}, {'italic': 1}, {'underline': 'none'},
                                    {'font_size_pt': True}, {'font_size_pt': float('nan')}, {'font_size_pt': 0},
                                    {'font_size_pt': 401}, {'text_color': 'red'}, {'text_color': '#12345Z'}])
def test_text_format_validation_rejects_invalid_values(values):
    from lazymind.chat.engine.tools.file_resources.text_format import text_format
    with pytest.raises(ValueError):
        text_format(**values)


def test_download_validates_and_registers_font_with_license(tmp_path, monkeypatch):
    from contextlib import contextmanager
    from fontTools.fontBuilder import FontBuilder
    from fontTools.pens.ttGlyphPen import TTGlyphPen
    from lazymind.chat.engine.tools.file_resources import font_library, presentation_edit
    import httpx
    builder = FontBuilder(1000, isTTF=True)
    builder.setupGlyphOrder(['.notdef'])
    builder.setupCharacterMap({})
    builder.setupGlyf({'.notdef': TTGlyphPen(None).glyph()})
    builder.setupHorizontalMetrics({'.notdef': (500, 0)})
    builder.setupHorizontalHeader(ascent=800, descent=-200)
    builder.setupNameTable({'familyName': 'Download Test', 'styleName': 'Regular',
                           'fullName': 'Download Test Regular', 'psName': 'DownloadTest-Regular'})
    builder.setupOS2()
    builder.setupPost()
    source = tmp_path / 'test.ttf'
    builder.save(source)
    payload = source.read_bytes()
    monkeypatch.setenv('LAZYMIND_FONT_DIR', str(tmp_path / 'library'))
    monkeypatch.setattr(font_library, 'DOWNLOAD_FONTS', {'Download Test': 'downloadtest/font.ttf'})
    monkeypatch.setattr(presentation_edit, 'available_font_families',
                        lambda: {f['family'] for f in font_library.local_fonts()})
    response = SimpleNamespace(raise_for_status=lambda: None, iter_bytes=lambda: iter([payload]))

    @contextmanager
    def stream(*args):
        yield response
    client = Mock()
    client.__enter__ = Mock(return_value=client)
    client.__exit__ = Mock(return_value=False)
    client.get.return_value = SimpleNamespace(raise_for_status=lambda: None, text='SIL OPEN FONT LICENSE Version 1.1')
    client.stream = stream
    factory = Mock(return_value=client)
    monkeypatch.setattr(httpx, 'Client', factory)
    assert font_library.ensure_font('Download Test', True) == 'Download Test'
    assert len(font_library.local_fonts()) == 1
    assert list((tmp_path / 'library').glob('*.OFL.txt'))
    assert font_library.ensure_font('Download Test', True) == 'Download Test'
    factory.assert_called_once_with(timeout=60, follow_redirects=False)


def make_font(folder, family='Test Font', fs_type=0, text='正文 textTitle0123456789BodyKeep notes'):
    from fontTools.fontBuilder import FontBuilder
    from fontTools.pens.ttGlyphPen import TTGlyphPen
    builder = FontBuilder(1000, isTTF=True)
    chars = sorted(set(text))
    glyphs = ['.notdef'] + [f'u{ord(c):04X}' for c in chars]
    builder.setupGlyphOrder(glyphs)
    builder.setupCharacterMap({ord(c): g for c, g in zip(chars, glyphs[1:])})
    builder.setupGlyf({g: TTGlyphPen(None).glyph() for g in glyphs})
    builder.setupHorizontalMetrics({g: (500, 0) for g in glyphs})
    builder.setupHorizontalHeader(ascent=800, descent=-200)
    builder.setupNameTable({'familyName': family, 'styleName': 'Regular',
                           'fullName': family + ' Regular', 'psName': family.replace(' ', '') + '-Regular'})
    builder.setupOS2(fsType=fs_type)
    builder.setupPost()
    path = folder / (family + '.ttf')
    builder.save(path)
    return path


def test_embedded_font_subset_is_stable_across_clock_ticks(deck, monkeypatch):
    from itertools import count
    from fontTools.ttLib.tables import _h_e_a_d
    from lazymind.chat.engine.tools.file_resources.font_library import local_fonts
    from lazymind.chat.engine.tools.file_resources.pptx_font_embedding import eot_data
    ticks = count(4000000000)
    monkeypatch.setattr(_h_e_a_d, 'timestampNow', lambda: next(ticks))
    record, = local_fonts()
    assert eot_data(record, '正文Title') == eot_data(record, '正文Title')


def test_font_resource_failures_do_not_change_source_or_destination(deck, tmp_path, monkeypatch):
    from lazymind.chat.engine.tools.file_resources.font_library import register_font
    from lazymind.chat.engine.tools.file_resources import presentation_edit
    monkeypatch.setattr(presentation_edit, 'available_font_families', lambda: {'Restricted', 'No Chinese'})
    register_font(make_font(tmp_path, 'Restricted', fs_type=4))
    register_font(make_font(tmp_path, 'No Chinese', text='text'))
    original = deck.read_bytes()
    output = tmp_path / 'unchanged.pptx'
    output.write_bytes(b'previous output')
    for family, message in [('Restricted', '不允许'), ('No Chinese', '缺少')]:
        with pytest.raises(ValueError, match=message):
            edit_fonts(deck, output, inspect_content(deck)['sha256'], family, pages=[1])
        assert deck.read_bytes() == original
        assert output.read_bytes() == b'previous output'


def test_repeated_font_edit_reuses_resources_and_preserves_other_slides(deck, tmp_path):
    first, second = tmp_path / 'first.pptx', tmp_path / 'second.pptx'
    edit_fonts(deck, first, inspect_content(deck)['sha256'], 'Test Font', pages=[1])
    edit_fonts(first, second, inspect_content(first)['sha256'], 'Test Font', pages=[2], text_scope='titles')
    with ZipFile(first) as before, ZipFile(second) as after:
        assert before.read('ppt/slides/slide1.xml') == after.read('ppt/slides/slide1.xml')
        fonts = [n for n in after.namelist() if n.startswith('ppt/fonts/')]
        assert len(fonts) == 1
        from lazymind.chat.engine.tools.file_resources.font_library import local_fonts, font_library_root
        raw = (font_library_root() / local_fonts()[0]['file']).read_bytes()
        import io
        import struct
        from fontTools.ttLib import TTFont
        embedded = after.read(fonts[0])
        size = struct.unpack_from('<I', embedded, 4)[0]
        with TTFont(io.BytesIO(embedded[-size:])) as subset_font, TTFont(io.BytesIO(raw)) as original:
            assert set(subset_font.getBestCmap()) < set(original.getBestCmap())
            assert set(map(ord, '正文 textTitle01Keep notes')) <= set(subset_font.getBestCmap())
        assert b'saveSubsetFonts="1"' in after.read('ppt/presentation.xml')
        from lxml import etree
        root = etree.fromstring(after.read('ppt/presentation.xml'))
        assert len(root.findall('.//{*}embeddedFont')) == 1


@pytest.fixture
def html_tools(monkeypatch, deck):
    import importlib.util
    from pathlib import Path
    spec = importlib.util.spec_from_file_location(
        'portable_html_tools', Path(__file__).parents[2]
        / 'workflows/ppt-workflow/scripts/tests/test_style_flow.py')
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    monkeypatch.setattr(module.TOOLS, 'available_font_families', lambda: {'Test Font'})
    return module.TOOLS


@pytest.mark.parametrize('scope,pages', [('all', [1]), ('titles', [1]), ('titles', None), ('all', None)])
def test_html_font_edit_packages_bytes_and_never_generates(html_tools, tmp_path, monkeypatch, scope, pages):
    folder = tmp_path / 'html'
    (folder / 'pages').mkdir(parents=True)
    original = ('<html><head></head><body><h1 data-el="title"><span style="font-family:Old">Title 1</span></h1>'
                '<p data-el="body">正文 text</p><script>const unchanged = 123;</script></body></html>')
    paths = [folder / f'pages/page_{p:03}.html' for p in (1, 2)]
    for path in paths:
        path.write_text(original)
    monkeypatch.setattr(html_tools, '_resolve_deck_dir', lambda _: folder)
    publisher = Mock(return_value={'published_count': len(pages or [1, 2]), 'failed_count': 0})
    monkeypatch.setattr(html_tools, '_publish_pages_from_disk', publisher)
    model = Mock(side_effect=AssertionError('font replacement must never generate'))
    monkeypatch.setattr(html_tools, '_run_stage_inprocess', model)
    result = html_tools.ppt_replace_deck_fonts(str(folder), 'Test Font', pages=pages, text_scope=scope)
    assert result['regenerated_pages'] == []
    assert publisher.call_args.args[1] == (pages or [1, 2])
    changed = paths[0].read_text()
    assert 'data-lazymind-font-required="true"' in changed
    assert '/static-files/presentation-fonts/' in changed
    assert 'data:font/' not in changed
    assert '<script>const unchanged = 123;</script>' in changed
    assert 'font-family:Old' not in changed
    if scope == 'titles':
        assert '<p data-el="body">正文 text</p>' in changed
    if pages:
        assert paths[1].read_text() == original
        assert not (folder / 'style_spec.json').exists()
    model.assert_not_called()


def test_html_missing_glyph_does_not_write_or_publish_any_page(html_tools, tmp_path, monkeypatch):
    folder = tmp_path / 'html'
    (folder / 'pages').mkdir(parents=True)
    paths = [folder / f'pages/page_{p:03}.html' for p in (1, 2)]
    for p, path in enumerate(paths):
        path.write_text('<html><body><h1>' + ('Title' if p == 0 else '缺字') + '</h1></body></html>')
    originals = [p.read_bytes() for p in paths]
    monkeypatch.setattr(html_tools, '_resolve_deck_dir', lambda _: folder)
    publisher = Mock()
    monkeypatch.setattr(html_tools, '_publish_pages_from_disk', publisher)
    with pytest.raises(RuntimeError, match='缺少'):
        html_tools.ppt_replace_deck_fonts(str(folder), 'Test Font')
    assert [p.read_bytes() for p in paths] == originals
    publisher.assert_not_called()


@pytest.mark.parametrize('stage', ['page-html', 'batch-page-html', 'style', 'outline'])
def test_font_followup_cannot_enter_generation_stage(html_tools, tmp_path, monkeypatch, stage):
    (tmp_path / 'pages').mkdir()
    original = '<html><body>Keep content</body></html>'
    path = tmp_path / 'pages/page_001.html'
    path.write_text(original)
    monkeypatch.setattr(html_tools, '_resolve_deck_dir', lambda _: tmp_path)
    monkeypatch.setattr(html_tools, '_require_html_output', lambda: None)
    monkeypatch.setattr(html_tools, 'require_context', lambda: SimpleNamespace(params={
        'current_user_input': '第一页字体换成 Test Font，不要重新生成',
    }))
    model = Mock(side_effect=AssertionError('generation reached'))
    monkeypatch.setattr(html_tools, '_run_stage_inprocess', model)
    with pytest.raises(RuntimeError, match='ppt_replace_deck_fonts'):
        html_tools.ppt_run_stage(str(tmp_path), stage, page=1)
    assert path.read_text() == original
    model.assert_not_called()


def test_selected_html_font_edit_also_carries_font_bytes(html_tools):
    original = ('<html><head></head><body><div data-el="title"><span>Title</span></div>'
                '<p data-el="body">Body</p></body></html>')
    updated, _, _, _ = html_tools._apply_html_ops(original, [
        {'op': 'set_style', 'el': 'title', 'styles': {'font-family': 'Test Font'}}])
    assert '<p data-el="body">Body</p>' in updated
    assert 'data-lazymind-font-required="true"' in updated


def test_import_font_attachment_is_available_to_scoped_native_edit(deck, tmp_path, monkeypatch):
    from lazymind.chat.engine.subagent import tools
    from lazymind.chat.engine.tools.file_resources import font_library, presentation_edit
    source = make_font(tmp_path, 'Uploaded Font')
    monkeypatch.setattr(tools, '_resolve_attachment', lambda filename, turn: (str(source), None))
    monkeypatch.setattr(tools, '_materialize_local_path', lambda value: value)
    result = tools.edit_presentation(source.name, 'import_font')
    assert result['portable'] is True and result['font']['family'] == 'Uploaded Font'
    monkeypatch.setattr(presentation_edit, 'available_font_families',
                        lambda: {r['family'] for r in font_library.local_fonts()})
    output = tmp_path / 'uploaded-font.pptx'
    report = edit_fonts(deck, output, inspect_content(deck)['sha256'], 'Uploaded Font', pages=[1])
    assert report['embedded_fonts'][0]['family'] == 'Uploaded Font'


def test_html_without_head_keeps_fonts_after_publication_sanitizer(html_tools):
    from lazymind.chat.engine.tools.file_resources.font_library import local_font_css
    original = '<html><body><h1 data-el="title">Title</h1><p>Body</p></body></html>'
    updated, _, _, _ = html_tools._apply_html_ops(original, [
        {'op': 'set_style', 'el': 'title', 'styles': {'font-family': 'Test Font'}}])
    assert 'data-lazymind-font-required="true"' in html_tools._sanitize_page_html(updated)
    assert local_font_css(updated) == ''  # Do not duplicate large font resources in preview.


def test_cloud_font_library_registers_verified_font_and_preserves_license(tmp_path, monkeypatch):
    import hashlib
    import json
    from lazymind import presentation_resources
    from lazymind.chat.engine.tools.file_resources import font_library, presentation_edit
    root = tmp_path / 'cloud'
    root.mkdir()
    source = make_font(root, family='Cloud Test Font')
    (root / 'OFL.txt').write_text('SIL OPEN FONT LICENSE test fixture')
    (root / 'manifest.json').write_text(json.dumps({'families': [{
        'family': 'Cloud Test Font', 'files': [{'file': source.name, 'weight': 400, 'variable': False,
                                               'sha256': hashlib.sha256(source.read_bytes()).hexdigest()}],
    }]}))
    monkeypatch.setenv('LAZYMIND_FONT_DIR', str(tmp_path / 'managed'))
    monkeypatch.setattr(presentation_resources, 'font_families', lambda: ['Cloud Test Font'])
    monkeypatch.setattr(presentation_resources, 'ensure_resource', lambda kind: root)
    monkeypatch.setattr(presentation_edit, 'available_font_families', lambda: set())
    assert font_library.ensure_font('cloud test font', True) == 'Cloud Test Font'
    record, = font_library.local_fonts()
    assert record['family'] == 'Cloud Test Font'
    assert (font_library.font_library_root() / (record['id'] + '.OFL.txt')).read_text().startswith('SIL')
    source.write_bytes(b'corrupted')
    with pytest.raises(ValueError, match='checksum'):
        font_library.ensure_font('Cloud Test Font', True)


def test_missing_font_has_actionable_error_code_without_substitution(monkeypatch):
    from lazymind.chat.engine.tools.file_resources import font_library, presentation_edit
    monkeypatch.setattr(presentation_edit, 'available_font_families', lambda: {'Songti SC', '宋体-简'})
    with pytest.raises(font_library.FontUnavailableError) as error:
        font_library.ensure_font('宋体', True)
    assert error.value.error_code == 'FONT_UNAVAILABLE'
    assert '宋体' in str(error.value)


@pytest.mark.parametrize('fs_type', [0, 256])
def test_html_font_references_verified_shared_resource(tmp_path, monkeypatch, fs_type):
    import io
    import re
    from fontTools.ttLib import TTFont
    from lazymind.chat.engine.tools.file_resources.font_library import register_font, local_font_css
    from lazymind.chat.engine.tools.file_resources.portable_fonts import package_html_fonts
    monkeypatch.setenv('LAZYMIND_FONT_DIR', str(tmp_path / 'library'))
    monkeypatch.setenv('LAZYMIND_UPLOAD_ROOT', str(tmp_path / 'uploads'))
    record = register_font(make_font(tmp_path, fs_type=fs_type, text='正文TitleUnused'))
    html = package_html_fonts('<html><head></head><body>正文Title</body></html>', 'Test Font', [record])
    resource = re.search(r'src:url\((/static-files/presentation-fonts/[^)]+)\)', html).group(1)
    data = (tmp_path / 'uploads' / resource.removeprefix('/static-files/')).read_bytes()
    assert data == (tmp_path / 'library' / record['file']).read_bytes()
    assert 'data:font/' not in html
    assert len(html.encode()) < 1000
    with TTFont(io.BytesIO(data)) as font:
        cmap = font.getBestCmap()
        assert all(ord(char) in cmap for char in '正文Title')
        assert ord('U') in cmap  # Preview resources remain reusable after text edits.
    again = package_html_fonts(html, 'Test Font', [record], text='Unused')
    assert again == html
    assert len(list((tmp_path / 'uploads/presentation-fonts').iterdir())) == 1
    assert local_font_css(html) == ''
    # An unselected intermediate weight must not reintroduce a full-family font.
    from lazymind.chat.engine.tools.file_resources import font_library
    monkeypatch.setattr(font_library, 'local_fonts', lambda: [{**record, 'id': 'other-weight'}])
    assert local_font_css(html) == ''


def test_generated_songti_page_references_font_before_publication(tmp_path, monkeypatch):
    import importlib.util
    from pathlib import Path
    from lazymind.chat.engine.tools.file_resources import font_library, presentation_edit
    script = Path(__file__).parents[2] / 'workflows/ppt-workflow/runtime/scripts/run_stage.py'
    spec = importlib.util.spec_from_file_location('songti_run_stage', script)
    stage = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(stage)
    monkeypatch.setenv('LAZYMIND_FONT_DIR', str(tmp_path / 'fonts'))
    monkeypatch.setenv('LAZYMIND_UPLOAD_ROOT', str(tmp_path / 'uploads'))
    font_library.register_font(make_font(tmp_path, family='Songti SC'))
    monkeypatch.setattr(presentation_edit, 'available_font_families', lambda: {'Songti SC'})
    html = ('<html><head><style>body{font-family:"Songti SC",serif}</style></head>'
            '<body><h1>正文Title</h1></body></html>')
    monkeypatch.setattr(stage, 'llm', lambda *a, **kw: html)
    monkeypatch.setattr(stage, 'prompt_catalog', lambda _: '')
    code, result = stage._capture_cmd(stage._write_page_html_from_query, tmp_path, 1, '正文',
                                      page_plan={}, inherited_image=None, background_image=None,
                                      prompt_mode='deterministic', language='zh')
    assert code == 0, result
    saved = (tmp_path / 'pages/page_001.html').read_text()
    assert '/static-files/presentation-fonts/' in saved
    assert 'data:font/' not in saved
    assert 'data-lazymind-font-required="true"' in saved
    assert '<h1>正文Title</h1>' in saved


@pytest.mark.parametrize('no_subset', [False, True])
def test_export_subsets_across_slides_and_notes(tmp_path, monkeypatch, no_subset):
    import importlib.util
    import io
    import struct
    from pathlib import Path
    from fontTools.ttLib import TTFont
    from lazymind.chat.engine.tools.file_resources.font_library import register_font
    from lazymind.chat.engine.tools.file_resources.pptx_font_embedding import embed_pptx_file
    monkeypatch.setenv('LAZYMIND_FONT_DIR', str(tmp_path / 'library'))
    record = register_font(make_font(tmp_path, text='甲乙丙丁', fs_type=256 if no_subset else 0))
    prs = Presentation()
    for text in ['甲', '乙']:
        slide = prs.slides.add_slide(prs.slide_layouts[1])
        slide.shapes.title.text = text
        slide.notes_slide.notes_text_frame.text = '丙'
    path = tmp_path / 'subset.pptx'
    prs.save(path)
    embed_pptx_file(path, 'Test Font', [record])
    script = Path(__file__).parents[2] / 'workflows/ppt-workflow/runtime/scripts/export_pptx/subset_fonts.py'
    spec = importlib.util.spec_from_file_location('export_font_subset', script)
    helper = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(helper)
    helper.subset_pptx(path)
    with ZipFile(path) as archive:
        fonts = [n for n in archive.namelist() if n.startswith('ppt/fonts/')]
        assert len(fonts) == 1
        eot = archive.read(fonts[0])
        size = struct.unpack_from('<I', eot, 4)[0]
        assert struct.unpack_from('<I', eot)[0] == len(eot)
        with TTFont(io.BytesIO(eot[-size:])) as font:
            assert set(font.getBestCmap()) == set(map(ord, '甲乙丙丁' if no_subset else '甲乙丙'))
    assert Presentation(path).slides[1].shapes.title.text == '乙'


def test_legacy_preview_font_injection_uses_same_resource(tmp_path, monkeypatch):
    from lazymind.chat.engine.tools.file_resources.font_library import register_font, local_font_css
    monkeypatch.setenv('LAZYMIND_FONT_DIR', str(tmp_path / 'library'))
    monkeypatch.setenv('LAZYMIND_UPLOAD_ROOT', str(tmp_path / 'uploads'))
    record = register_font(make_font(tmp_path, text='正文Unused'))
    css = local_font_css('<style>body{font-family:"Test Font"}</style><body>正文</body>')
    assert f'/static-files/presentation-fonts/{record["file"]}' in css
    assert 'data:font/' not in css
    assert '/*lazymind-font:' in css


def test_font_resource_cache_repairs_corruption_and_rejects_changed_original(tmp_path, monkeypatch):
    from lazymind.chat.engine.tools.file_resources.font_library import register_font, font_resource_url
    monkeypatch.setenv('LAZYMIND_FONT_DIR', str(tmp_path / 'library'))
    monkeypatch.setenv('LAZYMIND_UPLOAD_ROOT', str(tmp_path / 'uploads'))
    record = register_font(make_font(tmp_path))
    url = font_resource_url(record)
    resource = tmp_path / 'uploads' / url.removeprefix('/static-files/')
    original = tmp_path / 'library' / record['file']
    resource.write_bytes(b'corrupted cache')
    assert font_resource_url(record) == url
    assert resource.read_bytes() == original.read_bytes()
    original.write_bytes(b'changed original')
    with pytest.raises(ValueError, match='Font resource changed'):
        font_resource_url(record)


def test_published_legacy_font_resource_is_required_for_native_export(html_tools, tmp_path):
    html, _ = html_tools._inline_preview_images(
        '<html><head><style>body{font-family:"Test Font"}</style></head><body>正文</body></html>',
        tmp_path, tmp_path / 'page_001.html',
    )
    assert 'id="lazymind-local-fonts" data-lazymind-font-required="true"' in html
    assert '/static-files/presentation-fonts/' in html
    assert '/*lazymind-font:' in html
    assert 'data:font/' not in html
