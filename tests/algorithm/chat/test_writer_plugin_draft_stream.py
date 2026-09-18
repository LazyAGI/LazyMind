from __future__ import annotations

import importlib.util
import json
import sys
from pathlib import Path
from types import ModuleType, SimpleNamespace

import pytest


_ROOT = Path(__file__).resolve().parents[3]
_TOOLS_PATH = _ROOT / 'workflows' / 'writer-workflow' / 'scripts' / 'tools.py'


def _load_tools_module() -> ModuleType:
    module_name = 'writer_workflow_tools_draft_stream_test'
    sys.modules.pop(module_name, None)
    spec = importlib.util.spec_from_file_location(module_name, _TOOLS_PATH)
    assert spec is not None and spec.loader is not None
    module = importlib.util.module_from_spec(spec)
    sys.modules[module_name] = module
    spec.loader.exec_module(module)
    return module


@pytest.mark.parametrize(
    ('query', 'expected'),
    [
        ('写一篇 800 字左右的小说', {'target_chars': 800, 'max_chars': 880}),
        ('写一篇不超过500字的摘要', {'target_chars': 500, 'max_chars': 500}),
        ('写一篇短文', {}),
    ],
)
def test_build_writing_task_extracts_document_length_constraints(query, expected):
    from lazymind.document_tools.writing import parse_writer_request_constraints

    assert parse_writer_request_constraints(query) == expected


def test_workflow_build_task_is_a_thin_shared_execution_call(monkeypatch, tmp_path):
    from lazymind.document_tools import execution as document_execution

    tools = _load_tools_module()
    runtime = tools
    context = SimpleNamespace(
        workspace_path=str(tmp_path),
        params={'session_id': 'session-1'},
    )

    class FakeWriterCreateToolkit:
        def build_writing_task(self, query, task_id):
            return json.dumps({'query': query, 'task_id': task_id})

    monkeypatch.setattr(runtime, 'require_context', lambda: context)
    monkeypatch.setattr(
        document_execution, '_WriterCreateToolkit', FakeWriterCreateToolkit,
    )

    path = tools.writer_build_writing_task('写一篇 800 字左右的小说')
    task = runtime._read_json_file(path)

    assert task['task_id'] == 'session-1'
    assert task['constraints'] == {'target_chars': 800, 'max_chars': 880}
    assert task['output']['representation'] == 'markdown'


def test_writer_retrieve_uses_configured_search_provider(monkeypatch):
    from lazymind.document_tools import writing as writer

    class FakeSciverseSearch:
        def __key_source__(self):
            return True

        def search(self, query):
            return [{'title': query}]

    monkeypatch.setattr(writer, '_writer_selected_kb_ids', lambda: [])
    monkeypatch.setattr(writer, 'SciverseSearch', FakeSciverseSearch)

    tool_name, result = writer._writer_retrieve('evidence')

    assert tool_name == 'sciverse_search'
    assert result == [{'title': 'evidence'}]


def test_shared_media_collection_applies_input_reuse_policy(monkeypatch, tmp_path):
    from lazymind import model_config
    from lazymind.document_tools import writing as writer

    captured = {}

    class FakeWritingCapabilities:
        def build_resources(self, **_kwargs):
            return json.dumps([{'resource_id': 'upload-1', 'meta': {}}])

        def collect_available_media(self, **kwargs):
            captured.update(kwargs)
            return json.dumps({
                'media_assets': {'library_id': 'media-1', 'assets': {}},
                'profile_input_resources': json.loads(kwargs['input_resources_json']),
                'warnings': [],
            })

    monkeypatch.setattr(writer, 'WriterWritingCapabilities', FakeWritingCapabilities)
    monkeypatch.setattr(model_config, 'is_model_role_available', lambda _role: False)

    result = writer.collect_document_media(
        {'task_id': 'task-1', 'constraints': {
            'visual_policy': {'require_input_image_reuse': True},
        }},
        file_paths=['/tmp/reference.png'],
        media_store=str(tmp_path),
    )

    assert result['media_assets']['library_id'] == 'media-1'
    resources = json.loads(captured['input_resources_json'])
    assert resources[0]['meta']['origin'] == 'user_upload'
    assert captured['use_vision_model'] is False


def test_shared_visual_resolution_preserves_non_strict_failure(monkeypatch, tmp_path):
    from lazymind import model_config
    from lazymind.document_tools import writing as writer

    class FakeWritingCapabilities:
        def resolve_visual_needs(self, **_kwargs):
            raise RuntimeError('resolver unavailable')

    monkeypatch.setattr(writer, 'WriterWritingCapabilities', FakeWritingCapabilities)
    monkeypatch.setattr(model_config, 'is_model_role_available', lambda _role: False)
    media_assets = {'library_id': 'media-1', 'assets': {}}

    result = writer.resolve_visual_media(
        {'instructions': []},
        media_assets,
        media_store=str(tmp_path),
    )

    assert result['media_assets'] == media_assets
    assert result['warnings'] == [
        'Visual media resolution failed: RuntimeError: resolver unavailable',
    ]


@pytest.mark.parametrize(
    ('query', 'suggested_operation', 'expected_operation'),
    [
        (
            'AI Writer 根据上传材料创作一篇约 2000 字的原创克苏鲁小说。'
            '先生成大纲，并在相关章节的大纲指令中添加材料分析子任务：'
            '提炼材料中可借鉴的叙事结构、氛围营造与恐惧递进手法；'
            '完成子任务后再写成稿。',
            'revise_document',
            'create',
        ),
        ('修改上传的文章，让表达更简洁', 'create', 'revise_document'),
        ('重写上传的整篇文章', 'create', 'rewrite_document'),
    ],
)
def test_prepare_control_distinguishes_reference_from_edit_source(
    query,
    suggested_operation,
    expected_operation,
):
    from lazymind.document_tools.writing import resolve_prepare_control

    operation, target_stage = resolve_prepare_control(
        query,
        suggested_operation,
        has_document_source=True,
    )

    assert (operation, target_stage) == (expected_operation, 'document')


def test_write_document_revision_emits_markdown_draft_stream(monkeypatch, tmp_path):
    from lazymind.document_tools import revision as document_revision

    tools = _load_tools_module()
    runtime = tools
    events: list[dict] = []
    context = SimpleNamespace(
        workspace_path=str(tmp_path),
        params={'step_id': 'write_document'},
        emit=events.append,
    )

    class FakeWriterRevisionToolkit:
        def apply_string_replace(self, **_kwargs) -> str:
            return json.dumps({
                'string_replace_result': {'replaced': 1},
                'revised_document': '# Revised title\n\nUpdated body.\n',
            })

    monkeypatch.setattr(runtime, 'require_context', lambda: context)
    monkeypatch.setattr(
        document_revision, 'WriterRevisionCapabilities', FakeWriterRevisionToolkit,
    )
    base_document_path = tmp_path / 'draft.md'
    base_document_path.write_text('# Original\n', encoding='utf-8')
    writing_context_path = tmp_path / 'context.json'
    writing_context_path.write_text('{}', encoding='utf-8')
    revision_set_path = tmp_path / 'revisions.json'
    revision_set_path.write_text('{}', encoding='utf-8')

    result = tools.writer_apply_revision(
        str(base_document_path),
        str(writing_context_path),
        str(revision_set_path),
    )

    assert Path(result['draft_document']).read_text(encoding='utf-8') == (
        '# Revised title\n\nUpdated body.\n'
    )
    assert events[0]['type'] == 'artifact_stream_start'
    assert events[-1]['type'] == 'artifact_stream_end'
    assert all(event['slot'] == 'draft_document' for event in events)
    assert all(event['content_type'] == 'text/markdown' for event in events)
    deltas = [
        event['delta']
        for event in events
        if event['type'] == 'artifact_stream'
    ]
    assert ''.join(deltas) == '# Revised title\n\nUpdated body.\n'
    assert all(0 < len(delta) <= 2 for delta in deltas)
    assert [event['chunk_index'] for event in events] == list(
        range(1, len(events) + 1),
    )


def test_markdown_draft_blocks_do_not_pass_resolved_media(monkeypatch, tmp_path):
    from lazymind.document_tools import execution as document_execution

    tools = _load_tools_module()
    runtime = tools
    context = SimpleNamespace(
        workspace_path=str(tmp_path),
        params={'step_id': 'write_document'},
        emit=lambda _event: None,
    )
    captured = {}

    class FakeWriterCreateToolkit:
        def stream_draft_blocks_markdown(self, **kwargs):
            captured.update(kwargs)
            return json.dumps(['## 第一章\n\n正文。\n'])

    monkeypatch.setattr(runtime, 'require_context', lambda: context)
    monkeypatch.setattr(
        document_execution, '_WriterCreateToolkit', FakeWriterCreateToolkit,
    )
    writing_task_path = tmp_path / 'writing_task.json'
    writing_task_path.write_text('{}', encoding='utf-8')
    section_instructions_path = tmp_path / 'section_instructions.json'
    section_instructions_path.write_text('{}', encoding='utf-8')
    writing_context_path = tmp_path / 'writing_context.json'
    writing_context_path.write_text('{}', encoding='utf-8')
    visual_plan_path = tmp_path / 'visual_plan.json'
    visual_plan_path.write_text('{"instructions": []}', encoding='utf-8')

    paths = document_execution.invoke(
        vars(tools), '_writer_generate_draft_blocks_markdown', {
            'writing_task_path': str(writing_task_path),
            'section_instructions_path': str(section_instructions_path),
            'writing_context_path': str(writing_context_path),
            'visual_plan_path': str(visual_plan_path),
        },
    )

    assert 'media_assets_json' not in captured
    assert captured['visual_plan_json'] == '{"instructions": []}'
    assert Path(paths[0]).read_text(encoding='utf-8') == '## 第一章\n\n正文。\n'


def test_wrapped_idle_timeout_restarts_section_preview_and_retries(monkeypatch):
    from lazymind.document_tools import writing as writer
    from lazyllm.module.module import ModuleExecutionError

    complete = '## 第一章\n\n完整正文。\n'
    emitted: list[dict] = []
    attempts = 0

    class FakeStream:
        def __init__(self, attempt, artifact_store):
            self.attempt, self.artifact_store = attempt, Path(artifact_store)

        def __enter__(self):
            return self

        def __exit__(self, *_args):
            return None

        def __iter__(self):
            yield '## 第一章\n\n'
            if self.attempt == 1:
                yield '残缺正文'
                raise ModuleExecutionError(
                    'Draft Markdown stream was idle for 360 seconds.',
                )
            yield '完整正文。\n'

        def result(self):
            path = self.artifact_store / 'draft_section.md'
            path.write_text(complete, encoding='utf-8')
            return {'artifact_path': str(path)}

    def drafting_tools(**kwargs):
        nonlocal attempts
        attempts += 1
        return SimpleNamespace(
            stream_draft_section=lambda **_kwargs: FakeStream(
                attempts, kwargs['artifact_store'],
            ),
        )

    monkeypatch.setenv('LAZYMIND_WRITER_SECTION_MAX_ATTEMPTS', '2')
    monkeypatch.setattr(writer, 'AutoModel', lambda **_kwargs: object())
    monkeypatch.setattr(writer, 'WriterDraftingTools', drafting_tools)
    monkeypatch.setattr(writer, '_write_input_artifact', lambda *_args: '')

    emitter = writer.DraftMarkdownStreamEventEmitter(emitted.append)
    result = json.loads(writer.WriterWritingCapabilities().stream_draft_blocks_markdown(
        writing_task_json='{}',
        section_instructions_json=json.dumps({
            'instructions': [{
                'instruction_id': 'section-1',
                'content_ref': {'node_id': 'section-1'},
                'section_title': '第一章',
                'section_goal': '写作',
            }],
        }),
        writing_context_json='{}',
        on_delta=emitter.feed,
        on_preview_restart=emitter.restart,
    ))

    assert attempts == 2
    assert result == [complete.rstrip()]
    starts = [event for event in emitted if event['type'] == 'artifact_stream_start']
    assert len(starts) == 2
    latest_preview = ''.join(
        event['delta']
        for event in emitted
        if event['type'] == 'artifact_stream'
        and event['stream_id'] == starts[-1]['stream_id']
    )
    assert latest_preview == complete


def test_markdown_assembly_drops_unregistered_images(monkeypatch, tmp_path):
    from lazymind.document_tools import execution as document_execution

    tools = _load_tools_module()
    runtime = tools
    context = SimpleNamespace(workspace_path=str(tmp_path), emit=lambda _event: None)
    media_assets = {
        'assets': {
            'asset-1': {
                'uri': 'https://example.com/generated-1.png',
                'local_path': '/data/subagent/assets/generated-1.png',
            },
            'asset-2': {'uri': 'https://example.com/unmaterialized.png'},
        },
        'visual_need_asset_ids': {
            'need-1': ['asset-1'],
            'need-2': ['asset-2'],
        },
    }

    class FakeWriterCreateToolkit:
        def generate_draft_document_markdown(self, **kwargs):
            sections = json.loads(kwargs['draft_sections_json'])
            return json.dumps({'draft_document': '\n'.join(sections)})

    monkeypatch.setattr(runtime, 'require_context', lambda: context)
    monkeypatch.setattr(
        document_execution, '_WriterCreateToolkit', FakeWriterCreateToolkit,
    )
    sections = tmp_path / 'sections'
    sections.mkdir()
    (sections / 'draft_section_0001.md').write_text('\n'.join([
        '# Draft',
        '',
        '![Resolved](media-placeholder://need-1)',
        '![Unresolved](media-placeholder://need-2)',
        '![[Legacy]](media-placeholder://need-1)',
        '(media-placeholder://need-3)',
        '![Invalid](./images/AI-lighthouse.jpg)',
    ]), encoding='utf-8')
    context_path = tmp_path / 'writing_context.json'
    context_path.write_text('{}', encoding='utf-8')
    media_path = tmp_path / 'resolved_media_assets.json'
    media_path.write_text(json.dumps({'data': media_assets}), encoding='utf-8')

    result_path = document_execution.invoke(
        vars(tools), '_assemble_draft_document_markdown', {
            'draft_sections_anchor_path': str(sections),
            'writing_context_path': str(context_path),
            'resolved_media_assets_path': str(media_path),
        },
    )
    filled = Path(result_path).read_text(encoding='utf-8')

    assert '![Resolved](/data/subagent/assets/generated-1.png)' in filled
    assert '![Legacy](/data/subagent/assets/generated-1.png)' in filled
    assert '![Unresolved](https://example.com/unmaterialized.png)' in filled
    assert 'media-placeholder://' not in filled
    assert 'media-asset://' not in filled
    assert './images/AI-lighthouse.jpg' not in filled


def test_markdown_assembly_keeps_media_source_reference():
    from lazymind.document_tools import writing as document_writing

    markdown = '![Source](assets/source.png)\n'
    media_assets = {
        'assets': {
            'asset-source': {
                'uri': 'file:///mnt/obsidian/obs/assets/source.png',
                'local_path': '/data/subagent/assets/source.png',
                'meta': {'source_reference': 'assets/source.png'},
            },
        },
    }

    assert document_writing.drop_unregistered_markdown_images(
        markdown, media_assets,
    ) == markdown


def test_markdown_media_fill_drops_failed_need_marker_only():
    from lazymind.document_tools import writing as document_writing

    markdown = '\n'.join([
        'before',
        '![IMAGE-1]',
        '![Missing](media-placeholder://IMAGE-1)',
        '![IMAGE-2]',
        'after',
    ])

    filled = document_writing.fill_markdown_media_placeholders(
        markdown, {'assets': {}, 'visual_need_asset_ids': {}},
    )

    assert '![IMAGE-1]' not in filled
    assert 'media-placeholder://IMAGE-1' not in filled
    assert '![IMAGE-2]' in filled


def test_markdown_revision_fills_resolved_media_placeholder(monkeypatch, tmp_path):
    from lazymind.document_tools import revision as document_revision

    tools = _load_tools_module()
    runtime = tools
    context = SimpleNamespace(
        workspace_path=str(tmp_path),
        params={'step_id': 'write_document'},
        emit=lambda _event: None,
    )

    class FakeWriterRevisionToolkit:
        def apply_string_replace(self, **_kwargs) -> str:
            return json.dumps({
                'string_replace_result': {'replaced': 1},
                'revised_document': '![Visual](media-placeholder://need-1)',
            })

    monkeypatch.setattr(runtime, 'require_context', lambda: context)
    monkeypatch.setattr(
        document_revision, 'WriterRevisionCapabilities', FakeWriterRevisionToolkit,
    )
    base_document_path = tmp_path / 'draft.md'
    base_document_path.write_text('# Original\n', encoding='utf-8')
    writing_context_path = tmp_path / 'context.json'
    writing_context_path.write_text('{}', encoding='utf-8')
    revision_set_path = tmp_path / 'revisions.json'
    revision_set_path.write_text('{}', encoding='utf-8')
    visual_path = tmp_path / 'visual.png'
    visual_path.write_bytes(b'image')
    media_assets_path = tmp_path / 'media_assets.json'
    media_assets_path.write_text(json.dumps({
        'assets': {'asset-1': {'local_path': str(visual_path)}},
        'visual_need_asset_ids': {'need-1': ['asset-1']},
    }), encoding='utf-8')

    result = tools.writer_apply_revision(
        str(base_document_path),
        str(writing_context_path),
        str(revision_set_path),
        str(media_assets_path),
    )

    assert Path(result['draft_document']).read_text(encoding='utf-8') == (
        f'![Visual]({visual_path.as_posix()})'
    )


def test_markdown_no_image_request_skips_visual_planning(monkeypatch, tmp_path):
    from lazymind.document_tools import writing as writer

    calls = []

    class FakePlanningTools:
        def __init__(self, **_kwargs):
            pass

        def generate_visual_plan(self, **_kwargs):
            calls.append('generate_visual_plan')
            raise AssertionError('explicit no-image request must skip visual planning')

        def generate_section_instructions(self, **_kwargs):
            path = tmp_path / 'section_instructions.json'
            path.write_text(json.dumps({
                'data': {
                    'instruction_set_id': 'instructions-1',
                    'instructions': [],
                    'meta': {'representation': 'markdown'},
                },
            }), encoding='utf-8')
            return {'artifact_path': str(path)}

    monkeypatch.setattr(writer, 'WriterPlanningTools', FakePlanningTools)
    monkeypatch.setattr(writer, 'AutoModel', lambda **_kwargs: object())

    result = json.loads(writer.WriterWritingCapabilities().generate_section_instructions(
        writing_task_json=json.dumps({
            'task_id': 'task-1',
            'query': '请扩写这个大纲，不要图片',
            'task_type': 'write',
        }),
        outline_json='# 标题\n\n## 第一章\n',
        writing_context_json=json.dumps({'context_id': 'context-1'}),
    ))

    assert calls == []
    assert result['visual_plan']['instructions'] == []


def test_markdown_rewrite_no_image_request_skips_visual_planning(monkeypatch, tmp_path):
    from lazymind.document_tools import writing as writer

    calls = []

    class FakePlanningTools:
        def __init__(self, **_kwargs):
            pass

        def generate_rewrite_section_instructions(self, **_kwargs):
            path = tmp_path / 'rewrite_section_instructions.json'
            path.write_text(json.dumps({
                'data': {
                    'instruction_set_id': 'instructions-1',
                    'instructions': [],
                    'meta': {
                        'representation': 'markdown',
                        'document_title': 'Rewritten title',
                    },
                },
            }), encoding='utf-8')
            return {'artifact_path': str(path)}

        def generate_visual_plan(self, **_kwargs):
            calls.append('generate_visual_plan')
            raise AssertionError('explicit no-image request must skip visual planning')

    monkeypatch.setattr(writer, 'WriterPlanningTools', FakePlanningTools)
    monkeypatch.setattr(writer, 'AutoModel', lambda **_kwargs: object())

    result = json.loads(
        writer.WriterWritingCapabilities().generate_rewrite_section_instructions(
        writing_task_json=json.dumps({
            'task_id': 'task-1',
            'query': '请重写全文，不要图片',
            'task_type': 'write',
        }),
        source_document_json='# 原文\n\n正文。\n',
        writing_context_json=json.dumps({'context_id': 'context-1'}),
        )
    )

    assert calls == []
    assert result['visual_plan']['instructions'] == []
    assert result['document_title'] == 'Rewritten title'


def test_load_local_lmd_rejects_invalid_document(monkeypatch, tmp_path):
    tools = _load_tools_module()
    runtime = tools
    source = tmp_path / 'broken.lmd'
    source.write_text('{"stage":"outline","blocks":[]}', encoding='utf-8')
    context = SimpleNamespace(
        workspace_path=str(tmp_path),
        params={'history_files_per_turn': {'turn-1': [str(source)]}},
    )
    monkeypatch.setattr(runtime, 'require_context', lambda: context)

    with pytest.raises(ValueError, match=r'Cannot parse LMD file broken\.lmd'):
        tools.writer_load_local_document('broken.lmd')


def test_load_local_lmd_removes_cloud_binding(monkeypatch, tmp_path):
    tools = _load_tools_module()
    runtime = tools
    source = tmp_path / 'bound.lmd'
    source.write_text(json.dumps({'document_id': 'local-doc', 'blocks': [{
        'node_id': 'p1', 'type': 'paragraph', 'content': 'body',
        'provider_binding': {'block_id': 'cloud-block'},
    }], 'provider_binding': {'provider': 'feishu', 'document_id': 'cloud-doc'},
        'metadata': {'source': {'uri': 'https://example.feishu.cn/docx/cloud-doc'}},
    }), encoding='utf-8')
    context = SimpleNamespace(
        workspace_path=str(tmp_path),
        params={'history_files_per_turn': {'turn-1': [str(source)]}},
    )
    monkeypatch.setattr(runtime, 'require_context', lambda: context)

    loaded = runtime._read_json_file(tools.writer_load_local_document('bound.lmd'))

    assert loaded['document_id'] == 'local-doc'
    assert not loaded.get('provider_binding')
    assert 'source' not in loaded.get('metadata', {})
    assert not loaded['blocks'][0].get('provider_binding')


@pytest.mark.parametrize(
    ('representation', 'expected_writer'),
    [('ir', 'publish_revision'), ('markdown', None)],
)
def test_draft_workspace_revise_uses_writing_task_representation(
    monkeypatch,
    tmp_path,
    representation,
    expected_writer,
):
    tools = _load_tools_module()
    runtime = tools
    instruction = '修改文档'

    def write_json(name, payload):
        path = tmp_path / name
        path.write_text(json.dumps(payload), encoding='utf-8')
        return str(path)

    writer_command = write_json('writer_command.json', {
        'action': 'revise',
        'source_role': 'document',
        'target_stage': 'document',
        'next_step': 'write_document',
        'user_instruction': instruction,
        'request_fingerprint': runtime._writer_request_fingerprint(instruction),
    })
    writing_task = write_json(
        'writing_task.json',
        {'output': {'representation': representation}},
    )
    writing_context = write_json('writing_context.json', {})
    source_document = write_json('source_document.json', {})
    target_document = write_json('target_document.json', {})
    modify_plan = write_json('modify_plan.json', {'instructions': []})
    revision_set = write_json('revision_set.json', {})
    draft_document = write_json('draft_document.json', {})
    context_after_draft = write_json('context_after_draft.json', {})
    remote_inputs = {
        'writer_command': writer_command,
        'writing_task': writing_task,
        'writing_context': writing_context,
        'source_document': source_document,
        'target_document': target_document,
    }
    context = SimpleNamespace(
        workspace_path=str(tmp_path),
        params={
            'step_id': 'write_document',
            'user_input': instruction,
            'remote_inputs': remote_inputs,
        },
        emit=lambda _event: None,
    )
    state = {
        'result': {
            'document_revision_task': str(tmp_path / 'revision_task.json'),
            'document_locate_result': str(tmp_path / 'locate_result.json'),
            'document_modify_plan': modify_plan,
            'document_revision_set': revision_set,
            'draft_document': draft_document,
        },
        'completed': False,
    }
    calls = []

    def publish_revision(**_kwargs):
        calls.append('publish_revision')
        return {'publish_result': {'success': True}, 'draft_document': draft_document}

    monkeypatch.setattr(runtime, 'require_context', lambda: context)
    monkeypatch.setattr(
        runtime,
        '_draft_workspace_state',
        lambda _fingerprint: (state, tmp_path / 'checkpoint.json'),
    )
    monkeypatch.setattr(runtime, '_persist_draft_workspace_state', lambda *_args, **_kwargs: None)
    monkeypatch.setattr(runtime, '_save_draft_workspace_artifacts', lambda _result: ['draft_document'])
    monkeypatch.setattr(
        runtime,
        'writer_update_writing_context',
        lambda **_kwargs: context_after_draft,
    )
    monkeypatch.setattr(runtime, 'writer_publish_revision', publish_revision)

    result = tools.writer_draft_workspace()

    assert result['status'] == 'completed'
    assert calls == ([expected_writer] if expected_writer else [])
    if representation == 'markdown':
        assert state['result']['target_document'] == target_document
        assert state['result']['markdown_editor_prepared'] is True
        assert 'document_write_result' not in state['result']
