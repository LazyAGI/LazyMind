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
        path = root / 'workflows' / 'product_solution_delivery' / 'scripts' / 'writer_bridge.py'
        spec = importlib.util.spec_from_file_location('product_writer_bridge_for_test', path)
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


def _load_contract_tools(tmp_path):
    context = types.SimpleNamespace(workspace_path=str(tmp_path))
    stubs = {
        'lazymind': _stub_module('lazymind'),
        'lazymind.chat': _stub_module('lazymind.chat'),
        'lazymind.chat.engine': _stub_module('lazymind.chat.engine'),
        'lazymind.chat.engine.subagent': _stub_module('lazymind.chat.engine.subagent'),
        'lazymind.chat.engine.subagent.context': _stub_module(
            'lazymind.chat.engine.subagent.context', require_context=lambda: context,
        ),
    }
    previous = {name: sys.modules.get(name) for name in stubs}
    sys.modules.update(stubs)
    try:
        root = Path(__file__).resolve().parents[4]
        path = root / 'workflows' / 'product_solution_delivery' / 'scripts' / 'tools.py'
        spec = importlib.util.spec_from_file_location('product_contract_tools_for_test', path)
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


def test_product_outline_has_one_h1_and_contiguous_hierarchy():
    bridge = _load_writer_bridge()

    valid = bridge.validate_product_outline(
        'direction', '# 产品方向\n\n## 一句话方向\n\n## 目标用户与场景\n',
    )
    assert valid['valid'] is True
    assert valid['warnings']

    invalid = bridge.validate_product_outline(
        'direction', '# 产品方向\n\n### 跳级标题\n',
    )
    assert invalid['valid'] is False
    assert any('层级跳跃' in item for item in invalid['errors'])


def test_generated_outline_is_repaired_without_losing_outline_notes():
    bridge = _load_writer_bridge()

    normalized = bridge._normalize_generated_outline(
        'design', '### 核心流程\n\n- 保留这条说明\n\n##### 异常恢复',
    )

    assert bridge._heading_signature(normalized) == [
        (1, '产品方案'), (2, '核心流程'), (3, '异常恢复'),
    ]
    assert '- 保留这条说明' in normalized


def test_approved_outline_does_not_restore_deleted_template_sections():
    bridge = _load_writer_bridge()

    normalized = bridge._normalize_approved_outline('prd', '# 极简 PRD\n\n只保留必要内容。')

    assert normalized == '# 极简 PRD\n\n## 正文\n\n只保留必要内容。'
    assert '背景与目标' not in normalized


def test_product_draft_alignment_demotes_unapproved_headings_and_keeps_content():
    bridge = _load_writer_bridge()
    outline = '# 产品方向\n\n## 背景与证据\n\n## 范围与非目标\n'
    draft = '\n'.join([
        '# 产品方向', '## 背景与证据', '内容一。', '### 模型额外小结',
        '额外内容仍保留。', '## 范围与非目标', '内容二。',
    ])

    aligned = bridge._align_draft_headings(draft, outline)

    assert '### 模型额外小结' not in aligned
    assert '**模型额外小结**' in aligned
    assert '额外内容仍保留。' in aligned
    assert bridge._heading_signature(aligned) == bridge._heading_signature(outline)


def test_product_draft_alignment_preserves_missing_approved_heading_as_visible_gap():
    bridge = _load_writer_bridge()

    aligned = bridge._align_draft_headings(
        '# PRD\n\n## 背景与目标\n正文',
        '# PRD\n\n## 背景与目标\n\n## 验收标准\n',
    )

    assert '## 验收标准' in aligned
    assert '本节尚未生成有效正文，请在审批时补充。' in aligned
    assert bridge._heading_signature(aligned) == bridge._heading_signature(
        '# PRD\n\n## 背景与目标\n\n## 验收标准\n',
    )


def test_section_plan_uses_exact_approved_h2_order(monkeypatch, tmp_path):
    bridge = _load_writer_bridge()
    task = tmp_path / 'writing_task.json'
    context = tmp_path / 'writing_context.json'
    outline = tmp_path / 'outline_document.md'
    task.write_text(json.dumps({'product_parameters': {'stage_id': 'review'}}), encoding='utf-8')
    context.write_text('{}', encoding='utf-8')
    outline.write_text('# 评审报告\n\n## 评审结论\n\n## P1 关键问题\n', encoding='utf-8')

    output = tmp_path / 'output'
    output.mkdir()
    monkeypatch.setattr(bridge, '_run_root', lambda _name: output)

    result = bridge.product_writer_plan_sections(str(task), str(outline), str(context))
    planned = json.loads(Path(result['section_instructions']).read_text(encoding='utf-8'))
    assert [item['section_title'] for item in planned['instructions']] == [
        '评审结论', 'P1 关键问题',
    ]
    assert all(item['instruction_id'] for item in planned['instructions'])
    assert all(item['content_ref']['heading_path'][0] == '评审报告'
               for item in planned['instructions'])
    assert all(item['section_goal'] for item in planned['instructions'])
    assert planned['meta']['deterministically_normalized'] is True


def test_section_plan_does_not_call_generative_planner(monkeypatch, tmp_path):
    bridge = _load_writer_bridge()
    task = tmp_path / 'writing_task.json'
    context = tmp_path / 'writing_context.json'
    outline = tmp_path / 'outline_document.md'
    task.write_text(json.dumps({'product_parameters': {'stage_id': 'prd'}}), encoding='utf-8')
    context.write_text('{}', encoding='utf-8')
    outline.write_text('# PRD\n\n## 背景与目标\n\n## 验收标准\n', encoding='utf-8')

    class FakeToolkit:
        def __init__(self):
            raise AssertionError('the approved outline must not use a generative planner')

    output = tmp_path / 'output'
    output.mkdir()
    monkeypatch.setattr(bridge, 'WriterCreateToolkit', FakeToolkit)
    monkeypatch.setattr(bridge, '_run_root', lambda _name: output)

    result = bridge.product_writer_plan_sections(str(task), str(outline), str(context))
    planned = json.loads(Path(result['section_instructions']).read_text(encoding='utf-8'))
    assert [item['section_title'] for item in planned['instructions']] == [
        '背景与目标', '验收标准',
    ]
    assert all(set(('instruction_id', 'content_ref', 'section_goal')) <= set(item)
               for item in planned['instructions'])


def test_context_refresh_failure_preserves_completed_stage(monkeypatch, tmp_path):
    bridge = _load_writer_bridge()
    content = tmp_path / 'draft.md'
    context = tmp_path / 'writing_context.json'
    content.write_text('# 已完成正文\n', encoding='utf-8')
    context.write_text(json.dumps({'meta': {'existing': True}}), encoding='utf-8')

    class FakeToolkit:
        def update_writing_context(self, **_kwargs):
            raise RuntimeError('temporary model failure')

    output = tmp_path / 'output'
    output.mkdir()
    monkeypatch.setattr(bridge, 'WriterCreateToolkit', FakeToolkit)
    monkeypatch.setattr(bridge, '_run_root', lambda _name: output)

    result = bridge.product_writer_update_context(str(content), str(context))
    updated = json.loads(Path(result).read_text(encoding='utf-8'))
    assert updated['meta']['existing'] is True
    assert 'temporary model failure' in updated['meta']['context_update_warning']


def test_prepare_context_profiles_approved_upstream_artifacts(monkeypatch, tmp_path):
    bridge = _load_writer_bridge()
    profiles = tmp_path / 'resource_profiles.json'
    upstream = tmp_path / 'direction.md'
    profiles.write_text(json.dumps([{'id': 'uploaded-material'}]), encoding='utf-8')
    upstream.write_text('# 已批准产品方向\n', encoding='utf-8')

    class FakeToolkit:
        def build_writing_task(self, **_kwargs):
            return '{}'

        def build_resources(self, file_paths_json, **_kwargs):
            return file_paths_json

        def profile_resources(self, **_kwargs):
            return json.dumps([{'id': 'approved-direction'}])

        def create_writing_context(self, **_kwargs):
            return '{}'

    output = tmp_path / 'output'
    output.mkdir()
    context = types.SimpleNamespace(params={'session_id': 'test-session'})
    monkeypatch.setattr(bridge, 'WriterCreateToolkit', FakeToolkit)
    monkeypatch.setattr(bridge, 'require_context', lambda: context)
    monkeypatch.setattr(bridge, '_workspace_root', lambda: tmp_path)
    monkeypatch.setattr(bridge, '_run_root', lambda _name: output)

    result = bridge.product_writer_prepare_context(
        '生成产品方案',
        'design',
        json.dumps({'selected_stage': 'design'}),
        'WEB-001 evidence',
        resource_profiles_path=str(profiles),
        upstream_artifact_paths_json=json.dumps([str(upstream)]),
    )

    combined = json.loads(Path(result['resource_profiles']).read_text(encoding='utf-8'))
    assert combined == [{'id': 'uploaded-material'}, {'id': 'approved-direction'}]


def test_prepare_context_accepts_later_stage_in_approved_chain(monkeypatch, tmp_path):
    bridge = _load_writer_bridge()
    profiles = tmp_path / 'resource_profiles.json'
    profiles.write_text('[]', encoding='utf-8')

    class FakeToolkit:
        def build_writing_task(self, **_kwargs):
            return '{}'

        def create_writing_context(self, **_kwargs):
            return '{}'

    output = tmp_path / 'output'
    output.mkdir()
    context = types.SimpleNamespace(params={'session_id': 'chain-session'})
    monkeypatch.setattr(bridge, 'WriterCreateToolkit', FakeToolkit)
    monkeypatch.setattr(bridge, 'require_context', lambda: context)
    monkeypatch.setattr(bridge, '_workspace_root', lambda: tmp_path)
    monkeypatch.setattr(bridge, '_run_root', lambda _name: output)
    plan = json.dumps({'selected_stage': 'direction', 'stage_chain': ['direction', 'design']})

    result = bridge.product_writer_prepare_context(
        '继续生成产品方案',
        'design',
        plan,
        '',
        resource_profiles_path=str(profiles),
    )

    assert Path(result['writing_task']).is_file()
    with pytest.raises(ValueError, match='stage_chain'):
        bridge.product_writer_prepare_context(
            '越权生成 PRD',
            'prd',
            plan,
            '',
            resource_profiles_path=str(profiles),
        )


def test_prepare_context_accepts_json_and_evidence_artifact_paths(monkeypatch, tmp_path):
    bridge = _load_writer_bridge()
    profiles = tmp_path / 'resource_profiles.json'
    routing = tmp_path / 'execution_plan.json'
    evidence = tmp_path / 'research_evidence.md'
    profiles.write_text('[]', encoding='utf-8')
    routing.write_text(json.dumps({'stage_chain': ['direction']}), encoding='utf-8')
    evidence.write_text('KB-001 已核验材料', encoding='utf-8')

    class FakeToolkit:
        def build_writing_task(self, **_kwargs):
            return '{}'

        def create_writing_context(self, **_kwargs):
            return '{}'

    output = tmp_path / 'output'
    output.mkdir()
    context = types.SimpleNamespace(params={'session_id': 'path-session'})
    monkeypatch.setattr(bridge, 'WriterCreateToolkit', FakeToolkit)
    monkeypatch.setattr(bridge, 'require_context', lambda: context)
    monkeypatch.setattr(bridge, '_workspace_root', lambda: tmp_path)
    monkeypatch.setattr(bridge, '_run_root', lambda _name: output)

    result = bridge.product_writer_prepare_context(
        '生成产品方向',
        'direction',
        str(routing),
        str(evidence),
        resource_profiles_path=str(profiles),
    )

    stored = json.loads(Path(result['writing_context']).read_text(encoding='utf-8'))
    contract_fact = next(
        fact for fact in stored['facts'] if fact['fact_id'] == 'product-stage-contract'
    )
    assert isinstance(contract_fact['value'], str)
    assert json.loads(contract_fact['value'])['registered_evidence'] == 'KB-001 已核验材料'


def test_embedded_contract_and_workspace_local_html_output(tmp_path):
    tools = _load_contract_tools(tmp_path)

    contract = tools.load_product_skill_contract('prd')
    assert contract['skill_name'] == 'write-prd'
    assert '# 需求文档' in contract['contract_text']

    result = tools.write_product_artifact(
        'prototype.html',
        '<!doctype html><html><head><title>原型</title>'
        '<meta name="viewport" content="width=device-width"></head>'
        '<body><h1>原型</h1><button>下一步</button></body></html>',
        validate_as='prototype',
    )
    output = Path(result['path'])
    assert result['validation']['valid'] is True
    assert output.is_relative_to(tmp_path)
    assert output.name.endswith('prototype.html')


@pytest.mark.parametrize(
    ('stage_id', 'skill_name'),
    [
        ('direction', 'shape-product-direction'),
        ('competitive', 'analyze-competitors'),
        ('design', 'product-design-full-cycle'),
        ('prd', 'write-prd'),
        ('prototype', 'build-product-prototype'),
        ('review', 'review-product-artifact'),
        ('handoff', 'prepare-development-handoff'),
    ],
)
def test_all_source_stage_contracts_are_embedded(tmp_path, stage_id, skill_name):
    tools = _load_contract_tools(tmp_path)

    contract = tools.load_product_skill_contract(stage_id)

    assert contract['stage_id'] == stage_id
    assert contract['skill_name'] == skill_name
    assert contract['contract_sha256']
    assert f'children/{skill_name}/SKILL.md' in contract['contract_text']


@pytest.mark.parametrize(
    ('stage_chain', 'mode', 'skipped'),
    [
        (['design'], 'single', {'direction', 'competitive', 'prd', 'prototype', 'review', 'handoff'}),
        (['competitive', 'design', 'prd'], 'chain', {'direction', 'prototype', 'review', 'handoff'}),
        (
            ['direction', 'competitive', 'design', 'prd', 'prototype', 'review', 'handoff'],
            'full',
            set(),
        ),
    ],
)
def test_execution_plan_supports_single_partial_and_full_modes(
    tmp_path, stage_chain, mode, skipped,
):
    tools = _load_contract_tools(tmp_path)

    result = tools.validate_product_execution_plan({'stage_chain': stage_chain})

    assert result['valid'] is True
    assert result['execution_plan']['execution_mode'] == mode
    assert result['execution_plan']['selected_stage'] == stage_chain[0]
    assert set(result['skip_flags']) == {f'skip_{stage}' for stage in skipped}


def test_execution_plan_rejects_reverse_or_duplicate_chains(tmp_path):
    tools = _load_contract_tools(tmp_path)

    reverse = tools.validate_product_execution_plan({'stage_chain': ['prd', 'design']})
    duplicate = tools.validate_product_execution_plan({'stage_chain': ['design', 'design']})

    assert reverse['valid'] is False
    assert any('canonical forward' in error for error in reverse['errors'])
    assert duplicate['valid'] is False
    assert any('duplicate' in error for error in duplicate['errors'])


def test_preflight_parameters_normalize_into_deterministic_skip_gates(tmp_path):
    tools = _load_contract_tools(tmp_path)

    result = tools.normalize_product_parameters(
        '为研发团队设计需求评审助手',
        '竞品与生态位 → 产品方案 → PRD',
        '完整执行',
        '800 字',
        '使用默认结构',
    )

    assert result['execution_plan']['stage_chain'] == ['competitive', 'design', 'prd']
    assert result['execution_plan']['word_target'] == 800
    assert result['execution_plan']['reference_sample_status'] == 'none-confirmed'
    assert set(result['skip_flags']) == {
        'skip_direction', 'skip_prototype', 'skip_review', 'skip_handoff',
    }


def test_preflight_accepts_user_facing_option_labels(tmp_path):
    tools = _load_contract_tools(tmp_path)

    result = tools.normalize_product_parameters(
        '写产品方案', 'design', '轻量模式', '800 字', '不使用参考样例',
    )

    assert result['execution_plan']['execution_depth'] == 'light'
    assert result['execution_plan']['reference_sample_status'] == 'none-confirmed'


def test_preflight_rejects_missing_conditional_text_answers(tmp_path):
    tools = _load_contract_tools(tmp_path)

    with pytest.raises(ValueError, match='word_target'):
        tools.normalize_product_parameters(
            '写产品方案', 'design', 'full', '', 'none-confirmed',
        )
    with pytest.raises(ValueError, match='reference_sample_choice'):
        tools.normalize_product_parameters(
            '写产品方案', 'design', 'full', '800', '',
        )


def test_preflight_does_not_reject_large_requested_documents(tmp_path):
    tools = _load_contract_tools(tmp_path)

    result = tools.normalize_product_parameters(
        '编写完整 PRD', 'prd', 'full', '50000', 'none-confirmed',
    )

    assert result['execution_plan']['word_target'] == 50000
