"""Font discovery failures must not prevent an ordinary HTML text preview."""
import importlib.util
import json
from pathlib import Path
import subprocess
from types import SimpleNamespace

import pytest
from fontTools.fontBuilder import FontBuilder
from fontTools.pens.ttGlyphPen import TTGlyphPen
from fontTools.ttLib import TTCollection

from lazymind.chat.engine.tools.file_resources import font_library, portable_fonts, presentation_edit


@pytest.fixture
def font_discovery(monkeypatch):
    monkeypatch.setattr(font_library, 'local_fonts', lambda: [
        {'embeddable': True, 'aliases': ['Managed Font']},
    ])
    monkeypatch.setattr(presentation_edit.shutil, 'which', lambda _: 'fc-list')
    monkeypatch.setattr(presentation_edit, 'office_yahei_font_paths', lambda: ())
    # Keep the regression independent from fonts installed on the test machine.
    monkeypatch.setattr(Path, 'rglob', lambda *args: iter(()))


@pytest.mark.parametrize('output', [
    None, '', UnicodeDecodeError('gbk', b'\xff', 0, 1, 'invalid byte'),
    subprocess.TimeoutExpired('fc-list', 15), OSError('not installed'),
])
def test_text_preview_survives_font_enumeration_failure(tmp_path, monkeypatch, font_discovery, output):
    path = Path(__file__).resolve().parents[4] / 'workflows/ppt-workflow/scripts/tools.py'
    spec = importlib.util.spec_from_file_location('ppt_font_enumeration_test', path)
    tools = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(tools)

    def run(*args, **kwargs):
        if isinstance(output, Exception):
            raise output
        return SimpleNamespace(stdout=output)

    def model(_system, prompt, **kwargs):
        assert json.loads(prompt)['available_font_families'] == ['Managed Font']
        return '{"op":"replace_text","value":"New title"}'

    monkeypatch.setattr(presentation_edit.subprocess, 'run', run)
    monkeypatch.setattr(tools, '_agent_llm_call', model)
    html = '<html><head></head><body><h1 data-el="title">Old title</h1><p>Keep</p></body></html>'
    preview = tools.ppt_preview_selection_edit(
        artifact=html, instruction='让标题更精简', selection={'type': 'ppt_html', 'el': 'title', 'page': 1},
        artifact_store=str(tmp_path), slot='preview_html',
    )
    assert '>New title</h1>' in preview['candidate_html']
    assert '<p>Keep</p>' in preview['candidate_html']


@pytest.mark.parametrize('invalid_record', [b'', b'Bad\xff Font\n'])
def test_fontconfig_names_are_decoded_as_utf8(monkeypatch, font_discovery, invalid_record):
    def run(*args, **kwargs):
        raw = '宋体-简,Songti SC\nNoto Sans SC\n'.encode('utf-8') + invalid_record
        # Windows' locale codec is unrelated to fontconfig's UTF-8 output.
        text = raw.decode(kwargs.get('encoding') or 'ascii', errors=kwargs.get('errors', 'strict'))
        return SimpleNamespace(stdout=text)

    monkeypatch.setattr(presentation_edit.subprocess, 'run', run)
    assert presentation_edit.available_font_families() == {'Managed Font', '宋体-简', 'Songti SC', 'Noto Sans SC'}


@pytest.mark.parametrize('output', [None, '', UnicodeDecodeError('gbk', b'\xff', 0, 1, 'invalid byte')])
def test_font_paths_survive_failed_enumeration(monkeypatch, font_discovery, output):
    def run(*args, **kwargs):
        if isinstance(output, Exception):
            raise output
        return SimpleNamespace(stdout=output)

    monkeypatch.setattr(portable_fonts.subprocess, 'run', run)
    assert list(portable_fonts._system_font_paths()) == []


@pytest.mark.parametrize('invalid_record', [b'', b'C:/Windows/Fonts/Bad\xff.ttf\n'])
def test_fontconfig_paths_are_decoded_as_utf8(monkeypatch, font_discovery, invalid_record):
    def run(*args, **kwargs):
        raw = 'C:/Windows/Fonts/中文.ttf\n\n'.encode('utf-8') + invalid_record
        text = raw.decode(kwargs.get('encoding') or 'ascii', errors=kwargs.get('errors', 'strict'))
        return SimpleNamespace(stdout=text)

    monkeypatch.setattr(portable_fonts.subprocess, 'run', run)
    assert list(portable_fonts._system_font_paths()) == [Path('C:/Windows/Fonts/中文.ttf')]


@pytest.mark.parametrize('embedding', [8, 4])
def test_macos_office_yahei_selection_preview(tmp_path, monkeypatch, embedding):
    monkeypatch.setattr(presentation_edit, 'sys', SimpleNamespace(platform='darwin'))
    monkeypatch.setattr(Path, 'home', lambda: tmp_path)
    monkeypatch.setattr(Path, 'rglob', lambda *args: iter(()))
    original_is_file = Path.is_file
    monkeypatch.setattr(Path, 'is_file', lambda p: not p.is_relative_to('/Applications') and original_is_file(p))
    monkeypatch.setattr(presentation_edit.shutil, 'which', lambda _: 'fc-list')
    monkeypatch.setattr(presentation_edit.subprocess, 'run', lambda *a, **kw: SimpleNamespace(stdout='Other Font'))
    monkeypatch.setenv('LAZYMIND_FONT_DIR', str(tmp_path / 'fonts'))
    monkeypatch.setenv('LAZYMIND_UPLOAD_ROOT', str(tmp_path / 'uploads'))
    monkeypatch.setenv('LAZYMIND_RUNTIME_MODE', 'local')
    bundled = tmp_path / 'Applications/Microsoft PowerPoint.app/Contents/Resources/DFonts/msyh.ttc'
    bundled.parent.mkdir(parents=True)
    builder = FontBuilder(1000, isTTF=True)
    glyphs = ['.notdef', 'title']
    builder.setupGlyphOrder(glyphs)
    builder.setupCharacterMap({ord('甲'): 'title'})
    builder.setupGlyf({glyph: TTGlyphPen(None).glyph() for glyph in glyphs})
    builder.setupHorizontalMetrics({glyph: (500, 0) for glyph in glyphs})
    builder.setupHorizontalHeader(ascent=800, descent=-200)
    builder.setupNameTable({'familyName': 'Microsoft YaHei', 'styleName': 'Regular'})
    builder.setupOS2(fsType=embedding)
    builder.setupPost()
    collection = TTCollection()
    collection.fonts = [builder.font]
    collection.save(bundled)

    path = Path(__file__).resolve().parents[4] / 'workflows/ppt-workflow/scripts/tools.py'
    spec = importlib.util.spec_from_file_location('ppt_yahei_preview_test', path)
    tools = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(tools)
    monkeypatch.setattr(tools, '_agent_llm_call', lambda *a, **kw: pytest.fail('Exact font request called the model'))
    assert presentation_edit.available_font_families(include_managed=False) == {'Other Font'}
    html = '<html><head></head><body><h1 data-el="title">甲</h1><p>Keep</p></body></html>'
    edit = dict(artifact=html, instruction='这个字体 换成微软雅黑',
                selection={'type': 'ppt_html', 'el': 'title', 'page': 1},
                artifact_store=str(tmp_path), slot='preview_html')
    if embedding == 8:
        candidate = tools.ppt_preview_selection_edit(**edit)['candidate_html']
        assert 'Microsoft YaHei' in candidate and '/static-files/presentation-fonts/' in candidate
        assert '>甲</h1>' in candidate and '<p>Keep</p>' in candidate
    else:
        with pytest.raises(ValueError, match='没有可嵌入的字体文件'):
            tools.ppt_preview_selection_edit(**edit)
