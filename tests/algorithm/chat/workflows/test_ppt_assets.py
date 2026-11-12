"""Named assets must work offline in the real generation and native composition paths."""
import importlib.util
from copy import deepcopy
from pathlib import Path
import sys
from xml.etree import ElementTree as ET

from PIL import Image
from pptx import Presentation
import pytest


pytestmark = pytest.mark.usefixtures('ppt_asset_library')


ROOT = Path(__file__).resolve().parents[4]
LIB = ROOT / 'workflows/ppt-workflow/runtime/lib'
sys.path.insert(0, str(LIB))
import presentation_assets as assets  # noqa: E402


def test_cached_catalog_has_verified_offline_files_and_license_notices():
    data = assets.catalog()
    assert len(data['sources']) == 2
    assert len(data['assets']) == 2
    assert len({a['name'] for a in data['assets']}) == len(data['assets'])
    for source in data['sources'].values():
        assert (assets.asset_root() / source['license_file']).stat().st_size > 200
        assert source['source_url'].startswith('https://github.com/')
    for entry in data['assets']:
        _, png = assets._verified_file(entry, 'png')
        assert png.startswith(b'\x89PNG')
        if entry.get('svg'):
            _, svg = assets._verified_file(entry, 'svg')
            root = ET.fromstring(svg)
            assert root.get('viewBox') and root.get('preserveAspectRatio') == 'xMidYMid meet'
            assert not any(n.tag.split('}')[-1] in {'script', 'foreignObject', 'image'} for n in root.iter())


def test_chinese_search_returns_named_meaningful_assets_and_rejects_paths():
    result = assets.search('需求分析', 'fluent-3d')
    assert 'fluent-3d:clipboard' in [a['name'] for a in result['assets']]
    assert assets.search('does-not-exist')['assets'] == []
    for name in ('../secret', 'https://example.com/icon.svg', 'fluent-3d:unknown'):
        with pytest.raises(ValueError):
            assets.resolve(name)


def test_named_icons_coexist_with_evidence_image_and_are_offline_and_idempotent(tmp_path):
    html = '<img src="../images/evidence.png"><img data-ppt-asset="fluent-3d:clipboard" ' \
           'alt="需求分析" style="width:180px;height:80px;object-fit:fill">'
    result, names = assets.resolve_html(html, tmp_path)
    assert names == ['fluent-3d:clipboard']
    assert '<img src="../images/evidence.png">' in result
    assert 'src="../images/asset-fluent-3d--clipboard.png"' in result
    assert 'object-fit:fill' not in result
    assert assets.resolve_html(result, tmp_path)[0] == result
    assert (tmp_path / 'images/ASSET-LICENSES.txt').is_file()
    with pytest.raises(ValueError, match='Unknown presentation asset'):
        assets.resolve_html('<img data-ppt-asset="fluent-3d:not-found">', tmp_path)


def test_named_asset_checksum_failure_does_not_stage_mutated_image(tmp_path, monkeypatch):
    data = deepcopy(assets.catalog())
    data['assets'][0]['png_sha256'] = '0' * 64
    monkeypatch.setattr(assets, 'catalog', lambda: data)
    with pytest.raises(ValueError, match='checksum'):
        assets.stage(data['assets'][0]['name'], tmp_path, 'png')
    assert not list(tmp_path.iterdir())


def test_native_icons_embed_with_contained_aspect_ratio_and_keep_input_and_notes(tmp_path):
    from lazymind.chat.engine.tools.file_resources.pptx_template_fill import _inspect_page
    from lazymind.chat.engine.tools.file_resources.pptx_template_compose import compose_template
    source, output = tmp_path / 'source.pptx', tmp_path / 'result.pptx'
    prs = Presentation()
    prs.slides.add_slide(prs.slide_layouts[6])
    prs.save(source)
    info = _inspect_page(source, 1)
    pages = [{'source_page': 1, 'edits': [], 'notes': 'Preserve approved evidence.',
              'icons': [{'name': 'fluent-3d:clipboard', 'box': [1, 2, 4, 1]}]}]
    original = deepcopy(pages)
    normalized, files = assets.native_pages(pages, {}, tmp_path / 'assets')
    compose_template(source, output, info['sha256'], normalized, files)
    result = Presentation(output).slides[0]
    icon = next(s for s in result.shapes if s.shape_type == 13)
    with Image.open(files[next(iter(files))]['path']) as image:
        assert icon.width / icon.height == pytest.approx(image.width / image.height, abs=.01)
    assert 'Preserve approved evidence.' in result.notes_slide.notes_text_frame.text
    assert pages == original


def test_workflow_search_and_get_tools_are_registered_and_stage_real_assets(tmp_path, monkeypatch):
    import yaml
    path = ROOT / 'workflows/ppt-workflow/scripts/tools.py'
    spec = importlib.util.spec_from_file_location('ppt_named_asset_tools', path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    monkeypatch.setattr(module, '_conversation_root', lambda: tmp_path)
    assert module.ppt_search_assets('需求分析', 'fluent-3d')['assets']
    result = module.ppt_get_asset('fluent-3d:clipboard')
    assert Path(result['pptx_path']).is_file() and result['license'] == 'MIT'
    rendered, _ = assets.resolve_html('<img data-ppt-asset="fluent-color:clipboard-24">', tmp_path)
    monkeypatch.setattr(module, 'file_relative_path', lambda _: None)
    preview, count = module._inline_preview_images(rendered, tmp_path, tmp_path / 'pages/page_001.html')
    assert count == 1 and 'data:image/svg+xml;base64,' in preview
    workflow = yaml.safe_load((ROOT / 'workflows/ppt-workflow/workflow.yaml').read_text())
    functions = [name for script in workflow['tool_scripts'] for name in script['functions']]
    assert {'ppt_search_assets', 'ppt_get_asset'} <= set(functions)


def test_generator_resolves_named_assets_before_publishing(tmp_path):
    sys.path.insert(0, str(ROOT / 'workflows/ppt-workflow/runtime/scripts'))
    import run_stage
    (tmp_path / 'pages').mkdir()
    calls = []

    def model(system, query, **kwargs):
        calls.append(query)
        return '<!doctype html><html><head></head><body><div class="wrapper">' \
               '<img data-ppt-asset="fluent-3d:clipboard" alt="需求分析"></div></body></html>'

    result = run_stage._write_page_html_from_query(
        tmp_path, 1, '立体需求分析流程', page_plan={'slots': []}, inherited_image=None,
        background_image=None, prompt_mode='complete-page-prompt', language='zh', llm_call=model)
    assert result == 0
    saved = (tmp_path / 'pages/page_001.html').read_text()
    assert '../images/asset-fluent-3d--clipboard.png' in saved
    assert 'NAMED PRESENTATION ASSETS' in calls[0]
    assert len(calls) == 1
