"""Workflow API routes.

Routes:
    POST /api/writer/documents:sync      Persist a LazyMind WriterDocument edit.
    POST /api/writer/documents:convert   Convert Writer Markdown and LMD content.
    POST /api/subagent/tasks:cancel      LazyMind task cancellation callback.
"""
from __future__ import annotations

import base64
import inspect
import json
import logging
import os
import platform
import shutil
import sys
import tempfile
from pathlib import Path
from typing import Any, Dict, List, Literal, Optional

import httpx
import yaml
from fastapi import APIRouter, HTTPException, Response
from pydantic import BaseModel, Field

from lazymind.chat.engine.tool_auth import inject_tool_config
from lazyllm.tools.writer.data_models import WriterDocument
from lazyllm.tools.writer.utils import convert_writer_content
from lazymind.chat.engine.tools.writer import sync_writer_documents
from lazymind.config import config
from lazymind.model_config import inject_model_config
from lazymind.workflow_sdk import WorkflowClient
from lazymind.workflow_toolkit import load_workflow_package_tools

router = APIRouter()
logger = logging.getLogger(__name__)


def _env_flag_enabled(*names: str) -> bool:
    return any((os.environ.get(name) or '').strip().lower() == 'true' for name in names)


def _resolve_executable(command: str) -> Optional[str]:
    command = (command or '').strip()
    if not command:
        return None
    candidate = Path(command)
    return str(candidate) if candidate.is_file() else shutil.which(command)


def _local_platform_target() -> tuple[str, str]:
    target_platform = (
        'windows'
        if sys.platform == 'win32'
        else ('darwin' if sys.platform == 'darwin' else 'linux')
    )
    machine = platform.machine().strip().lower()
    target_arch = 'x64' if machine in {'amd64', 'x86_64'} else ('arm64' if machine in {'arm64', 'aarch64'} else machine)
    return target_platform, target_arch


def _local_editable_ppt_deps_dir(export_cli: Path) -> Path:
    deps_env = (os.environ.get('LAZYMIND_PPT_EXPORT_DEPS') or '').strip()
    return Path(deps_env) if deps_env else export_cli.parent


def _local_editable_pptx_installed() -> bool:
    export_cli = Path((os.environ.get('LAZYMIND_PPT_EXPORT_CLI') or '').strip())
    if not export_cli.is_file():
        return False
    install_dir = _local_editable_ppt_deps_dir(export_cli)
    node_modules = export_cli.parent / 'node_modules'
    if not (node_modules / 'pptxgenjs').is_dir() and (install_dir / 'node_modules' / 'pptxgenjs').is_dir():
        node_modules = install_dir / 'node_modules'
    manifest_path = install_dir / 'bundle-manifest.json'
    if (
        not manifest_path.is_file()
        or not (node_modules / 'pptxgenjs').is_dir()
        or not (node_modules / 'playwright').is_dir()
    ):
        return False
    try:
        manifest = json.loads(manifest_path.read_text(encoding='utf-8'))
    except (OSError, json.JSONDecodeError):
        return False
    target_platform, target_arch = _local_platform_target()
    if manifest.get('platform') != target_platform or manifest.get('arch') != target_arch:
        return False
    if not _resolve_executable(os.environ.get('LAZYMIND_PPT_EXPORT_NODE') or 'node'):
        return False
    browsers_dir = Path(
        (os.environ.get('PLAYWRIGHT_BROWSERS_PATH') or '').strip() or str(install_dir / 'browsers')
    )
    patterns = (
        'chromium_headless_shell-*/**/chrome-headless-shell',
        'chromium_headless_shell-*/**/chrome-headless-shell.exe',
        'chromium_headless_shell-*/**/headless_shell',
        'chromium-*/**/chrome',
        'chromium-*/**/chrome.exe',
        'chromium-*/**/Chromium',
    )
    return browsers_dir.is_dir() and any(
        candidate.is_file() for pattern in patterns for candidate in browsers_dir.glob(pattern)
    )


def editable_pptx_enabled() -> bool:
    if (os.environ.get('LAZYMIND_RUNTIME_MODE') or '').strip().lower() == 'local':
        return _local_editable_pptx_installed()
    return _env_flag_enabled('LAZYMIND_OUTPUT_EDITABLE_PPT', 'OUTPUT_EDITABLE_PPT')


class TaskCancelRequest(BaseModel):
    task_id: Optional[str] = None
    conversation_id: Optional[str] = None


class TaskCancelResponse(BaseModel):
    ok: bool


class WorkflowDriverRequest(BaseModel):
    workflow_id: str
    step_id: str
    step_result: str
    acceptance: str = ''
    driver_prompt: str = ''
    session_id: Optional[str] = None
    history_files_per_turn: Optional[Dict[str, List[str]]] = None
    llm_config: Optional[Dict[str, Any]] = None
    workflow_artifacts_summary: Optional[str] = None


class WorkflowDriverResponse(BaseModel):
    message: str


class WriterDocumentSyncRequest(BaseModel):
    source_document: WriterDocument
    revised_document: WriterDocument
    tool_config: Dict[str, Any] = Field(default_factory=dict)


class WriterDocumentConvertRequest(BaseModel):
    source_format: Literal['markdown', 'lmd', 'writer_document']
    target_format: Literal['markdown', 'lmd']
    content: str
    document_id: str = 'writer-document'


class PptExportPage(BaseModel):
    html: str
    notes: str = ''


class PptExportRequest(BaseModel):
    pages: List[PptExportPage]
    filename: Optional[str] = None


def _writer_artifact(result: dict, key: Optional[str] = None) -> str:
    path = result.get('artifact_path') if key is None else (
        (result.get('metadata') or {}).get('artifact_paths') or {}
    ).get(key)
    if not path:
        raise ValueError(f'Writer tool did not return artifact {key or "primary"!r}.')
    return path


class WorkflowActionInvokeRequest(BaseModel):
    workflow_id: str
    revision_id: str
    tree_hash: str = ''
    user_id: str = ''
    action: str
    phase: Literal['preview', 'execute']
    slot: str
    artifact: Any = None
    arguments: Dict[str, Any] = Field(default_factory=dict)
    artifact_store: str = ''
    llm_config: Optional[Dict[str, Any]] = None
    tool_config: Optional[Dict[str, Any]] = None


@router.post('/api/writer/documents:sync', summary='Persist an edited WriterDocument to its provider')
def sync_writer_document(request: WriterDocumentSyncRequest) -> dict:
    if not request.tool_config.get('feishu'):
        raise HTTPException(status_code=400, detail='tool_config.feishu is required.')

    try:
        inject_tool_config(request.tool_config)
        return sync_writer_documents(request.source_document, request.revised_document)
    except ValueError as exc:
        raise HTTPException(status_code=400, detail=str(exc)) from exc
    except Exception as exc:
        raise HTTPException(status_code=502, detail=str(exc)) from exc


@router.post('/api/writer/documents:convert', summary='Convert Writer Markdown and LMD content')
def convert_writer_document(request: WriterDocumentConvertRequest) -> Response:
    try:
        converted = convert_writer_content(
            request.content,
            request.source_format,
            request.target_format,
            document_id=request.document_id,
        )
    except (TypeError, ValueError) as exc:
        raise HTTPException(status_code=422, detail=str(exc)) from exc
    media_type = 'text/markdown; charset=utf-8' if request.target_format == 'markdown' \
        else 'application/vnd.lazymind.writer+json; charset=utf-8'
    return Response(content=converted.encode('utf-8'), media_type=media_type)


def _action_definition(
    request: WorkflowActionInvokeRequest,
) -> tuple[Dict[str, Any], Dict[str, Any]]:
    package = WorkflowClient(
        str(config['core_api_url']).rstrip('/'), request.user_id,
        host='lazymind', transport=httpx,
    ).get_workflow(request.workflow_id, request.revision_id).result
    if str(package.get('revision_id') or '') != request.revision_id:
        raise HTTPException(status_code=409, detail='workflow revision changed')
    if request.tree_hash and str(package.get('tree_hash') or '') != request.tree_hash:
        raise HTTPException(status_code=409, detail='workflow tree hash changed')
    files = package.get('files') if isinstance(package.get('files'), dict) else {}
    encoded = files.get('workflow.yaml')
    if not encoded:
        raise HTTPException(status_code=404, detail='workflow definition not found')
    raw = base64.b64decode(encoded) if isinstance(encoded, str) else bytes(encoded)
    document = yaml.safe_load(raw.decode('utf-8')) or {}
    actions = document.get('artifact_actions') or {}
    definition = actions.get(request.action) if isinstance(actions, dict) else None
    if not isinstance(definition, dict):
        raise HTTPException(status_code=404, detail='artifact action not found')
    return definition, package


@router.post('/api/workflow/actions:invoke', summary='Invoke a Workflow-owned artifact action')
def invoke_workflow_action(request: WorkflowActionInvokeRequest) -> Dict[str, Any]:
    definition, package = _action_definition(request)
    if request.slot not in (definition.get('slots') or []):
        raise HTTPException(status_code=400, detail='action is not enabled for this slot')
    tool_name = str(definition.get(f'{request.phase}_tool') or '')
    try:
        tools = load_workflow_package_tools(
            package, [tool_name], request.workflow_id, request.revision_id,
        ) if tool_name else {}
    except Exception as exc:
        raise HTTPException(status_code=500, detail='artifact action tool is unavailable') from exc
    tool = tools.get(tool_name)
    if tool is None:
        raise HTTPException(status_code=500, detail='artifact action tool is unavailable')

    kwargs = dict(request.arguments)
    reserved = {'artifact', 'artifact_store', 'slot'} & kwargs.keys()
    if reserved:
        raise HTTPException(status_code=400, detail=f'reserved arguments: {sorted(reserved)}')
    parameters = inspect.signature(tool).parameters
    if 'artifact' in parameters:
        kwargs['artifact'] = request.artifact
    if 'artifact_store' in parameters:
        kwargs['artifact_store'] = request.artifact_store
    if 'slot' in parameters:
        kwargs['slot'] = request.slot
    try:
        inject_model_config(request.llm_config or {})
        inject_tool_config(request.tool_config or {})
        return {'result': tool(**kwargs)}
    except ValueError as exc:
        code = str(getattr(exc, 'error_code', 'WORKFLOW_ACTION_INVALID'))
        detail: Dict[str, Any] = {'code': code, 'message': str(exc)}
        detail.update(getattr(exc, 'details', {}) or {})
        status = 409 if code in {'SELECTION_AMBIGUOUS', 'SELECTION_STALE'} else 422
        raise HTTPException(status_code=status, detail=detail) from exc
    except TypeError as exc:
        raise HTTPException(
            status_code=422,
            detail={'code': 'WORKFLOW_ACTION_INVALID', 'message': str(exc)},
        ) from exc
    except Exception as exc:
        logger.exception(
            'Workflow artifact action failed: workflow=%s action=%s phase=%s',
            request.workflow_id, request.action, request.phase,
        )
        raise HTTPException(
            status_code=502,
            detail={'code': 'WORKFLOW_ACTION_FAILED', 'message': str(exc)},
        ) from exc


@router.post('/api/workflow/driver', response_model=WorkflowDriverResponse,
             summary='Evaluate a terminal Workflow attempt')
async def workflow_driver(req: WorkflowDriverRequest) -> WorkflowDriverResponse:
    from lazymind.chat.workflow.driver_agent import DriverEvaluationError, evaluate_step

    try:
        result = evaluate_step(
            workflow_id=req.workflow_id, step_id=req.step_id, step_result=req.step_result,
            acceptance=req.acceptance, driver_prompt=req.driver_prompt,
            session_id=req.session_id,
            user_files=[p for paths in (req.history_files_per_turn or {}).values() for p in paths] or None,
            llm_config=req.llm_config,
            workflow_artifacts_summary=req.workflow_artifacts_summary,
        )
    except DriverEvaluationError as exc:
        raise HTTPException(status_code=503, detail=str(exc)) from exc
    return WorkflowDriverResponse(message=result['message'])


@router.post('/api/workflow/task-cancel', response_model=TaskCancelResponse, summary='Cancel a running SubAgent task')
async def task_cancel(req: TaskCancelRequest) -> TaskCancelResponse:
    """Enqueue a cancel signal for a running SubAgent ReAct loop.

    Called by the Go EventLoop when the user stops chat generation.
    The signal is written into the FileSystemQueue(klass='cancel') scoped
    to the task's sid, causing the ReAct stop_condition to raise CancelledError.

    Supports two identification modes:
    - task_id: direct task/session ID (original SubAgent path)
    - conversation_id: looks up the active chat session from _active_sessions
    """
    import json as _json
    from lazymind.chat.service.chat_service import _active_sessions
    try:
        import lazyllm
        from lazyllm.common.queue import FileSystemQueue

        sid: Optional[str] = None
        if req.conversation_id:
            sid = _active_sessions.get(req.conversation_id)
        elif req.task_id:
            sid = req.task_id

        if not sid:
            return TaskCancelResponse(ok=False)

        lazyllm.globals._init_sid(sid=sid)
        FileSystemQueue(klass='cancel').enqueue(_json.dumps({'tag': 'cancel'}))
    except Exception as exc:
        raise HTTPException(status_code=500, detail=str(exc))
    return TaskCancelResponse(ok=True)


@router.get('/api/workflow/ppt/capabilities', summary='PPT export capability flags')
async def ppt_export_capabilities() -> dict:
    enabled = editable_pptx_enabled()
    return {
        'editable_pptx': enabled,
        'mode': 'editable' if enabled else 'raster',
        'dependency_missing': not enabled,
    }


@router.post('/api/workflow/ppt/export', summary='Convert HTML slides to editable PPTX on demand')
async def export_ppt_from_html(req: PptExportRequest):
    """Write preview HTML into a temporary deck and invoke the configured exporter."""
    import asyncio
    import re
    import subprocess
    import urllib.error
    import urllib.request
    import uuid

    from fastapi.responses import FileResponse
    from starlette.background import BackgroundTask

    if not editable_pptx_enabled():
        local = (os.environ.get('LAZYMIND_RUNTIME_MODE') or '').strip().lower() == 'local'
        detail = (
            'Editable PPTX export dependency is not installed in the local runtime.'
            if local else
            'Editable PPTX export is disabled. Deploy with LAZYMIND_OUTPUT_EDITABLE_PPT=true, '
            'or use browser raster export.'
        )
        raise HTTPException(status_code=501, detail=detail)

    pages = [page for page in (req.pages or []) if (page.html or '').strip()]
    if not pages:
        raise HTTPException(status_code=400, detail='pages with html are required')
    if len(pages) > 30:
        raise HTTPException(status_code=400, detail='too many pages (max 30)')

    def sanitize_html(raw: str) -> str:
        value = (raw or '').strip()
        value = re.sub(r'(?is)<think\b[^>]*>[\s\S]*?</think>', '', value).strip()
        value = re.sub(r'(?is)^<think\b[^>]*>[\s\S]*?(?=<!doctype|<html\b|```)', '', value).strip()
        fence = re.search(r'(?is)```(?:html)?\s*\n([\s\S]*?)```', value)
        if fence:
            value = fence.group(1).strip()
        document = re.search(r'(?is)(<!doctype\s+html\b[\s\S]*?</html>|<html\b[\s\S]*?</html>)', value)
        return document.group(1).strip() if document else value

    workspace = Path(os.environ.get('LAZYMIND_SUBAGENT_WORKSPACE') or '/data/subagent')
    job_id = f'ppt_export_{uuid.uuid4().hex[:12]}'
    deck_dir = workspace / '.ppt_on_demand' / job_id
    pages_dir = deck_dir / 'pages'
    pages_dir.mkdir(parents=True, exist_ok=True)
    for index, page in enumerate(pages, start=1):
        (pages_dir / f'page_{index:03d}.html').write_text(sanitize_html(page.html), encoding='utf-8')
        if page.notes.strip():
            (pages_dir / f'page_{index:03d}.notes.txt').write_text(page.notes.strip(), encoding='utf-8')
    (deck_dir / 'task_pack.json').write_text(
        json.dumps({'deck_id': job_id, 'deck_dir': str(deck_dir)}, ensure_ascii=False), encoding='utf-8'
    )
    (deck_dir / 'review.md').write_text('# on-demand export\n', encoding='utf-8')

    export_cli = (os.environ.get('LAZYMIND_PPT_EXPORT_CLI') or '').strip()
    export_node = _resolve_executable(os.environ.get('LAZYMIND_PPT_EXPORT_NODE') or 'node')
    if export_cli and export_node and Path(export_cli).is_file():
        options: Dict[str, Any] = {}
        if os.name == 'nt':
            options['creationflags'] = subprocess.CREATE_NO_WINDOW
        export_env = os.environ.copy()
        if export_env.get('LAZYMIND_NODE_RUN_AS_NODE', '').strip().lower() == 'true':
            export_env['ELECTRON_RUN_AS_NODE'] = '1'
        process = await asyncio.create_subprocess_exec(
            export_node, export_cli, '--deck-dir', str(deck_dir), '--force',
            stdout=asyncio.subprocess.PIPE, stderr=asyncio.subprocess.PIPE,
            env=export_env, **options,
        )
        stdout, stderr = await process.communicate()
        raw = stdout.decode('utf-8', errors='replace')
        if process.returncode != 0:
            detail = stderr.decode('utf-8', errors='replace') or raw
            shutil.rmtree(deck_dir, ignore_errors=True)
            raise HTTPException(status_code=502, detail=f'ppt-export failed: {detail[-1500:]}')
    else:
        export_base = (
            os.environ.get('LAZYMIND_PPT_EXPORT_URL') or os.environ.get('PPT_EXPORT_URL')
            or 'http://ppt-export:8099'
        ).rstrip('/')
        url = f'{export_base}/export'
        request = urllib.request.Request(
            url, data=json.dumps({'deck_dir': str(deck_dir)}).encode('utf-8'),
            headers={'Content-Type': 'application/json'}, method='POST',
        )
        try:
            with urllib.request.urlopen(request, timeout=1200) as response:
                raw = response.read().decode('utf-8', errors='replace')
        except urllib.error.HTTPError as exc:
            detail = exc.read().decode('utf-8', errors='replace') if exc.fp else str(exc)
            shutil.rmtree(deck_dir, ignore_errors=True)
            raise HTTPException(status_code=502, detail=f'ppt-export failed: {detail[:1500]}') from exc
        except Exception as exc:
            shutil.rmtree(deck_dir, ignore_errors=True)
            raise HTTPException(status_code=503, detail=f'ppt-export unreachable at {url}: {exc}') from exc

    try:
        payload = json.loads(raw) if raw else {}
    except json.JSONDecodeError:
        payload = {}
    ok = isinstance(payload, dict) and (payload.get('status') == 'ok' or payload.get('success') is True)
    pptx_path = deck_dir / f'{job_id}.pptx'
    if not pptx_path.exists():
        candidates = list(deck_dir.glob('*.pptx'))
        pptx_path = candidates[0] if candidates else pptx_path
    if not ok or not pptx_path.exists():
        detail = (
            str(payload.get('error') or payload.get('detail') or payload.get('reason') or '')
            if isinstance(payload, dict)
            else ''
        )
        shutil.rmtree(deck_dir, ignore_errors=True)
        raise HTTPException(status_code=502, detail=detail or 'PPTX not produced')

    filename = (req.filename or f'{job_id}.pptx').strip()
    if not filename.lower().endswith('.pptx'):
        filename += '.pptx'
    temp_file = tempfile.NamedTemporaryFile(suffix='.pptx', delete=False)
    temp_path = Path(temp_file.name)
    temp_file.close()
    shutil.copy2(pptx_path, temp_path)
    shutil.rmtree(deck_dir, ignore_errors=True)

    def cleanup(path: str) -> None:
        try:
            Path(path).unlink(missing_ok=True)
        except OSError:
            pass

    return FileResponse(
        path=str(temp_path),
        media_type='application/vnd.openxmlformats-officedocument.presentationml.presentation',
        filename=filename,
        background=BackgroundTask(cleanup, str(temp_path)),
    )
