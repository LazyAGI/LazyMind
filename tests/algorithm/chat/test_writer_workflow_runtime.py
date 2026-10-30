from __future__ import annotations

import ast
import importlib.util
import json
import re
from pathlib import Path
from types import ModuleType, SimpleNamespace

import pytest
import yaml

from lazymind.document_tools import execution, writing


_ROOT = Path(__file__).resolve().parents[3]
_SCRIPTS_PATH = _ROOT / "workflows" / "writer-workflow" / "scripts"
_TOOLS_PATH = _SCRIPTS_PATH / "tools.py"
_EXECUTION_PATH = _ROOT / "algorithm" / "lazymind" / "document_tools" / "execution.py"
_WORKFLOW_PATH = _SCRIPTS_PATH.parent / "workflow.yaml"


def _load_tools() -> ModuleType:
    spec = importlib.util.spec_from_file_location(
        "writer_workflow_tools_runtime_test", _TOOLS_PATH
    )
    assert spec is not None and spec.loader is not None
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def test_yaml_tools_keep_only_private_orchestration_and_shared_execution_adapters():
    tools_tree = ast.parse(_TOOLS_PATH.read_text(encoding="utf-8"))
    execution_tree = ast.parse(_EXECUTION_PATH.read_text(encoding="utf-8"))
    adapters = [
        node
        for node in tools_tree.body
        if isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef))
        and node.name.startswith("writer_")
    ]
    tool_functions = {
        node.name: node
        for node in tools_tree.body
        if isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef))
    }

    assert not any(
        isinstance(node, (ast.Import, ast.ImportFrom))
        and any(
            (alias.name if isinstance(node, ast.Import) else node.module or "").startswith(
                "lazyllm"
            )
            for alias in node.names
        )
        for node in tools_tree.body
    )
    workflow_imports = {
        node.module or ""
        for node in tools_tree.body
        if isinstance(node, ast.ImportFrom)
        and (node.module or "").startswith(("lazyllm", "lazymind"))
    }
    assert workflow_imports <= {
        "lazymind.chat.engine.subagent.context",
        "lazymind.document_tools",
        "lazymind.document_tools.artifacts",
        "lazymind.document_tools.resources",
        "lazymind.document_tools.revision",
        "lazymind.document_tools.writing",
    }
    assert not any(
        isinstance(node, ast.ImportFrom)
        and (node.module or "").startswith(
            ("lazymind.chat", "workflows", "writer_workflow")
        )
        for node in execution_tree.body
    )
    locally_owned = {
        "writer_classify_structure",
        "writer_resolve_command",
        "writer_prepare_workspace",
        "writer_outline_workspace",
        "writer_draft_workspace",
        "writer_flat_draft_workspace",
    }
    for adapter in adapters:
        if adapter.name in locally_owned:
            continue
        statements = adapter.body[1:] if ast.get_docstring(adapter) else adapter.body
        assert len(statements) == 1 and isinstance(statements[0], ast.Return)
        calls = [node for node in ast.walk(adapter) if isinstance(node, ast.Call)]
        assert len(calls) == 3  # invoke(...) plus globals() and locals()
        invoke_call = next(call for call in calls if isinstance(call.func, ast.Attribute))
        assert isinstance(invoke_call.func.value, ast.Name)
        assert invoke_call.func.value.id == "_DOCUMENT_EXECUTION"
        assert invoke_call.func.attr == "invoke"

    workflow_text = _WORKFLOW_PATH.read_text(encoding="utf-8")
    workflow = yaml.safe_load(workflow_text)
    declared_tools = set(
        re.findall(
            r"\bwriter_[a-z_]+\b",
            workflow_text.split("tool_scripts:", 1)[1].split("artifact_actions:", 1)[0],
        )
    )
    assert declared_tools <= {adapter.name for adapter in adapters}
    assert workflow["artifact_actions"] == {
        "rewrite_selection": {
            "slots": ["outline_document", "flat_draft_document", "draft_document"],
            "preview_tool": "builtin:document.rewrite_selection.v1",
            "execute_tool": "builtin:document.rewrite_selection.v1",
        },
        "sync_document": {
            "slots": ["draft_document"],
            "execute_tool": "builtin:document.sync_document.v1",
        },
        "convert_document": {
            "slots": ["source_document", "outline_document", "flat_draft_document", "draft_document"],
            "preview_tool": "builtin:document.convert_document.v1",
            "execute_tool": "builtin:document.convert_document.v1",
        },
        "write_document": {
            "slots": ["flat_draft_document", "draft_document"],
            "execute_tool": "builtin:document.write_document.v1",
        },
        "render_document": {
            "slots": ["source_document", "outline_document", "flat_draft_document", "draft_document"],
            "preview_tool": "builtin:document.render_document.v1",
            "execute_tool": "builtin:document.render_document.v1",
        },
        "save_document": {
            "slots": ["outline_document", "flat_draft_document", "draft_document"],
            "execute_tool": "builtin:document.save_document.v1",
        },
    }

    assert not (_SCRIPTS_PATH / "runtime.py").exists()
    assert not (_SCRIPTS_PATH / "orchestration.py").exists()
    assert not (_SCRIPTS_PATH / "state.py").exists()


def test_authoritative_step_inputs_reject_unbound_agent_paths(tmp_path):
    runtime = _load_tools()
    context = SimpleNamespace(
        params={"step_id": "write_document", "remote_inputs": {}},
        workspace_path=str(tmp_path),
    )

    assert runtime._state_authoritative_input_path(context, "optional", "/guessed") == ""
    with pytest.raises(ValueError, match="required is missing"):
        runtime._state_authoritative_input_path(
            context,
            "required",
            "/guessed",
            require_workflow_binding=True,
        )


@pytest.mark.parametrize("step", ["outline", "draft", "flat_draft"])
@pytest.mark.parametrize("control", ["继续", "审批通过，继续执行", "重试"])
def test_writer_continuation_keeps_launch_request_identity(monkeypatch, step, control):
    runtime = _load_tools()
    request = "写一篇解释天空为什么是蓝色的科普文章"
    context = SimpleNamespace(params={
        "launch_user_input": request, "user_input": control,
        "current_user_input": control,
    })
    monkeypatch.setattr(runtime, "require_context", lambda: context)
    monkeypatch.setattr(runtime, "_emit_writer_progress", lambda *args, **kwargs: None)
    monkeypatch.setattr(runtime, "_authoritative_writer_input_path", lambda slot, **kwargs: slot)
    monkeypatch.setattr(runtime, "_read_json_file", lambda path: {})
    command = runtime.WriterCommand(
        action="create", source_role="none", target_stage="document",
        next_step="outline", structure_mode="flat" if step == "flat_draft" else "sectioned",
        user_instruction=request, request_fingerprint=runtime._writer_request_fingerprint(request),
    )
    monkeypatch.setattr(runtime, "_load_writer_command", lambda path: command)

    class ValidatedRequest(Exception):
        pass

    def validated(*args, **kwargs):
        assert request in args
        assert control not in args
        raise ValidatedRequest

    fingerprint = "_outline_workspace_fingerprint" if step == "outline" else "_draft_workspace_fingerprint"
    monkeypatch.setattr(runtime, fingerprint, validated)
    execute = getattr(runtime, f"writer_{step}_workspace")
    with pytest.raises(ValidatedRequest):
        execute()

    # A genuinely different workflow request must still fail provenance checks.
    context.params["launch_user_input"] = "写一篇完全不同的文章"
    with pytest.raises(ValueError, match="belongs to a different user request"):
        execute()


def test_writer_direct_request_still_uses_authoritative_input():
    runtime = _load_tools()
    assert runtime._state_authoritative_user_input(
        SimpleNamespace(params={"user_input": "原始请求"}), "模型改写的请求"
    ) == "原始请求"


def test_workspace_checkpoint_round_trip_and_stale_fingerprint(tmp_path):
    runtime = _load_tools()
    context = SimpleNamespace(params={}, workspace_path=str(tmp_path))
    fingerprint = runtime._state_workspace_fingerprint(operation="generate", source="a")
    state, path = runtime._state_load_workspace_state(context, "draft", fingerprint)
    assert path is not None
    assert state == {
        "schema_version": 1,
        "fingerprint": fingerprint,
        "result": {},
        "completed": False,
    }

    state["result"] = {"draft_document": "/tmp/draft.lmd"}
    runtime._state_persist_workspace_state(state, path, completed=True)
    restored, restored_path = runtime._state_load_workspace_state(
        context, "draft", fingerprint
    )
    assert restored_path == path
    assert restored["completed"] is True
    assert restored["result"] == {"draft_document": "/tmp/draft.lmd"}
    assert not list(path.parent.glob(f".{path.name}.*.tmp"))

    path.write_text(
        json.dumps({"fingerprint": "stale", "result": {"wrong": True}}),
        encoding="utf-8",
    )
    fresh, _ = runtime._state_load_workspace_state(context, "draft", fingerprint)
    assert fresh["fingerprint"] == fingerprint
    assert fresh["result"] == {}
    assert fresh["completed"] is False


def test_corrupt_checkpoint_fails_closed_to_fresh_state(tmp_path):
    runtime = _load_tools()
    context = SimpleNamespace(params={}, workspace_path=str(tmp_path))
    fingerprint = runtime._state_request_fingerprint(" write   this ")
    _, path = runtime._state_load_workspace_state(context, "outline", fingerprint)
    assert path is not None
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text("{broken", encoding="utf-8")

    restored, _ = runtime._state_load_workspace_state(context, "outline", fingerprint)
    assert restored["fingerprint"] == fingerprint
    assert restored["result"] == {}
    assert restored["completed"] is False


@pytest.mark.parametrize('operation', ['create', 'update'])
@pytest.mark.parametrize('fail', [False, True])
def test_inline_context_cleanup_preserves_facts_and_other_artifacts(monkeypatch, tmp_path, operation, fail):
    root = tmp_path / 'call'
    root.mkdir()
    checkpoint = tmp_path / 'checkpoint.json'
    checkpoint.write_text('{"version": 1}')
    monkeypatch.setattr(writing, '_temp_root', lambda: root)
    toolkit = writing.WriterWritingCapabilities()
    context = {'context_id': 'ctx', 'facts': [
        {'fact_id': 'f', 'key': 'evidence', 'value': 'Supported fact', 'source': ['reference'], 'locked': True}]}
    if fail:
        def cannot_read(result):
            raise OSError('handoff failed')
        monkeypatch.setattr(writing, '_primary_data', cannot_read)

    def run():
        if operation == 'create':
            return toolkit.create_writing_context(
                json.dumps({'query': 'Write', 'task_type': 'write'}),
                json.dumps([{'resource_id': 'evidence', 'resource_role': 'background',
                             'key_facts': ['Supported fact']}]),
            )
        return toolkit.update_writing_context('# Draft\n\nEdited text.', json.dumps(context))

    if fail:
        with pytest.raises(OSError, match='handoff failed'):
            run()
        assert root.exists() and list(root.iterdir())
    else:
        result = json.loads(run())
        assert not root.exists()
        if operation == 'create':
            assert result['facts'][0]['source'] == ['evidence']
        else:
            assert result['facts'][0]['value'] == 'Supported fact'
            assert result['facts'][0]['source'] == ['reference']
            assert result['facts'][0]['locked'] is True
            assert 'Edited text.' in result['document_summary']['summary']
    assert checkpoint.read_text() == '{"version": 1}'


@pytest.mark.parametrize('fail', [False, True])
def test_whole_document_source_released_only_after_save(monkeypatch, tmp_path, fail):
    source = tmp_path / 'whole-document-source'
    target = tmp_path / 'published.md'
    snapshot = tmp_path / 'input.json'
    snapshot.write_text('{}')
    task = {'task_type': 'write', 'constraints': {'target_chars': 1500},
            'output': {'representation': 'markdown'}}
    monkeypatch.setattr(execution, '_read_json_file', lambda _: task)
    monkeypatch.setattr(execution, '_run_root', lambda name: tmp_path / name)
    monkeypatch.setattr(execution, 'require_context', lambda: SimpleNamespace(emit=lambda _: None))
    def generate(*args, **kwargs):
        source.mkdir()
        (source / 'draft.md').write_text('# Title\nBody')
        return '# Title\nBody'
    def save(*args, **kwargs):
        if fail:
            raise OSError('save failed')
        target.write_text(args[1])
        return str(target)
    monkeypatch.setattr(execution, 'stream_whole_document', generate)
    monkeypatch.setattr(execution, '_save_writer_document', save)
    def run():
        return execution.invoke({}, '_writer_generate_draft_document', {
            'writing_task_path': 'task', 'section_instructions_path': 'instructions',
            'writing_context_path': 'context'})
    if fail:
        with pytest.raises(OSError, match='save failed'):
            run()
        assert (source / 'draft.md').exists()
    else:
        assert run()['draft_document'] == str(target)
        assert target.read_text() == '# Title\nBody'
        assert not source.exists()
    assert snapshot.exists()


def test_no_image_collection_keeps_required_artifact_only(monkeypatch, tmp_path):
    saved = []
    monkeypatch.setattr(execution, 'require_context', lambda: SimpleNamespace(params={}))
    monkeypatch.setattr(execution, '_read_json_file', lambda _: {
        'constraints': {'visual_policy': {'allow_visuals': False}}})
    monkeypatch.setattr(execution, '_run_root', lambda _: tmp_path)
    def save(name, content, schema, **kwargs):
        path = tmp_path / (name + '.json')
        path.write_text(content)
        saved.append(name)
        return str(path)
    monkeypatch.setattr(execution, '_save_json_artifact', save)
    result = execution._writer_collect_available_media('task')
    from lazyllm.tools.writer.data_models.multimodal import MediaAssetLibrary
    library = MediaAssetLibrary.model_validate_json((tmp_path / 'media_assets.json').read_text())
    assert library.library_id and library.assets == {}
    assert result['media_assets'] == str(tmp_path / 'media_assets.json')
    assert result['profile_input_resources'] == ''
    assert saved == ['media_assets']


@pytest.mark.parametrize('has_visuals', [False, True])
@pytest.mark.parametrize('representation', ['markdown', 'ir'])
def test_section_plan_preserves_required_ir_visual_artifact(monkeypatch, has_visuals, representation):
    saved = []
    payload = {'section_instructions': {'instructions': []},
               'visual_plan': {'instructions': [{'need_id': 'image'}] if has_visuals else []}}
    monkeypatch.setattr(execution, 'WriterCreateToolkit', lambda: SimpleNamespace(
        generate_section_instructions=lambda **kw: json.dumps(payload)))
    monkeypatch.setattr(execution, '_read_json_string',
                        lambda _: json.dumps({'output': {'representation': representation}}))
    monkeypatch.setattr(execution, '_save_json_artifact',
                        lambda name, *a, **kw: saved.append(name) or name + '.json')
    result = execution._writer_generate_section_instructions('task', 'outline', 'context')
    assert result['visual_need_count'] == int(has_visuals)
    keep_plan = has_visuals or representation == 'ir'
    assert result['visual_plan'] == ('visual_plan.json' if keep_plan else '')
    assert saved == ['section_instructions'] + (['visual_plan'] if keep_plan else [])


@pytest.mark.parametrize('fail', [False, True])
def test_section_tool_releases_only_its_own_successful_directory(monkeypatch, tmp_path, fail):
    root = tmp_path / 'temporary'
    root.mkdir()
    snapshot = tmp_path / 'checkpoint.json'
    snapshot.write_text('{}')
    def plan(**kw):
        (root / 'section_instructions.json').write_text('{}')
        if fail:
            raise ValueError('planning failed')
        return {'instructions': []}
    monkeypatch.setattr(writing, '_temp_root', lambda: root)
    monkeypatch.setattr(writing, 'AutoModel', lambda **kw: None)
    monkeypatch.setattr(writing, 'WriterPlanningTools', lambda **kw: SimpleNamespace(
        generate_section_instructions=plan))
    monkeypatch.setattr(writing, '_primary_data', lambda x: x)
    task = json.dumps({'constraints': {'visual_policy': {'allow_visuals': False}}})
    def run():
        return writing.WriterWritingCapabilities().generate_section_instructions(task, '# Title', '{}')
    if fail:
        with pytest.raises(ValueError, match='planning failed'):
            run()
        assert root.exists()
    else:
        assert json.loads(run())['section_instructions'] == {'instructions': []}
        assert not root.exists()
    assert snapshot.exists()


@pytest.mark.parametrize('policy', [
    {'allow_visuals': True},
    {'allow_visuals': False, 'require_input_image_reuse': True},
])
def test_ir_visual_planning_retains_requested_images(monkeypatch, tmp_path, policy):
    from lazyllm.tools.writer.data_models import WriterBlock, WriterDocument
    from lazyllm.tools.writer.data_models.multimodal import VisualPlan
    from lazyllm.tools.writer.tools.planning_tools import WriterPlanningTools

    calls = []
    monkeypatch.setattr(writing, '_temp_root', lambda: tmp_path / 'planning')
    monkeypatch.setattr(writing, 'AutoModel', lambda **kw: None)

    def model(self, prompt, schema, **kwargs):
        calls.append(schema)
        return VisualPlan()

    monkeypatch.setattr(WriterPlanningTools, '_call_llm_structured', model)
    outline = WriterDocument(document_id='outline', stage='outline', title='Report', blocks=[
        WriterBlock(node_id='section', type='heading', content='Overview', level=1),
    ])

    def run():
        return writing.WriterWritingCapabilities().generate_section_instructions(
            json.dumps({'query': 'Write a report', 'task_type': 'write', 'constraints': {'visual_policy': policy},
                        'output': {'representation': 'ir'}}),
            outline.model_dump_json(), json.dumps({'context_id': 'context'}),
        )

    if policy.get('require_input_image_reuse'):
        with pytest.raises(ValueError, match='Required input image reuse'):
            run()
    else:
        assert json.loads(run())['visual_plan'].get('instructions', []) == []
    assert calls == [VisualPlan]


@pytest.mark.parametrize('policy,query', [
    ({'allow_visuals': False}, 'Write a report'),
    ({}, '写一篇纯文字报告，不要图片'),
])
def test_ir_no_image_plan_resolves_and_generates_editable_draft(monkeypatch, tmp_path, policy, query):
    import tempfile
    from pathlib import Path

    from lazyllm.tools.writer.data_models import WriterBlock, WriterDocument
    from lazyllm.tools.writer.tools.base import WriterToolBase
    from lazyllm.tools.writer.utils import save_artifact_json
    from lazymind import model_config

    monkeypatch.setattr(writing, 'AutoModel', lambda **kw: None)
    monkeypatch.setattr(writing, '_temp_root', lambda: Path(tempfile.mkdtemp(dir=tmp_path)))
    monkeypatch.setattr(execution, '_workspace_root', lambda: tmp_path / 'artifacts')
    monkeypatch.setattr(execution, '_run_root', lambda name: tmp_path / name)
    monkeypatch.setattr(execution, 'require_context', lambda: SimpleNamespace(emit=lambda _: None))
    monkeypatch.setattr(model_config, 'is_model_role_available', lambda _: False)
    calls = []

    def unexpected_model(*args, **kwargs):
        pytest.fail('No-image planning and media resolution must not call a model')

    def draft_model(self, prompt, **kwargs):
        calls.append(prompt)
        body = '# Report\n\n## Overview\n\nA complete report body.\n'
        kwargs['stream_output']['_stream_sink']({'tag': 'text', 'delta': body})
        return body

    monkeypatch.setattr(WriterToolBase, '_call_llm_structured', unexpected_model)
    monkeypatch.setattr(WriterToolBase, '_call_llm_text', draft_model)
    outline = WriterDocument(document_id='outline', stage='outline', title='Report', blocks=[
        WriterBlock(node_id='section', type='heading', content='Overview', level=1,
                    outline_description='Explain the findings'),
    ])
    inputs = {
        'task': {'task_id': 'task', 'task_type': 'write', 'query': query,
                 'constraints': {'target_chars': 1000, 'visual_policy': policy},
                 'output': {'representation': 'ir'}},
        'outline': outline.model_dump(),
        'context': {'context_id': 'context'},
        'media': {'library_id': 'media', 'assets': {}},
    }
    paths = {}
    schemas = {'task': 'task.WritingTask', 'outline': 'writer_ir.WriterDocument',
               'context': 'context.WritingContext', 'media': 'multimodal.MediaAssetLibrary'}
    for name, value in inputs.items():
        path = tmp_path / f'{name}.json'
        save_artifact_json(value, str(path), schema_name=execution.writer_schema(schemas[name]))
        paths[name] = str(path)

    planned = execution.invoke({}, '_writer_generate_section_instructions', {
        'writing_task_path': paths['task'], 'outline_path': paths['outline'],
        'writing_context_path': paths['context'],
    })
    assert planned['section_count'] == 1
    assert planned['visual_need_count'] == 0
    assert execution._read_json_file(planned['visual_plan'])['instructions'] == []
    media = execution._writer_resolve_visual_media(planned['visual_plan'], paths['media'])
    assert execution._read_json_file(media['resolved_media_assets'])['assets'] == {}
    assert not media['warnings']
    assert calls == []

    result = execution.invoke({}, '_writer_generate_draft_document', {
        'writing_task_path': paths['task'], 'section_instructions_path': planned['section_instructions'],
        'writing_context_path': paths['context'], 'outline_path': paths['outline'],
        'visual_plan_path': planned['visual_plan'], 'resolved_media_assets_path': media['resolved_media_assets'],
    })
    document = WriterDocument.model_validate(execution._read_json_file(result['draft_document']))
    assert document.stage == 'draft' and document.ui_editable
    assert any('A complete report body.' in block.content for block in document.iter_blocks())
    assert len(calls) == 1


@pytest.mark.parametrize('operation', [
    'validate_patch_set', 'locate_revision_target', 'generate_modify_plan',
    'generate_patch_set', 'generate_string_replace_set', 'apply_patch', 'apply_string_replace',
])
@pytest.mark.parametrize('fail_handoff', [False, True])
def test_revision_cleanup_after_result_extraction(monkeypatch, tmp_path, operation, fail_handoff):
    from lazymind.document_tools import revision
    from lazyllm.tools.writer.tools.base import WriterToolBase

    root = tmp_path / 'call'
    root.mkdir()
    source = tmp_path / 'source.md'
    media = tmp_path / 'reference.png'
    media.write_bytes(b'preserved media')
    markdown = f'# Old title\n\nKeep evidence.\n\n![Reference]({media})\n'
    source.write_text(markdown)
    checkpoint = tmp_path / 'checkpoint.json'
    checkpoint.write_text(json.dumps({'source_document': str(source)}))
    monkeypatch.setattr(revision, '_temp_root', lambda: root)
    monkeypatch.setattr(revision, 'AutoModel', lambda **kwargs: None)

    def model(self, prompt, schema, **kwargs):
        values = {
            'AuditResult': {'is_passed': True, 'score': 100, 'summary': 'Valid revision'},
            'LocateResult': {'target_title': True},
            'GeneratedRevision': {'new_title': 'New title'},
            'StringReplaceSet': {'replacements': [{'old_string': '# Old title', 'new_string': '# New title'}]},
        }
        return schema.model_validate(values[schema.__name__])

    monkeypatch.setattr(WriterToolBase, '_call_llm_structured', model)
    if fail_handoff:
        def unreadable_result(result):
            raise OSError('result extraction failed')
        monkeypatch.setattr(revision, '_primary_data', unreadable_result)

    document = {'document_id': 'doc', 'title': 'Old title', 'stage': 'draft',
                'blocks': [{'node_id': 'p1', 'type': 'paragraph', 'content': 'Keep evidence.'}]}
    task = json.dumps({'task_id': 'task', 'query': 'Rename the title', 'task_type': 'revise'})
    context = json.dumps({'context_id': 'ctx'})
    plan = json.dumps({'scope': 'document', 'title_instruction': 'Rename the title'})
    patch = json.dumps({'target_doc_id': 'doc', 'new_title': 'New title', 'hunks': []})
    replacements = json.dumps({'replacements': [{'old_string': '# Old title', 'new_string': '# New title'}]})
    args = {
        'validate_patch_set': {'patch_set_json': patch, 'writing_task_json': task},
        'locate_revision_target': {'writing_task_json': task, 'writer_document_json': json.dumps(document)},
        'generate_modify_plan': {'writing_task_json': task, 'writer_document_json': json.dumps(document),
                                 'locate_result_json': json.dumps({
                                     'task_id': 'task', 'doc_id': 'doc', 'target_title': True})},
        'generate_patch_set': {'writer_document_json': json.dumps(document), 'modify_plan_json': plan},
        'generate_string_replace_set': {'markdown_document': markdown, 'modify_plan_json': plan},
        'apply_patch': {'writer_document_json': json.dumps(document), 'patch_set_json': patch},
        'apply_string_replace': {'markdown_document': markdown, 'string_replace_set_json': replacements},
    }

    def run():
        return getattr(revision.WriterRevisionCapabilities(), operation)(
            writing_context_json=context, **args[operation])

    if fail_handoff:
        with pytest.raises(OSError, match='result extraction failed'):
            run()
        assert root.exists() and list(root.iterdir())
    else:
        result = json.loads(run())
        assert not root.exists()
        if operation == 'apply_patch':
            assert result['patch_result']['success']
            assert result['revised_document']['title'] == 'New title'
            assert result['revised_document']['blocks'][0]['content'] == 'Keep evidence.'
        elif operation == 'apply_string_replace':
            assert result['string_replace_result']['success']
            assert result['revised_document'] == markdown.replace('# Old title', '# New title')
        elif operation == 'validate_patch_set':
            assert result['patch_set_review']['is_passed']
        elif operation == 'locate_revision_target':
            assert result['target_title'] and result['doc_id'] == 'doc'
        elif operation == 'generate_modify_plan':
            assert result['title_instruction'] == 'Rename the title'
        elif operation == 'generate_patch_set':
            assert result['new_title'] == 'New title' and result['target_doc_id'] == 'doc'
        else:
            assert result['replacements'][0]['new_string'] == '# New title'
    assert source.read_text() == markdown
    assert media.read_bytes() == b'preserved media'
    assert json.loads(checkpoint.read_text()) == {'source_document': str(source)}
