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
        ('在原文末尾追加联调记录，写回目标为上述文章', 'revise_document', 'revise_document'),
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


def _stub_prepare_workspace(monkeypatch, tmp_path, *, user_input, history_files_per_turn):
    tools = _load_tools_module()
    captured = {}
    context = SimpleNamespace(
        workspace_path=str(tmp_path),
        params={
            'history_files_per_turn': history_files_per_turn,
            'session_id': 'writer-session',
        },
        emit=lambda _event: None,
    )

    monkeypatch.setattr(tools, '_authoritative_writer_user_input', lambda _default: user_input)
    monkeypatch.setattr(tools, '_verified_knowledge_text', lambda value: value)
    monkeypatch.setattr(tools, 'require_context', lambda: context)
    monkeypatch.setattr(
        tools,
        '_provider_document_reference',
        lambda _value: user_input if user_input.startswith('obsidian://') else '',
    )
    monkeypatch.setattr(tools, '_provider_document_locator', lambda _value: '')
    monkeypatch.setattr(
        tools,
        '_resolve_prepare_control',
        lambda *_args, **_kwargs: ('revise_document', 'document'),
    )

    def resolve_command(**kwargs):
        captured['command'] = kwargs
        return 'writer-command.json'

    monkeypatch.setattr(tools, 'writer_resolve_command', resolve_command)
    monkeypatch.setattr(
        tools,
        '_load_writer_command',
        lambda _path: SimpleNamespace(structure_mode='flat', next_step='write_document'),
    )

    def load_document(*, user_input, stage):
        captured['provider_load'] = (user_input, stage)
        return {
            'source_document': 'source.md',
            'target_document': 'target.json',
            'representation': 'markdown',
        }

    monkeypatch.setattr(tools, 'writer_load_document', load_document)
    task_path = tmp_path / 'task.json'
    from lazymind.document_tools.writing import parse_writer_request_constraints

    task_path.write_text(json.dumps({
        'query': user_input, 'constraints': parse_writer_request_constraints(user_input),
    }))
    monkeypatch.setattr(tools, 'writer_build_writing_task', lambda **_kwargs: str(task_path))
    monkeypatch.setattr(
        tools,
        'writer_collect_available_media',
        lambda **_kwargs: {
            'media_assets': 'media.json',
            'profile_input_resources': 'profile-resources.json',
            'warnings': [],
        },
    )

    def profile_resources(**kwargs):
        captured['profile_inputs'] = kwargs
        return 'resources.json'

    monkeypatch.setattr(tools, 'writer_profile_resources', profile_resources)

    def create_context(**kwargs):
        captured['context_inputs'] = kwargs
        return 'context.json'

    def save_artifacts(result):
        captured['published'] = dict(result)
        return []

    monkeypatch.setattr(tools, 'writer_create_writing_context', create_context)
    monkeypatch.setattr(tools, '_save_draft_workspace_artifacts', save_artifacts)
    return tools, captured


def test_prepare_rejects_provider_document_with_source_filename(monkeypatch, tmp_path):
    locator = 'obsidian://open?vault=obs&file=note'
    tools, captured = _stub_prepare_workspace(
        monkeypatch,
        tmp_path,
        user_input=locator,
        history_files_per_turn={},
    )

    with pytest.raises(ValueError, match='both a provider document locator'):
        tools.writer_prepare_workspace(
            operation='revise_document',
            source_filename='note.md',
        )

    assert 'provider_load' not in captured
    assert 'local_load' not in captured


def test_prepare_rejects_real_uploaded_document_alongside_provider_locator(
    monkeypatch, tmp_path,
):
    locator = 'obsidian://open?vault=obs&file=note'
    tools, _ = _stub_prepare_workspace(
        monkeypatch,
        tmp_path,
        user_input=locator,
        history_files_per_turn={'1': [str(tmp_path / 'note.md')]},
    )

    with pytest.raises(ValueError, match='both a provider document locator'):
        tools.writer_prepare_workspace(
            operation='revise_document',
            source_filename='note.md',
        )


@pytest.mark.parametrize('filename', ['note.md', ''])
def test_prepare_keeps_local_source_without_provider_locator(monkeypatch, tmp_path, filename):
    original = tmp_path / 'note.md'
    original.write_text('# Original\n\nKeep this evidence.', encoding='utf-8')
    tools, captured = _stub_prepare_workspace(
        monkeypatch,
        tmp_path,
        user_input='修改上传的文章，让表达更简洁',
        history_files_per_turn={'1': [str(tmp_path / 'note.md')]},
    )

    tools.writer_prepare_workspace(
        operation='revise_document',
        source_filename=filename,
    )

    assert captured['profile_inputs']['source_uri'] == str(original)
    source = captured['published']['source_document']
    assert source != str(original)
    assert captured['profile_inputs']['source_document_path'] == source
    assert captured['context_inputs']['source_document_path'] == source
    assert tools._read_json_file(source) == tools._DOCUMENT_EXECUTION.normalize_writer_document(original.read_text())
    assert captured['context_inputs']['resource_profiles_path'] == 'resources.json'
    assert captured['published']['writing_context'] == 'context.json'
    assert 'resource_profiles' not in captured['published']
    assert 'provider_load' not in captured


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
    writing_context_path.write_text('{"context_id":"ctx"}', encoding='utf-8')
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
    writing_task_path.write_text('{"query":"Write","task_type":"write"}', encoding='utf-8')
    section_instructions_path = tmp_path / 'section_instructions.json'
    section_instructions_path.write_text('{}', encoding='utf-8')
    writing_context_path = tmp_path / 'writing_context.json'
    writing_context_path.write_text('{"context_id":"ctx"}', encoding='utf-8')
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
        writing_task_json='{"query":"Write","task_type":"write"}',
        section_instructions_json=json.dumps({
            'instructions': [{
                'instruction_id': 'section-1',
                'content_ref': {'node_id': 'section-1'},
                'section_title': '第一章',
                'section_goal': '写作',
            }],
        }),
        writing_context_json='{"context_id":"ctx"}',
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


def test_markdown_media_fill_preserves_registered_path_with_whitespace():
    from lazymind.document_tools import writing as document_writing

    local_path = '/Users/test/Library/Application Support/LazyMind/image.png'
    media_assets = {
        'assets': {
            'asset-generated': {
                'local_path': local_path,
            },
        },
        'visual_need_asset_ids': {
            'IMAGE-1': ['asset-generated'],
        },
    }

    filled = document_writing.fill_markdown_media_placeholders(
        '![Generated](media-placeholder://IMAGE-1)\n',
        media_assets,
    )

    expected = f'![Generated](<{local_path}>)\n'
    assert filled == expected
    assert document_writing.drop_unregistered_markdown_images(
        filled, media_assets,
    ) == expected


def test_drop_unregistered_images_accepts_percent_encoded_path():
    from lazymind.document_tools import writing as document_writing

    raw = '/Users/test/Library/Application Support/LazyMind/image.png'
    encoded = '/Users/test/Library/Application%20Support/LazyMind/image.png'
    markdown = f'![Generated]({encoded})\n'
    media_assets = {
        'assets': {
            'asset-generated': {
                'local_path': raw,
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
    writing_context_path.write_text('{"context_id":"ctx"}', encoding='utf-8')
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


@pytest.mark.parametrize('representation', ['markdown', 'ir'])
def test_rewrite_no_image_request_skips_visual_planning(monkeypatch, tmp_path, representation):
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
                        'representation': representation,
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
    monkeypatch.setattr(writer, '_temp_root', lambda: tmp_path / 'source')
    source = '# 原文\n\n正文。\n' if representation == 'markdown' else json.dumps({
        'document_id': 'source', 'stage': 'draft', 'title': '原文',
        'blocks': [{'node_id': 'body', 'type': 'paragraph', 'content': '正文。'}],
    })

    result = json.loads(
        writer.WriterWritingCapabilities().generate_rewrite_section_instructions(
        writing_task_json=json.dumps({
            'task_id': 'task-1',
            'query': '请重写全文，不要图片',
            'task_type': 'write',
        }),
        source_document_json=source,
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

    result = tools.writer_load_local_document('bound.lmd')
    assert result['source_uri'] == str(source)
    loaded = runtime._read_json_file(result['source_document'])

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
        {'query': instruction, 'task_type': 'revise', 'output': {'representation': representation}},
    )
    writing_context = write_json('writing_context.json', {'context_id': 'ctx'})
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
    monkeypatch.setattr(runtime, '_save_draft_delivery', lambda state, path: (
        runtime._draft_workspace_completion(state['result'], ['draft_document'])
    ))
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


def test_generated_outline_does_not_run_a_second_completion(monkeypatch):
    from lazymind.document_tools import writing as writer

    chunks = []
    completed = []

    class Toolkit:
        def stream_outline(self, *, on_delta, **kwargs):
            on_delta('# Story\n## Chapter\n')
            return '# Story\n## Chapter\n'

        def prepare_outline(self, **kwargs):
            raise AssertionError('generated outline must not call completion')

    monkeypatch.setattr(writer, 'WriterWritingCapabilities', Toolkit)
    result = writer.generate_outline(
        {'query': 'Write a story'}, {'context_id': 'ctx'}, on_delta=chunks.append,
        on_outline_prepared=lambda: completed.append(True),
    )
    assert result == ''.join(chunks)
    assert completed == [True]


@pytest.mark.parametrize('fail', [False, True])
def test_outline_stream_records_timing_trace_without_changing_preview(monkeypatch, fail):
    from types import SimpleNamespace
    import lazyllm.tracing as tracing
    from lazyllm.tools.writer.tools.stream_tools import DraftMarkdownStream
    from lazymind.document_tools.writing import WriterWritingCapabilities

    attributes, finished, errors, preview = {}, [], [], []
    handle = object()
    monkeypatch.setattr(tracing, 'start_span', lambda **kwargs: handle)
    monkeypatch.setattr(tracing, 'get_trace_context', lambda: SimpleNamespace(trace_id='trace-123'))
    monkeypatch.setattr(tracing, 'set_span_attributes', lambda span, attrs: attributes.update(attrs))
    monkeypatch.setattr(tracing, 'set_span_error', lambda span, exc: errors.append(exc))
    monkeypatch.setattr(tracing, 'finish_span', lambda span: finished.append(span))

    def call(sink):
        sink({'tag': 'think', 'delta': 'reasoning'})
        if fail:
            raise TimeoutError('test timeout')
        sink({'tag': 'text', 'delta': '# Outline'})
        return '# Outline'

    planning = SimpleNamespace(stream_outline=lambda **kwargs: DraftMarkdownStream(
        call=call, finalize=lambda body: {'body': body}, prefix='', idle_timeout=1,
    ))
    monkeypatch.setattr(WriterWritingCapabilities, '_outline_planning', lambda *args: (planning, '', ''))
    monkeypatch.setattr(WriterWritingCapabilities, '_outline_result', staticmethod(lambda result: result['body']))
    toolkit = WriterWritingCapabilities()
    if fail:
        with pytest.raises(TimeoutError, match='test timeout'):
            toolkit.stream_outline('{}', '{}', preview.append)
    else:
        assert toolkit.stream_outline('{}', '{}', preview.append) == '# Outline'
        assert ''.join(preview) == '# Outline\n'
    assert attributes['writer.outline.trace_id'] == 'trace-123'
    assert attributes['writer.outline.status'] == ('error' if fail else 'completed')
    assert attributes['writer.outline.first_response_ms'] >= 0
    assert ('writer.outline.first_visible_ms' in attributes) is not fail
    assert ('writer.outline.generation_finished_ms' in attributes) is not fail
    assert len(errors) == int(fail)
    assert finished == [handle]


@pytest.mark.parametrize(('target', 'maximum', 'whole'), [
    (3000, 3300, True), (5000, 5500, True), (5001, 5501, False),
    (6000, 6600, False), (None, 5000, True), (None, None, False),
])
def test_whole_document_generation_boundary(monkeypatch, tmp_path, target, maximum, whole):
    from lazymind.document_tools import execution
    task = {'query': 'Write', 'task_type': 'write', 'output': {'representation': 'markdown'},
            'constraints': {'target_chars': target, 'max_chars': maximum}}
    monkeypatch.setattr(execution, '_read_json_file',
                        lambda path: {'context_id': 'ctx'} if path == 'context' else task)
    monkeypatch.setattr(execution, '_read_json_string', lambda path: '{}')
    monkeypatch.setattr(execution, '_workspace_root', lambda: tmp_path)
    monkeypatch.setattr(execution, '_run_root', lambda name: tmp_path / name)
    monkeypatch.setattr(execution, 'require_context', lambda: SimpleNamespace(emit=lambda e: None))
    calls = []
    saved = []
    monkeypatch.setattr(execution, 'stream_whole_document',
                        lambda *a, **kw: calls.append('whole') or '# Story\n\n## 1. Background\n\n1. Body.')
    monkeypatch.setattr(execution, '_save_writer_document',
                        lambda key, document, **kw: saved.append(document) or 'whole.md')
    monkeypatch.setattr(execution, '_writer_generate_draft_blocks_markdown',
                        lambda **kw: calls.append('sections') or ['chapter.md'])
    monkeypatch.setattr(execution, '_assemble_draft_document_markdown', lambda **kw: 'sections.md')
    result = execution.invoke({}, '_writer_generate_draft_document', {
        'writing_task_path': 'task', 'section_instructions_path': 'instructions', 'writing_context_path': 'context',
    })
    assert calls == ['whole' if whole else 'sections']
    assert ('draft_blocks' not in result) == whole
    assert saved == (['# Story\n\n## Background\n\n1. Body.'] if whole else [])


def test_workspace_checkpoint_changes_when_outline_is_edited_in_place(tmp_path):
    tools = _load_tools_module()
    outline = tmp_path / 'outline.md'
    outline.write_text('# Story\n\n## Original\n')
    first = tools._state_workspace_fingerprint(outline_document_path=str(outline))
    assert tools._state_workspace_fingerprint(outline_document_path=str(outline)) == first
    outline.write_text('# Story\n\n## Edited\n')
    assert tools._state_workspace_fingerprint(outline_document_path=str(outline)) != first


@pytest.mark.parametrize('analyze', [False, True])
def test_source_image_analysis_policy_is_forwarded(monkeypatch, tmp_path, analyze):
    from lazymind import model_config
    from lazymind.document_tools import writing as writer
    captured = {}

    class Capabilities:
        def build_resources(self, **kwargs):
            return '[]'

        def collect_available_media(self, **kwargs):
            captured.update(kwargs)
            return '{}'

    monkeypatch.setattr(writer, 'WriterWritingCapabilities', Capabilities)
    monkeypatch.setattr(model_config, 'is_model_role_available', lambda _: True)
    task = {'constraints': {'visual_policy': {'require_input_image_reuse': True}}}
    writer.collect_document_media(task, file_paths=[], source_document='# Document',
                                  media_store=str(tmp_path), analyze_source_images=analyze)
    policy = json.loads(captured['writing_task_json'])['constraints']['visual_policy']
    assert policy == {'require_input_image_reuse': True, 'analyze_source_images': analyze}
    assert 'analyze_source_images' not in task['constraints']['visual_policy']
    assert captured['use_vision_model'] is True  # Independent references retain vision support.


def test_resource_profiles_skip_preserved_images_but_keep_reference_material(monkeypatch, tmp_path):
    from lazymind.document_tools import writing as writer
    captured = {}

    class ResourceTools:
        def __init__(self, **kwargs):
            pass

        def profile_resources(self, **kwargs):
            captured.update(kwargs)
            return {}

    monkeypatch.setattr(writer, 'WriterResourceTools', ResourceTools)
    monkeypatch.setattr(writer, 'AutoModel', lambda **kwargs: None)
    monkeypatch.setattr(writer, '_temp_root', lambda: tmp_path)
    monkeypatch.setattr(writer, '_primary_data', lambda _: [])
    resources = [
        {'resource_id': 'old', 'resource_type': 'image', 'meta': {'source_image_preserved': True}},
        {'resource_id': 'upload', 'resource_type': 'image'},
        {'resource_id': 'short-reference', 'resource_type': 'text', 'inline_text': 'Windows'},
        {'resource_id': 'source', 'resource_type': 'text', 'inline_text': 'Source document'},
    ]
    writer.WriterWritingCapabilities().profile_resources('{}', '', json.dumps(resources))
    assert [item.resource_id for item in captured['input_resources']] == ['upload', 'short-reference', 'source']


def test_context_fingerprint_uses_effective_content_not_path_or_storage_metadata(tmp_path):
    tools = _load_tools_module()
    first = tmp_path / 'context-a.json'
    second = tmp_path / 'context-b.json'
    first.write_text(json.dumps({'context_id': 'a', 'doc_id': 'doc-a', 'meta': {'source': 'prepare'}}))
    second.write_text(json.dumps({'schema': 'context', 'meta': {'created_at': 'later'}, 'data': {
        'context_id': 'b', 'doc_id': 'doc-b', 'meta': {'context_update_warning': 'diagnostic'},
    }}))
    expected = tools._state_workspace_fingerprint(writing_context_path=str(first))
    assert tools._state_workspace_fingerprint(writing_context_path=str(second)) == expected
    second.write_text(json.dumps({'context_id': 'b', 'style_profile': {'tone': 'formal'}}))
    assert tools._state_workspace_fingerprint(writing_context_path=str(second)) != expected


def test_task_fingerprint_ignores_tracking_but_preserves_revision_identity(tmp_path):
    tools = _load_tools_module()
    first = tmp_path / 'task-a.json'
    second = tmp_path / 'task-b.json'
    task = {'query': 'Write a report', 'task_type': 'write', 'task_id': 'a'}
    first.write_text(json.dumps(task))
    second.write_text(json.dumps({**task, 'task_id': 'b', 'meta': {'source': 'retry'}, 'constraints': {}}))
    expected = tools._state_workspace_fingerprint(writing_task_path=str(first))
    assert tools._state_workspace_fingerprint(writing_task_path=str(second)) == expected
    second.write_text(json.dumps({**task, 'constraints': {'max_chars': 100}}))
    assert tools._state_workspace_fingerprint(writing_task_path=str(second)) != expected
    first.write_text(json.dumps({**task, 'task_type': 'revise'}))
    second.write_text(json.dumps({**task, 'task_type': 'revise', 'task_id': 'b'}))
    assert tools._state_workspace_fingerprint(writing_task_path=str(first)) != tools._state_workspace_fingerprint(
        writing_task_path=str(second))


@pytest.mark.parametrize(('source_case', 'bound_resources'), [
    ('explicit', False), ('explicit', True),
    ('automatic', False), ('automatic', True),
    ('changed', False), ('unidentified', False), ('missing_resource', False),
])
def test_profile_selected_attachment_once_without_losing_other_materials(
    monkeypatch, tmp_path, source_case, bound_resources,
):
    from lazymind.document_tools import writing as writer
    from lazyllm.tools.writer.data_models.resource import ResourceProfile

    tools = _load_tools_module()
    original = tmp_path / 'original.md'
    original.write_text('# Original\n\nRequired evidence.\n', encoding='utf-8')
    reference = tmp_path / ('reference.md' if source_case == 'explicit' else 'reference.csv')
    reference.write_text(original.read_text() if source_case == 'explicit' else 'fact\nOther evidence.\n',
                         encoding='utf-8')
    task = tmp_path / 'task.json'
    task.write_text(json.dumps({'query': 'Revise using both materials', 'task_type': 'write'}))
    context = SimpleNamespace(workspace_path=str(tmp_path), params={
        'history_files_per_turn': {'1': [str(original), str(reference)]},
    })
    monkeypatch.setattr(tools, 'require_context', lambda: context)
    loaded = tools.writer_load_local_document(original.name if source_case == 'explicit' else '')
    source = Path(loaded['source_document'])
    assert loaded['source_uri'] == str(original)
    if source_case == 'changed':
        source.write_text('# Changed\n\nNew evidence.', encoding='utf-8')
    if source_case == 'missing_resource':
        context.params['history_files_per_turn'] = {'1': [str(reference)]}
    monkeypatch.setattr(writer, 'AutoModel', lambda **kwargs: object())
    calls = []

    def analyze(self, prompt, schema):
        calls.append(prompt)
        return ResourceProfile(resource_role='background', summary='Evidence', key_facts=['Keep evidence'])

    monkeypatch.setattr(writer.WriterResourceTools, '_call_llm_structured', analyze)
    resources = tmp_path / 'resources.json'
    resources.write_text(json.dumps([
        {'resource_id': path.name, 'resource_type': 'file', 'uri': str(path)}
        for path in ([reference] if source_case == 'missing_resource' else [original, reference])
    ]))
    result = tools.writer_profile_resources(
        writing_task_path=str(task), user_input='Revise using both materials',
        profile_input_resources_path=str(resources) if bound_resources else '',
        source_document_path=str(source),
        source_uri=loaded['source_uri'] if source_case != 'unidentified' else '',
    )
    profiles = tools._read_json_file(result)
    expected_ids = [reference.name] if source_case == 'missing_resource' else [original.name, reference.name]
    if source_case not in {'explicit', 'automatic'}:
        expected_ids.append('source_document')
    assert [profile['resource_id'] for profile in profiles] == expected_ids
    assert len(calls) == len(expected_ids)
    assert all(profile['key_facts'] == ['Keep evidence'] for profile in profiles)
    assert original.exists() and source.exists()


def test_quality_adapter_keeps_requirements_without_persisting_context_query(tmp_path):
    from unittest.mock import patch
    from lazyllm.tools.writer.tools.quality_tools import WriterQualityTools
    from lazymind.document_tools.writing import WriterWritingCapabilities

    context = {'context_id': 'ctx'}
    task = {'query': 'CURRENT_REQUEST', 'task_type': 'write'}
    with patch('lazymind.document_tools.writing._temp_root', return_value=tmp_path), \
            patch('lazymind.document_tools.writing.AutoModel', return_value=None), \
            patch.object(WriterQualityTools, '_call_llm_structured', side_effect=RuntimeError('captured')) as model:
        with pytest.raises(RuntimeError, match='captured'):
            WriterWritingCapabilities().check_consistency(
                '# Draft\n\nBody', json.dumps(context), json.dumps(task),
            )
    prompt = model.call_args.args[0]
    assert prompt.count('CURRENT_REQUEST') == 1
    saved = json.loads((tmp_path / 'writing_context.json').read_text())['data']
    assert 'query' not in saved
    assert saved['document_summary'] is None


@pytest.mark.parametrize(
    ('user_request', 'expected', 'absent'),
    [
        (
            '写一篇 3000 字左右的克苏鲁小说，配一张图',
            ['目标篇幅：3000 字', '篇幅上限：3300 字', '必须配图', '允许生成新图片'],
            ['篇幅下限', '2700'],
        ),
        (
            '写一篇不超过500字的摘要，不要配图，不要生图',
            ['篇幅上限：500 字', '图片及其他可视化：不使用', 'AI 生图：禁止生成新图片'],
            ['篇幅下限', '必须配图'],
        ),
        (
            '写一篇文章，必须复用我上传的图片',
            ['必须复用用户提供的图片', '禁止生成新图片'],
            ['目标篇幅：', '篇幅上限：', '篇幅下限：'],
        ),
        (
            '写一篇短文',
            ['未设置明确的字数约束', 'AI 生图：未单独指定'],
            ['目标篇幅：', '篇幅上限：', '篇幅下限：', '禁止生成新图片'],
        ),
    ],
)
def test_prepare_publishes_readable_brief_alongside_unchanged_task_json(
    monkeypatch, tmp_path, user_request, expected, absent,
):
    from lazymind.chat.engine.subagent import tools as artifact_tools

    tools, _ = _stub_prepare_workspace(
        monkeypatch, tmp_path, user_input=user_request, history_files_per_turn={},
    )
    context = tools.require_context()
    context.output_slots = ['writing_task', 'writing_brief']
    published = []

    def save_artifacts(entries):
        published.extend(entries)
        return {'status': 'ok'}

    monkeypatch.setattr(artifact_tools, 'save_artifacts', save_artifacts)
    monkeypatch.setattr(tools, '_save_draft_workspace_artifacts',
                        lambda result: tools._state_save_workspace_artifacts(context, result))
    original_json = (tmp_path / 'task.json').read_bytes()
    result = tools.writer_prepare_workspace(operation='revise_document')

    assert result['saved_artifact_keys'] == ['writing_task', 'writing_brief']
    assert {item['key'] for item in published} == {'writing_task', 'writing_brief'}
    assert all(item['content_type'] == 'file' and Path(item['value']).is_file() for item in published)
    assert Path(result['writing_task']).read_bytes() == original_json
    assert Path(result['writing_brief']).suffix == '.md'
    brief = Path(result['writing_brief']).read_text(encoding='utf-8')
    assert user_request in brief
    assert all(value in brief for value in expected)
    assert all(value not in brief for value in absent)


def test_writing_brief_preserves_readable_clarifications_and_explicit_bounds(monkeypatch, tmp_path):
    request = 'Write a short story.'
    answers = {'writing_goal': '克苏鲁小说', 'word_target': '3000',
               'visual_policy': '配一张契合小说内容与氛围的插图'}
    query = request + '\n\nWorkflow clarification answers:\n' + json.dumps(answers, ensure_ascii=False)
    tools, _ = _stub_prepare_workspace(
        monkeypatch, tmp_path, user_input=query, history_files_per_turn={},
    )
    task_path = tmp_path / 'task.json'
    task_path.write_text(json.dumps({'constraints': {'target_chars': 3000, 'min_chars': 2800, 'max_chars': 3200}}))

    result = tools.writer_prepare_workspace(operation='revise_document')
    brief = Path(result['writing_brief']).read_text(encoding='utf-8')

    assert request in brief
    assert '写作目标：克苏鲁小说' in brief
    assert '可视化与配图要求：配一张契合小说内容与氛围的插图' in brief
    assert '篇幅下限：2800 字' in brief and '篇幅上限：3200 字' in brief
    assert 'Workflow clarification answers' not in brief
    assert json.dumps(answers, ensure_ascii=False) not in brief


def test_writing_brief_keeps_unparseable_clarification_text(monkeypatch, tmp_path):
    query = '写一篇短文\n\nWorkflow clarification answers:\n补充：使用中文，配一张图'
    tools, _ = _stub_prepare_workspace(
        monkeypatch, tmp_path, user_input=query, history_files_per_turn={},
    )

    result = tools.writer_prepare_workspace(operation='revise_document')

    assert '补充：使用中文，配一张图' in Path(result['writing_brief']).read_text(encoding='utf-8')


@pytest.mark.parametrize('image_available', [True, False])
def test_prepare_publishes_uploaded_markdown_separately_from_brief_even_when_images_fail(
    monkeypatch, tmp_path, image_available,
):
    import base64
    import hashlib
    from lazymind import model_config
    from lazymind.document_tools import markdown_inputs
    from lazymind.chat.engine.subagent import tools as artifact_tools

    body = '# Uploaded material\n\n![figure](figure.png)\n'
    manifest = base64.b64encode(json.dumps({'images': [{'source': 'figure.png', 'resource_id': 'figure'}]}).encode()).decode()
    document = tmp_path / 'material.md'
    document.write_text(body + '\n\n<!-- lazymind-markdown-images:v1:' + manifest + ' -->\n')
    tools, _ = _stub_prepare_workspace(
        monkeypatch, tmp_path, user_input='根据材料写一篇文章', history_files_per_turn={'1': [str(document)]},
    )
    task = tmp_path / 'task.json'
    task.write_text(json.dumps({'task_type': 'write', 'task_id': 'test', 'query': 'Write from the material'}))
    monkeypatch.setattr(tools, 'writer_load_local_document', lambda *_: {
        'source_document': str(document), 'source_uri': str(document),
    })
    monkeypatch.setattr(model_config, 'is_model_role_available', lambda _: False)
    data = base64.b64decode('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=')

    def read(identifier, timeout):
        if not image_available:
            raise TimeoutError('image unavailable')
        return {'name': 'figure.png', 'mime_type': 'image/png', 'content_base64': base64.b64encode(data).decode(),
                'content_hash': 'sha256:' + hashlib.sha256(data).hexdigest()}

    monkeypatch.setattr(markdown_inputs, 'read_image_resource', read)
    # Restore the real Runtime media adapter; only HTTP and final Core publication are faked.
    monkeypatch.setattr(tools, 'writer_collect_available_media', lambda **kwargs: tools._DOCUMENT_EXECUTION.invoke(
        vars(tools), '_writer_collect_available_media', {'analyze_source_images': False, **kwargs}))
    context = tools.require_context()
    context.output_slots = ['writing_task', 'writing_brief', 'source_materials', 'media_assets', 'writing_context']
    published = []
    monkeypatch.setattr(artifact_tools, 'save_artifacts', lambda entries: published.extend(entries) or {'status': 'ok'})
    monkeypatch.setattr(tools, '_save_draft_workspace_artifacts',
                        lambda result: tools._state_save_workspace_artifacts(context, result))

    result = tools.writer_prepare_workspace(operation='revise_document')

    assert result['artifacts_saved'] is True
    keys = [item['key'] for item in published]
    assert keys.index('media_assets') < keys.index('source_materials')
    assert {'writing_brief', 'source_materials', 'media_assets'} <= set(result['saved_artifact_keys'])
    assert Path(result['source_materials'][0]).read_text() == body
    assert Path(result['writing_brief']).read_text().startswith('# 写作准备')
    library = tools._read_json_file(result['media_assets'])
    assert bool(library['assets']) is image_available
    assert all(item['content_type'] == 'file' for item in published)
    if not image_available:
        assert result['warnings']


@pytest.mark.parametrize('filename', ['', 'material.md', '0001_material.md'])
def test_bound_codex_material_reaches_prepare_and_media_library(monkeypatch, tmp_path, filename):
    import asyncio
    import base64
    from unittest.mock import AsyncMock
    from lazymind import model_config
    from lazymind.chat.workflow.remote_executor import RemoteWorkflowExecutor
    from lazymind.document_tools import execution, markdown_inputs
    from test_markdown_input_images import image_resource, markdown_file

    body = '# Original\n\n![Image](images/proof.png)\n'
    original = markdown_file(tmp_path / 'upload', body, {'images/proof.png': 'owned-image'})
    worker = RemoteWorkflowExecutor()
    worker.runtime = SimpleNamespace(input=AsyncMock(return_value={'items': [
        {'name': original.name, 'content_base64': base64.b64encode(original.read_bytes()).decode()},
    ]}))
    inputs = asyncio.run(worker._resolve_inputs(None, 'attempt', 'lease', {
        'inputs': {'source_files': [{'resource_id': 'owned-md', 'revision': 1}]},
        'declared_input_types': {'source_files': 'file'},
        'declared_input_transports': {'source_files': 'path'},
    }, str(tmp_path / 'attempt')))
    tools, captured = _stub_prepare_workspace(
        monkeypatch, tmp_path, user_input='基于附件扩写小说，保留原图',
        history_files_per_turn={'old': ['/unrelated/old.md']})
    ctx = tools.require_context()
    ctx.params.update(remote_inputs=inputs, remote_input_transports={'source_files': 'path'})
    monkeypatch.setattr(model_config, 'is_model_role_available', lambda _: False)
    resource = image_resource('red')
    monkeypatch.setattr(markdown_inputs, 'read_image_resource', lambda *_: resource)
    monkeypatch.setattr(tools, 'writer_collect_available_media', lambda **kwargs: execution.invoke(
        vars(tools), '_writer_collect_available_media', kwargs))
    (tmp_path / 'task.json').write_text(json.dumps({
        'task_id': 'writer', 'task_type': 'write', 'query': '基于附件扩写小说，保留原图',
    }))
    result = tools.writer_prepare_workspace(operation='revise_document', source_filename=filename)
    assert 'provider_load' not in captured
    assert Path(result['source_document']).read_text().strip() == body.strip()
    assert Path(result['source_materials'][0]).read_text() == body
    assert Path(result['writing_brief']).is_file()
    assets = tools._read_json_file(result['media_assets'])['assets']
    assert len(assets) == 1
    copy = Path(next(iter(assets.values()))['local_path'])
    assert copy.read_bytes() == base64.b64decode(resource['content_base64'])
    assert copy != original
    assert not result['warnings']


def test_writer_bindings_reject_non_path_transport_and_keep_ambiguous_sources(monkeypatch, tmp_path):
    from lazymind.document_tools.execution import writer_input_files
    with pytest.raises(ValueError, match='path transport'):
        writer_input_files({'remote_inputs': {'source_files': ['/untrusted.md']}})
    tools = _load_tools_module()
    paths = [str(tmp_path / f'{index:04d}_material.md') for index in [1, 2]]
    monkeypatch.setattr(tools, 'require_context', lambda: SimpleNamespace(params={
        'remote_inputs': {'source_files': paths}, 'remote_input_transports': {'source_files': 'path'},
    }))
    with pytest.raises(ValueError, match='Exactly one'):
        tools.writer_load_local_document('material.md')
