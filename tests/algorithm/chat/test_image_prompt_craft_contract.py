from pathlib import Path

import yaml


ROOT = Path(__file__).resolve().parents[3]
SKILL_DIR = ROOT / 'skills' / 'design' / 'image-prompt-craft'


def _workflow_step(workflow: str, step: str) -> dict:
    path = ROOT / 'workflows' / workflow / 'scenario' / 'state.yml'
    return yaml.safe_load(path.read_text(encoding='utf-8'))['steps'][step]


def test_image_prompt_craft_skill_routes_each_supported_mode() -> None:
    skill = (SKILL_DIR / 'SKILL.md').read_text(encoding='utf-8')

    assert '[TODO' not in skill
    assert 'references/general-image.md' in skill
    assert 'references/image-editing.md' in skill
    assert 'references/ppt-background.md' in skill
    assert 'references/series-consistency.md' in skill
    assert 'PPT 页面图' in skill
    assert '先确定画布和区域分配' in skill

    for name in (
        'general-image.md',
        'image-editing.md',
        'ppt-background.md',
        'series-consistency.md',
    ):
        assert (SKILL_DIR / 'references' / name).is_file()


def test_image_workflow_embeds_general_prompt_craft_contract() -> None:
    step = _workflow_step('image-workflow', 'optimize_prompt')
    contract = step['prompt'] + '\n' + step['acceptance_criteria']

    assert 'Image Prompt Craft contract' in contract
    assert 'canvas/aspect ratio, final use, composition' in contract
    assert '5–12 concrete visible nouns' in contract
    assert 'materials, lighting, and palette as separate controls' in contract
    assert 'quality-keyword stacks' in contract
