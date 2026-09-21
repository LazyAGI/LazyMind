import json
import zipfile
from pathlib import Path


REPO_ROOT = Path(__file__).parents[3]
LOCK_PATH = REPO_ROOT / 'skills/builtin-skills.lock.json'


def _deep_research_entry() -> dict:
    lock = json.loads(LOCK_PATH.read_text(encoding='utf-8'))
    return next(skill for skill in lock['skills'] if skill['key'] == 'deep-research')


def _deep_research_policy_text() -> str:
    entry = _deep_research_entry()
    package_path = REPO_ROOT / 'skills/.runtime/builtin-skills' / entry['package_file']
    if package_path.exists():
        with zipfile.ZipFile(package_path) as package:
            return package.read('SKILL.md').decode('utf-8')
    return entry['description']


def test_deep_research_does_not_claim_all_content_generation():
    content = _deep_research_policy_text()

    assert 'Load this skill BEFORE starting any content generation task' not in content
    assert 'normal answer' in content or 'Do NOT trigger for simple questions' in content
    assert 'Producing videos or multimedia content' not in content


def test_deep_research_is_source_agnostic_and_defers_routing_to_the_host():
    content = _deep_research_policy_text()

    assert 'source priorities supplied by the system and the user' in content
    for framework_term in ('@mentioned', 'KBToolkit', 'kb_search', 'web_search', 'url_fetch', 'Wikipedia'):
        assert framework_term not in content
