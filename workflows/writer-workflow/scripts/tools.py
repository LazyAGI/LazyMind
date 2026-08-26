"""Artifact-path adapters for the unified writer workflow.

The workflow owns orchestration only. Writing, revision, document conversion, and
provider synchronization continue to use the existing LazyMind/LazyLLM writer
tooling and the existing workflow artifact mechanism.
"""
from __future__ import annotations

import hashlib
import json
import logging
import re
import tempfile
import uuid
from pathlib import Path
from typing import Any, Callable, Iterator, Literal, Mapping

from lazyllm import AutoModel
from pydantic import BaseModel, ConfigDict
from lazyllm.tools.writer.data_models import (
    ContentRef,
    MediaAssetLibrary,
    ModifyInstruction,
    ModifyPlan,
    PatchResult,
    PatchSet,
    ShortWritingPlan,
    StringReplaceSet,
    TargetDocument,
    VisualPlan,
    WriterDocument,
)
from lazyllm.tools.writer.numbering import (
    build_numbering_view_from_ir,
    build_numbering_view_from_markdown,
    compute_numbering,
    dematerialize_markdown,
    dematerialize_ir,
    materialize_ir,
    materialize_markdown,
)
from lazyllm.tools.writer.tools import (
    WriterDraftingTools,
    WriterPlanningTools,
    WriterResourceTools,
    WriterRevisionTools,
)
from lazyllm.tools.writer.tools.revision_tools import apply_patch_to_ir
from lazyllm.tools.writer.utils import (
    load_artifact_json,
    parse_document_markdown,
    save_artifact_json,
)
from lazyllm.tools.tools.search import (
    BingSearch,
    BochaSearch,
    GoogleSearch,
    TavilySearch,
)
from lazymind.chat.engine.subagent.context import require_context
from lazymind.chat.engine.tools.writer import (
    DraftMarkdownStreamEventEmitter,
    WriterCreateToolkit,
    WriterResourceToolkit,
    WriterRevisionToolkit,
    WriterToolkitBase,
    sync_writer_documents,
    writer_schema,
)
from lazymind.chat.engine.tools.multimodal import image_generator
from lazymind.model_config import is_model_role_available

WRITER_IMAGE_ACQUISITION_PROMPT = '''Create one professional visual for a document.

Visual type: {visual_type}
The visual must communicate: {purpose}

Keep the composition clear and suitable for insertion into a document. Avoid watermarks,
brand logos, decorative filler, and small unreadable text. Return exactly one image.
'''


LOG = logging.getLogger(__name__)

WRITER_DEFAULT_STRUCTURE_MODE: Literal['flat', 'sectioned'] = 'sectioned'
_WRITER_STRUCTURE_CLASSIFIER_PROMPT = '''Classify the final presentation structure for a new
Writer document. Return exactly one JSON object and nothing else:
{"structure_mode":"flat|sectioned|unclear"}

Apply these rules in order:
1. An explicit presentation requirement overrides length. Chapters, sections, or subheadings
   mean sectioned. Continuous prose, or explicitly no chapters, sections, or subheadings, means
   flat. Asking for an outline as a planning step does not by itself require sectioned output.
2. With no explicit presentation requirement, a requested length at or below 1200 Chinese
   characters/words means flat; above 1200 means sectioned. An unquantified short article means
   flat and an unquantified long article means sectioned.
3. Return unclear when presentation and length are both unclear, when explicit requirements
   conflict, or when a mentioned length is not clearly the requested output length. Never infer
   length from topic complexity.

Examples:
- 写一篇1000字的文章 -> flat
- 写一篇1000字的文章，要有小标题 -> sectioned
- 写一篇2000字的文章，不要小标题 -> flat
- write a 900-word article with subheadings -> sectioned
- write an article about spring -> unclear
'''

_KB_EVIDENCE_TOOL_NAMES = {
    'KBToolkit_kb_search',
    'KBToolkit_kb_keyword_search',
    'KBToolkit_kb_get_parent_node',
    'KBToolkit_kb_get_window_nodes',
}


class WriterCommand(BaseModel):
    """Workflow-private control decision for one user writing request."""

    model_config = ConfigDict(extra='forbid')

    action: Literal['create', 'use_outline', 'rewrite', 'revise', 'read']
    source_role: Literal['none', 'outline', 'document']
    target_stage: Literal['prepared', 'outline', 'document']
    next_step: Literal['outline', 'write_flat_document', 'write_document', '__end__']
    structure_mode: Literal['flat', 'sectioned'] = 'sectioned'
    user_instruction: str
    source_ref: str | None = None
    target_ref: str | None = None
    request_fingerprint: str


WriterCommand.model_rebuild(_types_namespace={'Literal': Literal})


def _writer_request_fingerprint(user_input: str) -> str:
    normalized = ' '.join(str(user_input or '').split())
    return hashlib.sha256(normalized.encode('utf-8')).hexdigest()


def _emit_writer_progress(current_phase: str, **details: Any) -> None:
    """Publish writer internals through the existing task phase channel."""
    require_context().emit({
        'type': 'progress',
        # The subagent runner keeps progress monotonic. Reusing its initial value
        # updates only current_phase and leaves the existing percentage policy intact.
        'progress': 5,
        'current_phase': current_phase,
        **details,
    })


def _authoritative_writer_user_input(user_input: str) -> str:
    """Prefer the immutable workflow request over an agent paraphrase."""
    ctx = require_context()
    authoritative = str((ctx.params or {}).get('user_input') or '').strip()
    return authoritative or str(user_input or '').strip()


def _has_verified_kb_evidence() -> bool:
    """Return whether this prepare task has a successful KB retrieval result."""
    ctx = require_context()
    try:
        steps = ctx.db.load_steps(ctx.task_id)
    except Exception as exc:  # noqa: BLE001 - missing provenance must fail closed.
        LOG.warning('[Writer] Cannot verify knowledge_text provenance: %s', exc)
        return False
    for step in steps:
        if step.get('role') != 'tool':
            continue
        for result in (step.get('content') or {}).get('tool_results') or []:
            if result.get('name') not in _KB_EVIDENCE_TOOL_NAMES:
                continue
            raw = result.get('result')
            try:
                payload = json.loads(raw) if isinstance(raw, str) else raw
            except (TypeError, ValueError):
                payload = None
            if isinstance(payload, dict) and payload.get('success') is True:
                return True
            if isinstance(raw, str) and raw.startswith('[Large result offloaded to file'):
                return True
    return False


def _verified_knowledge_text(knowledge_text: str) -> str:
    normalized = str(knowledge_text or '').strip()
    if not normalized:
        return ''
    if _has_verified_kb_evidence():
        return normalized
    LOG.warning(
        '[Writer] Ignoring knowledge_text without a preceding successful KB retrieval.',
    )
    return ''


def _authoritative_writer_input_path(
    key: str | tuple[str, ...],
    supplied_path: str = '',
    *,
    require_workflow_binding: bool = False,
) -> str:
    """Resolve a path from immutable Workflow bindings, never an agent guess."""
    ctx = require_context()
    remote_inputs = (ctx.params or {}).get('remote_inputs') or {}
    keys = (key,) if isinstance(key, str) else key
    authoritative = next((
        str(remote_inputs.get(candidate) or '').strip()
        for candidate in keys
        if str(remote_inputs.get(candidate) or '').strip()
    ), '')
    step_id = str((ctx.params or {}).get('step_id') or '').strip()
    if step_id in {'outline', 'write_flat_document', 'write_document'}:
        if require_workflow_binding and not authoritative:
            raise ValueError(
                f'{keys[0]} is missing from authoritative workflow inputs.'
            )
        # A Workflow step may use only materialized slot bindings. In particular,
        # never accept a guessed path for an optional input that was not bound.
        return authoritative
    return authoritative or str(supplied_path or '').strip()


def _load_writer_command(path: str) -> WriterCommand:
    if not path:
        raise ValueError('writer_command_path is required.')
    return WriterCommand.model_validate(_read_json_file(path))


def _writer_structure_payload(value: Any) -> dict[str, Any]:
    if isinstance(value, dict):
        return value
    text = str(value or '').strip()
    fenced = re.search(r'```(?:json)?\s*([\s\S]*?)```', text, re.IGNORECASE)
    if fenced:
        text = fenced.group(1).strip()
    decoder = json.JSONDecoder()
    objects: list[dict[str, Any]] = []
    for match in re.finditer(r'\{', text):
        try:
            payload, _ = decoder.raw_decode(text, match.start())
        except json.JSONDecodeError:
            continue
        if isinstance(payload, dict):
            objects.append(payload)
    if not objects:
        raise ValueError('Writer structure classifier returned no JSON object.')
    return objects[-1]


def writer_classify_structure(user_input: str) -> Literal['flat', 'sectioned']:
    """Classify a new document inside prepare; uncertainty keeps the legacy route."""
    request = str(user_input or '').strip()
    if not request:
        return WRITER_DEFAULT_STRUCTURE_MODE
    try:
        raw = AutoModel(model='llm')(
            f'{_WRITER_STRUCTURE_CLASSIFIER_PROMPT}\nCurrent request:\n{request[:4000]}',
            response_format={'type': 'json_object'},
            stream_output=False,
        )
        structure_mode = str(
            _writer_structure_payload(raw).get('structure_mode') or ''
        ).strip().lower()
    except Exception as exc:  # noqa: BLE001 - classification must fail closed.
        LOG.warning(
            '[Writer] Structure classification failed; defaulting to %s: %s',
            WRITER_DEFAULT_STRUCTURE_MODE,
            exc,
        )
        return WRITER_DEFAULT_STRUCTURE_MODE
    if structure_mode == 'flat':
        return 'flat'
    if structure_mode == 'sectioned':
        return 'sectioned'
    LOG.info(
        '[Writer] Structure classification was unclear; defaulting to %s.',
        WRITER_DEFAULT_STRUCTURE_MODE,
    )
    return WRITER_DEFAULT_STRUCTURE_MODE


def writer_resolve_command(
    user_input: str,
    action: Literal['create', 'use_outline', 'rewrite', 'revise', 'read'],
    source_role: Literal['none', 'outline', 'document'],
    target_stage: Literal['prepared', 'outline', 'document'] = 'document',
    source_ref: str = '',
    target_ref: str = '',
    existing_writer_command_path: str = '',
) -> str:
    """Create or reuse the sole Writer control decision for the current request."""
    ctx = require_context()
    step_id = str((ctx.params or {}).get('step_id') or '').strip()
    if step_id and step_id != 'prepare':
        raise ValueError('WriterCommand can only be created during the prepare step.')
    user_input = _authoritative_writer_user_input(user_input)
    existing_writer_command_path = _authoritative_writer_input_path(
        'writer_command', existing_writer_command_path,
    )
    fingerprint = _writer_request_fingerprint(user_input)
    if existing_writer_command_path:
        existing = _load_writer_command(existing_writer_command_path)
        if existing.request_fingerprint == fingerprint:
            return existing_writer_command_path

    if action == 'create' and source_role != 'none':
        raise ValueError('create requires source_role="none".')
    if action == 'use_outline' and source_role != 'outline':
        raise ValueError('use_outline requires source_role="outline".')
    if action == 'rewrite' and source_role != 'document':
        raise ValueError('rewrite requires source_role="document".')
    if action == 'revise' and source_role not in {'outline', 'document'}:
        raise ValueError('revise requires source_role="outline" or "document".')
    if action == 'read' and target_stage != 'prepared':
        raise ValueError('read requires target_stage="prepared".')
    if target_stage == 'prepared' and action != 'read':
        raise ValueError('target_stage="prepared" requires action="read".')

    structure_mode = (
        writer_classify_structure(user_input)
        if action == 'create' and target_stage == 'document'
        else WRITER_DEFAULT_STRUCTURE_MODE
    )
    if action == 'read':
        next_step = '__end__'
    elif action == 'create' and target_stage == 'document' and structure_mode == 'flat':
        next_step = 'write_flat_document'
    elif action == 'rewrite' or (action == 'revise' and source_role == 'document'):
        next_step = 'write_document'
    else:
        next_step = 'outline'

    command = WriterCommand(
        action=action,
        source_role=source_role,
        target_stage=target_stage,
        next_step=next_step,
        structure_mode=structure_mode,
        user_instruction=user_input,
        source_ref=source_ref or None,
        target_ref=target_ref or None,
        request_fingerprint=fingerprint,
    )
    root = _run_root('command')
    return save_artifact_json(
        command,
        str(root / 'writer_command.json'),
        schema_name='writer-workflow.WriterCommand',
        created_by='writer-workflow-wrapper',
    )


_EXPLICIT_WRITER_MUTATION = re.compile(
    r'(?:修改|改写|重写|扩写|续写|润色|新增|添加|插入|删除|替换|调整|合并|重排|增强)'
    r'|\b(?:modify|revise|rewrite|expand|continue|polish|add|insert|delete|replace|edit)\b',
    re.IGNORECASE,
)
_EXPLICIT_OUTLINE_TARGET = re.compile(
    r'(?:\b(?:only|just)\b.{0,16}\b(?:outline|plan)\b)'
    r'|(?:(?:只|仅|只需|仅需|先).{0,12}(?:大纲|提纲))'
    r'|(?:(?:生成|写|创建|整理|修改|调整|完善|输出).{0,12}'
    r'(?:大纲|提纲)(?:即可|就行|就可以|[。！!？?]?\s*$))'
    r'|(?:(?:大纲|提纲)(?:即可|就行|就可以|[。！!？?]?\s*$))',
    re.IGNORECASE,
)
_EXPLICIT_PREPARE_ONLY = re.compile(
    r'(?:(?:只|仅|只需|仅需).{0,8}(?:准备|解析|读取|加载).{0,8}'
    r'(?:材料|文档|源文件)?(?:即可|就行|就可以|[。！!？?]?\s*$))'
    r'|(?:(?:不要|无需).{0,8}(?:生成|撰写|写).{0,8}(?:大纲|正文|文档|文章))'
    r'|(?:\b(?:prepare|read|load)\s+only\b)',
    re.IGNORECASE,
)
_SUPPLIED_OUTLINE_REQUEST = re.compile(
    r'(?:(?:根据|基于|使用|采用|用|从|提供|上传).{0,12}(?:大纲|提纲))'
    r'|(?:\b(?:from|using|supplied|uploaded)\b.{0,16}\b(?:outline|plan)\b)',
    re.IGNORECASE,
)
_EXPLICIT_REWRITE_REQUEST = re.compile(
    r'(?:重写|整体改写|整篇改写|重新组织|重构全文)'
    r'|(?:\b(?:rewrite|restructure)\b)',
    re.IGNORECASE,
)
_CLOUD_DOCUMENT_URL = re.compile(
    r"https?://[^\s<>\"']*(?:feishu\.(?:cn|com)|larksuite\.com)/[^\s<>\"']+",
    re.IGNORECASE,
)
_LOCAL_WRITER_DOCUMENT_SUFFIXES = {'.md', '.markdown', '.txt', '.lmd'}
_CHINESE_CHAR_LIMIT_RE = re.compile(
    r'(?P<prefix>不超过|至多|最多|约|大约|大概)?\s*'
    r'(?P<value>\d+(?:\.\d+)?)\s*(?P<unit>万|千)?\s*字'
    r'(?P<suffix>左右|上下|以内|以下)?'
)
_NO_VISUALS = (
    re.compile(
        r'(?:不要|不需要|无需|不用|禁止)\s*(?:使用|添加|插入|生成|展示|显示)?\s*'
        r'(?:任何\s*)?(?:图片|图像|插图|配图|视觉(?:素材|内容)?)'
        r'|不(?:使用|添加|插入|生成|展示|显示)\s*(?:任何\s*)?'
        r'(?:图片|图像|插图|配图|视觉(?:素材|内容)?)|不插图|无图',
    ),
    re.compile(
        r'\b(?:no|without)\s+(?:any\s+)?(?:images?|pictures?|illustrations?|visuals?)\b'
        r"|\b(?:do\s+not|don't)\s+(?:use|include|add|generate|insert|show|display)\s+"
        r'(?:any\s+)?(?:images?|pictures?|illustrations?|visuals?)\b',
        re.IGNORECASE,
    ),
)
_REQUIRE_INPUT_IMAGE_REUSE = re.compile(
    r'(?:必须|务必|只能|仅限|只).{0,12}复用.{0,16}(?:我)?(?:上传(?:的)?(?:原图|图片|图像)|原图)'
    r'|(?:必须|务必|只能|仅限|只).{0,12}(?:使用|采用).{0,16}'
    r'(?:我)?(?:上传(?:的)?(?:原图|图片|图像)|原图).{0,20}(?:插入|放入|嵌入)'
    r'|(?:must|only).{0,20}reuse.{0,20}(?:uploaded|original).{0,12}(?:image|picture|photo)'
    r'|(?:must|only).{0,20}use.{0,20}(?:uploaded|original).{0,12}'
    r'(?:image|picture|photo).{0,20}(?:insert|embed|include)',
    re.IGNORECASE,
)
_FORBID_IMAGE_GENERATION = re.compile(
    r'(?:不要|禁止|不得).{0,12}(?:生成|改用|替换|替代).{0,12}(?:图|图片|图像)'
    r"|(?:do\s+not|don't|never).{0,20}(?:generate|replace|substitute).{0,20}"
    r'(?:image|picture|photo)',
    re.IGNORECASE,
)
_REQUIRE_VISUALS = (
    re.compile(
        r'(?:必须|务必|一定要|要求).{0,12}'
        r'(?:包含|带有|加入|添加|插入|生成|绘制|制作|提供|使用|配上|附上|'
        r'放入|嵌入|展示).{0,8}'
        r'(?:图片|图像|插图|配图|封面图|示意图|图表|表格)'
        r'|(?:请|帮我|需要|想要).{0,8}'
        r'(?:加入|添加|插入|绘制|制作|提供|配上|附上|放入|嵌入).{0,8}'
        r'(?:图片|图像|插图|配图|封面图|示意图|图表|表格)'
        r'|配(?:上)?\s*(?:\d+|[一二两三四五六七八九十]+)?\s*(?:张|幅|个)?\s*'
        r'(?:图|图片|图像|插图|配图|封面图|示意图|图表)'
        r'|(?:插入|添加|附上|嵌入|放入).{0,6}'
        r'(?:\d+|[一二两三四五六七八九十]+)?\s*(?:张|幅|个)?\s*'
        r'(?:图片|图像|插图|配图|封面图|示意图|图表)'
        r'|生成\s*(?:\d+|[一二两三四五六七八九十]+)\s*(?:张|幅|个)\s*'
        r'(?:图片|图像|插图|配图|封面图|示意图)',
    ),
    re.compile(
        r'\b(?:must|require(?:s|d)?|please|need\s+to|want\s+to)\b.{0,20}'
        r'\b(?:include|add|insert|generate|create|provide|use|show)\b.{0,20}'
        r'\b(?:images?|pictures?|illustrations?|visuals?|charts?|diagrams?|tables?)\b',
        re.IGNORECASE,
    ),
)


def _parse_writer_request_constraints(query: str) -> dict[str, Any]:
    """Translate Writer Workflow request language into structured task policy."""
    constraints: dict[str, Any] = {}
    match = _CHINESE_CHAR_LIMIT_RE.search(query or '')
    if match is not None:
        multiplier = {'万': 10000, '千': 1000}.get(match.group('unit'), 1)
        target_chars = int(float(match.group('value')) * multiplier)
        approximate = (
            match.group('prefix') in {'约', '大约', '大概'}
            or match.group('suffix') in {'左右', '上下'}
        )
        constraints.update({
            'target_chars': target_chars,
            'max_chars': target_chars * 11 // 10 if approximate else target_chars,
        })

    no_visuals = any(pattern.search(query or '') for pattern in _NO_VISUALS)
    require_reuse = bool(_REQUIRE_INPUT_IMAGE_REUSE.search(query or ''))
    forbid_generation = bool(_FORBID_IMAGE_GENERATION.search(query or ''))
    require_visuals = not no_visuals and (
        require_reuse or any(pattern.search(query or '') for pattern in _REQUIRE_VISUALS)
    )
    if no_visuals or require_visuals or require_reuse or forbid_generation:
        constraints['visual_policy'] = {
            'allow_visuals': not no_visuals,
            'require_visuals': require_visuals,
            'require_input_image_reuse': require_reuse,
            'allow_image_generation': not (no_visuals or require_reuse or forbid_generation),
        }
    return constraints


def _resolve_prepare_control(
    user_input: str,
    suggested_operation: str,
    *,
    has_document_source: bool,
) -> tuple[str, str]:
    """Resolve prepare operation and terminal target from authoritative facts."""
    if _EXPLICIT_PREPARE_ONLY.search(user_input):
        return 'prepare_only', 'prepared'

    operation = suggested_operation
    if not has_document_source:
        operation = 'create'
    elif operation in {'create', 'prepare_only'}:
        if _SUPPLIED_OUTLINE_REQUEST.search(user_input):
            operation = 'use_outline'
        elif _EXPLICIT_REWRITE_REQUEST.search(user_input):
            operation = 'rewrite_document'
        else:
            operation = 'revise_document'

    if operation in {'rewrite_document', 'revise_document'}:
        return operation, 'document'
    if _EXPLICIT_OUTLINE_TARGET.search(user_input):
        return operation, 'outline'
    return operation, 'document'


def _workspace_root() -> Path:
    ctx = require_context()
    root = Path(ctx.workspace_path) if ctx.workspace_path else Path('/tmp')
    root.mkdir(parents=True, exist_ok=True)
    return root


def _run_root(name: str) -> Path:
    root = _workspace_root() / 'writer-workflow' / f'{name}-{uuid.uuid4().hex}'
    root.mkdir(parents=True, exist_ok=True)
    return root


def _read_json_file(path: str) -> Any:
    if Path(path).suffix.lower() in {'.md', '.markdown', '.txt'}:
        return Path(path).read_text(encoding='utf-8')
    with open(path, 'r', encoding='utf-8') as fh:
        raw = json.load(fh)
    if isinstance(raw, dict) and 'data' in raw:
        return raw['data']
    return raw


def _writer_tool_artifact_data(result: Any) -> Any:
    path = str(result.get('artifact_path') or '').strip() if isinstance(result, dict) else ''
    if not path:
        raise ValueError(f'Writer tool did not return artifact_path: {result!r}')
    return _read_json_file(path)


def _action_artifact_data(value: Any) -> Any:
    if isinstance(value, Mapping):
        if 'data' in value:
            return value['data']
        path = value.get('path')
        if isinstance(path, str) and path:
            return _read_json_file(path)
        return dict(value)
    if isinstance(value, str):
        candidate = Path(value)
        if candidate.is_file():
            return _read_json_file(value)
        try:
            return _json_loads(value, value)
        except json.JSONDecodeError:
            return value
    raise TypeError('artifact must be a JSON value, Markdown string, or file reference.')


def _action_context(document: Any) -> dict:
    return {
        'context_id': f'selection-{uuid.uuid4().hex}',
        'doc_id': document.get('document_id') if isinstance(document, dict) else None,
        'meta': {'source': 'rewrite_selection_action'},
    }


def _action_root(artifact_store: str, name: str) -> Path:
    base = Path(artifact_store) if artifact_store else Path(tempfile.gettempdir())
    root = base / 'writer-workflow' / f'{name}-{uuid.uuid4().hex}'
    root.mkdir(parents=True, exist_ok=True)
    return root


def _read_json_string(path: str) -> str:
    content = _read_json_file(path)
    return content if isinstance(content, str) else json.dumps(content, ensure_ascii=False)


def _json_loads(value: str, default: Any = None) -> Any:
    text = (value or '').strip()
    if not text:
        return default
    parsed = json.loads(text)
    if isinstance(parsed, dict) and 'data' in parsed:
        return parsed['data']
    return parsed


def _writer_document_json(
    value: str | dict,
    *,
    expected_stage: str | None = None,
    editable: bool = False,
) -> str:
    """Normalize IR while leaving Markdown content unchanged."""
    if isinstance(value, str):
        try:
            payload = _json_loads(value, {})
        except json.JSONDecodeError:
            return value
    else:
        payload = dict(value or {})
    if isinstance(payload, str):
        return payload
    document = WriterDocument.model_validate(payload)
    if expected_stage is not None and document.stage != expected_stage:
        raise ValueError(
            f'WriterDocument must have stage={expected_stage!r}; got {document.stage!r}.',
        )
    if document.metadata.get('kind') == 'step_status':
        raise ValueError('A writer status placeholder cannot be used as a document artifact.')
    if expected_stage == 'outline' and len(document.blocks) < 3:
        raise ValueError('An outline WriterDocument must contain at least three top-level blocks.')
    if editable:
        document.ui_editable = True
    return document.model_dump_json(exclude_defaults=True)


def _save_json_artifact(
    name: str,
    content_json: str,
    schema_name: str,
    *,
    directory: Path | None = None,
    extra_meta: dict[str, Any] | None = None,
) -> str:
    root = directory or _workspace_root()
    root.mkdir(parents=True, exist_ok=True)
    extension = (
        '.lmd'
        if schema_name in {
            WriterToolkitBase.WRITER_IR_SCHEMA,
            WriterToolkitBase.WRITER_BLOCK_SCHEMA,
        }
        else '.json'
    )
    return save_artifact_json(
        _json_loads(content_json, {}),
        str(root / f'{name}{extension}'),
        schema_name=schema_name,
        created_by='writer-workflow-wrapper',
        extra_meta=extra_meta,
    )


def _save_writer_document(
    name: str,
    value: str | dict,
    *,
    expected_stage: str | None = None,
    editable: bool = False,
    directory: Path | None = None,
    extra_meta: dict[str, Any] | None = None,
) -> str:
    """Persist a document as .lmd or .md according to its representation."""
    content = _writer_document_json(
        value,
        expected_stage=expected_stage,
        editable=editable,
    )
    try:
        _json_loads(content, {})
    except json.JSONDecodeError:
        root = directory or _workspace_root()
        root.mkdir(parents=True, exist_ok=True)
        path = root / f'{name}.md'
        path.write_text(content, encoding='utf-8')
        return str(path)
    return _save_json_artifact(
        name, content, WriterToolkitBase.WRITER_IR_SCHEMA, directory=directory,
        extra_meta=extra_meta,
    )


def _emit_draft_markdown_preview(document_path: str) -> None:
    """Publish a saved writer document through the draft Markdown stream."""
    try:
        document = _read_json_file(document_path)
        rendered = writer_render_document(document)
        representation = rendered.get('representation')
        if representation == 'markdown':
            markdown = str(rendered.get('document') or '')
        elif representation == 'ir':
            preview = _json_loads(
                WriterCreateToolkit().render_markdown(
                    writer_document_json=json.dumps(
                        document, ensure_ascii=False,
                    ),
                ),
                {},
            )
            markdown = str(preview.get('markdown') or '')
        else:
            markdown = ''
    except Exception:
        return
    if not markdown:
        return

    events = DraftMarkdownStreamEventEmitter(require_context().emit)
    for offset in range(0, len(markdown), 8192):
        events.feed(markdown[offset:offset + 8192])
    events.end()


def _markdown_filename(title: str) -> str:
    filename = re.sub(r'[\\/:*?"<>|\x00-\x1f]+', '_', title).strip(' ._')
    return f'{filename[:80] or "文稿"}.md'


def _save_publish_payload(payload: dict, root: Path) -> dict:
    draft_document = payload.get('draft_document') or {}
    publish_result = payload.get('publish_result') or {}
    if isinstance(publish_result, dict):
        publish_result = {
            **publish_result,
            'success': bool(publish_result.get('success', draft_document)),
        }
    return {
        'publish_result': _save_json_artifact(
            'publish_result',
            json.dumps(publish_result, ensure_ascii=False),
            writer_schema('revision.PatchResult'),
            directory=root,
        ),
        'draft_document': _save_writer_document(
            'draft_document',
            draft_document,
            editable=True,
            directory=root,
            extra_meta={
                'lazymind_provider_sync': {
                    'confirmed': True,
                    'provider': 'feishu',
                    'source': 'initial_auto',
                },
            },
        ),
        'published_link': str(payload.get('published_link') or ''),
    }


def writer_build_writing_task(query: str, representation: str = 'markdown') -> str:
    """Build a WritingTask artifact from the user's complete request."""
    if representation not in {'ir', 'markdown'}:
        raise ValueError("representation must be 'ir' or 'markdown'.")
    workflow_session_id = str(require_context().params.get('session_id') or '').strip()
    if not workflow_session_id:
        raise RuntimeError('writer workflow session_id is required to build a stable WritingTask')
    task = _json_loads(WriterCreateToolkit().build_writing_task(
        query=query, task_id=workflow_session_id,
    ), {})
    task['constraints'] = _parse_writer_request_constraints(query)
    task['output'] = {**(task.get('output') or {}), 'representation': representation}
    content = json.dumps(task, ensure_ascii=False)
    return _save_json_artifact('writing_task', content, writer_schema('task.WritingTask'))


def writer_load_local_document(filename: str = '') -> str:
    """Load one supplied Markdown, text, or Writer IR file as the working document."""
    files_by_turn = require_context().params.get('history_files_per_turn') or {}
    candidates = [
        Path(path)
        for paths in files_by_turn.values()
        for path in paths
        if Path(path).suffix.lower() in _LOCAL_WRITER_DOCUMENT_SUFFIXES
    ]
    if filename:
        candidates = [path for path in candidates if path.name == filename]
    if len(candidates) != 1:
        raise ValueError('Exactly one matching Markdown, text, or .lmd source file is required.')
    source = candidates[0]
    try:
        document = _read_json_file(str(source))
        if source.suffix.lower() == '.lmd':
            local_document = WriterDocument.model_validate(document)
            local_document.provider_binding.clear()
            local_document.metadata.pop('source', None)
            for block in local_document.iter_blocks():
                block.provider_binding.clear()
            document = local_document.model_dump(exclude_defaults=True)
    except (json.JSONDecodeError, ValueError) as exc:
        if source.suffix.lower() != '.lmd':
            raise
        raise ValueError(f'Cannot parse LMD file {source.name}: {exc}') from exc
    return _save_writer_document(
        'source_document',
        document,
        directory=_run_root('load-local-document'),
    )


def writer_load_document(user_input: str, stage: str = 'final') -> dict:
    """Load a Feishu/Lark document as source IR and preserve its target binding."""
    root = _run_root('load-document')
    payload = _json_loads(
        WriterResourceToolkit().load_document(user_input=user_input, stage=stage),
        {},
    )
    return {
        'source_document': _save_writer_document(
            'source_document',
            payload.get('source_document') or {},
            expected_stage=stage,
            directory=root,
        ),
        'target_document': _save_json_artifact(
            'target_document',
            json.dumps(payload.get('target_document') or {}, ensure_ascii=False),
            writer_schema('task.TargetDocument'),
            directory=root,
        ),
    }


def writer_profile_resources(
    writing_task_path: str,
    user_input: str,
    source_document_path: str = '',
    knowledge_text: str = '',
    profile_input_resources_path: str = '',
) -> str:
    """Profile attachments, a loaded source document, and retrieved KB evidence."""
    toolkit = WriterCreateToolkit()
    if profile_input_resources_path:
        resources = _read_json_file(profile_input_resources_path)
        resources.extend(_json_loads(toolkit.build_resources(
            file_paths_json='[]',
            source_document_json=(
                _read_json_string(source_document_path) if source_document_path else ''
            ),
            knowledge_text=knowledge_text,
        ), []))
    else:
        files_by_turn = require_context().params.get('history_files_per_turn') or {}
        file_paths = [path for paths in files_by_turn.values() for path in paths]
        resources = _json_loads(toolkit.build_resources(
            file_paths_json=json.dumps(file_paths, ensure_ascii=False),
            source_document_json=(
                _read_json_string(source_document_path) if source_document_path else ''
            ),
            knowledge_text=knowledge_text,
        ), [])
    content = toolkit.profile_resources(
        writing_task_json=_read_json_string(writing_task_path),
        user_input=user_input,
        resources_json=json.dumps(resources, ensure_ascii=False),
    )
    return _save_json_artifact(
        'resource_profiles', content, writer_schema('resource.ResourceProfile'),
    )


def writer_collect_available_media(
    writing_task_path: str,
    source_document_path: str = '',
) -> dict:
    """Collect attached and source-document images into the authoritative media library."""
    ctx = require_context()
    files_by_turn = ctx.params.get('history_files_per_turn') or {}
    file_paths: list[str] = []
    seen: set[str] = set()
    for paths in files_by_turn.values():
        for path in paths or []:
            normalized = str(path).strip()
            if normalized and normalized not in seen:
                seen.add(normalized)
                file_paths.append(normalized)

    toolkit = WriterCreateToolkit()
    resources = _json_loads(
        toolkit.build_resources(
            file_paths_json=json.dumps(file_paths, ensure_ascii=False),
        ),
        [],
    )
    writing_task_json = _read_json_string(writing_task_path)
    writing_task_value = _json_loads(writing_task_json, {})
    visual_policy = (writing_task_value.get('constraints') or {}).get('visual_policy') or {}
    if visual_policy.get('require_input_image_reuse'):
        for resource in resources:
            resource['meta'] = {
                **(resource.get('meta') or {}),
                'origin': 'user_upload',
            }
    root = _run_root('collect-media')
    media_root = root / 'media'
    media_root.mkdir(parents=True, exist_ok=True)
    try:
        payload = _json_loads(toolkit.collect_available_media(
            writing_task_json=writing_task_json,
            input_resources_json=json.dumps(resources, ensure_ascii=False),
            source_document_json=(
                _read_json_string(source_document_path) if source_document_path else ''
            ),
            media_store=str(media_root),
            use_vision_model=is_model_role_available('vlm'),
        ), {})
    except Exception as exc:
        task_id = str((_json_loads(writing_task_json, {}) or {}).get('task_id') or uuid.uuid4().hex)
        payload = {
            'media_assets': {
                'library_id': f'media-library-{task_id}',
                'assets': {},
            },
            'profile_input_resources': resources,
            'warnings': [
                f'Image collection failed: {type(exc).__name__}: {exc}',
            ],
        }
    media_assets_path = _save_json_artifact(
        'media_assets',
        json.dumps(payload.get('media_assets') or {}, ensure_ascii=False),
        writer_schema('multimodal.MediaAssetLibrary'),
        directory=root,
    )
    profile_input_resources_path = _save_json_artifact(
        'profile_input_resources',
        json.dumps(payload.get('profile_input_resources') or [], ensure_ascii=False),
        writer_schema('task.InputResource'),
        directory=root,
    )
    return {
        'media_assets': media_assets_path,
        'profile_input_resources': profile_input_resources_path,
        'warnings': payload.get('warnings') or [],
    }


def writer_create_writing_context(
    writing_task_path: str,
    resource_profiles_path: str,
    source_document_path: str = '',
) -> str:
    """Create WritingContext, optionally incorporating an existing WriterDocument."""
    content = WriterCreateToolkit().create_writing_context(
        writing_task_json=_read_json_string(writing_task_path),
        resource_profiles_json=_read_json_string(resource_profiles_path),
        writer_document_json=(
            _read_json_string(source_document_path) if source_document_path else ''
        ),
    )
    return _save_json_artifact(
        'writing_context', content, writer_schema('context.WritingContext'),
    )


def writer_prepare_workspace(
    operation: Literal[
        'create',
        'use_outline',
        'rewrite_document',
        'revise_document',
        'prepare_only',
    ] = 'create',
    source_filename: str = '',
    knowledge_text: str = '',
) -> dict:
    """Prepare one writing request.

    ``source_filename`` is only the basename of an uploaded Markdown, text, or LMD
    document. Feishu/Lark document URLs belong in ``user_input`` and are resolved as
    cloud documents.
    """
    user_input = _authoritative_writer_user_input('')
    knowledge_text = _verified_knowledge_text(knowledge_text)
    supported_operations = {
        'create',
        'use_outline',
        'rewrite_document',
        'revise_document',
        'prepare_only',
    }
    if operation not in supported_operations:
        raise ValueError(
            'operation must be create, use_outline, rewrite_document, '
            'revise_document, or prepare_only.',
        )
    ctx = require_context()
    files_by_turn = ctx.params.get('history_files_per_turn') or {}
    local_candidates = [
        Path(path)
        for paths in files_by_turn.values()
        for path in paths or []
        if Path(path).suffix.lower() in _LOCAL_WRITER_DOCUMENT_SUFFIXES
    ]
    source_filename = str(source_filename or '').strip()
    cloud_source_match = _CLOUD_DOCUMENT_URL.search(user_input or '')
    has_cloud_source = cloud_source_match is not None

    # Models occasionally copy a Feishu URL into both user_input and source_filename.
    # Treat that as one cloud source, never as a local filename override.
    source_filename_is_cloud = bool(
        source_filename and _CLOUD_DOCUMENT_URL.fullmatch(source_filename)
    )
    if source_filename_is_cloud:
        if source_filename not in user_input:
            raise ValueError(
                'A cloud source URL must appear in the authoritative user_input; '
                'do not supply it only through source_filename.',
            )
        source_filename = ''
        has_cloud_source = True
    elif source_filename:
        source_path = Path(source_filename)
        if source_path.name != source_filename or source_path.suffix.lower() \
                not in _LOCAL_WRITER_DOCUMENT_SUFFIXES:
            raise ValueError(
                'source_filename must be the basename of an uploaded Markdown, text, '
                'or .lmd document.',
            )
        if has_cloud_source:
            raise ValueError(
                'The request contains both a Feishu/Lark document URL and a local source '
                'document. Specify exactly one document source.',
            )

    source_kind = 'cloud' if has_cloud_source else (
        'local' if source_filename or local_candidates else 'cloud'
    )
    source_ref = (
        cloud_source_match.group(0) if cloud_source_match else source_filename
    )

    # The model may suggest an operation for ambiguous supplied documents, but it
    # does not own the terminal target. Resolve impossible combinations from the
    # authoritative request and the actual source bindings before creating the
    # immutable WriterCommand.
    operation, target_stage = _resolve_prepare_control(
        user_input,
        operation,
        has_document_source=bool(
            has_cloud_source or source_filename or local_candidates
        ),
    )

    command_action = {
        'create': 'create',
        'use_outline': 'use_outline',
        'rewrite_document': 'rewrite',
        'revise_document': 'revise',
        'prepare_only': 'read',
    }[operation]
    source_role = {
        'create': 'none',
        'use_outline': 'outline',
        'rewrite_document': 'document',
        'revise_document': 'document',
        'prepare_only': 'document',
    }[operation]
    writer_command = writer_resolve_command(
        user_input=user_input,
        action=command_action,
        source_role=source_role,
        target_stage=target_stage,
        source_ref=source_ref,
    )
    command = _load_writer_command(writer_command)

    source_document = ''
    target_document = ''
    representation = 'markdown'
    if operation != 'create':
        if source_kind == 'local':
            source_document = writer_load_local_document(source_filename)
            representation = (
                'ir' if Path(source_document).suffix.lower() == '.lmd' else 'markdown'
            )
        else:
            source_stage = {
                'use_outline': 'outline',
                'rewrite_document': 'draft',
                'revise_document': 'draft',
                'prepare_only': 'final',
            }[operation]
            loaded = writer_load_document(user_input=user_input, stage=source_stage)
            source_document = loaded['source_document']
            target_document = loaded['target_document']
            representation = 'ir'

    writing_task = writer_build_writing_task(
        query=user_input,
        representation=representation,
    )
    media_result = writer_collect_available_media(
        writing_task_path=writing_task,
        source_document_path=source_document if operation != 'use_outline' else '',
    )
    resource_profiles = writer_profile_resources(
        writing_task_path=writing_task,
        user_input=user_input,
        source_document_path=source_document,
        knowledge_text=knowledge_text,
        profile_input_resources_path=media_result['profile_input_resources'],
    )
    writing_context = writer_create_writing_context(
        writing_task_path=writing_task,
        resource_profiles_path=resource_profiles,
        source_document_path=source_document,
    )
    result = {
        'writer_command': writer_command,
        'writing_task': writing_task,
        'media_assets': media_result['media_assets'],
        'resource_profiles': resource_profiles,
        'writing_context': writing_context,
        'representation': representation,
        'structure_mode': command.structure_mode,
        'next_step': command.next_step,
        'control': {'next_step': command.next_step},
        'warnings': media_result.get('warnings') or [],
    }
    if source_document:
        result['source_document'] = source_document
    if target_document:
        result['target_document'] = target_document
    result['saved_artifact_keys'] = _save_draft_workspace_artifacts(result)
    result['artifacts_saved'] = True
    return result


def writer_prepare_outline(source_document_path: str) -> str:
    """Normalize a loaded outline document without regenerating its content."""
    content = WriterCreateToolkit().prepare_outline(
        source_document_json=_read_json_string(source_document_path),
    )
    return _save_writer_document(
        'outline_document', content, expected_stage='outline', editable=True,
    )


def writer_generate_outline(writing_task_path: str, writing_context_path: str) -> str:
    """Generate an outline-stage artifact with a Markdown preview stream."""
    _emit_writer_progress('正在生成大纲')
    events = DraftMarkdownStreamEventEmitter(
        require_context().emit,
        slot='outline_document',
    )
    output_started = False

    def emit_delta(delta: str) -> None:
        nonlocal output_started
        if not output_started and str(delta).strip():
            output_started = True
            _emit_writer_progress('正在输出大纲内容')
        events.feed(str(delta))

    try:
        generated = WriterCreateToolkit().stream_outline(
            writing_task_json=_read_json_string(writing_task_path),
            writing_context_json=_read_json_string(writing_context_path),
            on_delta=emit_delta,
        )
        _emit_writer_progress('大纲生成完成，正在校验并保存')
        outline_path = _save_writer_document(
            'outline_document', generated, expected_stage='outline', editable=True,
        )
    except Exception as exc:
        events.abort(str(exc))
        raise
    events.end()
    return outline_path


def _outline_workspace_fingerprint(
    operation: str,
    writing_context_path: str,
    user_input: str,
    writing_task_path: str,
    source_document_path: str,
    outline_document_path: str,
) -> str:
    payload = json.dumps({
        'operation': operation,
        'writing_context_path': writing_context_path,
        'user_input': user_input,
        'writing_task_path': writing_task_path,
        'source_document_path': source_document_path,
        'outline_document_path': outline_document_path,
    }, ensure_ascii=False, sort_keys=True)
    return hashlib.sha256(payload.encode('utf-8')).hexdigest()


def _outline_workspace_checkpoint_path(fingerprint: str) -> Path | None:
    try:
        require_context()
    except RuntimeError:
        return None
    return _workspace_root() / 'writer-workflow' / f'outline-workspace-{fingerprint}.json'


def _write_outline_workspace_checkpoint(path: Path, state: Mapping[str, Any]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    temporary = path.with_name(f'.{path.name}.{uuid.uuid4().hex}.tmp')
    temporary.write_text(
        json.dumps(dict(state), ensure_ascii=False, indent=2),
        encoding='utf-8',
    )
    temporary.replace(path)


def _outline_workspace_state(fingerprint: str) -> tuple[dict[str, Any], Path | None]:
    path = _outline_workspace_checkpoint_path(fingerprint)
    if path and path.exists():
        try:
            state = json.loads(path.read_text(encoding='utf-8'))
        except (OSError, json.JSONDecodeError):
            state = {}
        if state.get('fingerprint') == fingerprint:
            return state, path
    return {
        'schema_version': 1,
        'fingerprint': fingerprint,
        'result': {},
        'completed': False,
    }, path


def _persist_outline_workspace_state(
    state: dict[str, Any],
    path: Path | None,
    *,
    completed: bool = False,
) -> None:
    state['completed'] = completed
    if path:
        _write_outline_workspace_checkpoint(path, state)


def writer_outline_workspace() -> dict:
    """Run one existing outline workflow branch without changing its semantics."""
    user_input = _authoritative_writer_user_input('')
    writer_command_path = _authoritative_writer_input_path(
        'writer_command', require_workflow_binding=True,
    )
    writing_context_path = _authoritative_writer_input_path(
        ('writing_context_after_outline', 'writing_context'),
        require_workflow_binding=True,
    )
    writing_task_path = _authoritative_writer_input_path('writing_task')
    source_document_path = _authoritative_writer_input_path('source_document')
    outline_document_path = _authoritative_writer_input_path('outline_document')
    command = _load_writer_command(writer_command_path)
    if user_input and command.request_fingerprint != _writer_request_fingerprint(user_input):
        raise ValueError(
            'writer_command belongs to a different user request; restart from prepare.'
        )
    command_operation = {
        'create': 'generate',
        'use_outline': 'use_source',
        'revise': 'revise',
    }.get(command.action)
    if command_operation is None or (
        command.action == 'revise' and command.source_role != 'outline'
    ):
        raise ValueError(
            f'WriterCommand action={command.action!r} source_role={command.source_role!r} '
            'cannot execute the outline step.'
        )
    operation = command_operation
    if operation not in {'generate', 'use_source', 'revise'}:
        raise ValueError('operation must be generate, use_source, or revise.')
    if not writing_context_path:
        raise ValueError('writing_context_path is required.')

    fingerprint = _outline_workspace_fingerprint(
        operation,
        writing_context_path,
        user_input,
        writing_task_path,
        source_document_path,
        outline_document_path,
    )
    state, checkpoint_path = _outline_workspace_state(fingerprint)
    result: dict[str, Any] = dict(state.get('result') or {})
    result['operation'] = operation
    result['writer_command'] = writer_command_path
    result['control'] = {
        'next_step': 'write_document'
        if command.target_stage == 'document'
        else '__end__',
    }
    if state.get('completed'):
        _emit_writer_progress('正在复用已完成的大纲 checkpoint')
        if not state.get('artifacts_saved'):
            state['saved_artifact_keys'] = _save_draft_workspace_artifacts(result)
            state['artifacts_saved'] = True
            _persist_outline_workspace_state(state, checkpoint_path, completed=True)
        return result

    if operation == 'generate':
        if not writing_task_path:
            raise ValueError('writing_task_path is required for generate.')
        if not result.get('outline_document'):
            result['outline_document'] = writer_generate_outline(
                writing_task_path=writing_task_path,
                writing_context_path=writing_context_path,
            )
            state['result'] = result
            _persist_outline_workspace_state(state, checkpoint_path)
    elif operation == 'use_source':
        if not source_document_path:
            raise ValueError('source_document_path is required for use_source.')
        if not result.get('outline_document'):
            _emit_writer_progress('正在解析并规范化已有大纲')
            result['outline_document'] = writer_prepare_outline(source_document_path)
            state['result'] = result
            _persist_outline_workspace_state(state, checkpoint_path)
    else:
        if not user_input:
            raise ValueError('user_input is required for revise.')
        if not outline_document_path:
            raise ValueError('outline_document_path is required for revise.')
        if not result.get('outline_revision_task'):
            _emit_writer_progress('正在解析大纲修改要求')
            result['outline_revision_task'] = writer_build_revision_task(
                query=user_input,
                base_document_path=outline_document_path,
            )
            state['result'] = result
            _persist_outline_workspace_state(state, checkpoint_path)
        if not result.get('outline_locate_result'):
            _emit_writer_progress('正在定位需要修改的大纲结构')
            result['outline_locate_result'] = writer_locate_revision_target(
                base_document_path=outline_document_path,
                writing_context_path=writing_context_path,
                revision_task_path=result['outline_revision_task'],
            )
            state['result'] = result
            _persist_outline_workspace_state(state, checkpoint_path)
        if not result.get('outline_modify_plan'):
            _emit_writer_progress('正在生成大纲修改计划')
            result['outline_modify_plan'] = writer_generate_modify_plan(
                base_document_path=outline_document_path,
                writing_context_path=writing_context_path,
                revision_task_path=result['outline_revision_task'],
                locate_result_path=result['outline_locate_result'],
            )
            state['result'] = result
            _persist_outline_workspace_state(state, checkpoint_path)
        if not result.get('outline_revision_set'):
            _emit_writer_progress('正在生成大纲修改内容')
            result['outline_revision_set'] = writer_generate_revision_set(
                base_document_path=outline_document_path,
                writing_context_path=writing_context_path,
                modify_plan_path=result['outline_modify_plan'],
            )
            state['result'] = result
            _persist_outline_workspace_state(state, checkpoint_path)
        if not result.get('outline_document'):
            _emit_writer_progress('正在应用并校验大纲修改')
            applied = writer_apply_revision(
                base_document_path=outline_document_path,
                writing_context_path=writing_context_path,
                revision_set_path=result['outline_revision_set'],
            )
            result['outline_document'] = applied['outline_document']
            result['outline_revision_result'] = applied['revision_result']
            if applied.get('write_result'):
                result['outline_write_result'] = applied['write_result']
            state['result'] = result
            _persist_outline_workspace_state(state, checkpoint_path)

    if not result.get('writing_context_after_outline'):
        _emit_writer_progress('大纲已完成，正在更新写作上下文')
        result['writing_context_after_outline'] = writer_update_writing_context(
            content_artifact_path=result['outline_document'],
            writing_context_path=writing_context_path,
        )
    state['result'] = result
    _emit_writer_progress('大纲处理完成，正在保存工作区结果')
    state['saved_artifact_keys'] = _save_draft_workspace_artifacts(result)
    state['artifacts_saved'] = True
    _persist_outline_workspace_state(state, checkpoint_path, completed=True)
    return result


def writer_generate_rewrite_outline(
    writing_task_path: str,
    source_document_path: str,
    writing_context_path: str,
) -> str:
    """Generate a private outline used only to stream a complete document rewrite."""
    generated = WriterCreateToolkit().generate_rewrite_outline(
        writing_task_json=_read_json_string(writing_task_path),
        source_document_json=_read_json_string(source_document_path),
        writing_context_json=_read_json_string(writing_context_path),
    )
    return _save_json_artifact(
        'rewrite_outline', generated, WriterToolkitBase.WRITER_IR_SCHEMA,
        directory=_run_root('rewrite-outline'),
    )


def writer_generate_rewrite_section_instructions(
    writing_task_path: str,
    source_document_path: str,
    writing_context_path: str,
) -> dict:
    """Plan a complete IR or Markdown rewrite without creating an outline artifact."""
    payload = _json_loads(WriterCreateToolkit().generate_rewrite_section_instructions(
        writing_task_json=_read_json_string(writing_task_path),
        source_document_json=_read_json_string(source_document_path),
        writing_context_json=_read_json_string(writing_context_path),
    ), {})
    section_instructions = payload.get('section_instructions') or {}
    visual_plan = payload.get('visual_plan') or {'instructions': []}
    visual_needs = visual_plan.get('instructions') or []
    return {
        'section_instructions': _save_json_artifact(
            'section_instructions',
            json.dumps(section_instructions, ensure_ascii=False),
            writer_schema('planning.SectionInstructionList'),
        ),
        'visual_plan': _save_json_artifact(
            'visual_plan',
            json.dumps(visual_plan, ensure_ascii=False),
            writer_schema('multimodal.VisualPlan'),
        ),
        'visual_need_count': len(visual_needs),
        'section_count': len(section_instructions.get('instructions') or []),
        'visual_need_ids': [str(need.get('need_id') or '') for need in visual_needs],
        'document_title': payload.get('document_title') or '',
        'warnings': payload.get('warnings') or [],
    }


def writer_generate_section_instructions(
    writing_task_path: str,
    outline_path: str,
    writing_context_path: str,
) -> dict:
    """Generate internal section instructions from the selected outline IR."""
    payload = _json_loads(WriterCreateToolkit().generate_section_instructions(
        writing_task_json=_read_json_string(writing_task_path),
        outline_json=_read_json_string(outline_path),
        writing_context_json=_read_json_string(writing_context_path),
    ), {})
    section_instructions = payload.get('section_instructions') or {}
    visual_plan = payload.get('visual_plan') or {'instructions': []}
    visual_needs = visual_plan.get('instructions') or []
    return {
        'section_instructions': _save_json_artifact(
            'section_instructions',
            json.dumps(section_instructions, ensure_ascii=False),
            writer_schema('planning.SectionInstructionList'),
        ),
        'visual_plan': _save_json_artifact(
            'visual_plan',
            json.dumps(visual_plan, ensure_ascii=False),
            writer_schema('multimodal.VisualPlan'),
        ),
        'visual_need_count': len(visual_needs),
        'section_count': len(section_instructions.get('instructions') or []),
        'visual_need_ids': [str(need.get('need_id') or '') for need in visual_needs],
        'warnings': payload.get('warnings') or [],
    }


_IMAGE_URL_KEYS = (
    'contentUrl', 'content_url', 'imageUrl', 'image_url',
    'thumbnailUrl', 'thumbnail_url', 'src', 'url',
)


def _is_image_url(value: str) -> bool:
    lower = value.lower()
    if not (lower.startswith('http://') or lower.startswith('https://')):
        return False
    for extension in ('.jpg', '.jpeg', '.png', '.gif', '.webp', '.bmp', '.svg'):
        if extension in lower:
            return True
    return any(token in lower for token in ('image', 'img', 'photo', 'pic'))


def _collect_image_urls(node: Any, urls: list[str], seen: set[str]) -> None:
    if isinstance(node, dict):
        for key in _IMAGE_URL_KEYS:
            value = node.get(key)
            if isinstance(value, str) and _is_image_url(value) and value not in seen:
                seen.add(value)
                urls.append(value)
        for value in node.values():
            _collect_image_urls(value, urls, seen)
    elif isinstance(node, list):
        for value in node:
            _collect_image_urls(value, urls, seen)


def _tavily_image_urls(query: str, count: int) -> list[str]:
    engine = TavilySearch()
    if not engine.__key_source__():
        return []
    try:
        results = engine.search(query, include_images=True, max_results=count)
    except Exception as exc:
        LOG.warning('[Writer] Tavily image search failed: %s', type(exc).__name__)
        return []
    urls: list[str] = []
    seen: set[str] = set()
    for item in results or []:
        images = (item.get('extra') or {}).get('images') or []
        for image in images:
            if isinstance(image, str) and _is_image_url(image) and image not in seen:
                seen.add(image)
                urls.append(image)
    return urls[:count]


def _bocha_image_urls(query: str, count: int) -> list[str]:
    engine = BochaSearch()
    if not engine.__key_source__():
        return []
    try:
        response = engine._request(
            'POST',
            f'{engine._base_url}/v1/web-search',
            headers={'Content-Type': 'application/json'},
            json={'query': query, 'count': min(max(count, 1), 20)},
            timeout=engine._timeout,
        )
        payload = response.json()
    except Exception as exc:
        LOG.warning('[Writer] Bocha image search failed: %s', type(exc).__name__)
        return []
    urls: list[str] = []
    _collect_image_urls(payload, urls, set())
    return urls[:count]


def _pick_search_engine() -> Any | None:
    for search_type in (GoogleSearch, BingSearch, BochaSearch, TavilySearch):
        try:
            engine = search_type()
            if engine.__key_source__():
                return engine
        except Exception:
            continue
    return None


def _fallback_image_urls(query: str, count: int) -> list[str]:
    engine = _pick_search_engine()
    if engine is None:
        return []
    try:
        results = engine.search(f'{query} reference image illustration')
    except Exception as exc:
        LOG.warning('[Writer] %s image search failed: %s', type(engine).__name__, type(exc).__name__)
        return []
    return [
        str(item.get('url') or '').strip()
        for item in results or []
        if _is_image_url(str(item.get('url') or '').strip())
    ][:count]


def _acquire_web_search_resources(request: Mapping[str, Any]) -> list[dict]:
    purpose = str(request.get('purpose') or '').strip()
    query = ' '.join(part for part in (str(request.get('visual_type') or '').strip(), purpose) if part)
    urls = _tavily_image_urls(query, count=5)
    if not urls:
        urls = _bocha_image_urls(query, count=5)
    if not urls:
        urls = _fallback_image_urls(query, count=5)
    instruction_id = str(request.get('instruction_id') or uuid.uuid4().hex)
    return [
        {
            'resource_id': f'web-search-{instruction_id}-{index}',
            'resource_type': 'image',
            'uri': url,
            'title': purpose or url,
            'summary': purpose,
            'meta': {
                'source_type': 'web_search',
                'semantic_status': 'unverified',
            },
        }
        for index, url in enumerate(urls, start=1)
    ]
def writer_generate_short_writing_plan(
    writing_task_path: str,
    writing_context_path: str,
) -> str:
    """Generate and persist one whole-document plan for a flat article."""
    result = WriterPlanningTools(
        llm=AutoModel(model='llm'),
        artifact_store=str(_run_root('short-writing-plan-source')),
    ).generate_short_writing_plan(
        task=writing_task_path,
        context=writing_context_path,
    )
    content = ShortWritingPlan.model_validate(
        _writer_tool_artifact_data(result),
    ).model_dump_json(exclude_defaults=True)
    return _save_json_artifact(
        'short_writing_plan',
        content,
        writer_schema('planning.ShortWritingPlan'),
        directory=_run_root('short-writing-plan'),
    )


def writer_generate_short_visual_plan(
    writing_task_path: str,
    short_writing_plan_path: str,
    writing_context_path: str,
) -> dict:
    """Generate and persist one strongly typed visual plan for a flat article."""
    writing_task = _read_json_file(writing_task_path)
    visual_policy = (writing_task.get('constraints') or {}).get('visual_policy') or {}
    require_input_image_reuse = visual_policy.get('require_input_image_reuse') is True
    warnings: list[str] = []
    payload: Any = {'instructions': []}
    if visual_policy.get('allow_visuals') is not False:
        try:
            result = WriterPlanningTools(
                llm=AutoModel(model='llm'),
                artifact_store=str(_run_root('short-visual-plan-source')),
            ).generate_short_visual_plan(
                task=writing_task_path,
                short_writing_plan=short_writing_plan_path,
                context=writing_context_path,
            )
            payload = _writer_tool_artifact_data(result)
            warnings.extend((result.get('metadata') or {}).get('warnings') or [])
        except Exception as exc:
            if require_input_image_reuse:
                raise RuntimeError(
                    f'Required visual planning failed: {type(exc).__name__}: {exc}'
                ) from exc
            warnings.append(f'Visual planning failed: {type(exc).__name__}: {exc}')
    visual_plan = VisualPlan.model_validate(
        payload or {'instructions': []},
    ).model_dump()
    instructions = visual_plan['instructions']
    if require_input_image_reuse and not instructions:
        raise ValueError('Required input image reuse produced no visual plan instructions.')
    return {
        'visual_plan': _save_json_artifact(
            'visual_plan',
            json.dumps(visual_plan, ensure_ascii=False),
            writer_schema('multimodal.VisualPlan'),
            directory=_run_root('short-visual-plan'),
        ),
        'visual_need_count': len(instructions),
        'visual_need_ids': [str(need.get('need_id') or '') for need in instructions],
        'warnings': warnings,
    }


def writer_generate_short_document(
    writing_task_path: str,
    short_writing_plan_path: str,
    writing_context_path: str,
    visual_plan_path: str = '',
    resolved_media_assets_path: str = '',
) -> str:
    """Generate and persist one complete flat short document."""
    events = DraftMarkdownStreamEventEmitter(
        require_context().emit,
        slot='flat_draft_document',
    )
    try:
        drafting = WriterDraftingTools(
            llm=AutoModel(model='llm'),
            artifact_store=str(_run_root('short-document-source')),
        )
        with drafting.stream_short_document(
            task=writing_task_path,
            short_writing_plan=short_writing_plan_path,
            context=writing_context_path,
            visual_plan=visual_plan_path or None,
            media_assets=resolved_media_assets_path or None,
        ) as stream:
            for delta in stream:
                try:
                    events.feed(str(delta))
                except Exception as exc:  # noqa: BLE001 - preview forwarding is best effort.
                    LOG.warning('[Writer] Short document delta callback failed: %s', exc)
            result = stream.result()
        document = _writer_tool_artifact_data(result)
        if resolved_media_assets_path and isinstance(document, str):
            document = _fill_markdown_media_placeholders(
                document,
                _read_json_file(resolved_media_assets_path),
            )
        if isinstance(document, str) and 'media-placeholder://' in document:
            raise ValueError('Short document contains unresolved media placeholders.')
        path = _save_writer_document(
            'draft_document',
            document,
            expected_stage='draft',
            editable=True,
            directory=_run_root('short-document'),
        )
    except Exception as exc:
        events.abort(str(exc))
        raise
    events.end()
    return path


def _acquire_generated_image(
    request: Mapping[str, Any],
    *,
    generator: Callable[..., dict] | None = None,
) -> list[dict]:
    visual_type = str(request.get('visual_type') or '')
    prompt = WRITER_IMAGE_ACQUISITION_PROMPT.format(
        visual_type=visual_type,
        purpose=str(request.get('purpose') or ''),
    ).strip()
    result = (generator or image_generator)(
        prompt=prompt,
        image_size='1024x1024',
        batch_size=1,
    )
    local_path = str((result or {}).get('local_path') or '').strip()
    if not local_path:
        images = (result or {}).get('images') or []
        if images and isinstance(images[0], dict):
            local_path = str(images[0].get('local_path') or '').strip()
    if not local_path:
        raise ValueError('image_generator returned no local image path')
    return [{
        'resource_id': f"acquired-{request.get('instruction_id') or uuid.uuid4().hex}",
        'resource_type': 'image',
        'uri': local_path,
        'title': Path(local_path).name,
        'summary': str(request.get('purpose') or ''),
        'meta': {
            'source_type': 'image_generation',
            'generation_prompt': prompt,
            'summary_source': 'generation_prompt',
            'semantic_status': 'unverified',
        },
    }]


def _acquire_visual_media(
    request: Mapping[str, Any],
    acquirers: Mapping[str, Callable[[Mapping[str, Any]], list[dict]]],
) -> Iterator[dict]:
    strategies = list(request['strategies'])
    for strategy in strategies:
        acquirer = acquirers.get(strategy)
        if acquirer is None:
            continue
        try:
            resources = acquirer(request)
        except Exception as exc:
            LOG.warning(
                '[Writer] Failed to acquire %s for visual instruction %r: %s',
                strategy,
                request.get('instruction_id'),
                type(exc).__name__,
            )
            continue
        for candidate in resources:
            resource = dict(candidate)
            resource['meta'] = {
                **dict(resource.get('meta') or {}),
                'requested_strategy': strategies[0],
                'acquisition_strategy': strategy,
            }
            yield resource


def writer_resolve_visual_media(
    visual_plan_path: str,
    media_assets_path: str,
    strict_required: bool = False,
    allowed_strategies_json: str = '',
) -> dict:
    """Resolve visual needs and materialize missing media through registered acquirers.

    allowed_strategies_json: optional JSON list restricting acquisition strategies.
    """
    root = _run_root('resolve-media')
    media_root = root / 'media'
    media_root.mkdir(parents=True, exist_ok=True)
    toolkit = WriterCreateToolkit()
    media_assets_json = _read_json_string(media_assets_path)
    media_assets_value = _json_loads(media_assets_json, {})
    visual_policy = (media_assets_value.get('meta') or {}).get('visual_policy') or {}
    allow_image_generation = visual_policy.get('allow_image_generation') is not False
    require_visuals = (
        visual_policy.get('require_visuals') is True
        or visual_policy.get('require_input_image_reuse') is True
    )
    acquirers = {
        'web_search': _acquire_web_search_resources,
    }
    if allow_image_generation and is_model_role_available('image_generator'):
        acquirers['image_generation'] = _acquire_generated_image
    visual_plan_json = _read_json_string(visual_plan_path)
    try:
        matched = _json_loads(toolkit.resolve_visual_needs(
            visual_plan_json=visual_plan_json,
            media_assets_json=media_assets_json,
            allowed_strategies_json=allowed_strategies_json,
        ), {})
    except Exception as exc:
        if strict_required:
            raise
        matched = {
            'media_assets': _json_loads(media_assets_json, {}),
            'acquisition_requests': [],
            'warnings': [
                f'Visual media resolution failed: {type(exc).__name__}: {exc}',
            ],
        }
    warnings = list(matched.get('warnings') or [])
    resolved_library = matched.get('media_assets') or {}
    acquired_by_purpose: dict[tuple[str, str, tuple[str, ...]], dict] = {}
    for request in matched.get('acquisition_requests') or []:
        strategies = list(request.get('strategies') or [])
        if allow_image_generation \
                and not any(strategy in acquirers for strategy in strategies) \
                and 'image_generation' in acquirers:
            request = {**request, 'strategies': ['image_generation']}
        instruction_id = str(request['instruction_id'])
        key = (
            str(request.get('visual_type') or ''),
            ' '.join(str(request.get('purpose') or '').split()).casefold(),
            tuple(request['strategies']),
        )
        cached_resource = acquired_by_purpose.get(key)
        candidate_groups: list[tuple[bool, Iterator[dict]]] = []
        if cached_resource is not None:
            candidate_groups.append((True, iter((cached_resource,))))
        candidate_groups.append((False, _acquire_visual_media(request, acquirers)))
        resolved = False
        for from_cache, candidates in candidate_groups:
            for resource in candidates:
                try:
                    outcome = _json_loads(toolkit.materialize_acquired_media(
                        visual_plan_json=visual_plan_json,
                        media_assets_json=json.dumps(resolved_library, ensure_ascii=False),
                        acquired_resources_json=json.dumps({instruction_id: resource}, ensure_ascii=False),
                        media_store=str(media_root),
                    ), {})
                except Exception as exc:
                    LOG.warning(
                        '[Writer] Failed to materialize visual instruction %r: %s',
                        instruction_id,
                        type(exc).__name__,
                    )
                    continue
                candidate_library = outcome.get('media_assets') or {}
                candidate_assets = candidate_library.get('assets') or {}
                candidate_bindings = candidate_library.get('visual_need_asset_ids') or {}
                if not any(
                    asset_id in candidate_assets
                    and Path(str(candidate_assets[asset_id].get('local_path') or '')).is_file()
                    for asset_id in candidate_bindings.get(instruction_id, [])
                ):
                    continue
                resolved_library = candidate_library
                acquired_by_purpose[key] = resource
                resolved = True
                break
            if resolved:
                break
            if from_cache:
                acquired_by_purpose.pop(key, None)
        if not resolved:
            message = (
                f'Failed to acquire visual instruction {instruction_id!r}: '
                'no candidate could be materialized'
            )
            if (strict_required or require_visuals) and request.get('required') is True:
                raise RuntimeError(
                    f'{message}: {request.get("purpose") or "current visual requirement"}'
                )
            warnings.append(f'{message} (required={request.get("required", False)}).')
    plan_value = _json_loads(visual_plan_json, {})
    plan_data = plan_value.get('data', plan_value) if isinstance(plan_value, dict) else plan_value
    resolved_assets = resolved_library.get('assets') or {}
    resolved_bindings = resolved_library.get('visual_need_asset_ids') or {}
    unresolved_required = [
        str(instruction.get('need_id'))
        for instruction in (plan_data.get('instructions') or [])
        if (
            (strict_required or require_visuals)
            and instruction.get('required', False) is True
            and not any(
                asset_id in resolved_assets
                and Path(str(resolved_assets[asset_id].get('local_path') or '')).is_file()
                for asset_id in resolved_bindings.get(str(instruction.get('need_id')), [])
            )
        )
    ]
    if unresolved_required:
        raise RuntimeError(
            'Failed to resolve required visual media for: ' + ', '.join(unresolved_required)
        )
    resolved_path = save_artifact_json(
        resolved_library,
        str(root / 'resolved_media_assets.json'),
        schema_name=writer_schema('multimodal.MediaAssetLibrary'),
        created_by='writer-workflow-wrapper',
    )
    return {
        'resolved_media_assets': resolved_path,
        'warnings': warnings,
    }


def writer_resolve_revision_media(
    modify_plan_path: str,
    media_assets_path: str,
) -> dict:
    """Resolve required image additions in a revision plan without partial success."""
    root = _run_root('revision-visual-plan')
    visual_plan_json = WriterRevisionToolkit().build_revision_visual_plan(
        modify_plan_json=_read_json_string(modify_plan_path),
    )
    visual_plan_path = _save_json_artifact(
        'visual_plan',
        visual_plan_json,
        writer_schema('multimodal.VisualPlan'),
        directory=root,
    )
    return writer_resolve_visual_media(
        visual_plan_path=visual_plan_path,
        media_assets_path=media_assets_path,
        strict_required=True,
        allowed_strategies_json=json.dumps(['web_search', 'image_generation']),
    )


def writer_generate_draft_blocks(
    writing_task_path: str,
    section_instructions_path: str,
    writing_context_path: str,
    visual_plan_path: str = '',
    media_assets_path: str = '',
    checkpoint_dir: str = '',
) -> list[str]:
    """Generate and persist all planned draft blocks."""
    context = require_context()
    events = DraftMarkdownStreamEventEmitter(context.emit)

    def emit_progress(payload: dict[str, Any]) -> None:
        context.emit({'type': 'progress', **payload})

    try:
        blocks = _json_loads(WriterCreateToolkit().stream_draft_blocks_ir(
            writing_task_json=_read_json_string(writing_task_path),
            section_instructions_json=_read_json_string(section_instructions_path),
            writing_context_json=_read_json_string(writing_context_path),
            visual_plan_json=(
                _read_json_string(visual_plan_path) if visual_plan_path else ''
            ),
            media_assets_json=(
                _read_json_string(media_assets_path) if media_assets_path else ''
            ),
            on_delta=events.feed,
            on_section_end=events.flush,
            on_progress=emit_progress,
            on_preview_restart=events.restart,
            checkpoint_dir=checkpoint_dir,
        ), [])
        root = _run_root('draft-blocks')
        paths = []
        for index, block in enumerate(blocks, start=1):
            paths.append(_save_json_artifact(
                f'draft_block_{index:04d}',
                json.dumps(block, ensure_ascii=False),
                WriterToolkitBase.WRITER_BLOCK_SCHEMA,
                directory=root,
            ))
    except Exception as exc:
        events.abort(str(exc))
        raise
    events.end()
    return paths


def writer_generate_draft_blocks_markdown(
    writing_task_path: str,
    section_instructions_path: str,
    writing_context_path: str,
    visual_plan_path: str = '',
    checkpoint_dir: str = '',
) -> list[str]:
    """Generate and persist all planned draft sections as Markdown."""
    context = require_context()
    events = DraftMarkdownStreamEventEmitter(context.emit)

    def emit_progress(payload: dict[str, Any]) -> None:
        context.emit({'type': 'progress', **payload})

    try:
        sections = _json_loads(WriterCreateToolkit().stream_draft_blocks_markdown(
            writing_task_json=_read_json_string(writing_task_path),
            section_instructions_json=_read_json_string(section_instructions_path),
            writing_context_json=_read_json_string(writing_context_path),
            visual_plan_json=(
                _read_json_string(visual_plan_path) if visual_plan_path else ''
            ),
            on_delta=events.feed,
            on_section_end=events.flush,
            on_progress=emit_progress,
            on_preview_restart=events.restart,
            checkpoint_dir=checkpoint_dir,
        ), [])
        root = _run_root('draft-sections-markdown')
        paths = []
        for index, section in enumerate(sections, start=1):
            path = root / f'draft_section_{index:04d}.md'
            path.write_text(str(section), encoding='utf-8')
            paths.append(str(path))
    except Exception as exc:
        events.abort(str(exc))
        raise
    events.end()
    return paths


def _assemble_draft_document_ir(
    draft_blocks_anchor_path: str,
    writing_context_path: str,
    outline_path: str = '',
    document_title: str = '',
) -> str:
    """Combine draft WriterBlock artifacts into a draft WriterDocument."""
    anchor = (
        Path(draft_blocks_anchor_path)
        if draft_blocks_anchor_path else _workspace_root() / 'draft_blocks'
    )
    if not anchor.exists():
        candidates = sorted(
            (_workspace_root() / 'writer-workflow').glob('draft-blocks-*'),
            key=lambda path: path.stat().st_mtime,
            reverse=True,
        )
        if candidates:
            anchor = candidates[0]
    draft_blocks_dir = anchor if anchor.is_dir() else anchor.parent
    draft_block_paths = sorted(
        (str(path) for path in draft_blocks_dir.glob('draft_block_*.lmd')),
        key=lambda path: int(re.match(r'draft_block_(\d+)', Path(path).stem).group(1)),
    )
    if not draft_block_paths:
        raise ValueError(
            'draft_blocks_anchor_path must point to a generated draft block file or directory.',
        )

    draft_blocks = [_read_json_file(path) for path in draft_block_paths]
    content = WriterCreateToolkit().generate_draft_document(
        draft_blocks_json=json.dumps(draft_blocks, ensure_ascii=False),
        writing_context_json=_read_json_string(writing_context_path),
        outline_json=_read_json_string(outline_path) if outline_path else '',
        title=document_title,
    )
    return _save_writer_document(
        'draft_document', content, expected_stage='draft', editable=True,
    )


def _fill_markdown_media_placeholders(markdown: str, resolved_media_assets: Any) -> str:
    """Replace resolved Markdown media placeholders with image paths."""
    wiki_placeholder_pattern = re.compile(
        r'!\[\[([^\]]*)\]\]\(media-placeholder://([A-Za-z0-9_-]+)\)'
    )
    placeholder_pattern = re.compile(
        r'!\[([^\]]*)\]\(media-placeholder://([A-Za-z0-9_-]+)\)'
    )
    need_asset_ids = (resolved_media_assets or {}).get('visual_need_asset_ids') or {}
    assets = (resolved_media_assets or {}).get('assets') or {}
    dropped: list[str] = []

    def replace_image(match: re.Match) -> str:
        caption, need_id = match.group(1), match.group(2)
        asset_ids = need_asset_ids.get(need_id) or []
        if asset_ids:
            asset = assets.get(asset_ids[0]) or {}
            path = str(asset.get('local_path') or asset.get('uri') or '')
            if path:
                return f'![{caption}]({path})'
        dropped.append(need_id)
        return ''

    normalized = wiki_placeholder_pattern.sub(
        lambda match: f'![{match.group(1)}](media-placeholder://{match.group(2)})',
        markdown or '',
    )
    filled = placeholder_pattern.sub(replace_image, normalized)
    filled = re.sub(
        r'\(media-placeholder://([A-Za-z0-9_-]+)\)',
        lambda match: (dropped.append(match.group(1)), '')[1],
        filled,
    )
    if dropped:
        LOG.warning(
            '[Writer] Markdown media fill dropped %d unresolved placeholder(s): %s',
            len(dropped),
            ', '.join(sorted(set(dropped))),
        )
    return filled


def _drop_unregistered_markdown_images(
    markdown: str,
    resolved_media_assets: Any,
) -> str:
    assets = (resolved_media_assets or {}).get('assets') or {}
    allowed = {
        str(path).strip()
        for asset in assets.values()
        if isinstance(asset, Mapping)
        for path in (asset.get('uri'), asset.get('local_path'))
        if str(path or '').strip()
    }
    image_pattern = re.compile(r'!\[([^\]]*)\]\(([^)\n]+)\)')
    fence: str | None = None
    dropped: list[str] = []
    output: list[str] = []

    def replace_image(match: re.Match) -> str:
        destination = match.group(2).strip()
        if destination.startswith('<') and '>' in destination:
            target = destination[1:destination.index('>')]
        else:
            target = destination.split(maxsplit=1)[0]
        if target in allowed:
            return match.group(0)
        dropped.append(target)
        return ''

    for line in (markdown or '').splitlines(keepends=True):
        fence_match = re.match(r'^\s*(```+|~~~+)', line)
        if fence_match:
            marker = fence_match.group(1)[0]
            fence = marker if fence is None else None if fence == marker else fence
            output.append(line)
            continue
        output.append(image_pattern.sub(replace_image, line) if fence is None else line)

    if dropped:
        LOG.warning(
            '[Writer] Dropped %d unregistered Markdown image reference(s): %s',
            len(dropped), ', '.join(sorted(set(dropped))),
        )
    return ''.join(output)


def _assemble_draft_document_markdown(
    draft_sections_anchor_path: str,
    writing_context_path: str,
    outline_path: str = '',
    document_title: str = '',
    resolved_media_assets_path: str = '',
) -> str:
    """Assemble Markdown sections and preserve the Markdown document."""
    anchor = (
        Path(draft_sections_anchor_path)
        if draft_sections_anchor_path else _workspace_root() / 'draft_sections'
    )
    if not anchor.exists():
        candidates = sorted(
            (_workspace_root() / 'writer-workflow').glob('draft-sections-*'),
            key=lambda path: path.stat().st_mtime,
            reverse=True,
        )
        if candidates:
            anchor = candidates[0]
    sections_dir = anchor if anchor.is_dir() else anchor.parent
    section_paths = sorted(
        sections_dir.glob('draft_section_*.md'),
        key=lambda path: int(re.match(r'draft_section_(\d+)', path.stem).group(1)),
    )
    if not section_paths:
        raise ValueError(
            'draft_sections_anchor_path must point to a generated Markdown section or directory.',
        )
    sections = [path.read_text(encoding='utf-8') for path in section_paths]
    payload = _json_loads(WriterCreateToolkit().generate_draft_document_markdown(
        draft_sections_json=json.dumps(sections, ensure_ascii=False),
        writing_context_json=_read_json_string(writing_context_path),
        outline_json=_read_json_string(outline_path) if outline_path else '',
        title=document_title,
    ), {})
    markdown = payload.get('draft_document') or ''
    resolved_media_assets = (
        _read_json_file(resolved_media_assets_path)
        if resolved_media_assets_path else {}
    )
    if resolved_media_assets_path:
        markdown = _fill_markdown_media_placeholders(
            markdown,
            resolved_media_assets,
        )
    markdown = _drop_unregistered_markdown_images(
        markdown, resolved_media_assets,
    )
    if 'media-placeholder://' in markdown:
        raise ValueError(
            'Markdown draft contains unresolved media placeholders; '
            'resolve visual media before assembling the final document.'
        )
    root = _run_root('draft-document-markdown')
    return _save_writer_document(
        'draft_document',
        markdown,
        expected_stage='draft',
        editable=True,
        directory=root,
    )


def writer_generate_draft_document(
    writing_task_path: str,
    section_instructions_path: str,
    writing_context_path: str,
    outline_path: str = '',
    visual_plan_path: str = '',
    resolved_media_assets_path: str = '',
    document_title: str = '',
) -> dict:
    """Generate sections concurrently, stream in outline order, and assemble the draft."""
    task = _read_json_file(writing_task_path)
    representation = str(((task.get('output') or {}).get('representation') or '')).strip()
    checkpoint_payload = json.dumps({
        'version': 1,
        'task': _read_json_string(writing_task_path),
        'instructions': _read_json_string(section_instructions_path),
        'context': _read_json_string(writing_context_path),
        'outline': _read_json_string(outline_path) if outline_path else '',
        'visual_plan': _read_json_string(visual_plan_path) if visual_plan_path else '',
        'media_assets': (
            _read_json_string(resolved_media_assets_path)
            if resolved_media_assets_path else ''
        ),
        'document_title': document_title,
        'representation': representation,
    }, ensure_ascii=False, sort_keys=True)
    checkpoint_key = hashlib.sha256(checkpoint_payload.encode('utf-8')).hexdigest()
    checkpoint_dir = str(
        _workspace_root() / 'writer-workflow' / f'draft-sections-{checkpoint_key}'
    )
    if representation == 'markdown':
        draft_blocks = writer_generate_draft_blocks_markdown(
            writing_task_path=writing_task_path,
            section_instructions_path=section_instructions_path,
            writing_context_path=writing_context_path,
            visual_plan_path=visual_plan_path,
            checkpoint_dir=checkpoint_dir,
        )
        require_context().emit({
            'type': 'progress',
            'progress': 5,
            'current_phase': '章节已生成，正在组装文档并校验编号与引用',
        })
        draft_document = _assemble_draft_document_markdown(
            draft_sections_anchor_path=draft_blocks[0] if draft_blocks else '',
            writing_context_path=writing_context_path,
            outline_path=outline_path,
            document_title=document_title,
            resolved_media_assets_path=resolved_media_assets_path,
        )
    elif representation == 'ir':
        draft_blocks = writer_generate_draft_blocks(
            writing_task_path=writing_task_path,
            section_instructions_path=section_instructions_path,
            writing_context_path=writing_context_path,
            visual_plan_path=visual_plan_path,
            media_assets_path=resolved_media_assets_path,
            checkpoint_dir=checkpoint_dir,
        )
        require_context().emit({
            'type': 'progress',
            'progress': 5,
            'current_phase': '章节已生成，正在组装文档并校验编号与引用',
        })
        draft_document = _assemble_draft_document_ir(
            draft_blocks_anchor_path=draft_blocks[0] if draft_blocks else '',
            writing_context_path=writing_context_path,
            outline_path=outline_path,
            document_title=document_title,
        )
    else:
        raise ValueError("writing_task output representation must be 'markdown' or 'ir'.")
    require_context().emit({
        'type': 'progress',
        'progress': 5,
        'current_phase': '文档组装完成，正在保存结果',
    })
    return {
        'draft_blocks': draft_blocks,
        'draft_document': draft_document,
        'representation': representation,
    }


def writer_update_writing_context(
    content_artifact_path: str,
    writing_context_path: str,
) -> str:
    """Update WritingContext from a WriterDocument or WriterBlock."""
    content = WriterCreateToolkit().update_writing_context(
        content_artifact_json=_read_json_string(content_artifact_path),
        writing_context_json=_read_json_string(writing_context_path),
    )
    return _save_json_artifact(
        'writing_context', content, writer_schema('context.WritingContext'),
    )


def writer_export_markdown(content_path: str) -> str:
    """Export the latest WriterDocument as a downloadable Markdown file."""
    payload = _json_loads(WriterCreateToolkit().render_markdown(
        writer_document_json=_read_json_string(content_path),
    ), {})
    output_path = _run_root('export-markdown') / _markdown_filename(
        str(payload.get('title') or ''),
    )
    output_path.write_text(str(payload.get('markdown') or ''), encoding='utf-8')
    return str(output_path)


def writer_render_document(artifact: Any) -> dict:
    """Render a Writer IR or Markdown artifact with automatic numbering."""
    document = _action_artifact_data(artifact)
    if isinstance(document, str):
        title_match = re.search(r'^#\s+(.+)$', document, re.MULTILINE)
        return {
            'title': title_match.group(1).strip() if title_match else '',
            'representation': 'markdown',
            'document': materialize_markdown(document),
        }
    source = WriterDocument.model_validate(document)
    numbering = compute_numbering(build_numbering_view_from_ir(source))
    materialized = materialize_ir(source, numbering)
    return {
        'title': source.title,
        'representation': 'ir',
        'document': materialized.model_dump(exclude_defaults=True),
    }


def writer_save_document(artifact: Any, base_artifact: Any) -> dict:
    """Normalize a submitted IR edit back to clean source and re-materialize it."""
    current_value = _action_artifact_data(artifact)
    if isinstance(current_value, str):
        base_value = _action_artifact_data(base_artifact)
        base_numbering = (
            compute_numbering(build_numbering_view_from_markdown(base_value))
            if isinstance(base_value, str)
            else {}
        )
        clean = dematerialize_markdown(current_value, base_numbering)
        rendered = _json_loads(
            WriterCreateToolkit().render_markdown(writer_document_json=clean),
            {},
        )
        return {
            'source_document': clean,
            'title': rendered.get('title') or '',
            'representation': 'markdown',
            'document': rendered.get('markdown') or '',
        }
    current = WriterDocument.model_validate(current_value)
    base = WriterDocument.model_validate(_action_artifact_data(base_artifact))
    base_numbering = compute_numbering(build_numbering_view_from_ir(base))
    clean = dematerialize_ir(current, base_numbering)
    numbering = compute_numbering(build_numbering_view_from_ir(clean))
    materialized = materialize_ir(clean, numbering)
    return {
        'source_document': clean.model_dump(exclude_defaults=True),
        'title': clean.title,
        'representation': 'ir',
        'document': materialized.model_dump(exclude_defaults=True),
    }


def writer_build_revision_task(query: str, base_document_path: str) -> str:
    """Build a revision task for either an outline or a full document."""
    content = WriterRevisionToolkit().build_revision_task(
        query=query,
        writer_document_json=_read_json_string(base_document_path),
        allow_outline=require_context().params.get('step_id') != 'write_document',
    )
    return _save_json_artifact(
        'revision_task', content, writer_schema('task.WritingTask'),
        directory=_run_root('revision-task'),
    )


def writer_preview_selection_rewrite(
    artifact: Any,
    instruction: str,
    selection: Mapping[str, Any],
    artifact_store: str = '',
    slot: str = '',
) -> dict:
    """Preview a selected IR block or Markdown paragraph rewrite."""
    instruction = str(instruction or '').strip()
    if not instruction:
        raise ValueError('instruction must not be empty.')
    document = _action_artifact_data(artifact)
    context = _action_context(document)
    revision = WriterRevisionTools(
        llm=AutoModel(model='llm'),
        artifact_store=str(_action_root(artifact_store, 'rewrite-preview')),
    )
    if slot not in {'outline_document', 'flat_draft_document', 'draft_document'}:
        raise ValueError(
            'selection rewrite requires an outline_document, flat_draft_document, '
            'or draft_document slot.',
        )
    selection_type = str((selection or {}).get('type') or '')
    if isinstance(document, Mapping):
        source = WriterDocument.model_validate(document)
        if selection_type != 'ir':
            raise ValueError("IR artifacts require selection.type='ir'.")
        node_id = str(selection.get('node_id') or '')
        target = source.block_by_id(node_id)
        if target is None:
            raise ValueError('The selected IR node no longer exists.')
        plan = ModifyPlan(scope='block', instructions=[ModifyInstruction(
            instruction_id='rewrite-selection',
            content_ref=ContentRef(node_id=node_id),
            modify_type='update',
            instruction=instruction,
        )])
        output = revision.generate_patch_set(source, plan, context)
        patch_set = load_artifact_json(_action_result_path(output), PatchSet)
        revised, _ = apply_patch_to_ir(source, patch_set)
        candidate_path = Path(_save_writer_document(
            slot, revised,
            expected_stage='outline' if slot == 'outline_document' else None,
            editable=slot in {'flat_draft_document', 'draft_document'},
            directory=Path(revision.artifact_store),
        ))
        result = {
            'representation': 'ir',
            'target': {'type': 'block', 'block_type': target.type, 'node_id': node_id},
            'preview': {
                'old_text': target.content,
                'new_text': revised.block_by_id(node_id).content,
            },
            'patch': {'type': 'writer_ir_patch', 'payload': patch_set.model_dump()},
        }
    else:
        if selection_type != 'markdown':
            raise ValueError("Markdown artifacts require selection.type='markdown'.")
        if slot == 'flat_draft_document':
            instruction += (
                '\nKeep the replacement as exactly one Markdown paragraph; do not split it.'
            )
        replace_set = StringReplaceSet.model_validate(
            revision.build_selected_markdown_replace_set(
                document, instruction, str(selection.get('selected_text') or ''), context,
            ),
        )
        replacement = replace_set.replacements[0]
        output = revision.apply_string_replace(document, replace_set, context)
        candidate_path = Path(output['revised_document_md'])
        canonical_path = candidate_path.with_name(f'{slot}.md')
        if candidate_path != canonical_path:
            candidate_path.replace(canonical_path)
            candidate_path = canonical_path
        result = {
            'representation': 'markdown',
            'target': {'type': 'block', 'block_type': 'paragraph'},
            'preview': {
                'old_text': replacement.old_string,
                'new_text': replacement.new_string,
            },
            'patch': {'type': 'string_replace_set', 'payload': replace_set.model_dump()},
        }
    result['artifact'] = {
        'content_type': 'file',
        'value': {
            'path': str(candidate_path),
            'filename': candidate_path.name,
            'size': candidate_path.stat().st_size,
        },
    }
    return result


def writer_sync_document(
    source_document: Mapping[str, Any] | None = None,
    revised_document: Mapping[str, Any] | None = None,
    media_assets: Mapping[str, Any] | None = None,
    markdown_content: str = '',
    target_document: Mapping[str, Any] | None = None,
    title: str = '',
    artifact_store: str = '',
) -> dict:
    """Persist the selected IR or Markdown draft through its provider adapter."""
    if markdown_content:
        return _sync_markdown_document(
            markdown_content, target_document=target_document, title=title,
            media_assets=media_assets, artifact_store=artifact_store,
        )
    if revised_document is None:
        raise ValueError('revised_document is required for IR sync.')
    if source_document is None:
        document = WriterDocument.model_validate(revised_document)
        return _replace_document_and_read_back(
            document,
            title=document.title,
            media_assets=media_assets,
            artifact_store=artifact_store,
            source_format='lmd',
        )
    return sync_writer_documents(
        source_document,
        revised_document,
        media_assets,
        str(_action_root(artifact_store, 'sync-document')),
    )


def _sync_markdown_document(
    markdown_content: str,
    *,
    target_document: Mapping[str, Any] | None,
    title: str,
    media_assets: Mapping[str, Any] | None,
    artifact_store: str,
) -> dict:
    """Convert Markdown against a provider target, replace it, then read back IR."""
    markdown = markdown_content.strip()
    if not markdown:
        raise ValueError('Markdown draft is empty.')
    heading = re.search(r'^#\s+(.+?)\s*$', markdown, flags=re.MULTILINE)
    document_title = (heading.group(1).strip() if heading else title.strip()) or '未命名文档'
    return _replace_document_and_read_back(
        markdown_content,
        title=document_title,
        target_document=target_document,
        media_assets=media_assets,
        artifact_store=artifact_store,
        source_format='markdown',
    )


def _replace_document_and_read_back(
    content: str | WriterDocument,
    *,
    title: str,
    artifact_store: str,
    source_format: str,
    target_document: Mapping[str, Any] | None = None,
    media_assets: Mapping[str, Any] | None = None,
) -> dict:
    """Replace a Feishu document through the existing reference-preserving writer path."""
    root = _action_root(artifact_store, 'sync-document')
    if target_document:
        target = TargetDocument.model_validate(target_document)
    else:
        created = _json_loads(
            WriterResourceToolkit().create_document(title=title.strip() or '未命名文档'), {},
        )
        target = TargetDocument.model_validate(created)

    media_library = (
        MediaAssetLibrary.model_validate(media_assets) if media_assets else None
    )
    if isinstance(content, WriterDocument):
        publish_document = content.model_copy(deep=True)
    else:
        publish_document = parse_document_markdown(
            content,
            document_id=f'writer-document-{uuid.uuid4()}',
            stage='final',
            media_assets=media_library,
        )
        # Drafting and revision tools already retain the local path beside the
        # media asset id. Preserve the same established reference shape when
        # Markdown is converted for provider delivery.
        if media_library is not None:
            for block in publish_document.iter_blocks():
                if block.type != 'image':
                    continue
                for reference in block.references:
                    asset = media_library.assets.get(reference.get('id'))
                    if asset is not None and asset.uri:
                        reference.setdefault('path', asset.uri)

    payload = _json_loads(WriterResourceToolkit().replace_document(
        content_json=json.dumps(publish_document.model_dump(), ensure_ascii=False),
        source_document_json=json.dumps(publish_document.model_dump(), ensure_ascii=False),
        target_document_json=json.dumps(target.model_dump(), ensure_ascii=False),
        media_assets_json=(
            json.dumps(media_library.model_dump(), ensure_ascii=False)
            if media_library is not None else ''
        ),
    ), {})
    write_result = payload.get('publish_result') or {}
    persisted = WriterDocument.model_validate(payload.get('draft_document') or {})
    persisted.ui_editable = True
    result = PatchResult(
        success=True,
        message=(
            'Markdown converted to IR and document replaced.'
            if source_format == 'markdown'
            else 'Document written to Feishu and read back as Writer IR.'
        ),
        meta={
            'mode': 'replace',
            'source_format': source_format,
            'write_result': write_result,
        },
    )
    return {
        'success': True,
        'changed': True,
        'feishu_synced': True,
        'patch_result': result.model_dump(),
        'persisted_document': persisted.model_dump(),
    }


def _action_result_path(result: dict, key: str | None = None) -> str:
    path = result.get('artifact_path') if key is None else (
        (result.get('metadata') or {}).get('artifact_paths') or {}
    ).get(key)
    if not path:
        raise ValueError(f'Writer tool did not return artifact {key or "primary"!r}.')
    return path


def writer_locate_revision_target(
    base_document_path: str,
    writing_context_path: str,
    revision_task_path: str,
) -> str:
    """Locate the WriterDocument blocks affected by a revision task."""
    content = WriterRevisionToolkit().locate_revision_target(
        writing_task_json=_read_json_string(revision_task_path),
        writer_document_json=_read_json_string(base_document_path),
        writing_context_json=_read_json_string(writing_context_path),
    )
    return _save_json_artifact(
        'locate_result', content, writer_schema('revision.LocateResult'),
        directory=_run_root('revision-locate'),
    )


def writer_generate_modify_plan(
    base_document_path: str,
    writing_context_path: str,
    revision_task_path: str,
    locate_result_path: str,
) -> str:
    """Build a ModifyPlan for the located revision targets."""
    content = WriterRevisionToolkit().generate_modify_plan(
        writing_task_json=_read_json_string(revision_task_path),
        writer_document_json=_read_json_string(base_document_path),
        locate_result_json=_read_json_string(locate_result_path),
        writing_context_json=_read_json_string(writing_context_path),
    )
    return _save_json_artifact(
        'modify_plan', content, writer_schema('revision.ModifyPlan'),
        directory=_run_root('revision-plan'),
    )


def writer_generate_revision_set(
    base_document_path: str,
    writing_context_path: str,
    modify_plan_path: str,
    media_assets_path: str = '',
) -> str:
    """Generate an IR PatchSet or Markdown StringReplaceSet from a ModifyPlan."""
    document = _read_json_string(base_document_path)
    toolkit = WriterRevisionToolkit()
    is_markdown = Path(base_document_path).suffix.lower() in {'.md', '.markdown', '.txt'}
    if is_markdown:
        content = toolkit.generate_string_replace_set(
            markdown_document=document,
            modify_plan_json=_read_json_string(modify_plan_path),
            writing_context_json=_read_json_string(writing_context_path),
        )
        schema_name = writer_schema('revision.StringReplaceSet')
    else:
        content = toolkit.generate_patch_set(
            writer_document_json=document,
            modify_plan_json=_read_json_string(modify_plan_path),
            writing_context_json=_read_json_string(writing_context_path),
            media_assets_json=(
                _read_json_string(media_assets_path) if media_assets_path else ''
            ),
        )
        schema_name = writer_schema('revision.PatchSet')
    return _save_json_artifact(
        'revision_set', content, schema_name,
        directory=_run_root('revision-patch'),
    )


def writer_apply_revision(
    base_document_path: str,
    writing_context_path: str,
    revision_set_path: str,
    media_assets_path: str = '',
) -> dict:
    """Apply an IR patch or Markdown string replacements locally."""
    root = _run_root('apply-revision')
    is_body_step = require_context().params.get('step_id') == 'write_document'
    is_markdown = Path(base_document_path).suffix.lower() in {'.md', '.markdown', '.txt'}
    toolkit = WriterRevisionToolkit()
    if is_markdown:
        payload = _json_loads(toolkit.apply_string_replace(
            markdown_document=_read_json_string(base_document_path),
            string_replace_set_json=_read_json_string(revision_set_path),
            writing_context_json=_read_json_string(writing_context_path),
        ), {})
        if media_assets_path:
            payload['revised_document'] = _fill_markdown_media_placeholders(
                payload.get('revised_document') or '',
                _read_json_file(media_assets_path),
            )
        result_schema = writer_schema('revision.StringReplaceResult')
    else:
        payload = _json_loads(toolkit.apply_revision(
            writer_document_json=_read_json_string(base_document_path),
            patch_set_json=_read_json_string(revision_set_path),
            writing_context_json=_read_json_string(writing_context_path),
            media_assets_json=(
                _read_json_string(media_assets_path) if media_assets_path else ''
            ),
            sync_provider=not is_body_step,
            allow_outline=not is_body_step,
        ), {})
        result_schema = writer_schema('revision.PatchResult')
    document_key = 'draft_document' if is_body_step else 'outline_document'
    revised_document = _save_writer_document(
        document_key,
        payload.get('revised_document') or {},
        expected_stage=(None if is_markdown or is_body_step else 'outline'),
        editable=is_body_step,
        directory=root,
    )
    if is_body_step:
        _emit_draft_markdown_preview(revised_document)

    result = {
        'revision_result': _save_json_artifact(
            'revision_result',
            json.dumps(
                payload.get('string_replace_result') or payload.get('patch_result') or {},
                ensure_ascii=False,
            ),
            result_schema,
            directory=root,
        ),
        document_key: revised_document,
        'write_result': '',
    }
    if payload.get('write_result'):
        result['write_result'] = _save_json_artifact(
            'write_result',
            json.dumps(payload['write_result'], ensure_ascii=False),
            writer_schema('revision.PatchResult'),
            directory=root,
        )
    return result


def writer_convert_markdown_to_ir(content_path: str, stage: str = 'final') -> str:
    """Convert the supported Markdown subset to Writer IR for provider delivery."""
    markdown = _read_json_string(content_path)
    document = parse_document_markdown(
        markdown,
        document_id=f'writer-document-{uuid.uuid4()}',
        stage=stage,
    )
    return _save_writer_document(
        'delivery_document',
        document.model_dump(exclude_defaults=True),
        expected_stage=stage,
        directory=_run_root('markdown-to-ir'),
    )


def writer_publish_revision(
    source_document_path: str,
    revision_set_path: str,
    media_assets_path: str = '',
) -> dict:
    """Apply a prepared local revision to its bound source document."""
    root = _run_root('publish-revision')
    payload = _json_loads(WriterResourceToolkit().publish_revision(
        source_document_json=_read_json_string(source_document_path),
        patch_set_json=_read_json_string(revision_set_path),
        media_assets_json=(
            _read_json_string(media_assets_path) if media_assets_path else ''
        ),
    ), {})
    return _save_publish_payload(payload, root)


def writer_replace_document(
    content_path: str,
    source_document_path: str,
    target_document_path: str = '',
    target_uri: str = '',
    media_assets_path: str = '',
) -> dict:
    """Replace a bound cloud source with the selected final WriterDocument."""
    root = _run_root('replace-document')
    payload = _json_loads(WriterResourceToolkit().replace_document(
        content_json=_read_json_string(content_path),
        source_document_json=_read_json_string(source_document_path),
        target_document_json=(
            _read_json_string(target_document_path) if target_document_path else ''
        ),
        target_uri=target_uri,
        media_assets_json=(
            _read_json_string(media_assets_path) if media_assets_path else ''
        ),
    ), {})
    return _save_publish_payload(payload, root)


def writer_append_document(
    content_path: str,
    target_document_path: str = '',
    target_uri: str = '',
    publish_outline: bool = False,
    media_assets_path: str = '',
) -> dict:
    """Append a local WriterDocument to a Feishu target and return its confirmed IR."""
    root = _run_root('append-document')
    payload = _json_loads(WriterResourceToolkit().append_document(
        content_json=_read_json_string(content_path),
        target_document_json=(
            _read_json_string(target_document_path) if target_document_path else ''
        ),
        target_uri=target_uri,
        publish_outline=publish_outline,
        media_assets_json=(
            _read_json_string(media_assets_path) if media_assets_path else ''
        ),
    ), {})
    return _save_publish_payload(payload, root)


def writer_create_document(
    title: str,
    parent_uri: str = '',
) -> str:
    """Create an empty Feishu document and return its target artifact."""
    root = _run_root('create-document')
    content = WriterResourceToolkit().create_document(
        title=title,
        parent_uri=parent_uri,
    )
    return _save_json_artifact(
        'target_document',
        content,
        writer_schema('task.TargetDocument'),
        directory=root,
    )


def _draft_workspace_fingerprint(
    operation: str,
    user_input: str,
    writing_task_path: str,
    writing_context_path: str,
    media_assets_path: str,
    outline_document_path: str,
    source_document_path: str,
    draft_document_path: str,
    target_document_path: str,
) -> str:
    payload = json.dumps({
        'operation': operation,
        'user_input': user_input,
        'writing_task_path': writing_task_path,
        'writing_context_path': writing_context_path,
        'media_assets_path': media_assets_path,
        'outline_document_path': outline_document_path,
        'source_document_path': source_document_path,
        'draft_document_path': draft_document_path,
        'target_document_path': target_document_path,
    }, ensure_ascii=False, sort_keys=True)
    return hashlib.sha256(payload.encode('utf-8')).hexdigest()


def _draft_workspace_state(fingerprint: str) -> tuple[dict[str, Any], Path]:
    path = _workspace_root() / 'writer-workflow' / f'draft-workspace-{fingerprint}.json'
    if path.exists():
        try:
            state = json.loads(path.read_text(encoding='utf-8'))
        except (OSError, json.JSONDecodeError):
            state = {}
        if state.get('fingerprint') == fingerprint:
            return state, path
    return {
        'schema_version': 1,
        'fingerprint': fingerprint,
        'result': {},
        'completed': False,
    }, path


def _persist_draft_workspace_state(
    state: dict[str, Any],
    path: Path,
    *,
    completed: bool = False,
) -> None:
    state['completed'] = completed
    path.parent.mkdir(parents=True, exist_ok=True)
    temporary = path.with_name(f'.{path.name}.{uuid.uuid4().hex}.tmp')
    temporary.write_text(
        json.dumps(state, ensure_ascii=False, indent=2),
        encoding='utf-8',
    )
    temporary.replace(path)


def _cloud_bound_ir(path: str) -> bool:
    if not path or Path(path).suffix.lower() != '.lmd':
        return False
    document = _read_json_file(path)
    return isinstance(document, dict) and bool(document.get('provider_binding'))


def _modify_plan_needs_media(path: str) -> bool:
    plan = _read_json_file(path)
    return any(
        isinstance(instruction, dict) and bool(instruction.get('visual_instruction'))
        for instruction in (plan.get('instructions') or [])
    )


def _save_draft_workspace_artifacts(result: Mapping[str, Any]) -> list[str]:
    from lazymind.chat.engine.subagent.tools import save_artifacts

    ctx = require_context()
    allowed = set(ctx.output_slots or [])
    entries: list[dict[str, Any]] = []
    saved_keys: list[str] = []
    for key, value in result.items():
        if allowed and key not in allowed:
            continue
        if key == 'draft_blocks' and isinstance(value, list):
            for path in value:
                if isinstance(path, str) and Path(path).is_file():
                    entries.append({
                        'key': key,
                        'value': path,
                        'content_type': 'file',
                    })
                    saved_keys.append(key)
            continue
        if isinstance(value, str) and Path(value).is_file():
            entries.append({
                'key': key,
                'value': value,
                'content_type': 'file',
            })
            saved_keys.append(key)
    if not entries:
        raise RuntimeError('Draft workspace produced no saveable artifacts.')
    saved = save_artifacts(entries)
    if saved.get('status') != 'ok':
        raise RuntimeError(f'Failed to save draft workspace artifacts: {saved!r}')
    return list(dict.fromkeys(saved_keys))


def _draft_workspace_completion(
    result: Mapping[str, Any],
    saved_keys: list[str],
) -> dict[str, Any]:
    draft_blocks = result.get('draft_blocks')
    return {
        'status': 'completed',
        'operation': result.get('operation'),
        'representation': result.get('representation'),
        'draft_section_count': len(draft_blocks) if isinstance(draft_blocks, list) else None,
        'saved_artifact_keys': saved_keys,
        'warnings': list(result.get('warnings') or []),
        'artifacts_saved': True,
        'control': {'next_step': '__end__'},
    }


def writer_draft_workspace() -> dict:
    """Run one existing draft workflow branch through deterministic top-level tools."""
    _emit_writer_progress('正在读取成稿任务与已有 checkpoint')
    user_input = _authoritative_writer_user_input('')
    writer_command_path = _authoritative_writer_input_path(
        'writer_command', require_workflow_binding=True,
    )
    writing_task_path = _authoritative_writer_input_path(
        'writing_task', require_workflow_binding=True,
    )
    writing_context_path = _authoritative_writer_input_path(
        (
            'writing_context_after_draft',
            'writing_context_after_outline',
            'writing_context',
        ),
        require_workflow_binding=True,
    )
    media_assets_path = _authoritative_writer_input_path('media_assets')
    outline_document_path = _authoritative_writer_input_path('outline_document')
    source_document_path = _authoritative_writer_input_path('source_document')
    draft_document_path = _authoritative_writer_input_path('draft_document')
    target_document_path = _authoritative_writer_input_path('target_document')
    command = _load_writer_command(writer_command_path)
    continuing_completed_outline = (
        command.target_stage == 'outline'
        and command.action in {'create', 'use_outline'}
        and bool(outline_document_path)
    )
    if command.target_stage != 'document' and not continuing_completed_outline:
        raise ValueError('write_document requires writer_command.target_stage="document".')
    command_operation = {
        'create': 'generate',
        'use_outline': 'generate',
        'rewrite': 'rewrite',
        'revise': 'revise',
    }.get(command.action)
    if command_operation is None or (
        command.action == 'revise' and command.source_role != 'document'
    ):
        raise ValueError(
            f'WriterCommand action={command.action!r} source_role={command.source_role!r} '
            'cannot execute the document step.'
        )
    operation = command_operation
    if operation not in {'generate', 'rewrite', 'revise'}:
        raise ValueError('operation must be generate, rewrite, or revise.')
    if not writing_task_path or not writing_context_path:
        raise ValueError('writing_task_path and writing_context_path are required.')
    if continuing_completed_outline:
        # The immutable command still describes the original outline request.
        # A package-declared completed continuation reuses that request and the
        # latest selected outline revision; the UI's generic action text is not
        # a replacement writing brief.
        user_input = command.user_instruction
    if user_input and command.request_fingerprint != _writer_request_fingerprint(user_input):
        raise ValueError(
            'writer_command belongs to a different user request; restart from prepare.'
        )
    if operation == 'revise' and not user_input:
        user_input = command.user_instruction

    fingerprint = _draft_workspace_fingerprint(
        operation,
        user_input,
        writing_task_path,
        writing_context_path,
        media_assets_path,
        outline_document_path,
        source_document_path,
        draft_document_path,
        target_document_path,
    )
    state, checkpoint_path = _draft_workspace_state(fingerprint)
    result: dict[str, Any] = dict(state.get('result') or {})
    result['operation'] = operation
    result['writer_command'] = writer_command_path
    if state.get('completed'):
        _emit_writer_progress('正在复用已完成的成稿 checkpoint')
        saved_keys = list(state.get('saved_artifact_keys') or [])
        if not state.get('artifacts_saved'):
            saved_keys = _save_draft_workspace_artifacts(result)
            state['artifacts_saved'] = True
            state['saved_artifact_keys'] = saved_keys
            _persist_draft_workspace_state(state, checkpoint_path, completed=True)
        return _draft_workspace_completion(result, saved_keys)

    resolved_media = str(result.get('resolved_media_assets') or '')
    if operation in {'generate', 'rewrite'}:
        if operation == 'generate':
            if not outline_document_path:
                raise ValueError('outline_document_path is required for generate.')
            if not result.get('section_instructions'):
                _emit_writer_progress(
                    '正在根据大纲规划 section instructions、视觉需求与辅助任务'
                )
                planning = writer_generate_section_instructions(
                    writing_task_path=writing_task_path,
                    outline_path=outline_document_path,
                    writing_context_path=writing_context_path,
                )
                result.update({
                    'section_instructions': planning['section_instructions'],
                    'visual_plan': planning['visual_plan'],
                    'visual_need_count': planning['visual_need_count'],
                    'section_count': planning['section_count'],
                    'warnings': list(planning.get('warnings') or []),
                })
                state['result'] = result
                _persist_draft_workspace_state(state, checkpoint_path)
                _emit_writer_progress(
                    f"section instructions 已完成，共 {planning['section_count']} 章",
                    section_total=planning['section_count'],
                    visual_need_count=planning['visual_need_count'],
                )
            else:
                instructions = _read_json_file(result['section_instructions'])
                section_count = len((instructions or {}).get('instructions') or [])
                result['section_count'] = section_count
                _emit_writer_progress(
                    f'已复用 section instructions checkpoint，共 {section_count} 章',
                    section_total=section_count,
                )
            document_title = ''
            outline_path = outline_document_path
        else:
            rewrite_base = draft_document_path or source_document_path
            if not rewrite_base:
                raise ValueError('draft_document_path or source_document_path is required for rewrite.')
            if not result.get('section_instructions'):
                _emit_writer_progress(
                    '正在规划全文重写 section instructions、视觉需求与辅助任务'
                )
                planning = writer_generate_rewrite_section_instructions(
                    writing_task_path=writing_task_path,
                    source_document_path=rewrite_base,
                    writing_context_path=writing_context_path,
                )
                result.update({
                    'section_instructions': planning['section_instructions'],
                    'visual_plan': planning['visual_plan'],
                    'visual_need_count': planning['visual_need_count'],
                    'section_count': planning['section_count'],
                    'document_title': planning.get('document_title') or '',
                    'warnings': list(planning.get('warnings') or []),
                })
                state['result'] = result
                _persist_draft_workspace_state(state, checkpoint_path)
                _emit_writer_progress(
                    f"重写 section instructions 已完成，共 {planning['section_count']} 章",
                    section_total=planning['section_count'],
                    visual_need_count=planning['visual_need_count'],
                )
            else:
                instructions = _read_json_file(result['section_instructions'])
                section_count = len((instructions or {}).get('instructions') or [])
                result['section_count'] = section_count
                _emit_writer_progress(
                    f'已复用重写 section instructions checkpoint，共 {section_count} 章',
                    section_total=section_count,
                )
            document_title = str(result.get('document_title') or '')
            outline_path = ''

        task = _read_json_file(writing_task_path)
        representation = str(((task.get('output') or {}).get('representation') or '')).strip()
        needs_media = representation == 'ir' or int(result.get('visual_need_count') or 0) > 0
        if needs_media and not resolved_media:
            if not media_assets_path:
                raise ValueError('media_assets_path is required when the draft has visual media.')
            _emit_writer_progress(
                f"正在准备 {int(result.get('visual_need_count') or 0)} 项视觉素材与辅助资源",
                visual_need_count=int(result.get('visual_need_count') or 0),
            )
            media = writer_resolve_visual_media(
                visual_plan_path=result['visual_plan'],
                media_assets_path=media_assets_path,
            )
            resolved_media = media['resolved_media_assets']
            result['resolved_media_assets'] = resolved_media
            result['warnings'] = [
                *(result.get('warnings') or []),
                *(media.get('warnings') or []),
            ]
            state['result'] = result
            _persist_draft_workspace_state(state, checkpoint_path)
            _emit_writer_progress('辅助资源已准备完成，正在启动章节生成')
        elif not needs_media:
            _emit_writer_progress('无需准备视觉素材，正在启动章节生成')

        if not result.get('draft_document'):
            _emit_writer_progress('章节准备完成，正在优先生成并流式输出第 1 章')
            generated = writer_generate_draft_document(
                writing_task_path=writing_task_path,
                section_instructions_path=result['section_instructions'],
                writing_context_path=writing_context_path,
                outline_path=outline_path,
                visual_plan_path=result['visual_plan'],
                resolved_media_assets_path=resolved_media,
                document_title=document_title,
            )
            result['draft_blocks'] = generated['draft_blocks']
            result['draft_document'] = generated['draft_document']
            result['representation'] = generated['representation']
            state['result'] = result
            _persist_draft_workspace_state(state, checkpoint_path)

        should_write_back = (
            _cloud_bound_ir(source_document_path)
            and (operation == 'rewrite' or not draft_document_path)
        )
        if should_write_back and not result.get('document_write_result'):
            _emit_writer_progress('成稿已组装，正在写回目标文档')
            published = writer_replace_document(
                content_path=result['draft_document'],
                source_document_path=source_document_path,
                target_document_path=target_document_path,
                media_assets_path=resolved_media,
            )
            result['document_write_result'] = published['publish_result']
            result['draft_document'] = published['draft_document']
            state['result'] = result
            _persist_draft_workspace_state(state, checkpoint_path)
    else:
        base_document = draft_document_path or source_document_path
        if not user_input or not base_document:
            raise ValueError('user_input and a draft or source document are required for revise.')
        if not result.get('document_revision_task'):
            result['document_revision_task'] = writer_build_revision_task(
                query=user_input,
                base_document_path=base_document,
            )
            state['result'] = result
            _persist_draft_workspace_state(state, checkpoint_path)
        if not result.get('document_locate_result'):
            result['document_locate_result'] = writer_locate_revision_target(
                base_document_path=base_document,
                writing_context_path=writing_context_path,
                revision_task_path=result['document_revision_task'],
            )
            state['result'] = result
            _persist_draft_workspace_state(state, checkpoint_path)
        if not result.get('document_modify_plan'):
            result['document_modify_plan'] = writer_generate_modify_plan(
                base_document_path=base_document,
                writing_context_path=writing_context_path,
                revision_task_path=result['document_revision_task'],
                locate_result_path=result['document_locate_result'],
            )
            state['result'] = result
            _persist_draft_workspace_state(state, checkpoint_path)
        if _modify_plan_needs_media(result['document_modify_plan']) and not resolved_media:
            if not media_assets_path:
                raise ValueError('media_assets_path is required for a visual revision.')
            media = writer_resolve_revision_media(
                modify_plan_path=result['document_modify_plan'],
                media_assets_path=media_assets_path,
            )
            resolved_media = media['resolved_media_assets']
            result['resolved_media_assets'] = resolved_media
            result['warnings'] = list(media.get('warnings') or [])
            state['result'] = result
            _persist_draft_workspace_state(state, checkpoint_path)
        if not result.get('document_revision_set'):
            result['document_revision_set'] = writer_generate_revision_set(
                base_document_path=base_document,
                writing_context_path=writing_context_path,
                modify_plan_path=result['document_modify_plan'],
                media_assets_path=resolved_media,
            )
            state['result'] = result
            _persist_draft_workspace_state(state, checkpoint_path)
        if not result.get('draft_document'):
            applied = writer_apply_revision(
                base_document_path=base_document,
                writing_context_path=writing_context_path,
                revision_set_path=result['document_revision_set'],
                media_assets_path=resolved_media,
            )
            result['document_revision_result'] = applied['revision_result']
            result['draft_document'] = applied['draft_document']
            state['result'] = result
            _persist_draft_workspace_state(state, checkpoint_path)
        if not draft_document_path and _cloud_bound_ir(source_document_path) \
                and not result.get('document_write_result'):
            published = writer_publish_revision(
                source_document_path=source_document_path,
                revision_set_path=result['document_revision_set'],
                media_assets_path=resolved_media,
            )
            result['document_write_result'] = published['publish_result']
            result['draft_document'] = published['draft_document']
            state['result'] = result
            _persist_draft_workspace_state(state, checkpoint_path)

    if not result.get('writing_context_after_draft'):
        _emit_writer_progress('正在更新成稿上下文')
        result['writing_context_after_draft'] = writer_update_writing_context(
            content_artifact_path=result['draft_document'],
            writing_context_path=writing_context_path,
        )
    state['result'] = result
    _persist_draft_workspace_state(state, checkpoint_path)
    _emit_writer_progress('成稿校验完成，正在保存工作区结果')
    saved_keys = _save_draft_workspace_artifacts(result)
    state['artifacts_saved'] = True
    state['saved_artifact_keys'] = saved_keys
    _persist_draft_workspace_state(state, checkpoint_path, completed=True)
    return _draft_workspace_completion(result, saved_keys)


_FLAT_OUTPUT_SLOTS = {
    'draft_document': 'flat_draft_document',
    'writing_context_after_draft': 'flat_writing_context_after_draft',
    'visual_plan': 'flat_visual_plan',
    'resolved_media_assets': 'flat_resolved_media_assets',
}


def _published_flat_draft_workspace_result(
    result: Mapping[str, Any],
) -> dict[str, Any]:
    published = dict(result)
    for source, target in _FLAT_OUTPUT_SLOTS.items():
        if source in published:
            published[target] = published.pop(source)
    return published


def writer_flat_draft_workspace() -> dict:
    """Generate one new flat document on the dedicated flat Workflow path."""
    _emit_writer_progress('正在读取短文成稿任务与已有 checkpoint')
    user_input = _authoritative_writer_user_input('')
    writer_command_path = _authoritative_writer_input_path(
        'writer_command', require_workflow_binding=True,
    )
    writing_task_path = _authoritative_writer_input_path(
        'writing_task', require_workflow_binding=True,
    )
    writing_context_path = _authoritative_writer_input_path(
        'writing_context', require_workflow_binding=True,
    )
    media_assets_path = _authoritative_writer_input_path('media_assets')
    command = _load_writer_command(writer_command_path)
    if command.structure_mode != 'flat':
        raise ValueError(
            'write_flat_document requires writer_command.structure_mode="flat".'
        )
    if command.target_stage != 'document' or command.action != 'create':
        raise ValueError('write_flat_document only supports new flat document creation.')
    if not writing_task_path or not writing_context_path:
        raise ValueError('writing_task_path and writing_context_path are required.')
    if user_input and command.request_fingerprint != _writer_request_fingerprint(user_input):
        raise ValueError(
            'writer_command belongs to a different user request; restart from prepare.'
        )

    operation = 'generate'
    fingerprint = _draft_workspace_fingerprint(
        operation,
        user_input,
        writing_task_path,
        writing_context_path,
        media_assets_path,
        '',
        '',
        '',
        '',
    )
    state, checkpoint_path = _draft_workspace_state(fingerprint)
    result: dict[str, Any] = dict(state.get('result') or {})
    result['operation'] = operation
    result['writer_command'] = writer_command_path
    if state.get('completed'):
        _emit_writer_progress('正在复用已完成的短文成稿 checkpoint')
        saved_keys = list(state.get('saved_artifact_keys') or [])
        if not state.get('artifacts_saved'):
            saved_keys = _save_draft_workspace_artifacts(
                _published_flat_draft_workspace_result(result)
            )
            state['artifacts_saved'] = True
            state['saved_artifact_keys'] = saved_keys
            _persist_draft_workspace_state(state, checkpoint_path, completed=True)
        return _draft_workspace_completion(result, saved_keys)

    task = _read_json_file(writing_task_path)
    representation = str((task.get('output') or {}).get('representation') or '').strip()
    if representation not in {'markdown', 'ir'}:
        raise ValueError(
            "Flat short-document generation requires 'markdown' or 'ir' representation."
        )
    if not result.get('short_writing_plan'):
        result['short_writing_plan'] = writer_generate_short_writing_plan(
            writing_task_path=writing_task_path,
            writing_context_path=writing_context_path,
        )
        state['result'] = result
        _persist_draft_workspace_state(state, checkpoint_path)
    if not result.get('visual_plan'):
        planning = writer_generate_short_visual_plan(
            writing_task_path=writing_task_path,
            short_writing_plan_path=result['short_writing_plan'],
            writing_context_path=writing_context_path,
        )
        result.update({
            'visual_plan': planning['visual_plan'],
            'visual_need_count': planning['visual_need_count'],
            'warnings': [
                *(result.get('warnings') or []),
                *(planning.get('warnings') or []),
            ],
        })
        state['result'] = result
        _persist_draft_workspace_state(state, checkpoint_path)

    resolved_media = str(result.get('resolved_media_assets') or '')
    visual_need_count = int(result.get('visual_need_count') or 0)
    if visual_need_count > 0 and not resolved_media:
        if not media_assets_path:
            raise ValueError(
                'media_assets_path is required when the short draft has visual media.'
            )
        media = writer_resolve_visual_media(
            visual_plan_path=result['visual_plan'],
            media_assets_path=media_assets_path,
        )
        resolved_media = media['resolved_media_assets']
        result['resolved_media_assets'] = resolved_media
        result['warnings'] = [
            *(result.get('warnings') or []),
            *(media.get('warnings') or []),
        ]
        state['result'] = result
        _persist_draft_workspace_state(state, checkpoint_path)
    if not result.get('draft_document'):
        result['draft_document'] = writer_generate_short_document(
            writing_task_path=writing_task_path,
            short_writing_plan_path=result['short_writing_plan'],
            writing_context_path=writing_context_path,
            visual_plan_path=result['visual_plan'],
            resolved_media_assets_path=resolved_media,
        )
        result['representation'] = representation
        state['result'] = result
        _persist_draft_workspace_state(state, checkpoint_path)

    if not result.get('writing_context_after_draft'):
        _emit_writer_progress('正在更新短文成稿上下文')
        result['writing_context_after_draft'] = writer_update_writing_context(
            content_artifact_path=result['draft_document'],
            writing_context_path=writing_context_path,
        )
    state['result'] = result
    _persist_draft_workspace_state(state, checkpoint_path)
    _emit_writer_progress('短文成稿校验完成，正在保存工作区结果')
    saved_keys = _save_draft_workspace_artifacts(
        _published_flat_draft_workspace_result(result)
    )
    state['artifacts_saved'] = True
    state['saved_artifact_keys'] = saved_keys
    _persist_draft_workspace_state(state, checkpoint_path, completed=True)
    return _draft_workspace_completion(result, saved_keys)
