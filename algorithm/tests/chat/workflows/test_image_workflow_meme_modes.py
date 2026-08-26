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

    assert '给上传的小狗做敬礼手势，然后配上字幕‘收到!’' in workflow['when_to_use']
    assert 'first perform the' in analyze_prompt
    assert 'non-text visual edit, then add the exact caption with meme_add_caption' in analyze_prompt
    assert 'painted, printed,' in analyze_prompt
    assert 'engraved, or otherwise integrated into a physical object/scene region' in analyze_prompt
    assert '给这个小狗做敬礼手势，然后配上字幕‘收到!’' in optimize_contract
    assert 'caption is exactly "收到!"' in optimize_contract
    assert 'caption text must not be delegated to image_editor' in optimize_contract
    assert 'image_editor performs only the requested non-text visual change' in generate_prompt
    assert 'call meme_add_caption exactly once' in generate_prompt


def test_analyze_can_skip_material_collection_by_semantic_judgment():
    state = _load_state()
    analyze = state['steps']['analyze_subject']
    transitions = state['transitions']['analyze_subject']
    targets = {transition['to'] for transition in transitions}

    assert analyze['route'] == 'choice'
    assert targets == {'collect_materials', 'optimize_prompt'}
    assert all(transition.get('when') for transition in transitions)
    assert 'semantic dependencies, not from a fixed subject/category keyword list' in analyze['prompt']
    assert 'search merely because the workflow has a collect_materials step' in analyze['prompt']
    assert 'NEXT_STEPS: optimize_prompt,generate_image' in analyze['prompt']
    assert 'SKIP_STEPS: collect_materials,enhance_image' in analyze['prompt']

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
        'stroke_width_ratio', 'communication_task', 'image_prompt', 'motion_prompt',
    ):
        assert field in prompt
    assert 'Cap static packs at 12 items and animated packs at 5 items' in prompt


def test_meme_prompts_generate_text_free_media_then_add_caption_locally():
    state = _load_state()
    optimize_prompt = state['steps']['optimize_prompt']['prompt']
    generate_prompt = state['steps']['generate_image']['prompt']

    assert 'prohibit rendered words' in optimize_prompt
    assert 'Do not ask the image model to reserve a' in optimize_prompt
    assert '[0.15, 0.75, 0.85, 0.93]' in optimize_prompt
    assert 'call meme_add_caption exactly once' in generate_prompt
    assert 'text_color=<items[0].caption_style.text_color>' in generate_prompt
    assert 'stroke_color=<items[0].caption_style.stroke_color>' in generate_prompt
    assert 'stroke_width_ratio=<items[0].caption_style.stroke_width_ratio>' in generate_prompt
    assert 'horizontal/vertical' in generate_prompt
    assert 'Never save the uncaptioned base as the final meme' in generate_prompt
    assert 'meme_add_caption → sequential save_artifacts' in generate_prompt


def test_generate_state_owns_three_distinct_meme_strategies():
    workflow = _load_workflow()
    state = _load_state()
    generate = state['steps']['generate_image']
    prompt = generate['prompt']

    assert 'Mode 1: one static meme (CREATE_STATIC_MEME)' in prompt
    assert 'Mode 2: one animated meme (CREATE_ANIMATED_MEME)' in prompt
    assert 'Mode 3: meme pack (CREATE_MEME_PACK)' in prompt
    assert 'stop before calling a paid media tool' in prompt

    assert {'material': 'meme_generation_plan', 'required': False} in generate['inputs']
    assert {'material': 'meme_static_output', 'required': False} in generate['outputs']
    assert set(generate['tools']) >= {
        'image_generator', 'image_editor', 'video_generator', 'video_to_gif',
        'meme_add_caption',
    }

    tool_functions = {
        function
        for script in workflow['tool_scripts']
        for function in script['functions']
    }
    assert 'meme_add_caption' in tool_functions

    slots = {slot['id']: slot for slot in workflow['slots']}
    assert slots['meme_static_output']['type'] == 'image'
    assert slots['meme_static_output']['cardinality'] == 'list'
    assert slots['meme_static_output']['ordered'] is True


def test_existing_non_meme_routes_remain_available():
    state = _load_state()
    analyze_prompt = state['steps']['analyze_subject']['prompt']
    generate_prompt = state['steps']['generate_image']['prompt']

    for route in (
        'CREATE_NEW', 'KB_STYLE', 'REFERENCE_GENERATE', 'FIND_AND_EDIT',
        'EDIT_UPLOAD', 'CREATE_ANIMATED', 'ANIMATE_UPLOAD',
    ):
        assert route in analyze_prompt
    assert 'ordinary still image (CREATE_NEW / KB_STYLE / REFERENCE_GENERATE)' in generate_prompt
    assert 'legacy' in generate_prompt
    assert 'CREATE_ANIMATED / ANIMATE_UPLOAD routes' in generate_prompt
