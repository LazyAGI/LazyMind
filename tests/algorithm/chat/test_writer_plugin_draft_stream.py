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
    tools = _load_tools_module()

    task = json.loads(tools.WriterCreateToolkit().build_writing_task(query))

    assert task.get('constraints', {}) == expected


def test_write_document_revision_emits_markdown_draft_stream(monkeypatch, tmp_path):
    tools = _load_tools_module()
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

    monkeypatch.setattr(tools, 'require_context', lambda: context)
    monkeypatch.setattr(tools, 'WriterRevisionToolkit', FakeWriterRevisionToolkit)
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
    assert ''.join(deltas) == '# Revised title\n\nUpdated body.'
    assert all(0 < len(delta) <= 2 for delta in deltas)
    assert [event['chunk_index'] for event in events] == list(
        range(1, len(events) + 1),
    )


def test_markdown_draft_blocks_do_not_pass_resolved_media(monkeypatch, tmp_path):
    tools = _load_tools_module()
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

    monkeypatch.setattr(tools, 'require_context', lambda: context)
    monkeypatch.setattr(tools, 'WriterCreateToolkit', FakeWriterCreateToolkit)
    writing_task_path = tmp_path / 'writing_task.json'
    writing_task_path.write_text('{}', encoding='utf-8')
    section_instructions_path = tmp_path / 'section_instructions.json'
    section_instructions_path.write_text('{}', encoding='utf-8')
    writing_context_path = tmp_path / 'writing_context.json'
    writing_context_path.write_text('{}', encoding='utf-8')
    visual_plan_path = tmp_path / 'visual_plan.json'
    visual_plan_path.write_text('{"instructions": []}', encoding='utf-8')

    paths = tools.writer_generate_draft_blocks_markdown(
        str(writing_task_path),
        str(section_instructions_path),
        str(writing_context_path),
        str(visual_plan_path),
    )

    assert 'media_assets_json' not in captured
    assert captured['visual_plan_json'] == '{"instructions": []}'
    assert Path(paths[0]).read_text(encoding='utf-8') == '## 第一章\n\n正文。\n'


def test_interrupted_section_restarts_preview_and_retries(monkeypatch):
    from lazymind.chat.engine.tools import writer

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
                raise ConnectionResetError
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
    monkeypatch.setattr(
        writer, 'SectionInstruction',
        SimpleNamespace(model_validate=lambda value: value),
    )
    monkeypatch.setattr(writer, '_write_input_artifact', lambda *_args: '')

    emitter = writer.DraftMarkdownStreamEventEmitter(emitted.append)
    result = json.loads(writer.WriterCreateToolkit().stream_draft_blocks_markdown(
        writing_task_json='{}',
        section_instructions_json='{"instructions": [{}]}',
        writing_context_json='{}',
        on_delta=emitter.feed,
        on_preview_restart=emitter.restart,
    ))

    assert attempts == 2
    assert result == [complete]
    starts = [event for event in emitted if event['type'] == 'artifact_stream_start']
    assert len(starts) == 2
    latest_preview = ''.join(
        event['delta']
        for event in emitted
        if event['type'] == 'artifact_stream'
        and event['stream_id'] == starts[-1]['stream_id']
    )
    assert latest_preview == complete


def test_markdown_media_fill_uses_persistent_uri_and_drops_missing_assets():
    tools = _load_tools_module()

    filled = tools._fill_markdown_media_placeholders(
        '\n'.join([
            '# Draft',
            '',
            '![Resolved](media-placeholder://need-1)',
            '![Unresolved](media-placeholder://need-2)',
            '![[Legacy]](media-placeholder://need-1)',
            '(media-placeholder://need-3)',
        ]),
        {
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
        },
    )

    assert '![Resolved](https://example.com/generated-1.png)' in filled
    assert '![Legacy](https://example.com/generated-1.png)' in filled
    assert '![Unresolved](https://example.com/unmaterialized.png)' in filled
    assert 'media-placeholder://' not in filled
    assert 'media-asset://' not in filled


def test_markdown_revision_fills_resolved_media_placeholder(monkeypatch, tmp_path):
    tools = _load_tools_module()
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

    monkeypatch.setattr(tools, 'require_context', lambda: context)
    monkeypatch.setattr(tools, 'WriterRevisionToolkit', FakeWriterRevisionToolkit)
    base_document_path = tmp_path / 'draft.md'
    base_document_path.write_text('# Original\n', encoding='utf-8')
    writing_context_path = tmp_path / 'context.json'
    writing_context_path.write_text('{}', encoding='utf-8')
    revision_set_path = tmp_path / 'revisions.json'
    revision_set_path.write_text('{}', encoding='utf-8')
    media_assets_path = tmp_path / 'media_assets.json'
    media_assets_path.write_text(json.dumps({
        'assets': {'asset-1': {'local_path': '/data/subagent/assets/visual.png'}},
        'visual_need_asset_ids': {'need-1': ['asset-1']},
    }), encoding='utf-8')

    result = tools.writer_apply_revision(
        str(base_document_path),
        str(writing_context_path),
        str(revision_set_path),
        str(media_assets_path),
    )

    assert Path(result['draft_document']).read_text(encoding='utf-8') == (
        '![Visual](/data/subagent/assets/visual.png)'
    )


def test_markdown_no_image_request_skips_visual_planning(monkeypatch, tmp_path):
    from lazymind.chat.engine.tools import writer

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

    result = json.loads(writer.WriterCreateToolkit().generate_section_instructions(
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
    from lazymind.chat.engine.tools import writer

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

    result = json.loads(writer.WriterCreateToolkit().generate_rewrite_section_instructions(
        writing_task_json=json.dumps({
            'task_id': 'task-1',
            'query': '请重写全文，不要图片',
            'task_type': 'write',
        }),
        source_document_json='# 原文\n\n正文。\n',
        writing_context_json=json.dumps({'context_id': 'context-1'}),
    ))

    assert calls == []
    assert result['visual_plan']['instructions'] == []
    assert result['document_title'] == 'Rewritten title'


def test_selection_rewrite_uses_slot_markdown_artifact_filename(monkeypatch, tmp_path):
    tools = _load_tools_module()

    class FakeWriterRevisionTools:
        def __init__(self, *, llm, artifact_store):
            self.artifact_store = artifact_store

        def build_selected_markdown_replace_set(self, *_args):
            return {
                'replacements': [{
                    'replacement_id': 'replace-1',
                    'content_ref': {'document_root': True},
                    'old_string': 'Original body.',
                    'new_string': 'Polished body.',
                }],
            }

        def apply_string_replace(self, *_args):
            path = Path(self.artifact_store) / 'revised_document.md'
            path.write_text('# Title\n\nPolished body.\n', encoding='utf-8')
            return {'revised_document_md': str(path)}

    monkeypatch.setattr(tools, 'AutoModel', lambda **_kwargs: object())
    monkeypatch.setattr(tools, 'WriterRevisionTools', FakeWriterRevisionTools)
    source_path = tmp_path / 'revised_document.md'
    source_path.write_text('# Title\n\nOriginal body.\n', encoding='utf-8')

    result = tools.writer_preview_selection_rewrite(
        artifact={
            'path': str(source_path),
            'filename': source_path.name,
            'size': source_path.stat().st_size,
        },
        instruction='润色',
        selection={'type': 'markdown', 'selected_text': 'Original body.'},
        artifact_store=str(tmp_path),
        slot='draft_document',
    )

    artifact = result['artifact']['value']
    assert artifact['filename'] == 'draft_document.md'
    assert Path(artifact['path']).name == 'draft_document.md'
    assert Path(artifact['path']).read_text(encoding='utf-8') == (
        '# Title\n\nPolished body.\n'
    )


def test_selection_rewrite_uses_slot_ir_artifact_filename(monkeypatch, tmp_path):
    tools = _load_tools_module()

    class FakeWriterRevisionTools:
        def __init__(self, *, llm, artifact_store):
            self.artifact_store = artifact_store

        def generate_patch_set(self, *_args):
            return {'artifact_path': str(tmp_path / 'patch.json')}

    document = {
        'document_id': 'doc-1',
        'stage': 'final',
        'blocks': [{
            'node_id': 'paragraph-1',
            'type': 'paragraph',
            'content': 'Original body.',
            'stage': 'final',
        }],
    }
    monkeypatch.setattr(tools, 'AutoModel', lambda **_kwargs: object())
    monkeypatch.setattr(tools, 'WriterRevisionTools', FakeWriterRevisionTools)
    monkeypatch.setattr(
        tools,
        'load_artifact_json',
        lambda *_args: tools.PatchSet(target_doc_id='doc-1', hunks=[]),
    )
    monkeypatch.setattr(
        tools,
        'apply_patch_to_ir',
        lambda source, _patch: (source, None),
    )

    result = tools.writer_preview_selection_rewrite(
        artifact={'data': document},
        instruction='Polish',
        selection={'type': 'ir', 'node_id': 'paragraph-1'},
        artifact_store=str(tmp_path),
        slot='draft_document',
    )

    artifact = result['artifact']['value']
    assert artifact['filename'] == 'draft_document.lmd'
    assert Path(artifact['path']).name == 'draft_document.lmd'


def test_load_local_lmd_rejects_invalid_document(monkeypatch, tmp_path):
    tools = _load_tools_module()
    source = tmp_path / 'broken.lmd'
    source.write_text('{"stage":"outline","blocks":[]}', encoding='utf-8')
    context = SimpleNamespace(
        workspace_path=str(tmp_path),
        params={'history_files_per_turn': {'turn-1': [str(source)]}},
    )
    monkeypatch.setattr(tools, 'require_context', lambda: context)

    with pytest.raises(ValueError, match=r'Cannot parse LMD file broken\.lmd'):
        tools.writer_load_local_document('broken.lmd')


def test_load_local_lmd_removes_cloud_binding(monkeypatch, tmp_path):
    tools = _load_tools_module()
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
    monkeypatch.setattr(tools, 'require_context', lambda: context)

    loaded = tools._read_json_file(tools.writer_load_local_document('bound.lmd'))

    assert loaded['document_id'] == 'local-doc'
    assert not loaded.get('provider_binding')
    assert 'source' not in loaded.get('metadata', {})
    assert not loaded['blocks'][0].get('provider_binding')
