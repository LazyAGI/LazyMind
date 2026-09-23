from __future__ import annotations

from types import SimpleNamespace

from lazyllm.tools.agent.skill_manager import SkillManager


def _write_skill(base, name: str) -> None:
    folder = base / name
    folder.mkdir()
    (folder / 'SKILL.md').write_text(
        f'---\nname: {name}\ndescription: {name} workflow\ntags: [private-search-tag]\n---\n'
        f'Full {name} body.\n',
        encoding='utf-8',
    )


def test_prompt_catalog_does_not_limit_loading(tmp_path) -> None:
    _write_skill(tmp_path, 'resident')
    _write_skill(tmp_path, 'on-demand')
    manager = SkillManager(
        dir=str(tmp_path),
        skills=['resident', 'on-demand'],
        prompt_skills=['resident'],
        sandbox=SimpleNamespace(),
    )
    prompt = manager.build_prompt()
    assert 'resident workflow' in prompt
    assert 'on-demand workflow' not in prompt
    assert 'private-search-tag' not in prompt
    assert 'source:' not in prompt
    assert 'Full on-demand body.' in manager.get_skill('on-demand')['content']
    assert 'on-demand workflow' not in ''.join(part['content'] for part in manager.describe_prompt())
    manager.set_prompt_skills([])
    assert 'resident workflow' not in manager.build_prompt()
    assert 'Full on-demand body.' in manager.get_skill('on-demand')['content']


def test_default_prompt_catalog_preserves_existing_behavior(tmp_path) -> None:
    _write_skill(tmp_path, 'demo')
    manager = SkillManager(dir=str(tmp_path), sandbox=SimpleNamespace())
    prompt = manager.build_prompt()
    assert 'Demo workflow' in prompt or 'demo workflow' in prompt
    assert 'source:' in prompt
