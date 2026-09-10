import importlib.util
import json
import sys
import types
from pathlib import Path

import pytest


def _stub_module(name, **attributes):
    module = types.ModuleType(name)
    module.__dict__.update(attributes)
    if name in {'lazyllm', 'lazyllm.tools', 'lazyllm.tools.writer', 'lazymind',
                'lazymind.chat', 'lazymind.chat.engine', 'lazymind.chat.engine.subagent',
                'lazymind.chat.engine.tools'}:
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
        path = root / 'workflows' / 'bid_tech_proposal_writer' / 'scripts' / 'writer_bridge.py'
        spec = importlib.util.spec_from_file_location('bid_writer_bridge_for_test', path)
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


def test_draft_contract_completes_exact_trace_ids_without_another_model_call():
    bridge = _load_writer_bridge()
    outline = {
        'total_word_target': 1,
        'chapters': [{
            'title': '总体响应',
            'level': 1,
            'number': '1',
            'children': [{
                'title': '响应范围',
                'level': 2,
                'number': '1.1',
                'children': [],
                'bid_requirements_refs': ['BG-001', 'BG-002', 'BG-003'],
                'disqualification_refs': ['D-004'],
            }],
        }],
    }
    section = '\n'.join([
        '## 总体响应',
        '',
        '### 响应范围',
        '',
        '方案完整覆盖招标要求。',
        '',
        '追溯：BG-001~003。',
    ])

    result = bridge._enforce_draft_contract([section], outline)[0]

    assert set(bridge.BID_TRACE_ID.findall(result)) == {
        'BG-001', 'BG-002', 'BG-003', 'D-004',
    }


def test_text_input_accepts_wrapped_artifact_path(tmp_path):
    bridge = _load_writer_bridge()
    artifact = tmp_path / 'generation_parameters.json'
    artifact.write_text('{"data":{"word_target":1000}}', encoding='utf-8')

    value = bridge._text_content_or_path(str(artifact))

    assert bridge._json_value(value, {}) == {'word_target': 1000}


def test_draft_contract_adds_missing_leaf_trace_line():
    bridge = _load_writer_bridge()
    outline = {
        'total_word_target': 1,
        'chapters': [{
            'title': '性能响应',
            'level': 1,
            'number': '1',
            'children': [{
                'title': '性能指标',
                'level': 2,
                'number': '1.1',
                'children': [],
                'bid_requirements_refs': ['PERF-001', 'PERF-002'],
                'disqualification_refs': ['D-001', 'D-007'],
            }],
        }],
    }
    section = '## 性能响应\n\n### 性能指标\n\n满足全部性能要求。'

    result = bridge._enforce_draft_contract([section], outline)[0]

    assert '追溯：PERF-001、PERF-002、D-001、D-007。' in result


def test_draft_contract_preserves_short_output_and_repairs_missing_leaf():
    bridge = _load_writer_bridge()
    outline = {
        'total_word_target': 1000,
        'chapters': [{
            'title': '总体响应',
            'level': 1,
            'number': '1',
            'children': [{
                'title': '建设目标',
                'level': 2,
                'number': '1.1',
                'children': [],
                'bid_requirements_refs': ['BG-001'],
                'disqualification_refs': [],
            }, {
                'title': '建设范围',
                'level': 2,
                'number': '1.2',
                'children': [],
                'bid_requirements_refs': ['FUNC-001'],
                'disqualification_refs': ['D-001'],
            }],
        }],
    }

    result = bridge._enforce_draft_contract([
        '## 总体响应\n\n### 建设目标\n\n简短但有效的正文。',
    ], outline)[0]

    assert '简短但有效的正文。' in result
    assert '### 建设范围' in result
    assert '追溯：FUNC-001、D-001。' in result
    assert '请在正文审批中补充' in result


def test_section_plan_is_deterministic_from_approved_outline(tmp_path, monkeypatch):
    bridge = _load_writer_bridge()

    def run_root(name):
        root = tmp_path / name
        root.mkdir(parents=True, exist_ok=True)
        return root

    monkeypatch.setattr(bridge, '_run_root', run_root)
    task = tmp_path / 'writing_task.json'
    context = tmp_path / 'writing_context.json'
    outline_document = tmp_path / 'outline.md'
    effective = tmp_path / 'effective_outline.json'
    task.write_text('{}', encoding='utf-8')
    context.write_text('{}', encoding='utf-8')
    outline_document.write_text('# 测试方案\n\n## 总体响应\n\n### 建设范围\n', encoding='utf-8')
    effective.write_text(json.dumps({
        'project_name': '测试方案',
        'total_word_target': 1000,
        'chapters': [{
            'title': '总体响应', 'level': 1, 'number': '1', 'target_words': 1000,
            'children': [{
                'title': '建设范围', 'level': 2, 'number': '1.1',
                'target_words': 1000, 'children': [],
                'bid_requirements_refs': ['FUNC-001'], 'disqualification_refs': [],
            }],
        }],
    }, ensure_ascii=False), encoding='utf-8')

    result = bridge.bid_writer_plan_sections(
        str(task), str(outline_document), str(context), str(effective),
    )
    payload = json.loads(Path(result['section_instructions']).read_text(encoding='utf-8'))

    assert payload['meta']['deterministically_normalized'] is True
    assert payload['instructions'][0]['section_title'] == '总体响应'
    assert payload['instructions'][0]['meta']['bid_leaf_contract'][0]['title'] == '建设范围'


def test_outline_cannot_be_used_as_initial_bid_draft_revision(tmp_path):
    bridge = _load_writer_bridge()
    outline = tmp_path / 'outline_document.md'
    outline.write_text('# 方案\n\n## 总体响应\n\n### 响应范围\n', encoding='utf-8')

    with pytest.raises(ValueError, match='cannot be replaced'):
        bridge.bid_writer_revise_markdown(
            str(outline), 'unused-context.json', '生成全文', 'draft_document',
        )
