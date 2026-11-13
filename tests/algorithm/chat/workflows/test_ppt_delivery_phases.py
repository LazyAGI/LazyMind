"""PPT preparation and delivery boundaries, independent of real model services."""
import importlib.util
import json
from pathlib import Path
from types import SimpleNamespace

from pptx import Presentation
import pytest

from lazymind.chat.workflow import external_ppt_tools

ROOT = Path(__file__).resolve().parents[4]


@pytest.fixture
def tools(monkeypatch, tmp_path):
    spec = importlib.util.spec_from_file_location('ppt_delivery_tools', ROOT / 'workflows/ppt-workflow/scripts/tools.py')
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    monkeypatch.setattr(module, '_require_html_output', lambda: None)
    monkeypatch.setattr(module, '_conversation_root', lambda: tmp_path)
    monkeypatch.setattr(module, 'require_context', lambda: SimpleNamespace())
    return module


@pytest.mark.parametrize('outcome', ['ok', 'missing_page', 'failed'])
def test_requested_pptx_exports_selected_order_and_notes_before_publication(tools, monkeypatch, tmp_path, outcome):
    monkeypatch.setattr(tools, '_ui_slot_order_list', lambda slot: [4, 2])
    current = {
        'preview_html': ['<html><body>Current first</body></html>', '<html><body>Current second</body></html>'],
        'preview_notes': ['Saved first script', 'Saved second script'],
    }
    monkeypatch.setattr(tools, '_resolve_artifact_text',
                        lambda ctx, slot, sort_order: (current[slot][sort_order - 1], 'text'))
    monkeypatch.setattr(external_ppt_tools, '_cli', lambda: ('node', tmp_path / 'export.mjs'))
    monkeypatch.setattr(tools, '_agent_llm_call', lambda *a, **kw: pytest.fail('export must not generate content'))
    saved = []
    monkeypatch.setattr(tools, '_save_artifact', lambda **kw: saved.append(kw) or {'ok': True})

    def exporter(command, **kwargs):
        directory = Path(command[command.index('--deck-dir') + 1])
        for number in (1, 2):
            assert (directory / 'pages' / f'page_{number:03d}.html').read_text() == current['preview_html'][number - 1]
            notes = (directory / 'pages' / f'page_{number:03d}.notes.txt').read_text()
            assert notes == current['preview_notes'][number - 1]
        presentation = Presentation()
        for _ in range(1 if outcome == 'missing_page' else 2):
            presentation.slides.add_slide(presentation.slide_layouts[6])
        presentation.save(directory / 'presentation.pptx')
        return SimpleNamespace(returncode=0, stderr='', stdout=json.dumps({
            'status': 'failed' if outcome == 'failed' else 'ok',
            'failed': 1 if outcome == 'failed' else 0, 'error': 'page conversion failed',
        }))

    monkeypatch.setattr(tools.subprocess, 'run', exporter)
    if outcome == 'ok':
        result = tools.ppt_export_deck()
        assert result['published'] is True and result['page_count'] == 2
        assert result['visual_validation'] == 'not_performed'
        assert len(saved) == 1 and saved[0]['key'] == 'native_pptx'
        assert len(Presentation(saved[0]['value']).slides) == 2
    else:
        with pytest.raises(tools.ToolExecutionError, match='page count|conversion failed'):
            tools.ppt_export_deck()
        assert saved == []


def test_incomplete_page_pair_does_not_start_export(tools, monkeypatch):
    monkeypatch.setattr(tools, '_ui_slot_order_list', lambda slot: [0] if slot == 'preview_notes' else [0, 1])
    monkeypatch.setattr(external_ppt_tools, '_cli', lambda: pytest.fail('incomplete deck must not start export'))
    with pytest.raises(tools.ToolExecutionError, match='every approved HTML page'):
        tools.ppt_export_deck()


def test_preparation_uses_approved_order_without_extra_model_calls(tools, monkeypatch, tmp_path):
    (tmp_path / 'task_pack.json').write_text('{"params":{"page_count":2}}')
    monkeypatch.setattr(tools, '_selected_slide_outline_items', lambda: [
        (4, '# First\nApproved copy'), (2, '# Second\nApproved copy'),
    ])
    monkeypatch.setattr(tools, '_agent_llm_call', lambda *a, **kw: pytest.fail('must not rewrite approved prompts'))
    stages = []
    monkeypatch.setattr(tools, 'ppt_run_stage',
                        lambda deck, stage: stages.append(stage) or {'status': 'ok', 'pages': 2})
    result = tools._prepare_page_generation(tmp_path)
    assert stages == ['asset-plan']
    assert result[0]['page_count'] == 2
    index = json.loads((tmp_path / 'outline.json').read_text())['pages']
    assert [page['title'] for page in index] == ['First', 'Second']
    assert [page['page_no'] for page in index] == [1, 2]


def test_export_embeds_current_workspace_images_and_rejects_other_files(tools, monkeypatch, tmp_path):
    import base64
    image = tmp_path / 'material.png'
    image.write_bytes(b'current image bytes')
    original = '<html><body><img src="/static-files/material.png"></body></html>'
    monkeypatch.setattr(tools, 'local_path_from_static_file_url', lambda _: str(image))
    monkeypatch.setattr(tools, 'file_relative_path', lambda _: 'material.png')
    exported, count = tools._inline_preview_images(original, tmp_path, tmp_path / 'page.html', inline_assets=True)
    assert count == 1
    assert 'data:image/png;base64,' + base64.b64encode(image.read_bytes()).decode() in exported
    assert '/static-files/' not in exported
    assert image.read_bytes() == b'current image bytes'
    monkeypatch.setattr(tools, 'local_path_from_static_file_url', lambda _: str(tmp_path.parent / 'private.png'))
    with pytest.raises(ValueError, match='outside the current presentation workspace'):
        tools._inline_preview_images(original, tmp_path, tmp_path / 'page.html', inline_assets=True)


def test_native_tool_schema_requires_authored_outline_and_exports_without_model_arguments(tools):
    from lazyllm.tools.agent import ToolManager
    schemas = {item['function']['name']: item['function']['parameters'] for item in
               ToolManager([tools.ppt_build_outline, tools.ppt_export_deck]).tools_description}
    assert set(schemas['ppt_build_outline']['required']) == {'user_query', 'outline_markdown'}
    assert schemas['ppt_export_deck']['properties'] == {}
