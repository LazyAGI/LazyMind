import sys
from pathlib import Path

import json
import pytest
from algorithm.config import config

_ROOT = Path(__file__).resolve().parent.parent.parent
for _p in (_ROOT, _ROOT / 'algorithm'):
    s = str(_p)
    if s not in sys.path:
        sys.path.insert(0, s)


@pytest.fixture(autouse=True)
def _evo_test_data_env(tmp_path, monkeypatch):
    data_dir = tmp_path / 'evo_data'
    base_dir = tmp_path / 'evo_base'
    data_dir.mkdir()
    base_dir.mkdir()

    eval_payload = {
        'report_id': 'test-report',
        'kb_id': 'test-kb',
        'case_details': [
            {
                'case_id': '1',
                'trace_id': 'trace_1',
                'query': 'Why did retrieval miss the key fact?',
                'rag_answer': 'Generated answer 1',
                'ground_truth': 'Ground truth 1',
                'retrieve_contexts': ['retrieved context 1'],
                'reference_contexts': ['gold context 1'],
                'retrieve_doc': ['doc-a'],
                'reference_doc': ['doc-a'],
                'reference_chunk_ids': ['node-1'],
                'reference_docids': ['doc-a'],
                'answer_correctness': 0.2,
                'faithfulness': 0.4,
                'context_recall': 0.3,
                'doc_recall': 0.5,
                'is_valid': True,
                'key_points': ['key fact'],
                'hit_key': [],
                'judge_reason': 'missing key fact',
            },
            {
                'case_id': '2',
                'trace_id': 'trace_2',
                'query': 'Why was this answer correct?',
                'rag_answer': 'Generated answer 2',
                'ground_truth': 'Ground truth 2',
                'retrieve_contexts': ['retrieved context 2'],
                'reference_contexts': ['gold context 2'],
                'retrieve_doc': ['doc-b'],
                'reference_doc': ['doc-b'],
                'reference_chunk_ids': ['node-2'],
                'reference_docids': ['doc-b'],
                'answer_correctness': 0.9,
                'faithfulness': 0.8,
                'context_recall': 0.7,
                'doc_recall': 0.9,
                'is_valid': True,
                'key_points': ['key fact'],
                'hit_key': ['key fact'],
                'judge_reason': 'covered key fact',
            },
        ],
    }
    trace_payload = {
        'trace_1': {
            'query': 'Why did retrieval miss the key fact?',
            'modules': {
                'rewrite': {'input': 'q1', 'output': 'q1 rewritten'},
                'retrieve': {'input': 'q1 rewritten', 'output': ['node-1'], 'scores': [0.1]},
                'generate': {'input': ['node-1'], 'output': 'Generated answer 1'},
            },
        },
        'trace_2': {
            'query': 'Why was this answer correct?',
            'modules': {
                'rewrite': {'input': 'q2', 'output': 'q2 rewritten'},
                'retrieve': {'input': 'q2 rewritten', 'output': ['node-2'], 'scores': [0.9]},
                'generate': {'input': ['node-2'], 'output': 'Generated answer 2'},
            },
        },
    }
    (data_dir / 'eval_mock.json').write_text(json.dumps(eval_payload), encoding='utf-8')
    (data_dir / 'trace_mock.json').write_text(json.dumps(trace_payload), encoding='utf-8')

    monkeypatch.setenv('EVO_DATA_DIR', str(data_dir))
    monkeypatch.setenv('EVO_BASE_DIR', str(base_dir))
    monkeypatch.setenv('LAZYMIND_EVO_DATA_DIR', str(data_dir))
    monkeypatch.setenv('LAZYMIND_EVO_BASE_DIR', str(base_dir))
    config.refresh(['evo_data_dir', 'evo_base_dir'])
    yield
