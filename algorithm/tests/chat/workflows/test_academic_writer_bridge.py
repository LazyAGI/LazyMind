import importlib.util
import json
import sys
import types
from pathlib import Path

import pytest


def _stub_module(name, **attributes):
    module = types.ModuleType(name)
    module.__dict__.update(attributes)
    if name in {
        'lazyllm', 'lazyllm.tools', 'lazyllm.tools.writer', 'lazymind',
        'lazymind.chat', 'lazymind.chat.engine', 'lazymind.chat.engine.subagent',
        'lazymind.chat.engine.tools',
    }:
        module.__path__ = []
    return module


def _load_writer_bridge():
    stubs = {
        'lazyllm': _stub_module('lazyllm', AutoModel=object),
        'lazyllm.tools': _stub_module('lazyllm.tools'),
        'lazyllm.tools.writer': _stub_module('lazyllm.tools.writer'),
        'lazyllm.tools.writer.data_models': _stub_module(
            'lazyllm.tools.writer.data_models', StringReplaceSet=object,
        ),
        'lazyllm.tools.writer.tools': _stub_module(
            'lazyllm.tools.writer.tools', WriterRevisionTools=object,
        ),
        'lazymind': _stub_module('lazymind'),
        'lazymind.chat': _stub_module('lazymind.chat'),
        'lazymind.chat.engine': _stub_module('lazymind.chat.engine'),
        'lazymind.chat.engine.subagent': _stub_module('lazymind.chat.engine.subagent'),
        'lazymind.chat.engine.subagent.context': _stub_module(
            'lazymind.chat.engine.subagent.context', require_context=lambda: None,
        ),
        'lazymind.chat.engine.tools': _stub_module('lazymind.chat.engine.tools'),
        'lazymind.chat.engine.tools.writer': _stub_module(
            'lazymind.chat.engine.tools.writer',
            DraftMarkdownStreamEventEmitter=object,
            WriterCreateToolkit=object,
            WriterRevisionToolkit=object,
        ),
    }
    previous = {name: sys.modules.get(name) for name in stubs}
    sys.modules.update(stubs)
    try:
        root = Path(__file__).resolve().parents[4]
        path = root / 'workflows' / 'academic_research_pipeline' / 'scripts' / 'writer_bridge.py'
        spec = importlib.util.spec_from_file_location('academic_writer_bridge_for_test', path)
        assert spec and spec.loader
        module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(module)
        return module
    finally:
        for name, module in previous.items():
            if module is None:
                sys.modules.pop(name, None)
            else:
                sys.modules[name] = module


def test_top_level_leaf_is_normalized_to_single_h2_root():
    bridge = _load_writer_bridge()
    outline = {
        'total_word_target': 1,
        'count_unit': 'chinese_characters',
        'chapters': [{
            'title': '数据可用性', 'level': 1, 'number': '1',
            'target_words': 1, 'source_refs': [], 'children': [],
        }],
    }

    result = bridge._enforce_draft_contract([
        '### 数据可用性\n\n### 数据可用性\n\n研究不涉及开放数据。',
    ], outline)[0]

    assert result.count('## 数据可用性') == 1
    assert '### 数据可用性' not in result


def test_parent_chapter_gets_h2_root_and_keeps_leaf_h3():
    bridge = _load_writer_bridge()
    outline = {
        'total_word_target': 1,
        'count_unit': 'chinese_characters',
        'chapters': [{
            'title': '研究方法', 'level': 1, 'number': '1', 'children': [{
                'title': '研究设计', 'level': 2, 'number': '1.1',
                'target_words': 1, 'source_refs': [], 'children': [],
            }],
        }],
    }

    result = bridge._enforce_draft_contract([
        '### 研究设计\n\n采用理论分析。',
    ], outline)[0]

    assert result.startswith('## 研究方法')
    assert result.count('\n## ') == 0
    assert '### 研究设计' in result


def test_section_heading_levels_are_shifted_relatively():
    bridge = _load_writer_bridge()

    result = bridge._normalize_section_root(
        '#### 研究方法\n\n##### 研究设计\n\n###### 数据来源\n\n采用理论分析。',
        {'title': '研究方法'},
    )

    assert result.startswith('## 研究方法')
    assert '### 研究设计' in result
    assert '#### 数据来源' in result


def test_transient_outline_must_match_user_approved_markdown():
    bridge = _load_writer_bridge()
    approved = '# 论文\n\n## 引言\n\n### 背景\n\n## 结论\n'
    exact = {
        'paper_title': '论文',
        'chapters': [{
            'title': '引言', 'children': [{'title': '背景', 'children': []}],
        }, {
            'title': '结论', 'children': [],
        }],
    }

    bridge._assert_outline_matches_markdown(exact, approved)

    divergent = json.loads(json.dumps(exact, ensure_ascii=False))
    divergent['chapters'].insert(1, {'title': '讨论', 'children': []})
    with pytest.raises(ValueError, match='must exactly match'):
        bridge._assert_outline_matches_markdown(divergent, approved)


def test_approved_markdown_rebuilds_stale_outline_constraints():
    bridge = _load_writer_bridge()
    stale = {
        'paper_title': '旧标题',
        'total_word_target': 400,
        'count_unit': 'chinese_characters',
        'chapters': [{
            'title': '问题与方法', 'level': 1, 'number': '1',
            'target_words': 200, 'source_refs': [], 'children': [{
                'title': '研究问题', 'level': 2, 'number': '1.1',
                'target_words': 100, 'source_refs': ['SRC-001'], 'children': [],
            }, {
                'title': '分析方法', 'level': 2, 'number': '1.2',
                'target_words': 100, 'source_refs': [], 'children': [],
            }],
        }, {
            'title': '结论', 'level': 1, 'number': '2',
            'target_words': 200, 'source_refs': [], 'children': [],
        }],
    }
    approved = '\n'.join([
        '# 新标题', '', '## 摘要', '', '## 问题与方法', '',
        '### 研究问题', '', '### 分析方法', '', '## 结论', '', '## 参考文献', '',
    ])

    synchronized = bridge._synchronize_outline_with_markdown(stale, approved)

    assert synchronized['paper_title'] == '新标题'
    assert [item['title'] for item in synchronized['chapters']] == [
        '摘要', '问题与方法', '结论', '参考文献',
    ]
    assert sum(item['target_words'] for item in bridge._leaves(
        synchronized['chapters'],
    )) == 400
    research_question = synchronized['chapters'][1]['children'][0]
    assert research_question['source_refs'] == ['SRC-001']
    assert synchronized['chapters'][0]['source_refs'] == []
    bridge._assert_outline_matches_markdown(synchronized, approved)


def test_outline_cannot_be_used_as_initial_draft_revision(tmp_path):
    bridge = _load_writer_bridge()
    outline = tmp_path / 'outline_document.md'
    outline.write_text('# 论文\n\n## 引言\n\n### 背景\n', encoding='utf-8')

    with pytest.raises(ValueError, match='cannot be replaced'):
        bridge.academic_writer_revise_markdown(
            str(outline), 'unused-context.json', '生成全文', 'draft_document',
        )


def test_section_planning_uses_exact_projection_of_approved_outline(monkeypatch, tmp_path):
    bridge = _load_writer_bridge()
    task = tmp_path / 'writing_task.json'
    context = tmp_path / 'writing_context.json'
    approved_outline = tmp_path / 'outline_document.md'
    effective_outline = tmp_path / 'effective_outline.json'
    task.write_text('{}', encoding='utf-8')
    context.write_text('{}', encoding='utf-8')
    approved_outline.write_text('# 论文\n\n## 引言\n\n## 数据可用性\n', encoding='utf-8')
    effective_outline.write_text(json.dumps({
        'paper_title': '论文',
        'total_word_target': 100,
        'count_unit': 'chinese_characters',
        'chapters': [{
            'title': '引言', 'level': 1, 'number': '1',
            'target_words': 80, 'source_refs': [], 'children': [],
        }, {
            'title': '数据可用性', 'level': 1, 'number': '2',
            'target_words': 20, 'source_refs': [], 'children': [],
        }],
    }, ensure_ascii=False), encoding='utf-8')
    run_root = tmp_path / 'run'
    run_root.mkdir()
    monkeypatch.setattr(bridge, '_run_root', lambda _name: run_root)

    result = bridge.academic_writer_plan_sections(
        str(task), str(approved_outline), str(context), str(effective_outline),
    )

    plan = json.loads(Path(result['section_instructions']).read_text(encoding='utf-8'))
    assert [item['section_title'] for item in plan['instructions']] == [
        '引言', '数据可用性',
    ]
    assert plan['instruction_set_id'].startswith('academic-')
    assert result['warnings'] == []


def test_draft_contract_treats_leaf_evidence_mapping_as_planning_guidance():
    bridge = _load_writer_bridge()
    outline = {
        'total_word_target': 1,
        'count_unit': 'chinese_characters',
        'chapters': [{
            'title': '引言', 'level': 1, 'number': '1',
            'target_words': 1, 'source_refs': ['SRC-001', 'SRC-002'], 'children': [],
        }],
    }

    result = bridge._enforce_draft_contract([
        '## 引言\n\n仅使用更合适的版本（SRC-002）。',
    ], outline)
    assert 'SRC-001' not in result[0]

    note = bridge._enforce_draft_contract([
        '## 引言\n\n注册表核验说明提到了其他候选来源（SRC-999）。',
    ], outline)
    assert 'SRC-999' in note[0]


def test_stale_section_plan_is_repaired_before_drafting():
    bridge = _load_writer_bridge()
    outline = {
        'chapters': [
            {'title': '摘要'},
            {'title': '问题与方法'},
        ],
    }

    plan = {'instructions': [{'section_title': '问题与方法'}]}
    bridge._assert_section_instructions_match_outline(plan, outline)

    assert [item['section_title'] for item in plan['instructions']] == [
        '摘要', '问题与方法',
    ]

    repeated, _ = bridge._normalize_section_instructions({
        'instructions': [{'section_title': '完全不同的随机标题'}],
    }, outline)
    assert repeated == plan


def test_short_incomplete_draft_is_preserved_and_missing_structure_is_repaired():
    bridge = _load_writer_bridge()
    outline = {
        'total_word_target': 1000,
        'count_unit': 'chinese_characters',
        'chapters': [{
            'title': '问题与方法', 'level': 1, 'number': '1', 'children': [{
                'title': '研究问题', 'level': 2, 'number': '1.1', 'children': [],
            }, {
                'title': '分析方法', 'level': 2, 'number': '1.2', 'children': [],
            }],
        }, {
            'title': '结论', 'level': 1, 'number': '2', 'children': [],
        }],
    }

    result = bridge._enforce_draft_contract([
        '研究问题已经形成，但模型没有使用 Markdown 标题。',
    ], outline)

    assert len(result) == 2
    assert result[0].startswith('## 问题与方法')
    assert '### 研究问题' in result[0]
    assert '### 分析方法' in result[0]
    assert result[1].startswith('## 结论')
    assert '未获得独立生成内容' in result[1]


def test_outline_heading_levels_are_repaired_relative_to_first_heading():
    bridge = _load_writer_bridge()

    signature = bridge._markdown_heading_signature(
        '### 论文标题\n\n#### 引言\n\n###### 背景\n\n#### 结论\n',
    )

    assert signature == [(1, '论文标题'), (2, '引言'), (3, '背景'), (2, '结论')]


def test_outline_is_normalized_directly_without_llm_json_conversion(monkeypatch, tmp_path):
    bridge = _load_writer_bridge()
    source = tmp_path / 'outline.md'
    parameters = tmp_path / 'parameters.json'
    source.write_text('### 引言\n\n##### 背景\n\n### 结论\n', encoding='utf-8')
    parameters.write_text(json.dumps({
        'research_topic': '论文标题',
        'word_target': 600,
        'count_unit': 'chinese_characters',
    }, ensure_ascii=False), encoding='utf-8')
    run_root = tmp_path / 'normalized'
    run_root.mkdir()
    monkeypatch.setattr(bridge, '_run_root', lambda _name: run_root)

    result = bridge.academic_writer_normalize_outline(str(source), str(parameters))

    markdown = Path(result['outline_document']).read_text(encoding='utf-8')
    contract = json.loads(Path(result['effective_outline']).read_text(encoding='utf-8'))
    assert markdown.startswith('# 论文标题\n\n## 引言\n\n### 背景')
    assert '## 结论' in markdown
    assert sum(item['target_words'] for item in bridge._leaves(contract['chapters'])) == 600
    assert '## PASS' in result['outline_check_report']


def test_json_artifact_wrappers_are_recursively_unwrapped():
    bridge = _load_writer_bridge()

    assert bridge._json_value({
        'data': json.dumps({'word_target': 100}, ensure_ascii=False),
    }) == {'word_target': 100}


def test_feedback_revision_reads_materialized_text_artifact(monkeypatch, tmp_path):
    bridge = _load_writer_bridge()
    feedback = tmp_path / 'revision_roadmap.json'
    feedback.write_text(json.dumps({
        'text': '# 修订路线图\n\n- R-001：补充研究边界。',
    }, ensure_ascii=False), encoding='utf-8')
    captured = {}

    def revise(base_document_path, writing_context_path, instruction, document_slot):
        captured.update({
            'base_document_path': base_document_path,
            'writing_context_path': writing_context_path,
            'instruction': instruction,
            'document_slot': document_slot,
        })
        return {'revised_document': '/output/revised.md'}

    monkeypatch.setattr(bridge, 'academic_writer_revise_markdown', revise)

    result = bridge.academic_writer_revise_from_feedback(
        '/inputs/draft.md', '/inputs/context.json', str(feedback),
        'revised_document', '保留原有标题结构',
    )

    assert result == {'revised_document': '/output/revised.md'}
    assert captured['base_document_path'] == '/inputs/draft.md'
    assert captured['writing_context_path'] == '/inputs/context.json'
    assert captured['document_slot'] == 'revised_document'
    assert '# 修订路线图' in captured['instruction']
    assert 'R-001：补充研究边界' in captured['instruction']
    assert '保留原有标题结构' in captured['instruction']
    assert '"text"' not in captured['instruction']


def test_full_document_revision_uses_one_model_call_and_accepts_outer_fence(
    monkeypatch, tmp_path,
):
    bridge = _load_writer_bridge()
    source = tmp_path / 'draft.md'
    context = tmp_path / 'context.json'
    source.write_text('# 标题\n\n旧正文（SRC-001）。\n', encoding='utf-8')
    context.write_text('{"registered": ["SRC-001"]}', encoding='utf-8')
    calls = []

    class Revision:
        def __init__(self, **_kwargs):
            pass

        def _call_llm_text(self, prompt):
            calls.append(prompt)
            return '```markdown\n# 标题\n\n修订正文（SRC-001）。\n```'

    monkeypatch.setattr(bridge, 'WriterRevisionTools', Revision)
    monkeypatch.setattr(bridge, 'AutoModel', lambda **_kwargs: object())
    revision_root = tmp_path / 'revision'
    revision_root.mkdir()
    monkeypatch.setattr(bridge, '_run_root', lambda _name: revision_root)

    result = bridge.academic_writer_revise_markdown(
        str(source), str(context), '修订正文', 'revised_document',
    )

    assert len(calls) == 1
    assert 'SRC-001' in calls[0]
    assert Path(result['revised_document']).read_text(encoding='utf-8') == (
        '# 标题\n\n修订正文（SRC-001）。\n'
    )
    revision_result = json.loads(Path(result['revision_result']).read_text(encoding='utf-8'))
    assert revision_result['status'] == 'APPLIED'
    assert 'warning' not in result


def test_full_document_revision_failure_preserves_source_without_retry(monkeypatch, tmp_path):
    bridge = _load_writer_bridge()
    source = tmp_path / 'draft.md'
    context = tmp_path / 'context.json'
    source.write_text('# 标题\n\n已批准正文。\n', encoding='utf-8')
    context.write_text('{}', encoding='utf-8')
    calls = []

    class Revision:
        def __init__(self, **_kwargs):
            pass

        def _call_llm_text(self, prompt):
            calls.append(prompt)
            raise ValueError('provider returned empty output')

    monkeypatch.setattr(bridge, 'WriterRevisionTools', Revision)
    monkeypatch.setattr(bridge, 'AutoModel', lambda **_kwargs: object())
    revision_root = tmp_path / 'revision'
    revision_root.mkdir()
    monkeypatch.setattr(bridge, '_run_root', lambda _name: revision_root)

    result = bridge.academic_writer_revise_markdown(
        str(source), str(context), '修订正文', 'revised_document',
    )

    assert len(calls) == 1
    assert 'provider returned empty output' in result['warning']
    assert Path(result['revised_document']).read_text(encoding='utf-8') == (
        '# 标题\n\n已批准正文。\n'
    )
