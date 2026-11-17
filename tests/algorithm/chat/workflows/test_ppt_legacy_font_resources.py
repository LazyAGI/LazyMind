"""Text edits upgrade legacy glyph subsets using the original font identity."""
import base64
import hashlib
import importlib.util
import io
import json
from pathlib import Path

import pytest
from fontTools import subset
from fontTools.fontBuilder import FontBuilder
from fontTools.pens.ttGlyphPen import TTGlyphPen
from fontTools.ttLib import TTFont

from lazymind.chat.engine.tools.file_resources import font_library


@pytest.fixture
def legacy_font(tmp_path, monkeypatch):
    monkeypatch.setenv('LAZYMIND_FONT_DIR', str(tmp_path / 'fonts'))
    monkeypatch.setenv('LAZYMIND_UPLOAD_ROOT', str(tmp_path / 'uploads'))
    builder = FontBuilder(1000, isTTF=True)
    glyphs = ['.notdef', 'old', 'new']
    builder.setupGlyphOrder(glyphs)
    builder.setupCharacterMap({ord('甲'): 'old', ord('乙'): 'new'})
    builder.setupGlyf({g: TTGlyphPen(None).glyph() for g in glyphs})
    builder.setupHorizontalMetrics({g: (500, 0) for g in glyphs})
    builder.setupHorizontalHeader(ascent=800, descent=-200)
    builder.setupNameTable({'familyName': 'Legacy Font', 'styleName': 'Regular', 'fullName': 'Legacy Font'})
    builder.setupOS2(fsType=0)
    builder.setupPost()
    source = tmp_path / 'legacy.ttf'
    builder.save(source)
    record = font_library.register_font(source)
    with TTFont(source) as font:
        subsetter = subset.Subsetter()
        subsetter.populate(text='甲')
        subsetter.subset(font)
        font.flavor = 'woff'
        stream = io.BytesIO()
        font.save(stream)
    encoded = base64.b64encode(stream.getvalue()).decode()
    metadata = {'family': 'Legacy Font', 'weight': record['weight'], 'style': record['style'], 'sha256': record['id']}
    return record, metadata, encoded


@pytest.fixture
def preview(tmp_path, monkeypatch):
    path = Path(__file__).resolve().parents[4] / 'workflows/ppt-workflow/scripts/tools.py'
    spec = importlib.util.spec_from_file_location('ppt_legacy_font_resources_test', path)
    tools = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(tools)
    monkeypatch.setattr(tools, 'available_font_families', lambda: {'Legacy Font'})
    monkeypatch.setattr(tools, '_agent_llm_call', lambda *a, **kw: '{"op":"replace_text","value":"乙"}')
    monkeypatch.setattr(font_library, 'ensure_font', lambda *a, **kw: pytest.fail('Do not enumerate or download fonts'))

    def edit(metadata, encoded, declaration=None):
        token = base64.b64encode(json.dumps(metadata, ensure_ascii=False).encode()).decode()
        declaration = declaration or 'font-family:"Legacy Font";font-weight:400;font-style:normal;'
        style = ('<style id="legacy-font" data-lazymind-font-required="true">'
                 f'/*lazymind-font:{token}*/@font-face{{{declaration}'
                 f'src:url(data:font/woff;base64,{encoded})}}</style>')
        unrelated = '<style>.neighbor{color:red}</style>'
        html = ('<html><head>' + style + unrelated + '</head><body>'
                '<h1 data-el="title" style="font-family:\'Legacy Font\'">甲</h1><p>Keep</p></body></html>')
        result = tools.ppt_preview_selection_edit(
            artifact=html, instruction='改成乙', selection={'type': 'ppt_html', 'el': 'title', 'page': 1},
            artifact_store=str(tmp_path), slot='preview_html',
        )
        assert '>乙</h1>' in result['candidate_html'] and '<p>Keep</p>' in result['candidate_html']
        assert unrelated in result['candidate_html']
        return result['candidate_html'], style
    return edit


def test_text_preview_upgrades_legacy_subset_for_new_glyph(legacy_font, preview, tmp_path):
    record, metadata, encoded = legacy_font
    with TTFont(io.BytesIO(base64.b64decode(encoded))) as font:
        assert ord('乙') not in font.getBestCmap()
    html, _ = preview(metadata, encoded)
    assert 'id="legacy-font" data-lazymind-font-required="true"' in html
    assert f'/static-files/presentation-fonts/{record["file"]}' in html
    assert 'data:font/' not in html
    data = (tmp_path / 'uploads/presentation-fonts' / record['file']).read_bytes()
    assert hashlib.sha256(data).hexdigest() == record['id']
    with TTFont(io.BytesIO(data)) as font:
        assert set(font.getBestCmap()) == {ord('甲'), ord('乙')}


@pytest.mark.parametrize('change', [
    {'sha256': '0' * 64}, {'family': 'Other Font'}, {'weight': 700}, {'style': 'italic'},
    {'family': []}, {'sha256': 'invalid'},
])
def test_text_preview_retains_unmatched_legacy_package(legacy_font, preview, change):
    _, metadata, encoded = legacy_font
    html, style = preview({**metadata, **change}, encoded)
    assert style in html


@pytest.mark.parametrize('declaration', [
    'font-family:"Other Font";font-weight:400;font-style:normal;',
    'font-family:"Legacy Font";font-weight:700;font-style:normal;',
    'font-family:"Legacy Font";font-weight:400;font-style:italic;',
    'font-family:"Legacy Font";font-weight:400;font-style:normal;font-display:swap;',
    'font-family:"Legacy Font";font-weight:400;font-style:normal;unicode-range:U+0-FFFF;',
])
def test_text_preview_retains_authored_or_mismatched_font_rule(legacy_font, preview, declaration):
    _, metadata, encoded = legacy_font
    html, style = preview(metadata, encoded, declaration)
    assert style in html


@pytest.mark.parametrize('damage', ['missing', 'changed'])
def test_text_preview_keeps_legacy_font_when_original_is_unavailable(legacy_font, preview, damage):
    record, metadata, encoded = legacy_font
    original = font_library.font_library_root() / record['file']
    if damage == 'missing':
        original.unlink()
    else:
        original.write_bytes(b'corrupted')
    html, style = preview(metadata, encoded)
    assert style in html
