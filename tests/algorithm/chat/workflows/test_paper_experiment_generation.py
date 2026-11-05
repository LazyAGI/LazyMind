"""Host packaging and refined-topic handoff, without replacing any model."""
import copy
import json
from pathlib import Path

import pytest
import yaml

from lazymind.document_tools.paper import workflow
from lazymind.document_tools.paper.experiment.algorithm import build_output
from lazymind.document_tools.paper.experiment.generation import (
    GeneratedExperiment, assemble, finish_design_repair, validate_generation,
)
from lazymind.document_tools.paper.experiment.schema import ExperimentDraft
from lazymind.document_tools.paper.experiment.stream import ExperimentStream

ROOT = Path(__file__).resolve().parents[4]


@pytest.fixture
def example():
    return json.loads((ROOT / 'tests/fixtures/paper-writing/experiment.json').read_text())


def generation(example):
    value = copy.deepcopy(example['model_response'])
    files = {v['path']: v['content'] for v in value.pop('code_files')}
    return {**value, 'code': files['experiment.py'], 'config': json.loads(files['config.json']),
            'dependencies': [v for v in files['requirements.txt'].splitlines() if v.strip()]}


def test_model_schema_has_no_filenames():
    properties = GeneratedExperiment.model_json_schema()['properties']
    assert 'code_files' not in properties
    assert {'code', 'config', 'dependencies'} <= properties.keys()


def test_host_package_matches_existing_output_contract(example):
    candidate = generation(example)
    candidate['dependencies'] = []
    before = copy.deepcopy(candidate)
    # A valid generation must not use a writer or spend a repair model call.
    draft = validate_generation(None, candidate, example['input'])
    output = build_output(draft, example['input'])
    assert candidate == before
    files = {v['path']: v['content'] for v in output.pop('code_files')}
    expected = copy.deepcopy(example['output'])
    expected.pop('code_files')
    assert output == expected
    assert set(files) == {'experiment.py', 'config.json', 'requirements.txt', 'README.md'}
    assert files['requirements.txt'] == ''
    assert candidate['explanation'] in files['README.md']
    assert 'python run_experiment.py' in files['README.md']
    assert json.loads(files['config.json']) == candidate['config']


@pytest.mark.parametrize('field', ['code', 'config', 'dependencies'])
def test_missing_content_is_not_silently_invented(example, field):
    candidate = generation(example)
    del candidate[field]
    with pytest.raises(ValueError):
        assemble(candidate)


@pytest.mark.parametrize('field,value', [
    ('code', 'not python : :'), ('config', []), ('config', {'x': float('nan')}),
    ('dependencies', ['numpy\npandas']), ('plan', '  '),
])
def test_packaging_keeps_core_validation(example, field, value):
    candidate = generation(example)
    candidate[field] = value
    with pytest.raises(ValueError):
        assemble(candidate)


def test_model_cannot_override_host_filenames(example):
    candidate = generation(example)
    candidate['code_files'] = [{'path': '../../wrong', 'content': 'invalid'}]
    assert len(ExperimentDraft.model_validate(assemble(candidate)).code_files) == 4


def test_repaired_design_preserves_content_and_cannot_start_another_repair(example):
    candidate = generation(example)
    before = copy.deepcopy(candidate)
    draft = finish_design_repair(candidate)
    assert draft.plan == candidate['plan']
    assert draft.results.model_dump() == candidate['results']
    assert candidate == before
    candidate['results']['metrics'][0]['values'][0] = 'wrong type'
    with pytest.raises(ValueError):
        finish_design_repair(candidate)


def test_new_code_field_still_reaches_existing_preview(example):
    stream = ExperimentStream(lambda _event: None, 'T', ['experiment_code'])
    stream({'tag': 'text', 'delta': json.dumps(generation(example), ensure_ascii=False)})
    stream.preview()
    assert '## experiment.py' in stream.contents['experiment_code']
    assert 'run_experiment' in stream.contents['experiment_code']
    stream.close()


def test_refinement_can_enter_experiment_without_bypassing_literature():
    state = yaml.safe_load((ROOT / 'workflows/paper-writing/scenario/state.yml').read_text())
    assert 'experiment' in {v['to'] for v in state['transitions']['refine_topic']}
    assert 'experiment' not in {v['to'] for v in state['transitions']['research_topic']}
    assert {'refine_topic_pool', 'refine_topic_document'} <= {
        v['material'] for v in state['steps']['experiment']['inputs']}
    assert workflow.routes_after({'history': [{'node': 'F1', 'step': 'research_topic'}]}) == ['F2']
    assert workflow.routes_after({'history': [{'node': 'F1', 'step': 'refine_topic'}]}) == ['F2', 'F3']
    assert workflow.routes_after({'history': [{'node': 'F2'}, {'node': 'F1'}]}) == ['F2', 'F3']


def test_refined_pool_keeps_literature_and_latest_question():
    old = workflow.seed_pool()
    refined = copy.deepcopy(old)
    workflow.add_material(refined, 'question', '调整后要验证的研究问题', [], [])
    workflow.add_material(refined, 'evidence', '来自 F2 的综述证据', [], [])
    refined['sequence'] = old['sequence'] + 1
    refined['history'].append({'node': 'F1', 'step': 'refine_topic', 'inputs': [], 'outputs': [], 'request': ''})
    selected = workflow.pool_from_inputs({'literature_review_pool': old, 'refine_topic_pool': refined})
    inputs = workflow.business_input('experiment', selected, '继续实验')
    assert inputs['question'] == '调整后要验证的研究问题'
    assert inputs['evidence'] == '来自 F2 的综述证据'
    assert workflow.material(selected, 'papers')['content'] == workflow.material(old, 'papers')['content']
