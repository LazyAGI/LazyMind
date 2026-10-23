from pathlib import Path

import yaml


def test_template_collection_preserves_visuals_and_text_evidence():
    root = Path(__file__).resolve().parents[4]
    state = yaml.safe_load((root / 'workflows/ppt-workflow/scenario/state.yml').read_text())
    steps = state['steps']
    collect = steps['collect_materials']
    assert {'material': 'template_mode', 'required': False} in collect['inputs']
    assert 'Default to zero material_images' in collect['prompt']
    assert 'image_topk=0' in collect['prompt']
    assert 'No applicable KB alone is not a reason' in collect['prompt']
    assert 'must not skip required text sources' in collect['prompt']
    assert 'ppt_collect_github_prs' in collect['tools']
    assert steps['generate_backgrounds']['skip_if'] == {'material': 'skip_background_images'}
    assert 'Do not collect additional images or generate backgrounds' in steps['generate_ppt']['prompt']


def test_template_is_read_before_material_collection():
    root = Path(__file__).resolve().parents[4]
    state = yaml.safe_load((root / 'workflows/ppt-workflow/scenario/state.yml').read_text())
    routes = state['transitions']['analyze_requirements']
    assert routes[0]['to'] == 'collect_materials'
    assert 'template_mode is upload' in routes[0]['when']
    assert all('template_mode is none' in route['when'] for route in routes[1:])
    assert 'read_template' not in state['transitions']
    assert 'read_template' not in state['steps']
    read = state['steps']['analyze_requirements']
    assert read['tools'] == ['pptx_analyzer']
    assert 'following next_page until null' in read['prompt']
    assert {'material': 'template_content_brief', 'required': False} in read['outputs']
    workflow = yaml.safe_load((root / 'workflows/ppt-workflow/workflow.yaml').read_text())
    analysis = next(tab for tab in workflow['ui']['tabs'] if tab['id'] == 'analysis')
    assert {'id': 'template_content_brief'} in analysis['slots']
    for name in ('collect_materials', 'build_outline', 'generate_ppt'):
        assert {'material': 'template_content_brief', 'required': False} in state['steps'][name]['inputs']


def test_template_ask_omits_visuals_but_allows_missing_page_count():
    root = Path(__file__).resolve().parents[4]
    workflow = yaml.safe_load((root / 'workflows/ppt-workflow/workflow.yaml').read_text())
    fields = {field['id']: field for field in workflow['runtime']['clarification_fields']}
    assert 'never ask about style' in workflow['when_to_use']
    assert 'uploaded, selected or identified' in fields['visual_style']['ask_when']
    assert 'Uploaded templates always disable' in fields['generate_background_images']['ask_when']
    assert 'filling all existing template pages' in fields['slide_count']['ask_when']
    assert 'opening/closing pages' in fields['slide_count']['ask_when']
    assert 'do not ask for another upload' in fields['template_mode']['ask_when']
    assert 'probation defense' in fields['target_customer']['ask_when']


def test_native_pptx_is_a_final_result_not_a_separate_tab():
    root = Path(__file__).resolve().parents[4]
    workflow = yaml.safe_load((root / 'workflows/ppt-workflow/workflow.yaml').read_text())
    tabs = workflow['ui']['tabs']
    assert tabs[0]['id'] == 'analysis'
    assert all(tab['id'] != 'native_pptx' for tab in tabs)
    final = next(tab for tab in tabs if tab['id'] == 'composite_preview')
    assert final['step_id'] == 'generate_ppt'
    assert {'id': 'native_pptx'} in final['slots']
    assert any(child.get('slot') == 'native_pptx' for child in final['composite_layout']['children'])


def test_material_stage_only_displays_summary():
    root = Path(__file__).resolve().parents[4]
    workflow = yaml.safe_load((root / 'workflows/ppt-workflow/workflow.yaml').read_text())
    tab = next(tab for tab in workflow['ui']['tabs'] if tab['id'] == 'materials')
    assert tab['slots'] == [{'id': 'material_summary'}]
    slots = {slot['id']: slot for slot in workflow['slots']}
    for name in ('template_pptx', 'uploaded_materials', 'material_images'):
        assert slots[name]['exposed'] is False
