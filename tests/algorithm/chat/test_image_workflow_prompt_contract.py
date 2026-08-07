from pathlib import Path

import yaml


def _image_workflow_state():
    root = Path(__file__).resolve().parents[3]
    path = root / 'workflows' / 'image-workflow' / 'scenario' / 'state.yml'
    return yaml.safe_load(path.read_text(encoding='utf-8'))


def test_kb_style_accepts_text_only_materials_and_passes_them_to_prompt():
    steps = _image_workflow_state()['steps']
    collect = steps['collect_materials']
    optimize = steps['optimize_prompt']

    assert collect['outputs'][0] == {
        'material': 'material_images',
        'required': False,
    }
    assert 'A text-only KB result is successful' in collect['prompt']
    assert 'do NOT call web_search or' in collect['prompt']
    assert 'material_images are optional (0–3)' in collect['acceptance_criteria']

    assert '{{material_summary}}' in optimize['prompt']
    assert {
        'material': 'material_summary',
        'required': True,
    } in optimize['inputs']
    assert 'Do not require material_images when the KB text is sufficient' in optimize['prompt']
