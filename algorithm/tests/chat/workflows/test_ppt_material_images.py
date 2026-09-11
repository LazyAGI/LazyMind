from __future__ import annotations

import importlib.util
from pathlib import Path

import yaml


def _repo_root() -> Path:
    return Path(__file__).resolve().parents[4]


def _load_ppt_tools():
    path = _repo_root() / 'workflows' / 'ppt-workflow' / 'scripts' / 'tools.py'
    spec = importlib.util.spec_from_file_location('_test_ppt_material_tools', path)
    assert spec is not None and spec.loader is not None
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def test_ppt_material_slot_uses_same_image_list_contract_as_image_workflow():
    ppt = yaml.safe_load(
        (_repo_root() / 'workflows' / 'ppt-workflow' / 'workflow.yaml').read_text(
            encoding='utf-8'))
    image = yaml.safe_load(
        (_repo_root() / 'workflows' / 'image-workflow' / 'workflow.yaml').read_text(
            encoding='utf-8'))

    ppt_slot = next(slot for slot in ppt['slots'] if slot['id'] == 'material_images')
    image_slot = next(slot for slot in image['slots'] if slot['id'] == 'material_images')
    assert ppt_slot == {
        **image_slot,
        'label': 'Material Images',
    }

    materials_tab = next(tab for tab in ppt['ui']['tabs'] if tab['id'] == 'materials')
    assert materials_tab['layout'] == 'list'
    assert materials_tab['slots'] == [
        {'id': 'uploaded_materials'},
        {'id': 'material_summary'},
        {'id': 'material_images'},
    ]


def test_ppt_analysis_and_material_summary_share_markdown_widget():
    ppt = yaml.safe_load(
        (_repo_root() / 'workflows' / 'ppt-workflow' / 'workflow.yaml').read_text(
            encoding='utf-8'))

    assert ppt['ui']['slots']['requirement_analysis'] == {
        'widgetType': 'text-markdown',
    }
    assert ppt['ui']['slots']['material_summary'] == {
        'widgetType': 'text-markdown',
    }


def test_ppt_analysis_has_one_deterministic_kb_first_collection_route():
    state = yaml.safe_load(
        (_repo_root() / 'workflows' / 'ppt-workflow' / 'scenario' / 'state.yml').read_text(
            encoding='utf-8'))

    assert state['transitions']['analyze_requirements'] == [
        {'to': 'collect_materials'},
    ]
    assert state['steps']['analyze_requirements'].get('route') is None

    prompt = state['steps']['collect_materials']['prompt']
    assert 'KB-first gate (mandatory)' in prompt
    assert 'BEFORE calling any web tool' in prompt
    assert 'STOP retrieval and do not call any web tool' in prompt
    assert 'Do not call web_search merely because it is available' in prompt


def test_ppt_material_images_are_optional_and_search_failure_can_continue():
    state = yaml.safe_load(
        (_repo_root() / 'workflows' / 'ppt-workflow' / 'scenario' / 'state.yml').read_text(
            encoding='utf-8'))

    collect = state['steps']['collect_materials']
    outputs = {
        output['material']: output.get('required', True)
        for output in collect['outputs']
    }

    assert outputs == {
        'material_summary': True,
        'material_images': False,
    }
    assert 'A deck may complete with zero material_images' in collect['prompt']
    assert 'do not fail the step' in collect['prompt']
    criteria = ' '.join(collect['acceptance_criteria'].split())
    assert 'Its absence must never fail or retry this step' in criteria


def test_register_material_images_publishes_one_previewable_image_per_new_file(
    monkeypatch, tmp_path,
):
    tools = _load_ppt_tools()
    old = {
        'path': str(tmp_path / 'old.png'),
        'caption': 'old',
        'alt': 'old',
        'source': 'kb',
    }
    staged_indices: list[int] = []
    saved: list[dict] = []

    def stage(item, index):
        staged_indices.append(index)
        return {
            'path': str(tmp_path / f'material_{index + 1:02d}.png'),
            'caption': item['caption'],
            'alt': item['caption'],
            'source': item['source'],
            'origin': item.get('url', ''),
        }

    monkeypatch.setattr(tools, '_load_material_manifest', lambda: {'images': [old]})
    monkeypatch.setattr(tools, '_stage_one_material_image', stage)
    monkeypatch.setattr(tools, '_write_material_manifest', lambda manifest: tmp_path / 'manifest.json')
    monkeypatch.setattr(tools, '_ui_slot_order_list', lambda slot: [0])
    monkeypatch.setattr(
        tools, '_save_artifact',
        lambda **kwargs: saved.append(kwargs) or {'success': True, 'result': {}},
    )

    result = tools.ppt_register_material_images([
        {'url': 'https://example.com/one.png', 'caption': '基础示意图', 'source': 'kb'},
        {'url': 'https://example.com/two.png', 'caption': '杆塔基础', 'source': 'web'},
    ])

    assert result['success'] is True
    assert staged_indices == [1, 2]
    assert [item['content_type'] for item in saved] == ['image', 'image']
    assert [item['key'] for item in saved] == ['material_images', 'material_images']
    assert [item['caption'] for item in saved] == ['基础示意图', '杆塔基础']
    assert [item['sort_order'] for item in saved] == [None, None]
    assert result['result']['ui_published'] == 2


def test_replace_material_images_overwrites_cards_and_removes_stale_tail(
    monkeypatch, tmp_path,
):
    tools = _load_ppt_tools()
    saved: list[dict] = []
    deleted: list[int] = []

    def stage(item, index):
        return {
            'path': str(tmp_path / f'material_{index + 1:02d}.png'),
            'caption': item['caption'],
            'alt': item['caption'],
            'source': 'web',
            'origin': item['url'],
        }

    monkeypatch.setattr(tools, '_stage_one_material_image', stage)
    monkeypatch.setattr(tools, '_write_material_manifest', lambda manifest: tmp_path / 'manifest.json')
    monkeypatch.setattr(tools, '_ui_slot_order_list', lambda slot: [5, 6, 7, 8])
    monkeypatch.setattr(
        tools, '_delete_ui_slot_item',
        lambda slot, sort_order: deleted.append(sort_order) or {'ok': True},
    )
    monkeypatch.setattr(
        tools, '_save_artifact',
        lambda **kwargs: saved.append(kwargs) or {'success': True, 'result': {}},
    )

    result = tools.ppt_register_material_images([
        {'url': 'https://example.com/one.png', 'caption': 'one'},
        {'url': 'https://example.com/two.png', 'caption': 'two'},
    ], replace=True)

    assert result['success'] is True
    assert deleted == [4, 3]
    assert [item['sort_order'] for item in saved] == [1, 2]
    assert result['result']['ui_deleted'] == [{'ok': True}, {'ok': True}]
