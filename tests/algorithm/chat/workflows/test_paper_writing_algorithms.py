"""Contract/routing simulations with explicit algorithm substitutes; no model calls."""
import base64
import copy
import json
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[4]
PACKAGE = ROOT / 'workflows/paper-writing'
EXAMPLES = ROOT / 'tests/fixtures/paper-writing'
from lazymind.document_tools.paper import api
from lazymind.document_tools.paper import reading_reflection
from lazymind.document_tools.paper import research_topic
from lazymind.document_tools.paper import workflow as tools

TOPIC_MARKDOWN = '# 研究选题\n\n## 研究问题\n现有方法能否改善效果？\n\n## 证据状态\n尚待核验。'
REFINED_TOPIC_MARKDOWN = TOPIC_MARKDOWN + '\n\n## 文献反馈与调整依据\n根据综述调整验证路线。'
REFLECTION_MARKDOWN = ('# 阅读心得\n\n## 阅读范围\n本次阅读 P1 与 P2。\n\n## 核心理解与比较\n'
                       '两篇论文分别关注证据组织与引用核验。[P1] [P2]\n\n## 启发与局限\n'
                       '当前结论仅基于所提供材料。')
SINGLE_REFLECTION_MARKDOWN = ('# 阅读心得\n\n## 阅读范围\n本次阅读 P1。\n\n## 核心理解\n'
                              '论文讨论证据组织。[P1]\n\n## 启发与局限\n'
                              '当前结论仅基于所提供材料。')

pytestmark = pytest.mark.usefixtures('paper_simulation')



@pytest.fixture
def mock_topic_model(monkeypatch):
    monkeypatch.setattr(research_topic, 'AutoModel', lambda **_kwargs: lambda prompt: (
        (REFINED_TOPIC_MARKDOWN if '"mode": "refine"' in prompt else TOPIC_MARKDOWN)
        if 'Input materials (JSON):\n' in prompt else '论文讨论研究方法与结果。'
    ))


@pytest.fixture
def mock_reflection_model(monkeypatch):
    monkeypatch.setattr(reading_reflection, 'AutoModel',
                        lambda **_kwargs: lambda _prompt: REFLECTION_MARKDOWN)


@pytest.mark.parametrize('status', ['ready', 'failed', 'unavailable'])
def test_f7_prepare_papers_preserves_input_and_uses_available_content(status, monkeypatch):
    papers = [{'id': 'P1', 'title': '选中论文', 'abstract': '原始摘要', 'doi': '10.1/test'}]
    before = copy.deepcopy(papers)
    calls = []

    def read(paper_id):
        calls.append(paper_id)
        return {'status': status, 'text': '完整正文'}

    monkeypatch.setattr(reading_reflection.time, 'sleep', lambda _: pytest.fail('unexpected wait'))
    result = reading_reflection.prepare_papers(papers, read)
    assert result == ([{**papers[0], 'text': '完整正文'}] if status == 'ready' else papers)
    assert calls == ['P1'] and papers == before


def test_f7_prepare_papers_uses_one_wait_window_and_caches_ready_papers(monkeypatch):
    clock = [0]
    monkeypatch.setattr(reading_reflection.time, 'monotonic', lambda: clock[0])
    monkeypatch.setattr(reading_reflection.time, 'sleep', lambda seconds: clock.__setitem__(0, clock[0] + seconds))
    calls = []

    def read(paper_id):
        calls.append(paper_id)
        if paper_id == 'P1' or (paper_id == 'P2' and clock[0] >= 2):
            return {'status': 'ready', 'text': f'全文{paper_id}'}
        return {'status': 'processing'}

    papers = [{'id': f'P{i}', 'title': f'论文{i}', 'abstract': f'摘要{i}'} for i in range(1, 5)]
    result = reading_reflection.prepare_papers(papers, read)
    assert clock[0] == 60
    assert calls[:4] == ['P1', 'P2', 'P3', 'P4']
    assert calls.count('P1') == 1 and calls.count('P2') == 2
    assert calls.count('P3') == calls.count('P4') == 30
    assert [paper.get('text') for paper in result] == ['全文P1', '全文P2', None, None]
    assert result[2:] == papers[2:]


@pytest.mark.parametrize('failure', ['missing', 'empty_ready', 'network', 'duplicate'])
def test_f7_prepare_papers_does_not_hide_missing_or_invalid_content(failure, monkeypatch):
    papers = [{'id': 'P1', 'title': '必须阅读的论文', 'abstract': None}]
    calls = []

    def read(paper_id):
        calls.append(paper_id)
        if failure == 'network':
            raise OSError('read failed')
        return {'status': 'ready', 'text': ''} if failure == 'empty_ready' else {'status': 'unavailable'}

    if failure == 'duplicate':
        papers *= 2
    monkeypatch.setattr(reading_reflection.time, 'sleep', lambda _: pytest.fail('unexpected wait'))
    with pytest.raises((ValueError, OSError), match='必须阅读的论文|read failed|重复'):
        reading_reflection.prepare_papers(papers, read)
    assert calls == ([] if failure == 'duplicate' else ['P1'])


@pytest.mark.usefixtures('mock_topic_model', 'mock_reflection_model')
@pytest.mark.parametrize('step', list(tools.STEP_NODE))
def test_independent_algorithm_contract(step):
    example = json.loads((EXAMPLES / (step + '.json')).read_text())
    before = copy.deepcopy(example['input'])
    output = tools.run_algorithm(step, example['input'])
    contracts = tools.contracts
    module = contracts.ALIASES.get(step, step)
    contracts.validate_output(module, example['output'])
    assert example['input'] == before
    assert set(output) == {'is_demo', *contracts.OUTPUTS[module]}
    assert not {'pool', 'current', 'history', 'schema', 'remote_inputs'} & example['input'].keys()


@pytest.mark.usefixtures('mock_topic_model', 'mock_reflection_model')
@pytest.mark.parametrize('step,fields', [
    ('research_topic', {}), ('literature_review', {}),
    ('data_analysis', {'data': {'columns': ['score'], 'column_types': ['number'], 'units': ['point'], 'rows': [[1]]}}),
    ('write_document', {'outline': '# 大纲\n\n## 方法\n\n## 结论'}),
    ('peer_review', {'manuscript': '# 用户提供的稿件'}),
    ('reading_reflection', {'papers': [{'id': 'ref-001', 'title': '论文', 'text': '论文内容', 'url': None,
        'authors': [], 'year': None, 'doi': None, 'content_type': 'excerpt',
        'source': 'test', 'truncated': False}]}),
])
def test_middle_entry_needs_only_business_data(step, fields):
    pool = tools.pool_from_business({'topic': '用户主题', **fields})
    assert pool['history'] == []
    assert not any(item['mock'] for item in pool['materials'])
    assert tools.execute_step(step, pool)['status'] == 'completed'


@pytest.mark.parametrize('step,inputs', [
    ('peer_review', {'topic': 'T'}), ('revise_document', {'topic': 'T', 'manuscript': '# 稿件\n\n## 方法\n正文。'}),
    ('refine_topic', {'topic': 'T'}), ('reading_reflection', {'topic': 'T', 'papers': []}),
    ('data_analysis', {'topic': 'T', 'experiment': {
        'executed': False, 'plan': '方案', 'results': None, 'report': '仅方案', 'code': None, 'logs': None}}),
])
def test_missing_business_materials_stop_before_algorithm(step, inputs, monkeypatch):
    module = tools.contracts.ALIASES.get(step, step)
    def must_not_run(_):
        pytest.fail('missing materials reached algorithm')
    boundary = 'generated_analysis' if module == 'data_analysis' else 'run'
    monkeypatch.setattr(api.ALGORITHMS[module], boundary, must_not_run)
    message = '请明确' if step == 'revise_document' else '缺少'
    with pytest.raises(tools.MissingMaterials, match=message):
        tools.run_algorithm(step, inputs)


def test_f1_without_papers_uses_unverified_evidence_prompt(monkeypatch):
    prompts = []
    monkeypatch.setattr(research_topic, 'AutoModel',
                        lambda **_kwargs: lambda prompt: prompts.append(prompt) or TOPIC_MARKDOWN)
    result = tools.run_algorithm('research_topic', {'topic': '研究主题'})
    assert result == {'question': TOPIC_MARKDOWN, 'is_demo': False}
    assert 'No parsed papers are currently available' in prompts[0]
    assert 'Do not claim to have read papers' in prompts[0]
    assert 'Return only one Markdown document written in Chinese' in prompts[0]


def test_f1_topic_inference_reads_abstract_not_filename_or_full_text(monkeypatch):
    prompts = []
    monkeypatch.setattr(research_topic, 'AutoModel',
                        lambda **_kwargs: lambda prompt: prompts.append(prompt) or '{"topic": "证据可追溯性"}')
    papers = [{'id': 'P1', 'title': '无关文件名', 'abstract': '讨论证据可追溯性。', 'text': '不应发送的全文'}]
    assert research_topic.infer_topic('根据论文确定选题', papers) == '证据可追溯性'
    assert 'Use Chinese for a non-null topic value' in prompts[0]
    assert '讨论证据可追溯性' in prompts[0]
    assert '无关文件名' not in prompts[0]
    assert '不应发送的全文' not in prompts[0]


def test_f1_long_paper_condenses_without_mutating_source_pool(monkeypatch):
    paper = {'id': 'P1', 'title': '长论文', 'text': ''.join(
        f'<!-- page:{page} -->\n第{page}页证据。' + '研究方法与结果。' * 190 + '\n'
        for page in range(1, 5)
    )}
    pool = tools.pool_from_business({'topic': '研究主题', 'papers': [paper]})
    before = copy.deepcopy(pool)
    prompts = []

    def model(prompt):
        prompts.append(prompt)
        return TOPIC_MARKDOWN if 'Input materials (JSON):\n' in prompt else '[P1] 方法与结果仍需核验。'

    monkeypatch.setattr(research_topic, 'AutoModel', lambda **_kwargs: model)
    result = tools.execute_step('research_topic', pool)
    final_input = json.loads(prompts[-1].split('Input materials (JSON):\n', 1)[1])
    assert final_input['papers'] == [{'id': 'M1', 'title': '长论文', 'abstract': '[M1] 方法与结果仍需核验。'}]
    assert pool == before
    prepared = tools.material(result['pool'], 'papers')
    assert prepared['content'] == [{**paper, 'abstract': '[P1] 方法与结果仍需核验。'}]
    assert prepared['derived_from'] == [pool['current']['papers']]
    assert result['pool']['current']['papers'] in result['pool']['history'][-1]['inputs']
    assert all(research_topic.estimate_tokens(prompt) <= research_topic._prompt_token_limit() for prompt in prompts)
    prompts.clear()
    repeated = tools.execute_step('research_topic', result['pool'])
    assert repeated['pool']['current']['papers'] == result['pool']['current']['papers']
    assert len(prompts) == 1


@pytest.mark.parametrize('fields', [
    {'text': '全文'}, {'abstract': '摘要'}, {'abstract': '摘要', 'text': '全文'},
    {'abstract': None, 'text': '全文'}, {'abstract': '摘要', 'text': None},
])
def test_paper_contract_accepts_abstract_or_full_text(fields):
    papers = [{'id': 'P1', 'title': '论文', **fields}]
    tools.validate_pool(tools.pool_from_business({'topic': 'T', 'papers': papers}))


@pytest.mark.parametrize('fields', [{}, {'text': '', 'abstract': None}, {'text': ' ', 'abstract': ''}])
def test_paper_contract_rejects_metadata_without_content(fields):
    with pytest.raises(tools.MissingMaterials, match='摘要或全文'):
        tools.pool_from_business({'topic': 'T', 'papers': [{'id': 'P1', 'title': '论文', **fields}]})


@pytest.mark.parametrize('source,expected', [
    ('## Abstract\nA study.\n## Introduction\nBody.', 'A study.'),
    ('Abstract: A study.\nKeywords: retrieval', 'A study.'),
    ('Abstract\nA study.\n\nAuthor information', 'A study.'),
    ('摘要：研究证据。\n关键词：检索', '研究证据。'),
    ('# 摘 要\n研究方法。\n<!-- page:2 -->\n研究结果。\n1 引言\n正文', '研究方法。\n\n研究结果。'),
    ('Abstract\nA study.\n1. Introduction\nBody.', 'A study.'),
    ('只有正文，没有摘要边界。', None), ('Abstract\n没有结束边界', None),
    ('## Abstract\n## Methods\nBody.', None),
])
def test_f1_extracts_delimited_abstract(source, expected):
    assert research_topic._extract_abstract(source) == expected


def test_f1_prepares_only_missing_abstracts_and_projects_no_full_text(monkeypatch):
    papers = [
        {'id': 'P1', 'title': '一', 'abstract': '已有摘要', 'text': '全文一不应发送'},
        {'id': 'P2', 'title': '二', 'text': '## 摘要\n提取摘要\n## 引言\n全文二不应发送'},
        {'id': 'P3', 'title': '三', 'text': '需要总结的正文'},
    ]
    before = copy.deepcopy(papers)
    prompts = []
    def model(prompt):
        prompts.append(prompt)
        return TOPIC_MARKDOWN if 'Input materials (JSON):\n' in prompt else '生成摘要'
    monkeypatch.setattr(research_topic, 'AutoModel', lambda **_: model)
    tools.run_algorithm('research_topic', {'topic': 'T', 'papers': papers})
    assert len(prompts) == 2
    assert '需要总结的正文' in prompts[0]
    projected = json.loads(prompts[-1].split('Input materials (JSON):\n')[1])['papers']
    assert [paper['abstract'] for paper in projected] == ['已有摘要', '提取摘要', '生成摘要']
    assert all(set(paper) == {'id', 'title', 'abstract'} for paper in projected)
    assert papers == before


def test_f1_summary_over_400_tokens_is_accepted(monkeypatch):
    summary = '研究结果与方法。' * 60
    assert research_topic.estimate_tokens(summary) > 400
    monkeypatch.setattr(research_topic, 'AutoModel', lambda **_: lambda _: '<think>分析</think>\n' + summary)
    paper = {'id': 'P1', 'title': '论文', 'text': '原文'}
    assert research_topic.prepare_papers([paper]) == [{**paper, 'abstract': summary}]


def test_f1_groups_summaries_by_complete_prompt_budget(monkeypatch):
    paper = {'id': 'P1', 'title': '论文'}
    prompt_limit = len(research_topic._note_prompt('', paper)) + 3500
    monkeypatch.setattr(research_topic, '_prompt_token_limit', lambda: prompt_limit)
    monkeypatch.setattr(research_topic, 'estimate_tokens', len)
    monkeypatch.setattr(research_topic, '_paper_chunks', lambda *_: [f'chunk-{i}' for i in range(5)])
    summary = '中' * 1600
    merged_sizes = []

    def model(prompt):
        assert research_topic.estimate_tokens(prompt) <= research_topic._prompt_token_limit()
        source = json.loads(prompt.split('\n', 1)[1])['source']
        if source.startswith('chunk-'):
            return summary
        merged_sizes.append(source.count(summary))
        return '合并后的摘要'
    monkeypatch.setattr(research_topic, 'AutoModel', lambda **_: model)
    result = research_topic.prepare_papers([{'id': 'P1', 'title': '论文', 'text': '原文'}])
    assert result[0]['abstract'] == '合并后的摘要'
    assert max(merged_sizes) == 2  # Four notes would exceed the actual request budget.


def test_f1_paper_chunks_use_full_prompt_budget_without_losing_text(monkeypatch):
    paper = {'id': 'P1', 'title': '论文'}
    source = ''.join(f'第{line}行材料。\n' for line in range(30))
    monkeypatch.setattr(research_topic, 'estimate_tokens', len)
    full_prompt_limit = len(research_topic._note_prompt(source, paper))
    assert research_topic._paper_chunks(source, paper, full_prompt_limit) == [source]

    chunk_limit = len(research_topic._note_prompt('', paper)) + 80
    chunks = research_topic._paper_chunks(source, paper, chunk_limit)
    assert len(chunks) > 1
    assert ''.join(chunks) == source
    assert all(len(research_topic._note_prompt(chunk, paper)) <= chunk_limit for chunk in chunks)


@pytest.mark.parametrize('texts,limit,expected', [
    ([], 4, []), (['a'], 3, [['a']]),
    (['a', 'b', 'c'], 6, [['a', 'b'], ['c']]),
    (['a', 'b', 'c'], 5, [['a'], ['b'], ['c']]),
])
def test_paper_budget_groups_preserve_order_and_count_prompt_overhead(texts, limit, expected, monkeypatch):
    monkeypatch.setattr(research_topic, 'estimate_tokens', len)
    before = list(texts)
    groups = research_topic.group_texts_by_prompt_budget(
        texts, lambda source: f'[{source}]', limit, overflow_message='输入超预算',
    )
    assert groups == expected
    assert [text for group in groups for text in group] == texts == before


def test_paper_budget_helpers_preserve_caller_errors_and_empty_input(monkeypatch):
    monkeypatch.setattr(research_topic, 'estimate_tokens', len)
    for function, source in [(research_topic.split_text_by_prompt_budget, 'a'),
                             (research_topic.group_texts_by_prompt_budget, ['a'])]:
        with pytest.raises(ValueError, match='指定的预算错误'):
            function(source, lambda text: f'[{text}]', 2, overflow_message='指定的预算错误')
    assert research_topic.split_text_by_prompt_budget(
        '', lambda text: f'[{text}]', 2, overflow_message='指定的预算错误',
    ) == []


def test_f1_prompt_limit_uses_active_model_budget_and_128k_fallback(monkeypatch):
    import lazyllm
    from lazymind.chat.engine.agent_runtime.budget import build_context_budget
    from lazymind.config import config
    from lazymind import model_config

    with config.temp('context_compression_default_max_input_tokens', 131072), \
            config.temp('context_compression_reserved_output_tokens', 8192):
        monkeypatch.setattr(model_config, 'load_model_config', lambda: {'llm': {'source': 'dynamic'}})
        monkeypatch.setitem(lazyllm.globals['config'], 'dynamic_model_configs', {
            'llm': {'chat': {'max_input_tokens': 64000}},
        })
        expected = int(build_context_budget(llm_config={'llm': {'max_input_tokens': 64000}})
                       .effective_input_budget * 0.9)
        assert research_topic._prompt_token_limit() == expected

        monkeypatch.setattr(model_config, 'load_model_config', lambda: {'llm': {'source': 'openai'}})
        expected = int(build_context_budget().effective_input_budget * 0.9)
        assert research_topic._prompt_token_limit() == expected
        assert expected > 6000


@pytest.mark.parametrize('summary,error', [
    ('', '分段提炼为空'), ('<think>未结束', '思考内容未闭合'),
    ('中' * 5000, '超出合并输入预算'), ('中' * 2500, '无法在输入预算内继续合并'),
])
def test_f1_summary_failure_is_bounded_and_preserves_pool(summary, error, monkeypatch):
    paper = {'id': 'P1', 'title': '论文'}
    prompt_limit = len(research_topic._note_prompt('', paper)) + (
        3000 if len(summary) == 5000 else len(summary) + 20
    )
    monkeypatch.setattr(research_topic, '_prompt_token_limit', lambda: prompt_limit)
    monkeypatch.setattr(research_topic, 'estimate_tokens', len)
    monkeypatch.setattr(research_topic, '_paper_chunks', lambda *_: ['part one', 'part two'])
    calls = []
    monkeypatch.setattr(research_topic, 'AutoModel', lambda **_: lambda prompt: calls.append(prompt) or summary)
    pool = tools.pool_from_business({'topic': 'T', 'papers': [{'id': 'P1', 'title': '论文', 'text': '原文'}]})
    before = copy.deepcopy(pool)
    with pytest.raises(ValueError, match=error):
        tools.execute_step('research_topic', pool)
    assert pool == before
    assert len(calls) <= 2


@pytest.mark.parametrize('entry', ['infer_topic', 'run'])
def test_f1_checks_complete_final_prompt_before_calling_model(entry, monkeypatch):
    monkeypatch.setattr(research_topic, '_prompt_token_limit', lambda: 1000)
    monkeypatch.setattr(research_topic, 'estimate_tokens', len)
    monkeypatch.setattr(research_topic, 'AutoModel', lambda **_: pytest.fail('oversized prompt sent'))
    papers = [{'id': 'P1', 'title': '论文', 'abstract': '中' * 5000}]
    with pytest.raises(ValueError, match='输入过长|超出模型预算'):
        if entry == 'infer_topic':
            research_topic.infer_topic('主题', papers)
        else:
            tools.run_algorithm('research_topic', {'topic': 'T', 'papers': papers})


@pytest.mark.parametrize('invalid', ['', '## 其他章节\n内容',
                                     TOPIC_MARKDOWN + '\n\n## 研究问题\n另一个问题'])
def test_f1_rejects_invalid_model_output_without_publishing(invalid, monkeypatch):
    monkeypatch.setattr(research_topic, 'AutoModel', lambda **_kwargs: lambda _prompt: invalid)
    pool = tools.pool_from_business({'topic': 'T'})
    before = copy.deepcopy(pool)
    with pytest.raises(ValueError, match='研究选题输出'):
        tools.execute_step('research_topic', pool)
    assert pool == before


@pytest.mark.parametrize('fields,expected', [
    ({'text': '全文'}, {'text': '全文', 'source_kind': 'full_text'}),
    ({'abstract': '摘要'}, {'abstract': '摘要', 'source_kind': 'abstract'}),
    ({'text': '全文', 'abstract': '摘要'}, {'text': '全文', 'source_kind': 'full_text'}),
    ({'text': ' ', 'abstract': '摘要'}, {'abstract': '摘要', 'source_kind': 'abstract'}),
    ({'text': '片段', 'reading': {'truncated': True}}, {'text': '片段', 'source_kind': 'partial_text'}),
    ({'text': '片段', 'truncated': True}, {'text': '片段', 'source_kind': 'partial_text'}),
    ({'text': '片段', 'content_type': 'excerpt'}, {'text': '片段', 'source_kind': 'partial_text'}),
    ({'text': '全文', 'reading': {'truncated': False}}, {'text': '全文', 'source_kind': 'full_text'}),
    ({'text': ' ', 'abstract': '摘要', 'truncated': True}, {'abstract': '摘要', 'source_kind': 'abstract'}),
])
@pytest.mark.parametrize('user_request', ['总结阅读启发', '结合我的研究方向：证据可追溯性'])
def test_f7_single_paper_uses_chinese_markdown_prompt_without_evidence(fields, expected, user_request, monkeypatch):
    prompts = []
    monkeypatch.setattr(reading_reflection, 'AutoModel', lambda **_kwargs: lambda prompt: (
        prompts.append(prompt) or SINGLE_REFLECTION_MARKDOWN
    ))
    notes = [{'id': 'n-1', 'paper_id': 'P1', 'title': '已有笔记', 'content': '这个结论是否适用于其他数据集？'}]
    inputs = {'topic': '证据组织', 'request': user_request,
              'papers': [{'id': 'P1', 'title': '论文一', 'notes': notes, **fields}]}
    before = copy.deepcopy(inputs)
    result = tools.run_algorithm('reading_reflection', inputs)
    model_input = json.loads(prompts[0].split('Input materials (JSON):\n', 1)[1])
    assert result == {'reflection': SINGLE_REFLECTION_MARKDOWN, 'is_demo': False}
    assert inputs == before
    assert len(prompts) == 1
    assert model_input['papers'] == [{'id': 'P1', 'title': '论文一', 'notes': notes, **expected}]
    assert model_input['request'] == user_request
    assert model_input['evidence'] is None
    assert 'Exactly one paper is provided' in prompts[0]
    assert 'Return only one Markdown document written in Chinese' in prompts[0]
    assert 'do not claim access to any content outside the input' in prompts[0]
    assert 'Missing details in partial text or an abstract do not prove' in prompts[0]
    assert 'A generic request to generate a reflection is not a research direction' in prompts[0]
    assert 'Prioritize explicitly stated research interests' in prompts[0]


def test_f7_multiple_papers_uses_f2_evidence_as_supporting_context(monkeypatch):
    prompts = []
    monkeypatch.setattr(reading_reflection, 'AutoModel', lambda **_kwargs: lambda prompt: (
        prompts.append(prompt) or REFLECTION_MARKDOWN
    ))
    evidence = '# 文献综述\nP1 讨论组织，P2 讨论核验。'
    result = tools.run_algorithm('reading_reflection', {
        'topic': '证据可追溯性', 'request': '比较两种方法',
        'papers': [
            {'id': 'P1', 'title': '论文一', 'text': '研究证据组织。'},
            {'id': 'P2', 'title': '论文二', 'abstract': '研究引用核验。'},
            {'id': 'P3', 'title': '论文三', 'text': '已读取的部分结果。', 'reading': {'truncated': True}},
        ],
        'evidence': evidence,
    })
    model_input = json.loads(prompts[0].split('Input materials (JSON):\n', 1)[1])
    assert result == {'reflection': REFLECTION_MARKDOWN, 'is_demo': False}
    assert model_input['evidence'] == evidence
    assert 'text' in model_input['papers'][0] and 'abstract' in model_input['papers'][1]
    assert [paper['source_kind'] for paper in model_input['papers']] == ['full_text', 'abstract', 'partial_text']
    assert 'Multiple papers are provided' in prompts[0]
    assert 'optional evidence field is a literature review' in prompts[0]
    assert 'Do not concatenate paper-by-paper summaries' in prompts[0]
    assert 'experimental conditions are comparable' in prompts[0]
    assert 'state the evidence scope for each paper, including mixed inputs' in prompts[0]


@pytest.mark.parametrize('source_field,metadata,source_kind', [
    ('text', {}, 'full_text'),
    ('abstract', {}, 'abstract'),
    ('text', {'reading': {'truncated': True}}, 'partial_text'),
])
def test_f7_long_paper_condenses_without_mutating_source_pool(source_field, metadata, source_kind, monkeypatch):
    notes = [{'id': 'n-1', 'paper_id': 'P1', 'title': '已有笔记', 'content': '关注复现条件。'}]
    paper = {'id': 'P1', 'title': '长论文', source_field: ''.join(
        f'<!-- page:{page} -->\n第{page}页材料。' + '核心论点、方法、结果与局限。' * 190 + '\n'
        for page in range(1, 5)
    ), 'notes': notes, **metadata}
    papers = [paper, {'id': 'P2', 'title': '另一篇论文', 'abstract': '另一篇摘要'}]
    pool = tools.pool_from_business({'topic': '研究主题', 'papers': papers})
    before = copy.deepcopy(pool)
    prompts = []
    monkeypatch.setattr(reading_reflection, '_prompt_token_limit', lambda: 5000)
    monkeypatch.setattr(reading_reflection, 'estimate_tokens', len)
    monkeypatch.setattr(research_topic, 'estimate_tokens', len)

    def model(prompt):
        prompts.append(prompt)
        if 'Input materials (JSON):\n' in prompt:
            return SINGLE_REFLECTION_MARKDOWN
        note_input = json.loads(prompt.split('\n', 1)[1])
        return f'[{note_input["paper_id"]}] 核心论点与局限。'

    monkeypatch.setattr(reading_reflection, 'AutoModel', lambda **_kwargs: model)
    result = tools.execute_step('reading_reflection', pool)
    final_input = json.loads(prompts[-1].split('Input materials (JSON):\n', 1)[1])
    assert len(prompts) > 2
    assert all(len(prompt) <= 5000 for prompt in prompts)
    assert [item['id'] for item in final_input['papers']] == ['P1', 'P2']
    kind = {'full_text': 'full-text', 'partial_text': 'partial-text', 'abstract': 'abstract-only'}[source_kind]
    assert f'Reading notes derived from {kind}' in final_input['papers'][0][source_field]
    assert final_input['papers'][0]['source_kind'] == source_kind
    assert final_input['papers'][0]['notes'] == notes
    assert '[P1]' in final_input['papers'][0][source_field]
    assert '[P2]' in final_input['papers'][1]['abstract']
    note_inputs = [json.loads(prompt.split('\n', 1)[1]) for prompt in prompts[:-1]]
    assert all(item['source_kind'] == source_kind and 'notes' not in item
               for item in note_inputs if item['paper_id'] == 'P1')
    assert result['document'] == SINGLE_REFLECTION_MARKDOWN
    assert pool == before
    assert tools.material(result['pool'], 'papers')['content'] == papers


@pytest.mark.parametrize('inputs,error', [
    ({'papers': [{'id': 'P1', 'title': '论文', 'text': '全文'}]}, 'topic'),
    ({'topic': 'T', 'papers': []}, '缺少材料'),
    ({'topic': 'T', 'papers': [{'id': 'P1', 'title': '论文', 'abstract': '', 'text': None}]}, '摘要或全文'),
])
def test_f7_invalid_inputs_stop_before_model(inputs, error, monkeypatch):
    monkeypatch.setattr(reading_reflection, 'AutoModel', lambda **_: pytest.fail('invalid input reached model'))
    with pytest.raises(ValueError, match=error):
        tools.run_algorithm('reading_reflection', inputs)


def test_f7_chunks_preserve_source_and_use_complete_prompt_budget(monkeypatch):
    paper = {'id': 'P1', 'title': '论文', 'text': ''}
    source = '\n  <!-- page:1 -->\n' + '  原文带空白和换行。\r\n' * 30 + '\n<!-- page:2 -->\n尾部  '
    monkeypatch.setattr(research_topic, 'estimate_tokens', len)
    full_limit = len(reading_reflection._note_prompt(source, 'T', '阅读要求', paper))
    assert reading_reflection._paper_chunks(source, 'T', '阅读要求', paper, full_limit) == [source]
    overhead = len(reading_reflection._note_prompt('', 'T', '阅读要求', paper))
    chunks = reading_reflection._paper_chunks(source, 'T', '阅读要求', paper, overhead + 80)
    assert len(chunks) > 1
    assert ''.join(chunks) == source
    assert all(len(reading_reflection._note_prompt(chunk, 'T', '阅读要求', paper)) <= overhead + 80
               for chunk in chunks)
    with pytest.raises(ValueError, match='片段提示超出'):
        reading_reflection._paper_chunks(source, 'T', '阅读要求', paper, overhead)


def test_f7_notes_over_400_tokens_merge_by_complete_prompt_budget(monkeypatch):
    paper = {'id': 'P1', 'title': '论文', 'text': '原文'}
    summary = '中' * 1600
    assert reading_reflection.estimate_tokens(summary) > 400
    monkeypatch.setattr(reading_reflection, 'estimate_tokens', len)
    monkeypatch.setattr(research_topic, 'estimate_tokens', len)
    limit = len(reading_reflection._note_prompt('', 'T', '要求', paper)) + 3500
    monkeypatch.setattr(reading_reflection, '_paper_chunks', lambda *_: [f'chunk-{i}' for i in range(5)])
    merged_sizes = []

    def model(prompt):
        assert len(prompt) <= limit
        source = json.loads(prompt.split('\n', 1)[1])['source']
        if source.startswith('chunk-'):
            return '<think>分析</think>\n' + summary
        merged_sizes.append(source.count(summary))
        return '合并后的阅读笔记'

    monkeypatch.setattr(reading_reflection, 'AutoModel', lambda **_: model)
    result = reading_reflection._condensed_papers([paper], 'T', '要求', limit)
    assert result[0]['text'].endswith('合并后的阅读笔记')
    assert max(merged_sizes) == 2
    assert paper['text'] == '原文'


@pytest.mark.parametrize('note,error', [
    ('', '阅读笔记为空'), ('<think>未结束', '思考内容未闭合'),
    ('中' * 5000, '超出合并输入预算'), ('中' * 2500, '无法在输入预算内继续合并'),
])
def test_f7_note_failures_are_bounded(note, error, monkeypatch):
    paper = {'id': 'P1', 'title': '论文', 'text': '原文'}
    monkeypatch.setattr(reading_reflection, 'estimate_tokens', len)
    monkeypatch.setattr(research_topic, 'estimate_tokens', len)
    limit = len(reading_reflection._note_prompt('', 'T', '', paper)) + 3500
    monkeypatch.setattr(reading_reflection, '_paper_chunks', lambda *_: ['chunk-1', 'chunk-2'])
    calls = []
    monkeypatch.setattr(reading_reflection, 'AutoModel',
                        lambda **_: lambda prompt: calls.append(prompt) or note)
    with pytest.raises(ValueError, match=error):
        reading_reflection._condensed_papers([paper], 'T', '', limit)
    assert len(calls) <= 2
    assert paper['text'] == '原文'


def test_f7_oversized_note_prompt_is_not_sent(monkeypatch):
    monkeypatch.setattr(reading_reflection, 'AutoModel', lambda **_: pytest.fail('oversized prompt sent'))
    with pytest.raises(ValueError, match='超出模型输入预算'):
        reading_reflection._condense('正文', 'T', '', {'id': 'P1', 'title': '论文', 'text': '正文'}, 1)


def test_f7_final_budget_includes_evidence_after_condensing(monkeypatch):
    monkeypatch.setattr(reading_reflection, '_prompt_token_limit', lambda: 5000)
    monkeypatch.setattr(reading_reflection, 'estimate_tokens', len)
    monkeypatch.setattr(research_topic, 'estimate_tokens', len)
    calls = []

    def model(prompt):
        assert len(prompt) <= 5000
        assert 'Input materials (JSON):\n' not in prompt
        calls.append(prompt)
        return '[P1] 阅读笔记'

    monkeypatch.setattr(reading_reflection, 'AutoModel', lambda **_: model)
    inputs = {'topic': 'T', 'papers': [{'id': 'P1', 'title': '论文', 'abstract': '摘要'}],
              'evidence': '综述' * 3000}
    before = copy.deepcopy(inputs)
    with pytest.raises(ValueError, match='提炼后仍超出'):
        tools.run_algorithm('reading_reflection', inputs)
    assert len(calls) == 1
    assert inputs == before


@pytest.mark.parametrize('invalid', [
    '', '```\n# 阅读心得\n```', '## 阅读心得\n内容', '<think>未完成',
])
def test_f7_rejects_invalid_model_output_without_publishing(invalid, monkeypatch):
    monkeypatch.setattr(reading_reflection, 'AutoModel', lambda **_kwargs: lambda _prompt: invalid)
    pool = tools.pool_from_business({'topic': 'T', 'papers': [
        {'id': 'P1', 'title': '论文', 'text': '论文内容'},
    ]})
    before = copy.deepcopy(pool)
    with pytest.raises(ValueError, match='阅读心得输出|未闭合的思考内容'):
        tools.execute_step('reading_reflection', pool)
    assert pool == before


def test_f2_feedback_refines_f1_question_without_overwriting_evidence(monkeypatch):
    old_question = '# 旧选题\n\n## 研究问题\n原方法能否改善效果？'
    papers = [{'id': 'P1', 'title': '论文一', 'abstract': '原有摘要', 'citation': '论文一[Z].'}]
    added = {'id': 'P2', 'title': '论文二', 'text': '摘要：新证据\n关键词：检索', 'citation': '论文二[Z].'}
    pool = tools.pool_from_business({'topic': 'T', 'question': old_question, 'papers': papers})
    monkeypatch.setattr(api.ALGORITHMS['literature_review'], 'run', lambda inputs: {
        'evidence': '# 初始综述\n尚待人工核对。', 'papers': [*inputs['papers'], added], 'is_demo': False,
    })
    reviewed = tools.execute_step('literature_review', pool)
    edited = '# 人工确认的综述\n需要缩小研究范围。'
    selected = tools.pool_from_inputs({
        'literature_review_pool': reviewed['pool'], 'literature_review_document': edited,
    })
    prompts = []
    monkeypatch.setattr(research_topic, 'AutoModel', lambda **_kwargs: lambda prompt: (
        prompts.append(prompt) or REFINED_TOPIC_MARKDOWN
    ))
    result = tools.execute_step('refine_topic', selected, '缩小研究范围')
    model_input = json.loads(prompts[0].split('Input materials (JSON):\n', 1)[1])
    assert model_input['question'] == old_question
    assert model_input['evidence'] == edited
    assert [paper['abstract'] for paper in model_input['papers']] == ['原有摘要', '新证据']
    assert tools.material(result['pool'], 'papers')['content'] == [*papers, {**added, 'abstract': '新证据'}]
    assert result['document'] == REFINED_TOPIC_MARKDOWN


def test_one_real_algorithm_replacement_preserves_storage_and_next_input(monkeypatch):
    pool = tools.pool_from_business({'topic': '实际主题', 'papers': [
        {'id': 'ref-001', 'title': '原文', 'text': '真实输入正文', 'abstract': '已有摘要', 'url': None,
         'authors': [], 'year': None, 'doi': None, 'content_type': 'excerpt',
         'source': 'test', 'truncated': False, 'citation': '原文[Z].'}]})
    before = copy.deepcopy(pool)
    seen = []
    def research(inputs):
        seen.append(copy.deepcopy(inputs))
        inputs['papers'].clear()  # A module cannot mutate caller-owned state.
        return {'question': '# 新选题\n真实算法返回的候选问题', 'is_demo': False}
    monkeypatch.setattr(api.ALGORITHMS['research_topic'], 'run', research)
    selected = tools.execute_step('research_topic', pool)
    assert pool == before
    assert selected['mock'] is False
    assert '演示数据' not in selected['document']
    assert seen[0]['papers'][0]['id'] == 'ref-001'
    edited = '# 用户确认后的选题\nEDITED-UPSTREAM'
    updated = tools.pool_from_inputs({'research_topic_pool': selected['pool'], 'research_topic_document': edited})
    def literature(inputs):
        assert inputs['question'] == edited
        assert inputs['papers'][0]['text'] == '真实输入正文'
        return {'evidence': '# 新综述\n根据新选题完成', 'papers': inputs['papers'], 'is_demo': False}
    monkeypatch.setattr(api.ALGORITHMS['literature_review'], 'run', literature)
    result = tools.execute_step('literature_review', updated)
    assert result['mock'] is False
    assert tools.material(result['pool'], 'question')['content'] == edited
    assert tools.material(result['pool'], 'evidence')['content'] == '# 新综述\n根据新选题完成'


@pytest.mark.usefixtures('mock_topic_model', 'mock_reflection_model')
@pytest.mark.parametrize('path', [
    ['research_topic', 'literature_review', 'refine_topic', 'literature_review'],
    ['literature_review', 'write_document', 'peer_review', 'revise_document', 'peer_review'],
    ['experiment', 'data_analysis', 'write_document'], ['literature_review', 'reading_reflection'],
])
def test_every_path_supplies_exact_current_business_values(path, monkeypatch):
    pool = tools.seed_pool()
    contracts = tools.contracts
    for step in path:
        module = api.ALGORITHMS[contracts.ALIASES.get(step, step)]
        if step == 'write_document':
            pool['current'].pop('manuscript', None)
            # Drafting now consumes an already confirmed outline.
            tools.add_material(pool, 'outline', '# 测试论文\n\n## 方法', [], [])
        if step == 'revise_document':
            pool['selected_issue_ids'] = ['R1']
        if step == 'data_analysis':
            pool['current'].pop('experiment', None)  # Explicitly choose the supplied raw table.
        expected = contracts.validate_input(step, tools.business_input(step, pool, '本轮要求'))
        if contracts.ALIASES.get(step, step) == 'research_topic':
            expected['papers'] = research_topic.prepare_papers(expected['papers'])
        original = module.run
        received = []

        def capture(value, *, _received=received, _original=original, **kwargs):
            _received.append(copy.deepcopy(value))
            return _original(value, **kwargs)
        with monkeypatch.context() as patch:
            patch.setattr(module, 'run', capture)
            pool = tools.execute_step(step, pool, '本轮要求')['pool']
        assert received == [expected]


@pytest.mark.parametrize('value', [
    {'topic': 'T', 'data': {'columns': ['a'], 'rows': [[1, 2]]}},
    {'topic': 'T', 'data': {'columns': ['a'], 'rows': [[float('nan')]]}},
    {'topic': 'T', 'papers': [{'id': 'P1', 'title': 'T'}]},
    {'topic': 'T', 'demo': True, 'manuscript': '真实稿件'},
])
def test_invalid_business_entry_rejected(value):
    with pytest.raises(ValueError):
        tools.pool_from_business(value)




def test_business_input_data_is_not_mistaken_for_runtime_envelope(tmp_path):
    value = {'topic': 'T', 'data': {'columns': ['a'], 'rows': [[1]]}}
    path = tmp_path / 'input.json'
    path.write_text(json.dumps(value))
    assert tools._read_bound(str(path)) == value


@pytest.mark.usefixtures('mock_topic_model')
def test_demo_provenance_cannot_be_erased_by_real_algorithm(monkeypatch):
    monkeypatch.setattr(api.ALGORITHMS['research_topic'], 'run',
                        lambda inputs: {'question': '# 结果', 'is_demo': False})
    result = tools.execute_step('research_topic', tools.seed_pool())
    assert result['mock'] is True
    assert '演示数据' in result['document']


@pytest.mark.usefixtures('mock_topic_model')
def test_plugin_contains_only_thin_entry_and_calls_shared_runtime(tmp_path, monkeypatch):
    from lazymind.workflow_toolkit import load_workflow_package_tools
    files = {}
    for source in PACKAGE.rglob('*'):
        if not source.is_file() or '__pycache__' in source.parts:
            continue
        relative = source.relative_to(PACKAGE)
        target = tmp_path / relative
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_bytes(source.read_bytes())
        files[str(relative)] = base64.b64encode(source.read_bytes()).decode()
    assert sorted(p.relative_to(PACKAGE).as_posix() for p in (PACKAGE / 'scripts').rglob('*.py')) == ['scripts/tools.py']
    assert len((PACKAGE / 'scripts/tools.py').read_text().splitlines()) < 15
    loaded = load_workflow_package_tools({'files': files}, ['run_paper_step'],
                                        'paper-writing', 'isolated-revision', package_root=tmp_path)
    monkeypatch.setattr(tools, 'run_paper_step', lambda: {'status': 'shared-runtime'})
    assert loaded['run_paper_step']() == {'status': 'shared-runtime'}
    assert api.run_algorithm('research_topic', {'topic': 'T'})['question']
