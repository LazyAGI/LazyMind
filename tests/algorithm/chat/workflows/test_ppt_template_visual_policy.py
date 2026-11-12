from pathlib import Path

import yaml

from lazymind.chat.workflow.clarification import resolve_fields


def test_template_collection_preserves_visuals_and_text_evidence():
    root = Path(__file__).resolve().parents[4]
    state = yaml.safe_load((root / 'workflows/ppt-workflow/scenario/state.yml').read_text(encoding='utf-8'))
    steps = state['steps']
    collect = steps['collect_materials']
    assert {'material': 'template_mode', 'required': False} in collect['inputs']
    assert 'Evidence diagrams/product screenshots are still valid' in collect['prompt']
    assert 'avoid unrelated decorative' in collect['prompt']
    assert 'only for a concrete remaining gap' in collect['prompt']
    assert 'required papers or other explicitly required sources are unread' in collect['prompt']
    assert 'ppt_collect_github_prs' in collect['tools']
    assert steps['generate_backgrounds']['skip_if'] == {'material': 'skip_background_images'}
    assert 'bound slide_outline, background_images, material_images' in steps['generate_ppt']['prompt']


def test_template_is_read_before_material_collection():
    root = Path(__file__).resolve().parents[4]
    state = yaml.safe_load((root / 'workflows/ppt-workflow/scenario/state.yml').read_text(encoding='utf-8'))
    routes = state['transitions']['analyze_requirements']
    assert routes[0]['to'] == 'collect_materials'
    assert 'template_mode is upload' in routes[0]['when']
    assert all('template_mode is none' in route['when'] for route in routes[1:])
    assert 'read_template' not in state['transitions']
    assert 'read_template' not in state['steps']
    read = state['steps']['analyze_requirements']
    assert read['tools'] == ['pptx_analyzer']
    assert 'across every page' in read['prompt']
    assert {'material': 'template_content_brief', 'required': False} in read['outputs']
    workflow = yaml.safe_load((root / 'workflows/ppt-workflow/workflow.yaml').read_text(encoding='utf-8'))
    analysis = next(tab for tab in workflow['ui']['tabs'] if tab['id'] == 'analysis')
    assert analysis['slots'] == [{'id': 'requirement_analysis'}]
    brief_slot = next(slot for slot in workflow['slots'] if slot['id'] == 'template_content_brief')
    assert brief_slot['exposed'] is False and brief_slot['user_visible'] is False
    for name in ('collect_materials', 'build_outline', 'generate_ppt'):
        assert {'material': 'template_content_brief', 'required': False} in state['steps'][name]['inputs']


def test_template_ask_omits_visuals_but_allows_missing_page_count():
    root = Path(__file__).resolve().parents[4]
    workflow = yaml.safe_load((root / 'workflows/ppt-workflow/workflow.yaml').read_text(encoding='utf-8'))
    runtime = workflow['runtime']
    fields = {field['id']: field for field in runtime['clarification_fields']}
    assert 'never ask about style' in workflow['when_to_use']
    assert 'filling all existing template pages' in fields['slide_count']['ask_when']
    assert 'opening/closing pages' in fields['slide_count']['ask_when']
    assert 'probation defense' in fields['target_customer']['ask_when']

    supplied = {
        'topic': '转正答辩',
        'template_pptx': 'template.pptx',
        'presentation_output_format': 'native_pptx',
        'visual_style': '科研风格',
        'generate_background_images': 'enabled',
    }
    values, missing = resolve_fields(runtime, '用这个模板制作转正答辩 PPTX', supplied)
    assert values['template_mode'] == 'upload'
    assert values['presentation_output_format'] == 'native_pptx'
    assert {'visual_style', 'generate_background_images'}.isdisjoint(values)
    assert [field['id'] for field in missing] == ['slide_count']


def test_native_pptx_is_a_final_result_not_a_separate_tab():
    root = Path(__file__).resolve().parents[4]
    workflow = yaml.safe_load((root / 'workflows/ppt-workflow/workflow.yaml').read_text(encoding='utf-8'))
    tabs = workflow['ui']['tabs']
    assert tabs[0]['id'] == 'analysis'
    assert all(tab['id'] != 'native_pptx' for tab in tabs)
    final = next(tab for tab in tabs if tab['id'] == 'composite_preview')
    assert final['step_id'] == 'generate_ppt'
    assert {'id': 'native_pptx'} in final['slots']
    assert any(child.get('slot') == 'native_pptx' for child in final['composite_layout']['children'])


def test_material_stage_only_displays_summary():
    root = Path(__file__).resolve().parents[4]
    workflow = yaml.safe_load((root / 'workflows/ppt-workflow/workflow.yaml').read_text(encoding='utf-8'))
    tab = next(tab for tab in workflow['ui']['tabs'] if tab['id'] == 'materials')
    assert tab['slots'] == [{'id': 'material_summary'}]
    slots = {slot['id']: slot for slot in workflow['slots']}
    for name in ('template_pptx', 'uploaded_materials', 'material_images'):
        assert slots[name]['exposed'] is False
