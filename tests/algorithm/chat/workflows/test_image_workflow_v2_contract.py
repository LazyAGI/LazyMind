import base64
from pathlib import Path

import yaml

from lazymind.workflow_toolkit import load_workflow_package_tools


ROOT = Path(__file__).resolve().parents[4]
WORKFLOW_ROOT = ROOT / 'workflows' / 'image-workflow-v2'


def _load(relative_path: str):
    return yaml.safe_load((WORKFLOW_ROOT / relative_path).read_text(encoding='utf-8'))


def test_package_declares_three_final_deliverable_routes():
    workflow = _load('workflow.yaml')
    state = _load('scenario/state.yml')
    targets = {edge['to'] for edge in state['transitions']['classify_request']}
    assert targets == {'ordinary_prepare', 'edit_prepare', 'meme_brief'}
    assert workflow['runtime']['publisher_owned_slots'] == [
        'route_plan', 'material_summary', 'ordinary_prompt',
        'ordinary_output', 'authoritative_image', 'edit_contract', 'edit_candidate',
    ]
    assert state['steps']['classify_request']['tools'] == ['classify_image_request']
    assert state['steps']['classify_request']['terminal_tools'] == ['classify_image_request']
    assert state['steps']['ordinary_prepare']['tools'] == ['prepare_ordinary_request']
    assert state['steps']['ordinary_prepare']['terminal_tools'] == ['prepare_ordinary_request']


def test_ordinary_and_edit_routes_use_workflow_owned_baoyu_seedream_tools():
    workflow = _load('workflow.yaml')
    state = _load('scenario/state.yml')
    declarations = {
        entry['path']: set(entry['functions'])
        for entry in workflow['tool_scripts']
    }
    assert declarations['scripts/baoyu.py'] == {
        'baoyu_image_generator',
        'baoyu_image_editor',
        'generate_ordinary_image',
        'generate_edit_candidate',
    }
    assert state['steps']['ordinary_generate']['tools'] == ['generate_ordinary_image']
    assert state['steps']['ordinary_generate']['terminal_tools'] == ['generate_ordinary_image']
    assert state['steps']['edit_candidate']['tools'] == ['generate_edit_candidate']
    assert state['steps']['edit_candidate']['terminal_tools'] == ['generate_edit_candidate']
    assert 'Seedream 5.0' in state['steps']['ordinary_generate']['prompt']
    edit_prompt = ' '.join(state['steps']['edit_candidate']['prompt'].split())
    assert 'authoritative image and complete edit contract' in edit_prompt


def test_meme_route_keeps_native_media_toolchain_and_validated_brief():
    workflow = _load('workflow.yaml')
    state = _load('scenario/state.yml')
    support_tools = next(
        set(entry['functions'])
        for entry in workflow['tool_scripts']
        if entry['path'] == 'scripts/tools.py'
    )
    assert {
        'save_validated_meme_brief',
        'compose_character_anchor_sheet',
        'meme_add_caption',
        'meme_video_to_captioned_gif',
    } <= support_tools
    assert state['steps']['meme_brief']['tools'] == ['save_validated_meme_brief']
    assert state['steps']['render_static']['tools'] == ['image_editor']
    assert state['steps']['render_keyframes']['tools'] == ['image_editor']
    assert state['steps']['video_canary']['tools'] == ['video_generator']
    assert state['steps']['caption_dynamic']['tools'] == ['meme_video_to_captioned_gif']


def test_ui_uses_picture_production_and_hides_empty_branch_outputs():
    workflow = _load('workflow.yaml')
    tabs = {tab['id']: tab for tab in workflow['ui']['tabs']}
    assert workflow['i18n']['zh-CN']['tabs']['production']['label'] == '图片制作'
    assert tabs['character']['composite_behavior']['hide_empty_columns'] is True
    assert tabs['production']['composite_behavior']['hide_empty_columns'] is True

    character_slots = {slot['id'] for slot in tabs['character']['slots']}
    production_slots = {slot['id'] for slot in tabs['production']['slots']}
    assert {'face_anchor_images', 'face_anchor_grid'} <= character_slots
    assert {'canary_video', 'remaining_videos'} <= production_slots


def test_static_and_dynamic_meme_routes_are_mutually_exclusive():
    state = _load('scenario/state.yml')
    assert state['transitions']['design_best_performances'] == [
        {'to': 'plan_static', 'when': 'meme_brief_data.delivery_mode is static'},
        {'to': 'plan_dynamic', 'when': 'meme_brief_data.delivery_mode is dynamic'},
    ]
    assert state['transitions']['caption_static'] == [{'to': 'review_pack'}]
    assert state['transitions']['caption_dynamic'] == [{'to': 'review_pack'}]


def test_workflow_contains_no_workplace_meme_template_or_replay_feature():
    source = '\n'.join(
        path.read_text(encoding='utf-8')
        for path in WORKFLOW_ROOT.rglob('*')
        if path.is_file() and path.suffix in {'.yaml', '.yml', '.md', '.py'}
    ).lower()
    forbidden = (
        '_workplace_meme_performances', '职场文字抽象梗',
        '收到', '辛苦了', '加油', '下班', 'replay', '回放',
    )
    assert all(term not in source for term in forbidden)


def test_declared_tool_scripts_load_from_an_immutable_package():
    workflow = _load('workflow.yaml')
    names = [
        name
        for declaration in workflow['tool_scripts']
        for name in declaration['functions']
    ]
    files = {
        path.relative_to(WORKFLOW_ROOT).as_posix(): base64.b64encode(path.read_bytes()).decode()
        for path in WORKFLOW_ROOT.rglob('*')
        if path.is_file() and '__pycache__' not in path.parts
    }
    tools = load_workflow_package_tools(
        {'files': files}, names, 'image-workflow-v2', 'test-revision',
    )
    assert set(tools) == set(names)
    base_tools = tools['meme_add_caption'].__globals__['_legacy_image_tools']()
    assert callable(base_tools.meme_add_caption)
