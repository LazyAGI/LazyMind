"""Regression coverage for writer-workflow draft preview events."""
from __future__ import annotations

import importlib.util
import json
import sys
from pathlib import Path
from types import ModuleType, SimpleNamespace


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
    assert [event['type'] for event in events] == [
        'artifact_stream_start',
        'artifact_stream',
        'artifact_stream_end',
    ]
    assert all(event['slot'] == 'draft_document' for event in events)
    assert all(event['content_type'] == 'text/markdown' for event in events)
    assert events[1]['delta'] == '# Revised title\n\nUpdated body.\n'


def test_outline_revision_uses_outline_document_filename(monkeypatch, tmp_path):
    tools = _load_tools_module()
    context = SimpleNamespace(
        workspace_path=str(tmp_path),
        params={'step_id': 'outline'},
    )

    class FakeWriterRevisionToolkit:
        def apply_string_replace(self, **_kwargs) -> str:
            return json.dumps({
                'string_replace_result': {'replaced': 1},
                'revised_document': '# Revised outline\n',
            })

    monkeypatch.setattr(tools, 'require_context', lambda: context)
    monkeypatch.setattr(tools, 'WriterRevisionToolkit', FakeWriterRevisionToolkit)
    base_document_path = tmp_path / 'revised_document.md'
    base_document_path.write_text('# Original outline\n', encoding='utf-8')
    writing_context_path = tmp_path / 'context.json'
    writing_context_path.write_text('{}', encoding='utf-8')
    revision_set_path = tmp_path / 'revisions.json'
    revision_set_path.write_text('{}', encoding='utf-8')

    result = tools.writer_apply_revision(
        str(base_document_path),
        str(writing_context_path),
        str(revision_set_path),
    )

    assert 'revised_document' not in result
    assert Path(result['outline_document']).name == 'outline_document.md'
    assert Path(result['outline_document']).read_text(encoding='utf-8') == (
        '# Revised outline\n'
    )


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
