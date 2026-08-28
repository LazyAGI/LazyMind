import importlib.util
import json
import re
from pathlib import Path


def _load_pipeline_tools():
    root = Path(__file__).resolve().parents[4]
    path = root / 'workflows' / 'academic_research_pipeline' / 'scripts' / 'pipeline_tools.py'
    spec = importlib.util.spec_from_file_location('academic_pipeline_tools_for_test', path)
    assert spec and spec.loader
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def _load_document_builder():
    root = Path(__file__).resolve().parents[4]
    path = root / 'workflows' / 'academic_research_pipeline' / 'scripts' / 'document_builder.py'
    spec = importlib.util.spec_from_file_location('academic_document_builder_for_test', path)
    assert spec and spec.loader
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def test_workflow_feedback_path_bindings_use_path_transport():
    root = Path(__file__).resolve().parents[4]
    workflow_root = root / 'workflows' / 'academic_research_pipeline'
    state = (workflow_root / 'scenario' / 'state.yml').read_text(encoding='utf-8')
    workflow = (workflow_root / 'workflow.yaml').read_text(encoding='utf-8')
    step_blocks = dict(re.findall(
        r'^  ([a-z][a-z0-9_]*):\n(.*?)(?=^  [a-z][a-z0-9_]*:\n|\Z)',
        state, flags=re.MULTILINE | re.DOTALL,
    ))

    checked = set()
    for step_id, block in step_blocks.items():
        for material in re.findall(
            r'\bfeedback_path\s*=\s*([a-z][a-z0-9_]*)', block,
        ):
            assert re.search(
                rf'- \{{id: {re.escape(material)}, .* type: text,', workflow,
            )
            assert re.search(
                rf'- \{{material: {re.escape(material)}, [^}}]*transport: path[^}}]*\}}',
                block,
            ), (
                f'{step_id}.{material} is passed to a feedback_path argument and must be '
                'materialized as a Workflow path'
            )
            checked.add((step_id, material))

    assert checked == {
        ('revise_paper', 'revision_roadmap'),
        ('second_revision', 're_review_report'),
    }


def test_normalize_academic_parameters_preserves_explicit_preflight_values():
    tools = _load_pipeline_tools()

    result = tools.normalize_academic_parameters(
        '生成式 AI 对高等教育质量保障的影响', '5000', '文献综述',
        '中文', 'APA 7', 'Word',
    )

    assert result['research_topic'] == '生成式 AI 对高等教育质量保障的影响'
    assert result['word_target'] == 5000
    assert result['paper_type'] == 'literature_review'
    assert result['paper_language'] == 'zh-CN'
    assert result['citation_style'] == 'APA 7'
    assert result['output_format'] == 'docx'
    assert result['source_skill']['name'] == 'academic-pipeline'
    assert 'bilingual_abstract' not in result

    chinese_research = tools.normalize_academic_parameters(
        '个人知识积累如何与人工智能结合', '3000', 'research',
        'zh', 'gbt7714', 'docx',
    )
    assert chinese_research['paper_type'] == 'research'
    assert chinese_research['citation_style'] == 'GB/T 7714'

    theoretical = tools.normalize_academic_parameters(
        '个人知识积累如何与人工智能结合', '3000', '理论探讨/思辨论文',
        '中文', 'GB/T 7714', 'Word (.docx)',
    )
    assert theoretical['paper_type'] == 'theoretical'
    assert theoretical['output_format'] == 'docx'

    short_smoke = tools.normalize_academic_parameters(
        'RAG', '100', '学术论文', '中文', 'APA 7', 'md',
    )
    assert short_smoke['research_topic'] == 'RAG'
    assert short_smoke['word_target'] == 100
    assert short_smoke['paper_type'] == 'research'

    natural_short_paper = tools.normalize_academic_parameters(
        'RAG', '1000字左右', '学术论文（短篇）', '中文', 'GB/T 7714', 'Markdown',
    )
    assert natural_short_paper['paper_type'] == 'research'
    assert natural_short_paper['word_target'] == 1000

    assert tools._json_object({
        'data': json.dumps({'word_target': 100}, ensure_ascii=False),
    }, 'parameters')['word_target'] == 100


def test_academic_outline_allocates_exact_target_and_removes_unknown_evidence():
    tools = _load_pipeline_tools()
    outline = {
        'paper_title': '测试论文',
        'chapters': [{
            'title': '引言', 'level': 1, 'number': '1', 'children': [{
                'title': '研究背景', 'level': 2, 'number': '1.1',
                'target_words': 300, 'source_refs': ['SRC-001'], 'children': [],
            }],
        }, {
            'title': '研究方法', 'level': 1, 'number': '2', 'children': [{
                'title': '研究设计', 'level': 2, 'number': '2.1',
                'target_words': 300, 'source_refs': [], 'children': [],
            }],
        }, {
            'title': '结论', 'level': 1, 'number': '3', 'children': [{
                'title': '研究结论', 'level': 2, 'number': '3.1',
                'target_words': 400, 'source_refs': [], 'children': [],
            }],
        }],
    }

    result = tools.validate_and_allocate_academic_outline(
        json.dumps(outline, ensure_ascii=False), '## SRC-001\n真实来源', '1000', '中文',
    )

    assert result['valid'] is True
    leaves = tools._leaves(result['normalized_outline']['chapters'])
    assert sum(item['target_words'] for item in leaves) == 1000

    english = tools.validate_and_allocate_academic_outline(
        json.dumps(outline, ensure_ascii=False), '## SRC-001\n真实来源', '1000', '英文',
    )
    assert english['normalized_outline']['count_unit'] == 'words'

    outline['chapters'][0]['children'][0]['source_refs'] = ['SRC-999']
    invalid = tools.validate_and_allocate_academic_outline(
        json.dumps(outline, ensure_ascii=False), '## SRC-001\n真实来源', '1000', '中文',
    )
    assert invalid['valid'] is True
    assert 'SRC-999' in invalid['report']
    assert invalid['normalized_outline']['chapters'][0]['children'][0]['source_refs'] == []


def test_evidence_protocol_examples_are_not_registered_sources():
    tools = _load_pipeline_tools()

    assert tools._registered_evidence_ids(
        '编号格式示例为 SRC-001/KB-001；本轮没有实际检索记录。'
    ) == set()
    assert tools._registered_evidence_ids(
        '## SRC-001\n真实来源\n\n| KB-001 | 知识库记录 |'
    ) == {'SRC-001', 'KB-001'}
    assert tools._registered_evidence_ids(json.dumps({
        'text': '**SRC-001** | 真实来源\n\n- **KB-001**: 知识库记录',
    }, ensure_ascii=False)) == {'SRC-001', 'KB-001'}

    assert tools._cited_evidence_ids(
        '已有研究支持该结论（Author, 2025；SRC-001）。核验表列出 SRC-999。'
    ) == {'SRC-001'}


def test_outline_validator_normalizes_markdown_levels_and_root_children():
    tools = _load_pipeline_tools()
    candidate = {
        'title': '测试论文',
        'level': 1,
        'children': [{
            'title': '引言', 'level': 2, 'children': [{
                'title': '背景', 'level': 3, 'source_refs': ['SRC-001'],
            }],
        }, {
            'title': '方法', 'level': 2,
        }, {
            'title': '结论', 'level': 2,
        }],
    }

    result = tools.validate_and_allocate_academic_outline(
        json.dumps(candidate, ensure_ascii=False),
        '**SRC-001** | 真实来源', '1000', '中文',
    )

    assert result['valid'] is True
    assert result['normalized_outline']['paper_title'] == '测试论文'
    assert [item['level'] for item in result['normalized_outline']['chapters']] == [1, 1, 1]
    assert result['normalized_outline']['chapters'][0]['children'][0]['level'] == 2
    assert result['normalized_outline']['chapters'][0]['children'][0]['source_refs'] == ['SRC-001']


def test_bound_outline_validator_reads_evidence_without_model_copy(monkeypatch, tmp_path):
    tools = _load_pipeline_tools()
    evidence = tmp_path / 'literature_evidence.json'
    evidence.write_text(json.dumps({
        'text': '**SRC-001** | 真实来源',
    }, ensure_ascii=False), encoding='utf-8')
    monkeypatch.setattr(tools, '_remote_inputs', lambda: {
        'literature_evidence': str(evidence),
    })
    candidate = {
        'paper_title': '测试论文',
        'chapters': [
            {'title': '引言', 'source_refs': ['SRC-001']},
            {'title': '方法'},
            {'title': '结论'},
        ],
    }

    result = tools.validate_and_allocate_academic_outline_from_inputs(
        json.dumps(candidate, ensure_ascii=False), '1000', '中文',
    )

    assert result['valid'] is True
    assert '- 可用证据：1' in result['report']


def test_outline_validator_repairs_common_llm_shape_variations():
    tools = _load_pipeline_tools()
    candidate = {
        'paper_title': '测试论文',
        'chapters': [
            '引言',
            {'title': '', 'level': 'not-a-number', 'children': 'none'},
        ],
    }

    result = tools.validate_and_allocate_academic_outline(
        f'```json\n{json.dumps(candidate, ensure_ascii=False)}\n```', '', '500', '中文',
    )

    assert result['valid'] is True
    assert len(result['normalized_outline']['chapters']) == 2
    assert result['normalized_outline']['chapters'][0]['title'] == '引言'
    assert result['normalized_outline']['chapters'][1]['title'].startswith('未命名章节')


def test_review_decision_normalizer_is_tolerant_and_safe():
    tools = _load_pipeline_tools()

    assert tools.normalize_academic_review_decision(
        'Decision: minor revision\n仅有格式问题。',
    ) == 'MINOR_REVISION'
    assert tools.normalize_academic_review_decision('建议接受。') == 'ACCEPT'
    assert tools.normalize_academic_review_decision('REJECT: evidence is insufficient') == (
        'MAJOR_REVISION'
    )


def test_delivery_preserves_short_approved_manuscript_and_nested_parameters(
    monkeypatch, tmp_path,
):
    builder = _load_document_builder()
    parameters = tmp_path / 'parameters.json'
    manuscript = tmp_path / 'draft.md'
    output = tmp_path / 'delivery'
    output.mkdir()
    parameters.write_text(json.dumps({
        'data': json.dumps({'output_format': 'md'}, ensure_ascii=False),
    }, ensure_ascii=False), encoding='utf-8')
    manuscript.write_text('# 短论文\n\n正文。', encoding='utf-8')
    monkeypatch.setattr(builder, '_remote_inputs', lambda: {
        'generation_parameters': str(parameters), 'draft_document': str(manuscript),
    })
    monkeypatch.setattr(builder, '_run_root', lambda: output)

    result = builder.compose_academic_paper_from_inputs()

    assert Path(result['final_paper']).read_text(encoding='utf-8').startswith('# 短论文')


def test_integrity_audit_blocks_unknown_citation_and_missing_declarations():
    tools = _load_pipeline_tools()
    manuscript = '\n'.join([
        '# 测试论文',
        '## 摘要',
        '## 引言',
        '错误引用 [SRC-999]。' + '中' * 950,
        '## 研究方法',
        '## 讨论',
        '## 结论',
        '## 参考文献',
    ])
    parameters = {
        'word_target': 1000,
        'count_unit': 'chinese_characters',
    }

    result = tools.audit_academic_manuscript(
        manuscript, '## SRC-001\n真实来源', json.dumps(parameters),
    )

    assert result['summary']['status'] == 'FAIL'
    assert result['summary']['unknown_citations'] == ['SRC-999']
    assert result['summary']['missing_sections']


def test_integrity_audit_respects_sections_removed_from_approved_outline():
    tools = _load_pipeline_tools()
    approved_outline = '\n'.join([
        '# 测试论文', '## 摘要', '## 引言', '## 研究方法', '## 结论', '## 参考文献',
    ])
    manuscript = approved_outline + '\n' + '中' * 950
    parameters = {'word_target': 1000, 'count_unit': 'chinese_characters'}

    result = tools.audit_academic_manuscript(
        manuscript, '', json.dumps(parameters), approved_outline_markdown=approved_outline,
    )

    assert result['summary']['status'] == 'WARN'
    assert result['summary']['missing_sections'] == []
    assert 'Discussion/讨论' in result['summary']['outline_omitted_sections']
    assert not result['summary']['failures']
