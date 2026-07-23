"""Thin path adapters for revise-plugin.

Document parsing, revision, conflict detection, and Feishu writes belong to
LazyLLM writer tools. This module only converts plugin artifact paths to those
tool calls and returns paths for save_artifact.
"""
from __future__ import annotations

import json
import re
import uuid
from pathlib import Path
from typing import Any

from lazyllm.tools.writer.data_models import TargetDocument, WriterDocument
from lazyllm.tools.writer.tools import WriterResourceTools
from lazyllm.tools.writer.utils import save_artifact_json

from lazymind.chat.engine.subagent.context import require_context
from lazymind.chat.engine.tools.writer import (
    WriterRevisionToolkit,
    WriterToolkitBase,
    build_writer_status_ir,
    writer_schema,
)


_FEISHU_URL_RE = re.compile(
    r"https?://[^\s<>\"']*(?:feishu\.cn|larksuite\.com)/"
    r"[^\s<>\"'，。；！？、）】》」』]+",
    re.IGNORECASE,
)


def _workspace_root() -> Path:
    ctx = require_context()
    root = Path(ctx.workspace_path) if ctx.workspace_path else Path('/tmp')
    root.mkdir(parents=True, exist_ok=True)
    return root


def _run_root(name: str) -> Path:
    root = _workspace_root() / 'revise-plugin' / f'{name}-{uuid.uuid4().hex}'
    root.mkdir(parents=True, exist_ok=True)
    return root


def _read_json_file(path: str) -> Any:
    with open(path, 'r', encoding='utf-8') as fh:
        value = json.load(fh)
    return value.get('data') if isinstance(value, dict) and 'data' in value else value


def _read_json_string(path: str) -> str:
    return json.dumps(_read_json_file(path), ensure_ascii=False)


def _json_loads(value: str, default: Any = None) -> Any:
    text = (value or '').strip()
    if not text:
        return default
    result = json.loads(text)
    return result.get('data') if isinstance(result, dict) and 'data' in result else result


def _writer_document_json(value: str, *, ui_editable: bool) -> str:
    document = WriterDocument.model_validate(_json_loads(value, {}))
    document.ui_editable = ui_editable
    return document.model_dump_json()


def _save_json_artifact(
    name: str,
    content_json: str,
    schema_name: str,
    *,
    directory: Path | None = None,
) -> str:
    root = directory or _workspace_root()
    root.mkdir(parents=True, exist_ok=True)
    return save_artifact_json(
        _json_loads(content_json, {}),
        str(root / f'{name}.json'),
        schema_name=schema_name,
        created_by='revise-plugin-wrapper',
    )


def _result_path(result: dict) -> str:
    path = str(result.get('artifact_path') or '')
    if not path:
        raise ValueError(f'LazyLLM writer tool did not return artifact_path: {result!r}')
    return path


def _feishu_url(user_input: str) -> str:
    match = _FEISHU_URL_RE.search(user_input or '')
    if not match:
        raise ValueError('A Feishu document URL is required.')
    return match.group(0).rstrip(').,;!?]}，。；！？】》」』')


def revise_load_document(user_input: str) -> dict:
    """Create the immutable revise task and load its source document."""
    root = _run_root('load')
    target = TargetDocument(uri=_feishu_url(user_input), adapter='feishu')
    revise_task = WriterRevisionToolkit().build_revise_task(
        query=user_input,
        target_document_json=json.dumps(target.model_dump(), ensure_ascii=False),
    )
    result = WriterResourceTools(llm=None, artifact_store=str(root)).document_to_docir(target)
    loaded_document_json = _read_json_string(_result_path(result))

    return {
        'revise_task': _save_json_artifact(
            'revise_task', revise_task, writer_schema('task.WritingTask'), directory=root,
        ),
        'source_ir': _save_json_artifact(
            'source_ir', _writer_document_json(loaded_document_json, ui_editable=False),
            WriterToolkitBase.WRITER_IR_SCHEMA, directory=root,
        ),
    }


def revise_build_context(revise_task_path: str, source_ir_path: str) -> dict:
    """Build the model context for revision as its own observable plugin step."""
    root = _run_root('context')
    writer = WriterRevisionToolkit()
    revise_task_json = _read_json_string(revise_task_path)
    context_json = writer.create_writing_context(
        writing_task_json=revise_task_json,
        resource_profiles_json='[]',
        writer_document_json=_read_json_string(source_ir_path),
    )
    return {
        'revision_context': _save_json_artifact(
            'revision_context', context_json, writer_schema('context.WritingContext'), directory=root,
        ),
        'revision_context_ir': _save_json_artifact(
            'revision_context_ir',
            build_writer_status_ir(
                'context_ready',
                '已成功构造写作修改上下文',
                source='revise-plugin',
            ),
            WriterToolkitBase.WRITER_IR_SCHEMA,
            directory=root,
        ),
    }


def revise_generate_revision(
    base_ir_path: str,
    revision_context_path: str,
    revise_task_path: str,
) -> dict:
    """Revise the document, write the PatchSet to Feishu, and return all artifacts."""
    root = _run_root('revision')
    writer = WriterRevisionToolkit()
    base_ir = _read_json_string(base_ir_path)
    context = _read_json_string(revision_context_path)
    task = _read_json_string(revise_task_path)
    located = writer.locate_revision_target(
        writing_task_json=task,
        writer_document_json=base_ir,
        writing_context_json=context,
    )
    plan = writer.generate_modify_plan(
        writing_task_json=task,
        writer_document_json=base_ir,
        locate_result_json=located,
        writing_context_json=context,
    )
    patch_set = writer.generate_patch_set(
        writer_document_json=base_ir,
        modify_plan_json=plan,
        writing_context_json=context,
    )
    applied = _json_loads(writer.apply_patch(
        writer_document_json=base_ir,
        patch_set_json=patch_set,
        writing_context_json=context,
    ), {})

    write_back = WriterResourceTools(
        llm=None,
        artifact_store=str(root),
    ).apply_patch_to_document(
        source_document=_read_json_file(base_ir_path),
        patch_set=_json_loads(patch_set, {}),
    )
    write_result_json = _read_json_string(_result_path(write_back))
    artifact_paths = (write_back.get('metadata') or {}).get('artifact_paths') or {}
    persisted_document_path = str(artifact_paths.get('persisted_document') or '')
    if not persisted_document_path:
        raise ValueError(f'LazyLLM writer tool did not return persisted_document: {write_back!r}')

    return {
        'locate_result': _save_json_artifact(
            'locate_result', located, writer_schema('revision.LocateResult'), directory=root,
        ),
        'modify_plan': _save_json_artifact(
            'modify_plan', plan, writer_schema('revision.ModifyPlan'), directory=root,
        ),
        'patch_set': _save_json_artifact(
            'patch_set', patch_set, writer_schema('revision.PatchSet'), directory=root,
        ),
        'patch_result': _save_json_artifact(
            'patch_result', json.dumps(applied.get('patch_result') or {}, ensure_ascii=False),
            writer_schema('revision.PatchResult'), directory=root,
        ),
        'candidate_ir': _save_json_artifact(
            'candidate_ir', _writer_document_json(
                json.dumps(applied.get('revised_document') or {}, ensure_ascii=False),
                ui_editable=False,
            ),
            WriterToolkitBase.WRITER_IR_SCHEMA, directory=root,
        ),
        'write_result': _save_json_artifact(
            'write_result', write_result_json, writer_schema('revision.PatchResult'), directory=root,
        ),
        'synced_snapshot': _save_json_artifact(
            'synced_ir', _writer_document_json(
                _read_json_string(persisted_document_path), ui_editable=True,
            ),
            WriterToolkitBase.WRITER_IR_SCHEMA, directory=root,
        ),
    }
