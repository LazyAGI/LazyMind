"""Paper entry formatting tolerance and early citation validation."""
import json
from pathlib import Path

import pytest
import yaml

from lazymind.document_tools.paper import contracts, workflow
from lazymind.document_tools.paper.analysis.schema import Constraints


ROOT = Path(__file__).resolve().parents[4]
STYLES = ('APA 7', 'Chicago Author-Date', 'MLA 9', 'IEEE', 'Vancouver', 'GB/T 7714')


def bind_entry(tmp_path, value, slot='paper_topic', text='Paper'):
    structured, plain = tmp_path / 'paper_input.json', tmp_path / 'topic.txt'
    structured.write_text(json.dumps(value), encoding='utf-8')
    plain.write_text(text, encoding='utf-8')
    return {'paper_input': str(structured), slot: str(plain)}


@pytest.mark.parametrize('topic,incoming', [
    ('AI在论文写作中的应用', 'AI在论文写作中的应用'),
    ('AI在论文写作中的应用', '\n AI 在论文\t写作中的应用\r\n'),
    ('AI在论文写作中的应用', 'ＡＩ\u3000在论文\u00a0写作中的应用'),
    ('Document-State Evolution', 'document-state\nEVOLUTION\n'),
    ('“AI”的应用', '"AI"的应用'),
    ('「AI」的应用', '“ai”的应用'),
    ("AI's role", 'AI’s role'),
])
def test_equivalent_topics_initialize_one_pool(tmp_path, topic, incoming):
    bound = bind_entry(tmp_path, {'topic': topic}, text=incoming)
    pool = workflow.initialize_pool(bound)
    assert pool['topic'] == incoming.strip()
    assert workflow.validate_pool(pool) is pool


@pytest.mark.parametrize('topic,incoming', [
    ('提升准确率', '提升效率'), ('C++ writing', 'C# writing'), ('P53', 'P54'),
])
def test_different_topics_show_both_versions(tmp_path, topic, incoming):
    bound = bind_entry(tmp_path, {'topic': topic}, text=incoming)
    with pytest.raises(ValueError, match='topic 存在冲突输入') as error:
        workflow.initialize_pool(bound)
    assert repr(topic) in str(error.value)
    assert repr(incoming) in str(error.value)


@pytest.mark.parametrize('field,slot', [('manuscript', 'manuscript_text'), ('question', 'experiment_question')])
def test_non_topic_inputs_keep_exact_comparison(tmp_path, field, slot):
    bound = bind_entry(tmp_path, {'topic': 'Paper', field: 'Original'}, slot, 'Original\n')
    with pytest.raises(ValueError, match=f'{field} 存在冲突输入'):
        workflow.initialize_pool(bound)


@pytest.mark.parametrize('style', STYLES)
def test_supported_citation_styles_survive_entry_pool_and_f4(tmp_path, style):
    bound = bind_entry(tmp_path, {'topic': 'Paper', 'constraints': {'citation_style': style}})
    pool = workflow.initialize_pool(bound)
    assert pool['constraints']['citation_style'] == style
    assert Constraints.model_validate(pool['constraints']).citation_style == style


@pytest.mark.parametrize('style', ['APA', 'MLA', 'unknown'])
def test_unsupported_style_is_rejected_before_any_step(tmp_path, style):
    bound = bind_entry(tmp_path, {'topic': 'Paper', 'constraints': {'citation_style': style}})
    with pytest.raises(ValueError, match='citation_style') as error:
        workflow.initialize_pool(bound)
    assert repr(style) in str(error.value)
    assert all(allowed in str(error.value) for allowed in STYLES)


def test_discovery_and_runtime_share_f4_citation_values():
    package = yaml.safe_load((ROOT / 'workflows/paper-writing/workflow.yaml').read_text())
    entry = package['runtime']['input_contracts']['paper_input']
    field = entry['schema']['$defs']['business']['properties']['constraints']['properties']['citation_style']
    assert tuple(field['enum']) == contracts.CITATION_STYLES == STYLES
    assert all(style in entry['description'] for style in STYLES)
