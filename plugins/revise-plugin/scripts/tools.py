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

from lazyllm.tools.writer.data_models import TargetDocument
from lazyllm.tools.writer.tools import WriterResourceTools
from lazyllm.tools.writer.utils import save_artifact_json

from lazymind.chat.engine.subagent.context import require_context
from lazymind.chat.engine.tools.writer import (
    WriterToolkitBase,
    writer_artifact_schema,
    writer_schema,
)


_FEISHU_URL_RE = re.compile(
    r"https?://[^\s<>\"']*(?:feishu\.cn|larksuite\.com)/[^\s<>\"']+",
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
    """Load the immutable source and editable starting IR from Feishu."""
    root = _run_root('load')
    target = TargetDocument(uri=_feishu_url(user_input), adapter='feishu')
    result = WriterResourceTools(llm=None, artifact_store=str(root)).document_to_docir(target)
    loaded_doc_ir_json = _read_json_string(_result_path(result))

    source_ir_path = _save_json_artifact(
        'source_ir', loaded_doc_ir_json, writer_schema('docir.DocIR'), directory=root,
    )
    return {
        'source_ir': source_ir_path,
        'working_ir': _save_json_artifact(
            'working_ir', loaded_doc_ir_json, writer_schema('docir.DocIR'), directory=root,
        ),
        'remote_snapshot': _save_json_artifact(
            'remote_snapshot', loaded_doc_ir_json, writer_schema('docir.DocIR'), directory=root,
        ),
    }


def revise_build_context(user_input: str, source_ir_path: str) -> dict:
    """Build the model context for revision as its own observable plugin step."""
    root = _run_root('context')
    writer = WriterToolkitBase()
    target = TargetDocument(uri=_feishu_url(user_input), adapter='feishu')
    revise_task_json = writer.build_revise_task(
        query=user_input,
        target_document_json=json.dumps(target.model_dump(), ensure_ascii=False),
    )
    context_json = writer.create_writing_context(
        writing_task_json=revise_task_json,
        resource_profiles_json='[]',
        doc_ir_json=_read_json_string(source_ir_path),
    )
    return {
        'revision_context': _save_json_artifact(
            'revision_context', context_json, writer_schema('context.WritingContext'), directory=root,
        ),
    }


def revise_generate_revision(base_ir_path: str, revision_context_path: str, query: str) -> dict:
    """Delegate the complete revision pipeline to common writer tools."""
    root = _run_root('revision')
    writer = WriterToolkitBase()
    base_ir = _read_json_string(base_ir_path)
    context = _read_json_string(revision_context_path)

    task = writer.build_revise_task(query=query)
    located = writer.locate_revision_target(task, base_ir, context)
    plan = writer.generate_modify_plan(task, base_ir, located, context)
    patch_set = writer.generate_patch_set(base_ir, plan, context)
    applied = _json_loads(writer.apply_patch(base_ir, patch_set, context), {})

    return {
        'revise_task': _save_json_artifact(
            'revise_task', task, writer_schema('task.WritingTask'), directory=root,
        ),
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
            'candidate_ir', json.dumps(applied.get('revised_doc_ir') or {}, ensure_ascii=False),
            writer_schema('docir.DocIR'), directory=root,
        ),
    }


def revise_write_back(candidate_ir_path: str, remote_snapshot_path: str) -> dict:
    """Delegate optimistic DocIR write-back to LazyLLM WriterResourceTools."""
    root = _run_root('write-back')
    result = WriterResourceTools(
        llm=None,
        artifact_store=str(root),
    ).write_docir_to_document(
        source_doc_ir=remote_snapshot_path,
        revised_doc_ir=candidate_ir_path,
    )
    write_result_json = _read_json_string(_result_path(result))
    candidate_ir_json = _read_json_string(candidate_ir_path)
    return {
        'write_result': _save_json_artifact(
            'write_result', write_result_json, writer_artifact_schema('write_result'), directory=root,
        ),
        'synced_snapshot': _save_json_artifact(
            'synced_snapshot', candidate_ir_json, writer_schema('docir.DocIR'), directory=root,
        ),
    }
