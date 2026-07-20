from pathlib import Path


SKILL_PATH = Path(__file__).parents[3] / 'skills/research/deep-research/SKILL.md'


def test_deep_research_does_not_claim_all_content_generation():
    content = SKILL_PATH.read_text(encoding='utf-8')

    assert 'Load this skill BEFORE starting any content generation task' not in content
    assert 'Do not load it for a normal answer' in content
    assert 'Producing videos or multimedia content' not in content


def test_deep_research_is_source_agnostic_and_defers_routing_to_the_host():
    content = SKILL_PATH.read_text(encoding='utf-8')

    assert 'Material Retrieval and Source Planning' in content
    assert "source priorities supplied by the system and the user" in content
    for framework_term in ('@mentioned', 'KBToolkit', 'kb_search', 'web_search', 'url_fetch', 'Wikipedia'):
        assert framework_term not in content
