"""Shared offline substitutes for storage/routing simulations (never autouse).

Used by test_paper_writing, test_paper_writing_algorithms and selected runtime
cases. Keep algorithm integration tests independent: opt in explicitly with
paper_simulation or usefixtures, so real Writer paths are not silently mocked.
"""
import copy
import json
from pathlib import Path
from unittest.mock import Mock

import pytest


@pytest.fixture
def paper_simulation(monkeypatch, request):
    from lazymind.document_tools.paper import api, workflow
    from lazymind.document_tools.paper.review import materials as review_materials
    from lazymind.document_tools.paper import source_materials
    from lazymind.document_tools.paper.experiment import preparation

    def select_source(prompt):
        payload = json.loads(prompt[prompt.index('\n{') + 1:])
        if 'blocks' in payload:
            return json.dumps({'blocks': [block['id'] for block in payload['blocks']]})
        return json.dumps({'action': 'use_existing' if payload['tables'] else 'missing',
                           'table_ids': list(payload['tables']), 'reason': '沿用提供的测试数据'})

    selector = Mock(side_effect=select_source)
    selector.share.return_value = selector
    selector.prompt.return_value = selector
    monkeypatch.setattr(source_materials, 'AutoModel', lambda **_: selector)
    monkeypatch.setattr(preparation, 'AutoModel', lambda **_: selector)
    # This fixture simulates routing/publication, including F2 source selection.
    monkeypatch.setattr(review_materials, 'current_handoff', lambda: None)
    monkeypatch.setattr(review_materials, 'prepare', lambda inputs, *a, **kw: copy.deepcopy(inputs['papers']))
    source = json.loads((Path(__file__).resolve().parents[4] /
        'tests/fixtures/paper-writing/write_document.json').read_text())['input']
    source['analysis'] = json.loads((Path(__file__).resolve().parents[4] /
        'tests/fixtures/paper-writing/data_analysis.json').read_text())['output']['analysis']
    original_seed = workflow.seed_pool

    def seed():
        pool = original_seed()
        workflow.add_material(pool, 'outline', '# 测试论文\n\n## 方法\n已确认测试大纲。', [], [], is_demo=True)
        for paper in workflow.material(pool, 'papers')['content']:
            paper.update(url=None, authors=[], year=None, doi=None, content_type='excerpt', source='test', truncated=False)
        pool['constraints'] = copy.deepcopy(source.get('constraints', {}))
        workflow.material(pool, 'data')['content'].update(column_types=['string', 'number'], units=[None, 'point'])
        return pool

    def question(inputs):
        value = copy.deepcopy(source['question'])
        return {'question': value, 'is_demo': True}

    def literature(inputs):
        papers = copy.deepcopy(inputs['papers'])
        if not papers:
            papers = copy.deepcopy(source['papers'])
        for paper in papers:
            paper.setdefault('citation', paper['title'] + '[Z].')
        value = '# 模拟综述\n\n测试材料，不作为真实证据。[' + papers[0]['id'] + ']'
        return {'papers': papers, 'evidence': value, 'is_demo': True}

    def analysis(inputs):
        from lazymind.document_tools.paper import contracts
        from lazymind.document_tools.paper.analysis.schema import binding
        inputs = contracts.validate_input('data_analysis', inputs)
        value = copy.deepcopy(source['analysis'])
        value['summary'] = '# 模拟分析\n\n演示数据。\n\n' + value['summary']
        value.update(binding(inputs))
        table = value['data_sources'][0]
        refs = [dict(table_id=table['id'], column=column) for column in table['columns']]
        value['targets'][0]['columns'] = refs
        value['findings'] = [{**value['findings'][0], 'kind': 'count', 'value': float(table['row_count']),
            'statement': '模拟来源记录数。', 'calculation': 'len(rows)', 'unit': '条',
            'columns': refs, 'sample_size': dict(source_rows=[dict(table_id=table['id'], count=table['row_count'])],
                                               n=None, unit='记录', excluded_n=None)}]
        charts = [dict(id='means', title='模拟图', svg='<svg/>', caption='模拟数据，仅用于流程测试。',
                       columns=refs, finding_ids=[value['findings'][0]['id']])]
        return {'analysis': value, 'charts': charts, 'is_demo': True}

    def writer(inputs):
        body = inputs['manuscript'] or ((inputs['outline'] or '# 测试论文\n\n## 方法') + '\n测试正文。')
        result = {'manuscript': body + '\n测试修订。', 'is_demo': True}
        return result

    def reviewer(inputs):
        from lazymind.document_tools.paper.peer_review import ReviewResult, build_output
        result = build_output(ReviewResult.model_validate({
            'summary': '测试审稿', 'issues': [{'severity': 'minor',
            'category': 'language', 'location': '全文', 'action_type': 'text_revision', 'quote': None,
            'problem': '测试问题', 'suggestion': '补充测试说明'}], 'limitations': ['算法替身测试']}), inputs)
        result['is_demo'] = True
        return result

    def revision(inputs):
        output = writer(inputs)
        return {**output, **reviewer({**inputs, 'manuscript': output['manuscript']})}

    monkeypatch.setattr(workflow, 'seed_pool', seed)
    for name, fn in [('research_topic', question), ('literature_review', literature), ('data_analysis', analysis),
                     ('write_document', writer), ('revise_document', revision), ('peer_review', reviewer)]:
        if name == 'research_topic' and request.module.__name__.endswith(('test_paper_writing', 'test_paper_writing_algorithms')):
            continue
        monkeypatch.setattr(api.ALGORITHMS[name], 'run', fn)
