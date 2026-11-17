"""Font discovery failures must not prevent an ordinary HTML text preview."""
import importlib.util
import json
from pathlib import Path
import subprocess
from types import SimpleNamespace

import pytest

from lazymind.chat.engine.tools.file_resources import font_library, portable_fonts, presentation_edit


@pytest.fixture
def font_discovery(monkeypatch):
    monkeypatch.setattr(font_library, 'local_fonts', lambda: [
        {'embeddable': True, 'aliases': ['Managed Font']},
    ])
    monkeypatch.setattr(presentation_edit.shutil, 'which', lambda _: 'fc-list')
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
