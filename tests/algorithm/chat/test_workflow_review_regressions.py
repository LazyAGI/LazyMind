"""Behavioral regressions for workflow_dev review; no model or network calls."""
import importlib.util
import json
from pathlib import Path
from types import SimpleNamespace

import lazyllm
import pytest
from PIL import Image
from lazyllm.tools.agent import ToolExecutionError
from lazymind.chat.engine.subagent.context import SubAgentContext, set_context
from lazymind.chat.workflow.image_request import effective_image_request

ROOT = Path(__file__).resolve().parents[3]


def load(package, filename):
    spec = importlib.util.spec_from_file_location('review_' + filename, ROOT / 'workflows' / package / 'scripts' / (filename + '.py'))
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


@pytest.fixture
def image_context(tmp_path):
    previous = lazyllm.globals.get('subagent_ctx')
    config = lazyllm.globals.get('agentic_config')
    events = []
    ctx = SubAgentContext('task', 'conversation', 'workflow_step', 'edit image',
        {'remote_inputs': {}, 'output_slot_types': {'authoritative_image': 'image', 'edit_contract': 'json'}},
        str(tmp_path), ['source_image'], ['authoritative_image', 'edit_contract'],
        SimpleNamespace(next_artifact_seq=lambda *a: 1, load_artifacts=lambda *a, **k: []), events.append)
    set_context(ctx)
    lazyllm.globals['agentic_config'] = {'workflow_session_id': 'session'}
    try:
        yield ctx, events
    finally:
        lazyllm.globals['subagent_ctx'] = previous
        lazyllm.globals['agentic_config'] = config


def test_uploaded_edit_publishes_exact_bound_source_and_contract(image_context, tmp_path):
    module = load('image-workflow-v2', 'tools')
    ctx, events = image_context
    source = tmp_path / 'source.png'
    Image.new('RGB', (800, 600), 'red').save(source)
    ctx.params['remote_inputs']['source_image'] = str(source)
    result = module.publish_edit_contract('帽子变蓝', '帽子', '所有其他内容', '不要改变画幅', source_url='https://untrusted.example/wrong.png')
    assert result['status'] == 'ok'
    assert {event['slot'] for event in events} == {'authoritative_image', 'edit_contract'}
    source_event = next(e for e in events if e['slot'] == 'authoritative_image')
    assert Path(source_event['value']['path']).read_bytes() == source.read_bytes()
    assert next(e for e in events if e['slot'] == 'edit_contract')['value']['data']['requested_edit'] == '帽子变蓝'


def test_invalid_edit_contract_does_not_publish_partial_outputs(image_context):
    module = load('image-workflow-v2', 'tools')
    with pytest.raises(ToolExecutionError):
        module.publish_edit_contract('change hat', '', 'rest', 'no resize')
    assert not image_context[1]


@pytest.mark.parametrize('count', [0, 2])
def test_edit_requires_one_exact_bound_source(image_context, count):
    module = load('image-workflow-v2', 'tools')
    image_context[0].params['remote_inputs']['source_image'] = ['first.png', 'second.png'][:count]
    with pytest.raises(ToolExecutionError, match='exactly one'):
        module.publish_edit_contract('change hat', 'hat', 'rest', 'no resize')
    assert not image_context[1]


def test_product_body_preserves_business_content():
    module = load('product_solution_delivery', 'writer_bridge')
    source = '# 交付\n\n```json\n{"status":"accepted","value":null}\n```\n[链接](https://example.com/accepted)\n当前运行已核验 WEB-001，结论有公开资料支持。\n'
    assert module._present_reader_facing_markdown('handoff', source) == source


def test_flowchart_preserves_both_branch_labels_and_cycles():
    module = load('product_solution_delivery', 'writer_bridge')
    rendered = module._render_flow('flowchart TD\nA{已付款?} -->|是| B[发货]\nA -->|否| C[取消]\nC --> A')
    assert '>是<' in rendered and '>否<' in rendered
    assert rendered.count('class="flow-edge"') == 3
    assert rendered != module._render_flow('flowchart TD\nA --> B')


@pytest.mark.parametrize('source', ['flowchart LR\nsubgraph 支付\nA --> B\nend', 'flowchart TD\nA -.->|否| B', 'sequenceDiagram\nloop retry\nA->>B: 请求\nend'])
def test_unsupported_mermaid_is_visible_without_semantic_loss(source):
    import html
    module = load('product_solution_delivery', 'writer_bridge')
    output = module._render_flow(source)
    assert '<svg' not in output
    assert html.escape(source) in output
    assert 'diagram-source-fallback' in output


@pytest.mark.parametrize('control', ['继续', 'continue', '确认', 'retry', ''])
def test_launch_request_survives_control_messages(control):
    request = '生成16:9山谷海报'
    assert effective_image_request({'launch_user_input': request, 'user_input': control}) == request


def test_meme_update_overrides_launch_count_and_captions():
    module = load('image-workflow-v2', 'tools')
    request = effective_image_request({
        'launch_user_input': '生成4个动态表情，分别是“求求你啦”、“好不好嘛”、“你最好啦”、“拜托拜托”。',
        'current_user_input': '改为2个动态表情，分别是“开心啦”、“惊讶啦”。',
    })
    assert module._explicit_meme_count(request) == 2
    assert module._explicit_meme_captions(request, 2) == ['开心啦', '惊讶啦']


def test_explicit_reference_opt_out_does_not_repeat_launch_search(monkeypatch):
    module = load('image-workflow-v2', 'tools')
    params = {'launch_user_input': '搜索真实照片做海报', 'current_user_input': '不用搜索参考，直接画'}
    monkeypatch.setattr(module, 'image_search_and_validate', lambda *a, **k: pytest.fail('Search was explicitly removed'))
    refs, summary, outcomes = module._ordinary_reference_materials(effective_image_request(params), params)
    assert refs == []
    assert 'explicitly removed' in summary
    assert outcomes == []


def test_execution_limits_remain_opt_in_for_every_workflow():
    from lazymind.chat.workflow.execution_policy import policy_for
    for workflow_id in ['writer-workflow', 'product_solution_delivery', 'renamed-workflow']:
        assert policy_for({'workflow_id': workflow_id, 'step_id': 'write_prd_document'}) is None


def test_explicit_update_is_distinct_and_changes_generation_ratio(monkeypatch):
    from lazymind.chat.engine.subagent import context
    module = load('image-workflow-v2', 'baoyu')
    params = {'launch_user_input': '生成16:9山谷海报', 'user_input': '生成16:9山谷海报', 'current_user_input': '改成9:16'}
    monkeypatch.setattr(context, 'require_context', lambda: SimpleNamespace(params=params))
    captured = {}
    monkeypatch.setattr(module, '_artifact_text', lambda key: 'mountain landscape 16:9')
    monkeypatch.setattr(module, '_artifact_image_refs', lambda *keys: [])
    monkeypatch.setattr(module, 'baoyu_image_generator', lambda prompt, **kwargs: captured.update(kwargs) or {})
    monkeypatch.setattr(module, '_publish_generated_images', lambda *args: {})
    module.generate_ordinary_image()
    assert captured['aspect_ratio'] == '9:16'
    params['current_user_input'] = '继续'
    module.generate_ordinary_image()
    assert captured['aspect_ratio'] == '16:9'


def test_continue_retains_update_saved_in_bound_prompt(monkeypatch):
    module = load('image-workflow-v2', 'baoyu')
    bound_request = effective_image_request({'launch_user_input': '生成16:9海报', 'current_user_input': '改成9:16'})
    monkeypatch.setattr(module, '_artifact_text', lambda key: bound_request)
    monkeypatch.setattr(module, '_immutable_request', lambda: '生成16:9海报')
    monkeypatch.setattr(module, '_artifact_image_refs', lambda *keys: [])
    captured = {}
    monkeypatch.setattr(module, 'baoyu_image_generator', lambda prompt, **kwargs: captured.update(kwargs) or {})
    monkeypatch.setattr(module, '_publish_generated_images', lambda *args: {})
    module.generate_ordinary_image()
    assert captured['aspect_ratio'] == '9:16'


@pytest.mark.parametrize('size', [(4000, 500), (500, 4000), (4000, 100)])
def test_unrepresentable_source_ratio_fails_before_provider(tmp_path, size):
    module = load('image-workflow-v2', 'baoyu')
    source = tmp_path / 'wide.png'
    Image.new('RGB', size).save(source)
    with pytest.raises(ToolExecutionError, match='aspect ratio'):
        module._size_for_source_image(str(source))


@pytest.mark.parametrize('size', [(1600, 900), (900, 1600), (1000, 1000), (3000, 1000)])
def test_supported_source_ratio_is_preserved(tmp_path, size):
    module = load('image-workflow-v2', 'baoyu')
    source = tmp_path / 'source.png'
    Image.new('RGB', size).save(source)
    width, height = map(int, module._size_for_source_image(str(source)).split('x'))
    assert abs((width / height) / (size[0] / size[1]) - 1) < .01
    assert 512 <= width <= 4096 and 512 <= height <= 4096 and width * height > 3_686_400


def test_requested_search_references_are_retrieved_and_bound(monkeypatch):
    from lazymind.chat.engine.subagent import context, tools
    module = load('image-workflow-v2', 'tools')
    monkeypatch.setattr(context, 'require_context', lambda: SimpleNamespace(params={'launch_user_input': '搜索外滩真实照片，生成16:9海报', 'user_input': '继续'}))
    searches, saved = [], []
    monkeypatch.setattr(module, 'image_search_and_validate', lambda request, **kw: searches.append(request) or {'selected': [{'status': 'ok', 'url': 'https://example.com/photo.png?token=exact'}]})
    monkeypatch.setattr(tools, '_save_artifact', lambda key, value, **kw: saved.append((key, value)) or {'status': 'ok'})
    module.prepare_ordinary_request()
    assert searches and searches[0].startswith('搜索外滩')
    assert saved[0] == ('material_images', {'path': 'https://example.com/photo.png?token=exact'})
    assert 'No external facts' not in saved[1][1]


def test_empty_search_is_recorded_without_failing_preparation(monkeypatch):
    from lazymind.chat.engine.subagent import context, tools
    module = load('image-workflow-v2', 'tools')
    monkeypatch.setattr(context, 'require_context', lambda: SimpleNamespace(params={'user_input': '搜索真实参考素材'}))
    monkeypatch.setattr(module, 'image_search_and_validate', lambda *a, **kw: {'selected': []})
    saved = []
    monkeypatch.setattr(tools, '_save_artifact', lambda *a, **kw: saved.append(a))
    result = module.prepare_ordinary_request()
    assert result['status'] == 'ok'
    assert result['retrieval_results'][0]['status'] == 'empty'
    assert 'No verified external reference' in result['ordinary_prompt']
    assert [args[0] for args in saved] == ['material_summary', 'ordinary_prompt']


@pytest.mark.parametrize('kb_result', [RuntimeError('KB unavailable'), {'items': []}, {'ok': False, 'error': 'KB unavailable'}])
def test_kb_failure_does_not_discard_successful_web_references(monkeypatch, kb_result):
    from lazymind.chat.engine.subagent import context, tools
    from lazymind.chat.engine.tools.lazy_kb import KBToolkit
    module = load('image-workflow-v2', 'tools')
    monkeypatch.setattr(context, 'require_context', lambda: SimpleNamespace(params={
        'user_input': '搜索网页和知识库中的海报参考', 'filters': {'kb_id': ['brand']},
    }))
    def search_kb(*args, **kwargs):
        if isinstance(kb_result, Exception):
            raise kb_result
        return kb_result
    monkeypatch.setattr(KBToolkit, 'kb_search', search_kb)
    monkeypatch.setattr(module, 'image_search_and_validate', lambda *a, **kw: {
        'selected': [{'status': 'ok', 'url': 'https://example.com/reference.png'}],
    })
    saved = []
    monkeypatch.setattr(tools, '_save_artifact', lambda key, value, **kw: saved.append((key, value)) or {})
    result = module.prepare_ordinary_request()
    assert result['status'] == 'ok'
    assert result['retrieval_results'][0]['status'] in {'empty', 'error'}
    assert result['retrieval_results'][1] == {'source': 'web', 'status': 'ok'}
    assert saved[0] == ('material_images', {'path': 'https://example.com/reference.png'})
    assert 'https://example.com/reference.png' in result['ordinary_prompt']
    assert 'No verified external reference' not in result['ordinary_prompt']


@pytest.mark.parametrize('kb_available', [True, False])
def test_web_failure_preserves_kb_evidence_or_reports_both_sources(monkeypatch, kb_available):
    from lazymind.chat.engine.subagent import context, tools
    from lazymind.chat.engine.tools.lazy_kb import KBToolkit
    module = load('image-workflow-v2', 'tools')
    monkeypatch.setattr(context, 'require_context', lambda: SimpleNamespace(params={'user_input': '搜索网页和知识库中的海报参考'}))
    monkeypatch.setattr(KBToolkit, 'kb_search', lambda *a, **kw: {
        'items': [{'text': '品牌标准色为蓝色'}] if kb_available else [],
    })
    def search_web(*args, **kwargs):
        raise RuntimeError('Web search unavailable')
    monkeypatch.setattr(module, 'image_search_and_validate', search_web)
    saved = []
    monkeypatch.setattr(tools, '_save_artifact', lambda key, value, **kw: saved.append((key, value)) or {})
    result = module.prepare_ordinary_request()
    assert result['status'] == 'ok'
    assert result['retrieval_results'][1]['status'] == 'error'
    assert 'Web search unavailable' in result['ordinary_prompt']
    if kb_available:
        assert '品牌标准色为蓝色' in result['ordinary_prompt']
    else:
        assert 'No verified external reference' in result['ordinary_prompt']
    assert [key for key, _ in saved] == ['material_summary', 'ordinary_prompt']


def test_artifact_publication_failure_is_not_treated_as_retrieval_warning(monkeypatch):
    from lazymind.chat.engine.subagent import context, tools
    module = load('image-workflow-v2', 'tools')
    monkeypatch.setattr(context, 'require_context', lambda: SimpleNamespace(params={'user_input': '画一张山谷插画'}))
    def fail_publication(*args, **kwargs):
        raise RuntimeError('Artifact storage failed')
    monkeypatch.setattr(tools, '_save_artifact', fail_publication)
    with pytest.raises(RuntimeError, match='Artifact storage failed'):
        module.prepare_ordinary_request()


def test_knowledge_base_evidence_reaches_the_image_prompt(monkeypatch):
    from lazymind.chat.engine.subagent import context, tools
    from lazymind.chat.engine.tools.lazy_kb import KBToolkit
    module = load('image-workflow-v2', 'tools')
    monkeypatch.setattr(context, 'require_context', lambda: SimpleNamespace(params={'user_input': '按知识库品牌风格做海报', 'filters': {'kb_id': ['brand']}}))
    searches, saved = [], []
    monkeypatch.setattr(KBToolkit, 'kb_search', lambda self, **kw: searches.append(kw) or {'items': [{'text': '品牌标准色为蓝色', 'kb_id': 'brand'}]})
    monkeypatch.setattr(tools, '_save_artifact', lambda key, value, **kw: saved.append((key, value)) or {})
    module.prepare_ordinary_request()
    assert searches[0]['filters'] == {'kb_id': ['brand']}
    assert '品牌标准色为蓝色' in saved[-1][1]


def test_v2_loads_without_another_workflow_source_tree(monkeypatch, tmp_path):
    from lazymind.workflow_toolkit import load_workflow_package_tools
    import base64
    import yaml
    files = {name: base64.b64encode((ROOT / 'workflows/image-workflow-v2' / name).read_bytes()).decode() for name in ['workflow.yaml', 'scripts/tools.py', 'scripts/baoyu.py']}
    monkeypatch.chdir(tmp_path)
    functions = [function for script in yaml.safe_load((ROOT / 'workflows/image-workflow-v2/workflow.yaml').read_text(encoding='utf-8'))['tool_scripts'] for function in script['functions']]
    loaded = load_workflow_package_tools({'files': files}, functions, 'image-workflow-v2', 'immutable-test')
    assert set(loaded) == set(functions)
    # Execute the shared helper too: loading alone would not catch a lazy V1 import.
    assert loaded['validate_image_ref'](str(tmp_path / 'missing.png')).startswith('status: invalid')
