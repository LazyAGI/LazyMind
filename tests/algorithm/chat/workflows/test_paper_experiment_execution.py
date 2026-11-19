"""POC execution and F4 handoff checks, with real subprocesses and no model replacements."""
import copy
import json
import sys
from pathlib import Path

import pytest

from lazymind.document_tools.paper.experiment.algorithm import build_output
from lazymind.document_tools.paper.experiment.executor import decide, execute, validate_code
from lazymind.document_tools.paper.experiment.handoff import to_analysis
from lazymind.document_tools.paper.experiment.schema import ExperimentDraft

ROOT = Path(__file__).resolve().parents[4]
SANDBOX = sys.platform in ('win32', 'darwin') or (sys.platform == 'linux' and any(
    Path(marker).exists() for marker in ('/.dockerenv', '/run/.containerenv')))


@pytest.fixture
def example():
    return json.loads((ROOT / 'tests/fixtures/paper-writing/experiment.json').read_text())


@pytest.mark.parametrize('requirements', [
    {'device': 'gpu'}, {'requires_network': True}, {'estimated_seconds': 3600},
    {'requires_external_data': True},
])
def test_complex_experiments_use_preview_without_execution(example, requirements):
    example['model_response']['execution_requirements'].update(requirements)
    draft = ExperimentDraft.model_validate(example['model_response'])
    receipt = execute(draft, example['input'], allowed=True)
    assert receipt['status'] == 'not_run'
    assert receipt['elapsed_seconds'] == 0
    output = build_output(draft, example['input'], receipt)
    assert output['experiment']['results'] == example['output']['experiment']['results']
    assert to_analysis({'topic': 'T', **{k: output[k] for k in ('experiment', 'result_meta')}})['data']['rows']


def test_refusal_never_starts_experiment(example):
    draft = ExperimentDraft.model_validate(example['model_response'])
    assert '未获' in decide(draft, example['input'], False)
    assert execute(draft, example['input'])['status'] == 'not_run'


def test_future_annotations_is_not_an_external_dependency(example):
    source = 'from __future__ import annotations\n' + example['model_response']['code_files'][0]['content']
    assert validate_code(source)


def test_type_name_in_validation_error_is_allowed(example):
    source = example['model_response']['code_files'][0]['content']
    source += '\ndef explain(value):\n    return type(value).__name__\n'
    assert validate_code(source)


def test_dimension_metadata_does_not_break_failure_fallback(example):
    example['model_response']['metrics'].append({'column': 'method', 'description': '方法标签', 'unit': None})
    draft = ExperimentDraft.model_validate(example['model_response'])
    assert all(item.column != 'method' for item in draft.metrics)
    output = build_output(draft, example['input'], {
        'status': 'failed', 'reason': 'test error', 'logs': 'test error',
        'elapsed_seconds': 0.01, 'results': None,
    })
    assert to_analysis({'topic': 'T', 'experiment': output['experiment'],
                        'result_meta': output['result_meta']})['data']['rows']


def test_no_preview_request_keeps_failure_explicit(example):
    candidate = example['model_response']
    candidate.update(results=None, no_results_reason='只接受真实计算结果')
    candidate['execution_requirements']['device'] = 'gpu'
    draft = ExperimentDraft.model_validate(candidate)
    output = build_output(draft, example['input'], execute(draft, example['input'], allowed=True))
    assert output['experiment']['results'] is None
    assert output['result_meta']['data_origin'] == 'none'


@pytest.mark.parametrize('statement', [
    'import os', 'import requests', 'open("/tmp/x", "w")', 'eval("1")', 'import torch',
])
def test_unsupported_code_does_not_enter_worker(example, statement):
    source = example['model_response']['code_files'][0]['content'] + '\n' + statement
    with pytest.raises(ValueError):
        validate_code(source)


@pytest.mark.skipif(not SANDBOX, reason='Requires a Linux container or native desktop isolation')
def test_real_computation_replaces_preview_and_matches_f4(example):
    draft = ExperimentDraft.model_validate(example['model_response'])
    receipt = execute(draft, example['input'], allowed=True)
    assert receipt['status'] == 'succeeded', receipt
    output = build_output(draft, example['input'], receipt)
    assert output['experiment']['executed'] is True
    assert output['experiment']['results'] != example['output']['experiment']['results']
    assert output['result_meta']['method'] == 'simulation'
    adapted = to_analysis({'topic': 'T', 'experiment': output['experiment'], 'result_meta': output['result_meta']})
    assert adapted['data']['rows'] == receipt['results']['rows']
    assert adapted['data']['column_types'] == ['integer', 'string', 'number']
    assert adapted['data']['units'] == [None, None, None]
    assert '实际运行' in adapted['data_context']['description']
    # Fixed seed gives independently repeated subprocesses the same computed table.
    assert execute(draft, example['input'], allowed=True)['results'] == receipt['results']


@pytest.mark.skipif(not SANDBOX, reason='Requires a Linux container or native desktop isolation')
def test_execution_failure_preserves_preview_and_error(example):
    candidate = copy.deepcopy(example['model_response'])
    code = candidate['code_files'][0]
    code['content'] = code['content'].replace(
        '    rng =', '    raise RuntimeError("deliberate POC failure")\n    rng =')
    draft = ExperimentDraft.model_validate(candidate)
    receipt = execute(draft, example['input'], allowed=True)
    assert receipt['status'] == 'failed'
    assert 'deliberate POC failure' in receipt['logs']
    output = build_output(draft, example['input'], receipt)
    assert output['experiment']['executed'] is False
    assert output['experiment']['results'] == example['output']['experiment']['results']
    assert output['result_meta']['execution_status'] == 'failed'
    adapted = to_analysis({'topic': 'T', 'experiment': output['experiment'], 'result_meta': output['result_meta']})
    assert adapted['data']['rows'] == example['output']['experiment']['results']['rows']
    assert '模型生成' in adapted['data_context']['description']
