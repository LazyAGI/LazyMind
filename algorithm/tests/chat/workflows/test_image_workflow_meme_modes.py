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


def test_image_workflow_declares_three_canonical_meme_routes():
    workflow = _load_workflow()
    prompt = _load_state()['steps']['analyze_subject']['prompt']

    assert 'CREATE_STATIC_MEME' in prompt
    assert 'CREATE_ANIMATED_MEME' in prompt
    assert 'CREATE_MEME_PACK' in prompt
    assert 'Explicit multi-item meme/reaction/sticker pack → CREATE_MEME_PACK' in prompt
    assert 'One explicit animated meme/reaction/chat sticker → CREATE_ANIMATED_MEME' in prompt
    assert 'HIGHEST-PRIORITY POST-CAPTION/SUBTITLE OVERRIDE' in prompt
    assert 'Explicit static post-caption/subtitle' in prompt
    assert 'Never classify a static request with explicit post-caption/subtitle text' in prompt

    when_to_use = workflow['when_to_use']
    assert 'HIGHEST-PRIORITY TEXT-OVERLAY RULE' in when_to_use
    assert 'even when the user never says meme/表情包' in when_to_use
    assert 'Do not send the caption text to built-in image_generator/image_editor directly' in when_to_use


def test_static_subtitle_after_source_edit_routes_to_caption_postprocessor():
    workflow = _load_workflow()
    state = _load_state()
    analyze_prompt = state['steps']['analyze_subject']['prompt']
    optimize_prompt = state['steps']['optimize_prompt']['prompt']
    optimize_contract = optimize_prompt + '\n' + state['steps']['optimize_prompt']['acceptance_criteria']
    generate_prompt = state['steps']['generate_image']['prompt']
    enhance_prompt = state['steps']['enhance_image']['prompt']

    assert '给上传的小狗做敬礼手势，然后配上字幕‘收到!’' in workflow['when_to_use']
    assert 'first perform the' in analyze_prompt
    assert 'non-text visual edit, then add the exact caption with meme_add_caption' in analyze_prompt
    assert 'painted, printed,' in analyze_prompt
    assert 'engraved, or otherwise integrated into a physical object/scene region' in analyze_prompt
    assert '给这个小狗做敬礼手势，然后配上字幕‘收到!’' in optimize_contract
    assert 'caption is exactly "收到!"' in optimize_contract
    assert 'caption text must not be delegated to image_editor' in optimize_contract
    assert 'Never add the caption here' in generate_prompt
    assert 'Call meme_add_caption once per base/item' in enhance_prompt


def test_analyze_can_skip_material_collection_by_semantic_judgment():
    state = _load_state()
    analyze = state['steps']['analyze_subject']
    analyze_transitions = state['transitions']['analyze_subject']
    targets = {transition['to'] for transition in analyze_transitions}

    assert analyze['route'] == 'choice'
    assert targets == {'collect_materials', 'optimize_prompt'}
    assert all(transition.get('when') for transition in analyze_transitions)
    assert 'semantic dependencies, not from a fixed subject/category keyword list' in analyze['prompt']
    assert 'search merely because the workflow has a collect_materials step' in analyze['prompt']
    assert 'NEXT_STEPS: optimize_prompt,generate_image' in analyze['prompt']
    assert 'REQUIRES:' in analyze['prompt']

    optimize = state['steps']['optimize_prompt']
    assert {'material': 'material_summary', 'required': False} in optimize['inputs']
    assert 'skipped collect_materials for a self-contained request' in optimize['prompt']


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

    prompt = optimize['prompt']
    for field in (
        'schema_version', 'mode', 'delivery', 'count', 'items', 'caption',
        'caption_box', 'caption_style', 'text_color', 'stroke_color',
        'stroke_width_ratio', 'communication_task', 'image_prompt',
        'last_frame_prompt', 'motion_prompt',
    ):
        assert field in prompt
    assert 'Cap static packs at 12 items and animated packs at 10 items' in prompt


def test_meme_prompts_generate_text_free_media_then_add_caption_locally():
    state = _load_state()
    optimize_prompt = state['steps']['optimize_prompt']['prompt']
    generate_prompt = state['steps']['generate_image']['prompt']
    enhance_prompt = state['steps']['enhance_image']['prompt']

    assert 'prohibit rendered words' in optimize_prompt
    assert 'Do not ask the image model to reserve a' in optimize_prompt
    assert '[0.15, 0.75, 0.85, 0.93]' in optimize_prompt
    assert 'Never add the caption here' in generate_prompt
    assert 'Call meme_add_caption once per base/item' in enhance_prompt
    assert 'caption_box and caption_style fields' in enhance_prompt
    assert 'never expose the uncaptioned base as the final meme' in enhance_prompt


def test_generate_then_enhance_owns_distinct_meme_strategies():
    workflow = _load_workflow()
    state = _load_state()
    generate = state['steps']['generate_image']
    enhance = state['steps']['enhance_image']
    prompt = generate['prompt'] + '\n' + enhance['prompt']

    assert 'CREATE_STATIC_MEME' in prompt
    assert 'CREATE_ANIMATED_MEME' in prompt
    assert 'CREATE_MEME_PACK' in prompt
    assert 'generated_base_image' in prompt

    assert {'material': 'meme_generation_plan', 'required': False} in generate['inputs']
    assert {'material': 'generated_base_image', 'required': True} in generate['outputs']
    assert {'material': 'meme_static_output', 'required': False} in enhance['outputs']
    assert set(generate['tools']) == {
        'image_generator', 'select_image_postprocess_route',
    }
    assert set(enhance['tools']) == {
        'image_editor', 'video_generator', 'video_to_gif', 'meme_add_caption',
    }
    assert 'No image edit, video, GIF, or caption tool may' in generate['acceptance_criteria']
    assert 'Meme/caption routes whose REQUIRES contains image_editor' in enhance['prompt']

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
    assert 'Never jump directly to enhance_image' in optimize['prompt']
    assert 'return generate_image' in optimize['acceptance_criteria']


def test_existing_non_meme_routes_remain_available():
    state = _load_state()
    analyze_prompt = state['steps']['analyze_subject']['prompt']
    generate_prompt = state['steps']['generate_image']['prompt']

    for route in (
        'CREATE_NEW', 'KB_STYLE', 'REFERENCE_GENERATE', 'FIND_AND_EDIT',
        'EDIT_UPLOAD', 'CREATE_ANIMATED', 'ANIMATE_UPLOAD',
    ):
        assert route in analyze_prompt
    assert 'CREATE_NEW / KB_STYLE / REFERENCE_GENERATE' in generate_prompt
    assert 'CREATE_ANIMATED / ANIMATE_UPLOAD / CREATE_ANIMATED_MEME' in generate_prompt
    assert 'FIND_AND_EDIT / EDIT_UPLOAD' in generate_prompt


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
    assert 'ZERO-IMAGE FAILURE CONTRACT' in generate['prompt']
    assert 'Chat must tell the user why no image' in generate['prompt']


def test_startup_ask_is_only_an_information_completeness_gate():
    when_to_use = _load_workflow()['when_to_use']

    assert 'ASK/CLARIFICATION RULE' in when_to_use
    assert 'ask only whether essential request information is complete' in when_to_use
    assert 'never use the Ask card to present a plan' in when_to_use


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
        assert slot_id in generate['prompt']

    generate_tab = tabs['generate_image']
    assert generate_tab['composite_tab_position'] == 'left'
    assert generate_tab['composite_behavior']['hide_empty_columns'] is True
    assert [slot['id'] for slot in generate_tab['slots']] == [
        'generated_image_output',
        'generated_first_frame',
        'generated_last_frame',
        'generation_reference_images',
    ]
    assert 'if the user uploaded three images, all three must be visible' in generate['prompt']

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
        'video_output',
        'gif_output',
    ]
    collect_tab = tabs['collect_materials']
    assert collect_tab['composite_behavior']['hide_empty_columns'] is True
    assert {'material': 'generated_first_frame', 'required': False} in enhance['inputs']
    assert {'material': 'generated_last_frame', 'required': False} in enhance['inputs']
    assert {'material': 'generation_reference_images', 'required': False} in enhance['inputs']
    assert 'first_frame_url=' in enhance['prompt']
    assert 'last_frame_url=' in enhance['prompt']
    assert 'reference_urls=' in enhance['prompt']
    assert 'does not mix frame roles with reference-image mode' in enhance['prompt']
    assert "use ratio='adaptive'" in enhance['prompt']


def test_generic_multi_gif_count_is_aligned_with_composite_pages():
    state = _load_state()
    optimize = state['steps']['optimize_prompt']
    generate = state['steps']['generate_image']
    enhance = state['steps']['enhance_image']

    assert 'COUNT: <requested item count, default 1, clamp to 1–10>' in optimize['prompt']
    assert 'generate exactly COUNT first/base images in order' in generate['prompt']
    assert 'repeat it COUNT times' in generate['prompt']
    assert 'base count must equal COUNT' in enhance['prompt']
    assert 'same sort_order as its first/base frame' in enhance['acceptance_criteria']
