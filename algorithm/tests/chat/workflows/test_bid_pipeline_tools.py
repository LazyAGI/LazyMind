import importlib.util
import json
from pathlib import Path


def _load_pipeline_tools():
    root = Path(__file__).resolve().parents[4]
    path = root / 'workflows' / 'bid_tech_proposal_writer' / 'scripts' / 'pipeline_tools.py'
    spec = importlib.util.spec_from_file_location('bid_pipeline_tools_for_test', path)
    assert spec and spec.loader
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def test_small_target_allocates_positive_leaf_targets_with_exact_total():
    tools = _load_pipeline_tools()
    children = [
        {
            'title': f'功能{i}',
            'level': 2,
            'number': f'1.{i}',
            'target_words': 100,
            'bid_requirements_refs': [],
            'disqualification_refs': [],
            'children': [],
        }
        for i in range(1, 18)
    ]
    outline = {
        'project_name': '测试项目',
        'total_word_target': 1000,
        'chapters': [{
            'title': '功能设计',
            'level': 1,
            'number': '1',
            'target_words': 1700,
            'bid_requirements_refs': [],
            'disqualification_refs': [],
            'children': children,
        }],
    }

    result = tools.validate_and_allocate_outline(json.dumps(outline), '', '', '1000')

    assert result['valid'] is True
    allocated = result['normalized_outline']['chapters'][0]['children']
    targets = [item['target_words'] for item in allocated]
    assert min(targets) > 0
    assert sum(targets) == 1000
    assert result['normalized_outline']['chapters'][0]['target_words'] == 1000
