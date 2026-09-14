from __future__ import annotations

from pathlib import Path

import yaml


def _repo_root() -> Path:
    return Path(__file__).resolve().parents[4]


def _load_workflow() -> dict:
    path = _repo_root() / 'workflows' / 'image-workflow' / 'workflow.yaml'
    return yaml.safe_load(path.read_text(encoding='utf-8'))


def _load_state() -> dict:
    path = _repo_root() / 'workflows' / 'image-workflow' / 'scenario' / 'state.yml'
    return yaml.safe_load(path.read_text(encoding='utf-8'))


def test_analyze_can_skip_material_collection_by_semantic_judgment():
    state = _load_state()
    analyze = state['steps']['analyze_subject']
    analyze_transitions = state['transitions']['analyze_subject']
    targets = {transition['to'] for transition in analyze_transitions}

    assert analyze['route'] == 'choice'
    assert targets == {'collect_materials', 'optimize_prompt'}
    assert all(transition.get('when') for transition in analyze_transitions)

    optimize = state['steps']['optimize_prompt']
    assert {'material': 'material_summary', 'required': False} in optimize['inputs']


def test_optimize_step_produces_optional_structured_meme_plan():
    workflow = _load_workflow()
    state = _load_state()
    slots = {slot['id']: slot for slot in workflow['slots']}
    optimize = state['steps']['optimize_prompt']

    assert slots['meme_generation_plan'] == {
        'id': 'meme_generation_plan',
        'label': 'Meme Generation Plan',
        'type': 'json',
        'cardinality': 'single',
    }
    assert {'material': 'meme_generation_plan', 'required': False} in optimize['outputs']


def test_generate_then_enhance_owns_distinct_meme_strategies():
    workflow = _load_workflow()
    state = _load_state()
    generate = state['steps']['generate_image']
    enhance = state['steps']['enhance_image']

    assert {'material': 'meme_generation_plan', 'required': False} in generate['inputs']
    assert {'material': 'generated_base_image', 'required': True} in generate['outputs']
    assert {'material': 'meme_static_output', 'required': False} in enhance['outputs']
    assert set(generate['tools']) == {
        'image_generator', 'select_image_postprocess_route',
    }
    assert set(enhance['tools']) == {
        'image_editor', 'video_generator', 'video_to_gif', 'meme_add_caption',
    }

    tool_functions = {
        function
        for script in workflow['tool_scripts']
        for function in script['functions']
    }
    assert 'meme_add_caption' in tool_functions
    assert 'select_image_postprocess_route' in tool_functions
    assert 'check_image_workflow_capabilities' in tool_functions

    slots = {slot['id']: slot for slot in workflow['slots']}
    assert slots['meme_static_output']['type'] == 'image'
    assert slots['meme_static_output']['cardinality'] == 'list'
    assert slots['meme_static_output']['ordered'] is True


def test_optimize_step_freezes_the_only_valid_image_route():
    workflow = _load_workflow()
    optimize = _load_state()['steps']['optimize_prompt']
    tool_functions = {
        function
        for script in workflow['tool_scripts']
        for function in script['functions']
    }

    assert 'select_image_route' in tool_functions
    assert optimize['tools'] == ['select_image_route']
    assert optimize['terminal_tools'] == ['select_image_route']


def test_code_level_capability_gate_and_zero_image_failure_contract_are_required():
    workflow = _load_workflow()
    state = _load_state()
    generate = state['steps']['generate_image']
    slots = {slot['id']: slot for slot in workflow['slots']}
    checks = workflow['runtime']['post_step_checks']

    assert 'capability_check' not in state['steps']
    assert checks == [{
        'step_id': 'analyze_subject',
        'tool': 'check_image_workflow_capabilities',
        'arguments': {'workflow_routing': 'workflow_routing'},
    }]
    assert 'capability_report' not in slots
    assert slots['generated_base_image']['exposed'] is False
    assert {'material': 'generated_base_image', 'required': True} in generate['outputs']


def test_workflow_panel_navigation_matches_the_five_visible_runtime_steps():
    workflow = _load_workflow()

    assert [tab['id'] for tab in workflow['ui']['tabs']] == [
        'analyze_subject',
        'collect_materials',
        'optimize_prompt',
        'generate_image',
        'enhance_image',
    ]


def test_generate_exposes_only_present_seedance_frame_and_reference_materials():
    workflow = _load_workflow()
    state = _load_state()
    slots = {slot['id']: slot for slot in workflow['slots']}
    generate = state['steps']['generate_image']
    enhance = state['steps']['enhance_image']
    tabs = {tab['id']: tab for tab in workflow['ui']['tabs']}

    for slot_id in (
        'generated_first_frame',
        'generated_last_frame',
        'generation_reference_images',
    ):
        assert slots[slot_id]['type'] == 'image'
        assert slots[slot_id]['cardinality'] == 'list'
        assert slots[slot_id]['ordered'] is True
        assert {'material': slot_id, 'required': False} in generate['outputs']

    generate_tab = tabs['generate_image']
    assert generate_tab['composite_tab_position'] == 'left'
    assert generate_tab['composite_behavior']['hide_empty_columns'] is True
    assert [slot['id'] for slot in generate_tab['slots']] == [
        'generated_image_output',
        'generated_first_frame',
        'generated_last_frame',
        'generation_reference_images',
    ]

    enhance_tab = tabs['enhance_image']
    assert enhance_tab['slot_scope'] == 'selected'
    assert enhance_tab['composite_tab_position'] == 'left'
    assert enhance_tab['composite_layout']['direction'] == 'row'
    assert all('tabs' not in child for child in enhance_tab['composite_layout']['children'])
    assert enhance_tab['composite_behavior']['repeat_single_slots'] == [
        'generated_first_frame',
        'generated_base_image',
        'generated_last_frame',
        'material_images',
    ]
    assert enhance_tab['composite_behavior']['mutually_exclusive'][0]['prefer'][:2] == [
        'generated_first_frame',
        'generated_base_image',
    ]
    assert enhance_tab['composite_behavior']['mutually_exclusive'][1]['prefer'][:2] == [
        'gif_output',
        'video_output',
    ]
    collect_tab = tabs['collect_materials']
    assert collect_tab['composite_behavior']['hide_empty_columns'] is True
    assert {'material': 'generated_first_frame', 'required': False} in enhance['inputs']
    assert {'material': 'generated_last_frame', 'required': False} in enhance['inputs']
    assert {'material': 'generation_reference_images', 'required': False} in enhance['inputs']
