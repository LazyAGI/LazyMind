"""Experiment requests use the active model window, including the output schema."""
import copy
import json
from pathlib import Path
from unittest.mock import Mock

import lazyllm
import pytest
from lazyllm.tools.writer.tools.base import WriterToolBase

from lazymind import model_config
from lazymind.common.token_estimation import estimate_tokens
from lazymind.config import config
from lazymind.document_tools.paper import contracts, experiment, workflow
from lazymind.document_tools.paper.experiment import algorithm, preparation
from lazymind.document_tools.paper.experiment.generation import GeneratedExperiment
from lazymind.document_tools.paper.prompts.experiment_prompt import EXPERIMENT_PROMPT


@pytest.fixture
def model_response(monkeypatch):
    path = Path(__file__).resolve().parents[3] / 'fixtures/paper-writing/experiment.json'
    example = json.loads(path.read_text())
    response = copy.deepcopy(example['model_response'])
    files = {item['path']: item['content'] for item in response.pop('code_files')}
    response.update(code=files['experiment.py'], config=json.loads(files['config.json']), dependencies=[])
    model = Mock(return_value=json.dumps(response, ensure_ascii=False))
    model.share.return_value = model
    model.prompt.return_value = model
    monkeypatch.setattr(algorithm, 'AutoModel', lambda **_: model)
    selector = Mock(return_value=json.dumps({'action': 'simulation', 'table_ids': [], 'reason': '按要求模拟'}))
    selector.share.return_value = selector
    selector.prompt.return_value = selector
    monkeypatch.setattr(preparation, 'AutoModel', lambda **_: selector)
    return example['input'], model


def test_large_current_review_fits_128k_model_without_truncation(model_response, monkeypatch):
    inputs, model = model_response
    inputs['evidence'] = '这是实际综述证据，不要截断。' * 1500
    before = copy.deepcopy(inputs)
    monkeypatch.setattr(model_config, 'load_model_config', lambda: {'llm': {'source': 'openai'}})
    with config.temp('context_compression_default_max_input_tokens', 131072):
        result = experiment.run(inputs)
    model.assert_called_once()
    prompt = model.call_args.args[0]
    assert estimate_tokens(prompt) > 16000
    assert json.loads(prompt.split('Input JSON:\n', 1)[1])['evidence'] == inputs['evidence']
    assert inputs == before
    assert result['result_meta']['execution_status'] == 'not_run'


@pytest.mark.parametrize('source', ['openai', 'dynamic'])
def test_schema_overhead_is_checked_against_selected_model(model_response, monkeypatch, source):
    inputs, model = model_response
    normalized = contracts.validate_input('experiment', inputs)
    prompt = EXPERIMENT_PROMPT + '\n\nInput JSON:\n' + json.dumps(normalized, ensure_ascii=False)
    system = WriterToolBase()._structured_output_prompt(GeneratedExperiment)
    input_tokens, total_tokens = estimate_tokens(prompt), estimate_tokens(prompt + system)
    window = int((input_tokens + total_tokens) / 2 / 0.9)
    role = {'source': source, 'max_input_tokens': window} if source == 'openai' else {'source': source}
    monkeypatch.setattr(model_config, 'load_model_config', lambda: {'llm': role})
    monkeypatch.setitem(lazyllm.globals['config'], 'dynamic_model_configs', {
        'llm': {'chat': {'max_input_tokens': window}},
    })
    before = copy.deepcopy(inputs)
    with config.temp('context_compression_reserved_output_tokens', 0):
        with pytest.raises(ValueError, match='实验输入超过当前模型预算'):
            experiment.run(inputs)
    assert input_tokens < int(window * 0.9) < total_tokens
    model.assert_not_called()
    assert inputs == before


def test_experiment_projects_current_materials_without_serializing_historical_pool():
    pool = workflow.seed_pool()
    workflow.add_material(pool, 'evidence', '过期证据' * 100000, [], [])
    workflow.add_material(pool, 'evidence', '当前综述', [], [])
    workflow.add_material(pool, 'question', '当前研究问题', [], [])
    before = copy.deepcopy(pool)
    inputs = workflow.business_input('experiment', pool, '继续实验')
    assert inputs == {'topic': pool['topic'], 'request': '继续实验',
                      'question': '当前研究问题', 'evidence': '当前综述',
                      'data': workflow.material(pool, 'data')['content'],
                      'papers': workflow.material(pool, 'papers')['content'], 'input_is_demo': True}
    assert pool == before


def test_generation_receives_only_literature_abstracts(model_response, monkeypatch):
    inputs, model = model_response
    inputs['papers'] = [{'id': 'reference', 'title': 'Related work', 'abstract': 'A concise abstract.',
                         'text': 'EXTERNAL_DOCUMENT_BODY' * 30000,
                         'reading': {'content_type': 'document', 'content': 'NESTED_DOCUMENT_BODY' * 30000}}]
    before = copy.deepcopy(inputs)
    monkeypatch.setattr(model_config, 'load_model_config', lambda: {'llm': {'source': 'openai'}})
    with config.temp('context_compression_default_max_input_tokens', 131072):
        experiment.run(inputs)
    model.assert_called_once()
    prompt = model.call_args.args[0]
    payload = json.loads(prompt.split('Input JSON:\n', 1)[1])
    assert payload['papers'] == [{'id': 'reference', 'title': 'Related work', 'abstract': 'A concise abstract.'}]
    assert 'EXTERNAL_DOCUMENT_BODY' not in prompt
    assert 'NESTED_DOCUMENT_BODY' not in prompt
    assert payload['data'] == inputs.get('data')
    assert inputs == before
