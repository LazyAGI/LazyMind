"""Common writer tools with string/JSON inputs and outputs."""
from __future__ import annotations

import hashlib
import json
import os
import re
import tempfile
import time
import uuid
from collections.abc import Callable
from copy import deepcopy
from pathlib import Path
from queue import Empty, Queue
from threading import Event, RLock
from typing import Any, ClassVar

from lazyllm import LOG, AutoModel, ThreadPoolExecutor
from lazyllm.tools.writer.data_models import (
    InputResource,
    MediaAssetLibrary,
    ModifyPlan,
    PatchResult,
    PatchSet,
    SectionInstruction,
    SectionInstructionList,
    TargetDocument,
    VisualInstruction,
    VisualPlan,
    WriterBlock,
    WriterDocument,
    WritingTask,
)
from lazyllm.tools.writer.tools import (
    WriterContextTools,
    WriterDraftingTools,
    WriterMultimodalTools,
    WriterPlanningTools,
    WriterQualityTools,
    WriterResourceTools,
    WriterRevisionTools,
)
from lazyllm.tools.writer.numbering import materialize_markdown
from lazyllm.tools.writer.utils import (
    render_block_markdown,
    save_artifact_json,
    writer_document_to_markdown,
)

WRITER_DATA_MODEL_SCHEMA_PREFIX = 'lazyllm.tools.writer.data_models'
_FEISHU_URL_RE = re.compile(
    r"https?://[^\s<>\"']*(?:feishu\.(?:cn|com)|larksuite\.com)/"
    r"[^\s<>\"'，。；！？、（）【】《》「」『』]+",
    re.IGNORECASE,
)
_CHINESE_CHAR_LIMIT_RE = re.compile(
    r'(?P<prefix>不超过|至多|最多|约|大约|大概)?\s*'
    r'(?P<value>\d+(?:\.\d+)?)\s*(?P<unit>万|千)?\s*字'
    r'(?P<suffix>左右|上下|以内|以下)?'
)
_MARKDOWN_ATX_HEADING_RE = re.compile(
    r'^(?P<indent> {0,3})(?P<marks>#{1,6})[ \t]+'
    r'(?P<title>.*?)(?:[ \t]+#+)?[ \t]*$',
)
_MARKDOWN_FENCE_RE = re.compile(r'^ {0,3}(?P<marks>`{3,}|~{3,})')
_MARKDOWN_DRAFT_ROOT_ERROR = 'Markdown draft section must contain exactly one H2 root heading.'


def _extract_length_constraints(query: str) -> dict[str, int]:
    match = _CHINESE_CHAR_LIMIT_RE.search(query)
    if match is None:
        return {}
    multiplier = {'万': 10000, '千': 1000}.get(match.group('unit'), 1)
    target_chars = int(float(match.group('value')) * multiplier)
    approximate = (
        match.group('prefix') in {'约', '大约', '大概'}
        or match.group('suffix') in {'左右', '上下'}
    )
    return {
        'target_chars': target_chars,
        'max_chars': target_chars * 11 // 10 if approximate else target_chars,
    }


_MARKDOWN_NO_MEDIA_PATTERNS = (
    re.compile(
        r'(?:不要|不需要|无需|不用|禁止)\s*(?:使用|添加|插入|生成|展示|显示)?\s*'
        r'(?:任何\s*)?(?:图片|图像|插图|配图|视觉(?:素材|内容)?)'
        r'|不(?:使用|添加|插入|生成|展示|显示)\s*(?:任何\s*)?'
        r'(?:图片|图像|插图|配图|视觉(?:素材|内容)?)'
        r'|不插图|无图',
    ),
    re.compile(
        r'\b(?:no|without)\s+(?:any\s+)?(?:images?|pictures?|illustrations?|visuals?)\b'
        r"|\b(?:do\s+not|don't)\s+(?:use|include|add|generate|insert|show|display)\s+"
        r'(?:any\s+)?(?:images?|pictures?|illustrations?|visuals?)\b',
        re.IGNORECASE,
    ),
)


class DraftMarkdownStreamEventEmitter:
    """Publish one attempt-scoped Markdown preview for a Writer artifact."""

    MAX_DELTA_CHARS: ClassVar[int] = 2

    EVENT_TYPES: ClassVar[dict[str, str]] = {
        'start': 'artifact_stream_start',
        'delta': 'artifact_stream',
        'end': 'artifact_stream_end',
        'abort': 'artifact_stream_abort',
    }

    def __init__(
        self,
        emit: Callable[[dict[str, Any]], None],
        *,
        slot: str = 'draft_document',
    ) -> None:
        if not slot.strip():
            raise ValueError('slot must not be empty.')
        self._emit = emit
        self._slot = slot.strip()
        self._stream_id = uuid.uuid4().hex
        self._chunk_index = 0
        self._closed = False
        self._lock = RLock()
        with self._lock:
            self._publish_locked('start')

    @property
    def stream_id(self) -> str:
        return self._stream_id

    def feed(self, delta: str) -> None:
        if not delta:
            return
        with self._lock:
            if self._closed:
                return
            # Model providers and the IR/Markdown normalizers may deliver a
            # whole sentence or paragraph in one callback. Keep the artifact
            # stream's display contract stable by publishing small deltas while
            # preserving the exact text and order.
            for start in range(0, len(delta), self.MAX_DELTA_CHARS):
                self._publish_locked(
                    'delta',
                    delta=delta[start:start + self.MAX_DELTA_CHARS],
                )

    def end(self) -> None:
        self._finish('end')

    def abort(self, message: str = '') -> None:
        self._finish('abort', message=message)

    def flush(self) -> None:
        """Compatibility no-op: deltas are already published immediately."""

    def _finish(self, event: str, *, message: str = '') -> None:
        with self._lock:
            if self._closed:
                return
            self._publish_locked(event, message=message)
            self._closed = True

    def _publish_locked(self, event: str, *, delta: str = '', message: str = '') -> None:
        self._chunk_index += 1
        payload: dict[str, Any] = {
            'type': self.EVENT_TYPES[event],
            'slot': self._slot,
            'content_type': 'text/markdown',
            'stream_id': self._stream_id,
            'chunk_index': self._chunk_index,
        }
        if event == 'delta':
            payload['delta'] = delta
        elif event == 'abort' and message:
            payload['message'] = message
        try:
            self._emit(payload)
        except Exception as exc:  # noqa: BLE001 - preview forwarding is best effort.
            LOG.warning('[Writer] failed to forward artifact stream event: %s', exc)


def writer_schema(name: str) -> str:
    return f'{WRITER_DATA_MODEL_SCHEMA_PREFIX}.{name}'


def _json_dumps(value: Any) -> str:
    return json.dumps(value, ensure_ascii=False, indent=2)


def _json_loads(value: str, default: Any = None) -> Any:
    text = (value or '').strip()
    if not text:
        return default
    parsed = json.loads(text)
    if isinstance(parsed, dict) and 'data' in parsed:
        return parsed['data']
    return parsed


def _bind_document_cross_reference_targets(instructions: list[Any]) -> None:
    targets = list(dict.fromkeys(
        str(target)
        for instruction in instructions if isinstance(instruction, dict)
        for target in [
            (instruction.get('meta') or {}).get('outline_node_id'),
            *[
                item.get('target')
                for item in (instruction.get('meta') or {}).get('cross_references') or []
                if isinstance(item, dict)
            ],
        ]
        if target
    ))
    for instruction in instructions:
        if isinstance(instruction, dict):
            instruction.setdefault('meta', {})['cross_reference_targets'] = targets


def _read_artifact_data(path: str) -> Any:
    if Path(path).suffix.lower() in {'.md', '.markdown'}:
        return Path(path).read_text(encoding='utf-8')
    with open(path, 'r', encoding='utf-8') as fh:
        raw = json.load(fh)
    if isinstance(raw, dict) and 'data' in raw:
        return raw['data']
    return raw


def _temp_root() -> Path:
    root = Path(tempfile.gettempdir()) / 'lazymind-writer-tools' / uuid.uuid4().hex
    root.mkdir(parents=True, exist_ok=True)
    return root


def _write_input_artifact(root: Path, filename: str, data: Any, schema_name: str) -> str:
    return save_artifact_json(
        data,
        str(root / filename),
        schema_name=schema_name,
        created_by='WriterToolkit',
    )


def _document_value(value: str) -> Any:
    try:
        return _json_loads(value, {})
    except json.JSONDecodeError:
        return value


def _markdown_media_is_explicitly_disabled(writing_task: dict[str, Any]) -> bool:
    """Return whether the structured Writer request policy forbids visual media."""
    policy = (writing_task.get('constraints') or {}).get('visual_policy') or {}
    if policy.get('allow_visuals') is False:
        return True
    query = str(writing_task.get('query') or '')
    return any(pattern.search(query) for pattern in _MARKDOWN_NO_MEDIA_PATTERNS)


def _requires_input_image_reuse(writing_task: dict[str, Any]) -> bool:
    visual_policy = (writing_task.get('constraints') or {}).get('visual_policy') or {}
    return visual_policy.get('require_input_image_reuse') is True


def _ensure_required_visual_plan(visual_plan: dict[str, Any], *, required: bool) -> None:
    if required and not (visual_plan.get('instructions') or []):
        raise ValueError('Required input image reuse produced no visual plan instructions.')


def _write_document_input(root: Path, name: str, value: str) -> str:
    content = _document_value(value)
    if isinstance(content, str):
        path = root / f'{name}.md'
        path.write_text(content, encoding='utf-8')
        return str(path)
    return _write_input_artifact(root, f'{name}.lmd', content, WriterToolkitBase.WRITER_IR_SCHEMA)


def _primary_data(result: dict) -> Any:
    artifact_path = result.get('artifact_path')
    if not artifact_path:
        raise ValueError(f'Writer tool did not return artifact_path: {result!r}')
    return _read_artifact_data(artifact_path)


def _markdown_heading_key(value: str) -> str:
    text = re.sub(r'^\s*\d+(?:\.\d+)*[、.．\s　]+', '', str(value or ''))
    return re.sub(r'[\s\W_]+', '', text, flags=re.UNICODE).lower()


def _markdown_heading_rows(lines: list[str]) -> list[tuple[int, int, str]]:
    rows: list[tuple[int, int, str]] = []
    fence_mark = ''
    for index, line in enumerate(lines):
        fence = _MARKDOWN_FENCE_RE.match(line)
        if fence:
            mark = fence.group('marks')
            if not fence_mark:
                fence_mark = mark
            elif mark[0] == fence_mark[0] and len(mark) >= len(fence_mark):
                fence_mark = ''
            continue
        if fence_mark:
            continue
        heading = _MARKDOWN_ATX_HEADING_RE.match(line)
        if heading:
            rows.append((index, len(heading.group('marks')), heading.group('title').strip()))
    return rows


def _normalize_streamed_markdown_section(
    markdown: str,
    instruction: SectionInstruction,
) -> str:
    """Repair relative Markdown headings without spending another model call."""
    title = instruction.section_title.strip()
    if not title:
        raise ValueError('Markdown draft section title must not be empty.')
    lines = str(markdown or '').replace('\r\n', '\n').replace('\r', '\n').strip().split('\n')
    rows = _markdown_heading_rows(lines)
    title_key = _markdown_heading_key(title)
    root_index = next(
        (index for index, _, heading_title in rows
         if _markdown_heading_key(heading_title) == title_key),
        -1,
    )

    removed: set[int] = set()
    if root_index >= 0:
        lines[root_index] = f'## {title}'
        removed.update(
            index for index, _, heading_title in rows
            if index != root_index and _markdown_heading_key(heading_title) == title_key
        )
    else:
        lines = [f'## {title}', '', *lines]
        root_index = 0
        rows = [(index + 2, level, heading_title) for index, level, heading_title in rows]

    child_rows = [
        (index, level, heading_title)
        for index, level, heading_title in rows
        if index != root_index and index not in removed
    ]
    if child_rows:
        shift = 3 - min(level for _, level, _ in child_rows)
        for index, level, heading_title in child_rows:
            lines[index] = f"{'#' * min(6, max(3, level + shift))} {heading_title}"

    result = '\n'.join(
        line for index, line in enumerate(lines) if index not in removed
    ).strip()
    top_rows = [row for row in _markdown_heading_rows(result.splitlines()) if row[1] <= 2]
    if len(top_rows) != 1 or top_rows[0][1] != 2:
        raise ValueError(_MARKDOWN_DRAFT_ROOT_ERROR)
    if _markdown_heading_key(top_rows[0][2]) != title_key:
        raise ValueError('Markdown draft section heading does not match its content_ref.')
    return result


def _result_data(result: dict, key: str) -> Any:
    path = ((result.get('metadata') or {}).get('artifact_paths') or {}).get(key)
    if not path:
        raise ValueError(f'Writer tool did not return artifact {key!r}: {result!r}')
    return _read_artifact_data(path)


def sync_writer_documents(
    source_value: Any,
    revised_value: Any,
    media_assets: Any = None,
    artifact_store: str = '',
) -> dict[str, Any]:
    """Persist one WriterDocument delta and return its provider-confirmed IR."""
    source = WriterDocument.model_validate(source_value)
    revised = WriterDocument.model_validate(revised_value)
    if source.document_id != revised.document_id:
        raise ValueError('WriterDocument document_id values must match.')
    for field in ('stage', 'revision', 'provider_binding'):
        if getattr(source, field) != getattr(revised, field):
            raise ValueError(f'WriterDocument {field} values must match.')
    for source_block in source.iter_blocks():
        revised_block = revised.block_by_id(source_block.node_id)
        if revised_block is None:
            continue
        revised_block.provider_binding = deepcopy(source_block.provider_binding)
        revised_block.provider_payload = deepcopy(source_block.provider_payload)
        revised_block.editable = source_block.editable

    library = MediaAssetLibrary.model_validate(media_assets) if media_assets else None
    root = Path(artifact_store) if artifact_store else _temp_root()
    root.mkdir(parents=True, exist_ok=True)
    if source.title == revised.title and source.blocks == revised.blocks:
        patch = PatchSet(
            patch_id=f'patch-{source.document_id}', target_doc_id=source.document_id,
        )
    else:
        revision = WriterRevisionTools(llm=None, artifact_store=str(root))
        patch = PatchSet.model_validate(_primary_data(
            revision.build_patch_set_from_documents(source, revised, library),
        ))
    changed = bool(patch.hunks or patch.new_title is not None)
    if changed:
        output = WriterResourceTools(
            llm=None, artifact_store=str(root),
        ).apply_patch_to_document(patch, source, media_assets=library)
        candidate = WriterDocument.model_validate(_result_data(output, 'persisted_document'))
        result = PatchResult.model_validate(_result_data(output, 'patch_result'))
    else:
        candidate = source.model_copy(deep=True)
        result = PatchResult(
            patch_id=patch.patch_id,
            success=True,
            message='No document changes.',
        )
    candidate.ui_editable = True
    return {
        'success': result.success,
        'changed': changed,
        'feishu_synced': result.success,
        'patch_set': patch.model_dump(),
        'patch_result': result.model_dump(),
        'persisted_document': candidate.model_dump(),
    }


def _feishu_url(user_input: str) -> str:
    match = _FEISHU_URL_RE.search(user_input or '')
    if not match:
        raise ValueError('A Feishu/Lark document URL is required.')
    return match.group(0).rstrip(').,;!?]}，。；！？】》」』')


def _extract_feishu_resources(user_input: str) -> list[dict]:
    resources: list[dict] = []
    seen: set[str] = set()
    for idx, match in enumerate(_FEISHU_URL_RE.finditer(user_input or '')):
        url = match.group(0).rstrip(').,;!?]}，。；！？】》」』')
        if url in seen:
            continue
        seen.add(url)
        resources.append({
            'resource_id': f'feishu_{idx}',
            'resource_type': 'url',
            'uri': url,
            'title': None,
            'mime_type': None,
            'summary': None,
            'meta': {'provider': 'feishu', 'role': 'background'},
        })
    return resources


def _set_document_editable(value: Any, *, stage: str | None = None) -> WriterDocument:
    document = WriterDocument.model_validate(value)
    if stage is not None:
        document.stage = stage
    document.ui_editable = True

    def update_blocks(blocks: list[WriterBlock], level: int = 1) -> None:
        for block in blocks:
            block.editable = True
            if stage is not None:
                block.stage = stage
            heading_level = block.numbering.get('level')
            if block.type == 'heading' and (
                not isinstance(heading_level, int)
                or isinstance(heading_level, bool)
                or not 1 <= heading_level <= 9
            ):
                block.numbering['level'] = min(level, 9)
            update_blocks(block.children, level + 1)

    update_blocks(document.blocks)
    return document


def _target_from_document(value: Any) -> TargetDocument | None:
    document = WriterDocument.model_validate(value)
    source = document.metadata.get('source')
    if not isinstance(source, dict):
        return None
    try:
        target = TargetDocument.model_validate(source)
    except Exception:
        return None
    return target if target.uri or target.doc_id else None


def _document_text(document: WriterDocument) -> str:
    return '\n'.join(
        block.content
        for block in document.iter_blocks()
        if block.content
    )


def _published_link(target: TargetDocument) -> str:
    link = str(
        target.meta.get('browser_url')
        or (target.uri if target.uri.startswith(('http://', 'https://')) else '')
    ).strip()
    if not link:
        raise ValueError('Provider write succeeded but no browser URL was returned.')
    return link


def _resolve_target(
    source_document: WriterDocument | None = None,
    target_document_json: str = '',
    target_uri: str = '',
) -> TargetDocument | None:
    target = _target_from_document(source_document) if source_document else None
    if target_document_json.strip():
        target = TargetDocument.model_validate(
            _json_loads(target_document_json, {}),
        )
    if target_uri.strip():
        target = TargetDocument(uri=target_uri.strip(), adapter='feishu')
    return target


class WriterToolkitBase:
    """Adapters for LazyLLM's unified WriterDocument/WriterBlock tool APIs."""

    WRITER_IR_SCHEMA = f'{WRITER_DATA_MODEL_SCHEMA_PREFIX}.writer_ir.WriterDocument'
    WRITER_BLOCK_SCHEMA = f'{WRITER_DATA_MODEL_SCHEMA_PREFIX}.writer_ir.WriterBlock'
    __public_apis__: list[str] = []

    def build_writing_task(self, query: str, task_id: str = '') -> str:
        """Build a provider-neutral writing task from the user's request."""
        task = WritingTask(
            task_id=task_id.strip() or None,
            query=query,
            task_type='write',
            constraints=_extract_length_constraints(query),
        )
        return _json_dumps(task.model_dump(exclude_defaults=True))

    def build_resources(
        self,
        file_paths_json: str = '[]',
        source_document_json: str = '',
        knowledge_text: str = '',
    ) -> str:
        """Build normalized InputResource data from workflow runtime inputs."""
        file_paths = _json_loads(file_paths_json, [])
        if not isinstance(file_paths, list):
            raise TypeError('file_paths_json must be a JSON array.')
        resources = [{
            'resource_id': os.path.basename(path),
            'resource_type': 'file',
            'uri': path,
            'title': os.path.basename(path),
            'mime_type': None,
            'summary': None,
            'meta': {},
        } for path in file_paths]

        if source_document_json:
            source = _document_value(source_document_json)
            document = WriterDocument.model_validate(source) if isinstance(source, dict) else None
            target = _target_from_document(document) if document else None
            resources.append({
                'resource_id': 'source_document',
                'resource_type': 'text',
                'inline_text': _document_text(document) if document else source,
                'title': document.title or None if document else None,
                'summary': None,
                'meta': {
                    'provider': target.adapter if target else None,
                    'uri': target.uri if target else None,
                    'role': 'background',
                },
            })

        if knowledge_text.strip():
            resources.append({
                'resource_id': 'knowledge_base_evidence',
                'resource_type': 'text',
                'inline_text': knowledge_text,
                'title': 'Knowledge base evidence',
                'summary': None,
                'meta': {'provider': 'knowledge_base', 'role': 'background'},
            })
        return _json_dumps(resources)

    def collect_available_media(
        self,
        writing_task_json: str,
        input_resources_json: str = '[]',
        media_store: str = '',
        use_vision_model: bool = False,
        source_document_json: str = '',
    ) -> str:
        """Collect available images through LazyLLM's multimodal writer tools."""
        root = _temp_root()
        task_path = _write_input_artifact(
            root,
            'writing_task.json',
            _json_loads(writing_task_json, {}),
            writer_schema('task.WritingTask'),
        )
        resources_path = _write_input_artifact(
            root,
            'input_resources.json',
            _json_loads(input_resources_json, []),
            writer_schema('task.InputResource'),
        )
        source_document_path = (
            _write_document_input(root, 'source_document', source_document_json)
            if source_document_json else None
        )
        artifact_store = Path(media_store.strip()) if media_store.strip() else root
        artifact_store.mkdir(parents=True, exist_ok=True)
        result = WriterMultimodalTools(
            llm=AutoModel(model='vlm') if use_vision_model else None,
            artifact_store=str(artifact_store),
        ).collect_available_media(
            task=task_path,
            input_resources=resources_path,
            source_document=source_document_path,
        )
        return _json_dumps({
            'media_assets': _result_data(result, 'media_assets'),
            'profile_input_resources': _result_data(result, 'profile_input_resources'),
            'warnings': (result.get('metadata') or {}).get('warnings') or [],
        })

    def resolve_visual_needs(
        self,
        visual_plan_json: str,
        media_assets_json: str,
        allowed_strategies_json: str = '',
    ) -> str:
        """Match visual needs against media already available to the task."""
        root = _temp_root()
        visual_plan_path = _write_input_artifact(
            root,
            'visual_plan.json',
            _json_loads(visual_plan_json, {}),
            writer_schema('multimodal.VisualPlan'),
        )
        media_assets_path = _write_input_artifact(
            root,
            'media_assets.json',
            _json_loads(media_assets_json, {}),
            writer_schema('multimodal.MediaAssetLibrary'),
        )
        allowed_strategies = _json_loads(allowed_strategies_json, None)
        if allowed_strategies is not None and not isinstance(allowed_strategies, list):
            raise TypeError('allowed_strategies_json must contain a JSON list.')
        result = WriterMultimodalTools(
            llm=AutoModel(model='llm'),
        ).resolve_visual_needs(
            visual_plan=visual_plan_path,
            media_assets=media_assets_path,
            allowed_strategies=allowed_strategies,
        )
        return _json_dumps({
            **result,
            'media_assets': result['media_assets'].model_dump(),
        })

    def materialize_acquired_media(
        self,
        visual_plan_json: str,
        media_assets_json: str,
        acquired_resources_json: str,
        media_store: str = '',
    ) -> str:
        """Validate and bind explicitly acquired media to visual needs."""
        root = _temp_root()
        visual_plan_path = _write_input_artifact(
            root,
            'visual_plan.json',
            _json_loads(visual_plan_json, {}),
            writer_schema('multimodal.VisualPlan'),
        )
        media_assets_path = _write_input_artifact(
            root,
            'media_assets.json',
            _json_loads(media_assets_json, {}),
            writer_schema('multimodal.MediaAssetLibrary'),
        )
        acquired_resources_path = _write_input_artifact(
            root,
            'acquired_resources.json',
            _json_loads(acquired_resources_json, {}),
            'lazyllm.tools.writer.artifacts.acquired_resources',
        )
        artifact_store = Path(media_store.strip()) if media_store.strip() else root
        artifact_store.mkdir(parents=True, exist_ok=True)
        result = WriterMultimodalTools(
            artifact_store=str(artifact_store),
        ).materialize_acquired_media(
            visual_plan=visual_plan_path,
            media_assets=media_assets_path,
            acquired_resources=acquired_resources_path,
        )
        return _json_dumps({
            **result,
            'media_assets': result['media_assets'].model_dump(),
        })

    def profile_resources(self, writing_task_json: str, user_input: str, resources_json: str = '[]') -> str:
        """Profile writing resources."""
        root = _temp_root()
        task_data = _json_loads(writing_task_json, {})
        resources = _json_loads(resources_json, [])
        if resources is None:
            resources = []
        if not isinstance(resources, list):
            raise TypeError('resources_json must be a JSON array.')
        has_feishu_resource = any(
            isinstance(item, dict)
            and isinstance(item.get('meta'), dict)
            and item['meta'].get('provider') == 'feishu'
            for item in resources
        )
        if not has_feishu_resource:
            resources += _extract_feishu_resources(user_input)

        task_path = _write_input_artifact(
            root, 'writing_task.json', task_data, writer_schema('task.WritingTask'),
        )
        input_resources = [InputResource.model_validate(item) for item in resources]
        result = WriterResourceTools(
            llm=AutoModel(model='llm'),
            artifact_store=str(root),
        ).profile_resources(task=task_path, input_resources=input_resources)
        return _json_dumps(_primary_data(result))

    def build_revise_task(self, query: str, target_document_json: str = '') -> str:
        """Build a revise-type WritingTask from the user's revision request."""
        target_document = None
        if target_document_json:
            target_document = TargetDocument.model_validate(
                _json_loads(target_document_json, {}),
            )
        task = WritingTask(
            query=query,
            task_type='revise',
            scope='auto',
            target_document=target_document,
        )
        return _json_dumps(task.model_dump(exclude_defaults=True))

    def build_revision_task(
        self,
        query: str,
        writer_document_json: str,
        allow_outline: bool = True,
    ) -> str:
        """Build a revision task directly from its current document."""
        source = _document_value(writer_document_json)
        document = WriterDocument.model_validate(source) if isinstance(source, dict) else None
        if document and document.stage == 'outline' and not allow_outline:
            raise ValueError(
                'A full-document revision cannot use an outline-stage document.',
            )
        target = _target_from_document(document) if document else None
        return self.build_revise_task(
            query=query,
            target_document_json=(
                _json_dumps(target.model_dump(exclude_defaults=True)) if target else ''
            ),
        )

    def validate_patch_set(
        self,
        patch_set_json: str,
        writing_context_json: str,
        writing_task_json: str,
    ) -> str:
        """Validate a PatchSet and return its audit result."""
        root = _temp_root()
        patch_set_path = _write_input_artifact(
            root, 'patch_set.json', _json_loads(patch_set_json, {}), writer_schema('revision.PatchSet'),
        )
        context_path = _write_input_artifact(
            root, 'writing_context.json', _json_loads(writing_context_json, {}),
            writer_schema('context.WritingContext'),
        )
        task_path = _write_input_artifact(
            root, 'writing_task.json', _json_loads(writing_task_json, {}), writer_schema('task.WritingTask'),
        )
        result = WriterQualityTools(
            llm=AutoModel(model='llm'), artifact_store=str(root),
        ).validate_patch_set(
            patch_set=patch_set_path, context=context_path, task=task_path,
        )
        return _json_dumps({
            'patch_set_review': _primary_data(result),
            'patch_set_review_summary': result.get('summary') or '',
        })

    def create_writing_context(
        self,
        writing_task_json: str,
        resource_profiles_json: str = '[]',
        writer_document_json: str = '',
    ) -> str:
        """Create context from a task, profiles, and an optional document."""
        root = _temp_root()
        task_path = _write_input_artifact(
            root, 'writing_task.json', _json_loads(writing_task_json, {}), writer_schema('task.WritingTask'),
        )
        profiles_path = _write_input_artifact(
            root, 'resource_profiles.json', _json_loads(resource_profiles_json, []),
            writer_schema('resource.ResourceProfile'),
        )
        document_path = None
        if writer_document_json:
            document_path = _write_document_input(root, 'writer_document', writer_document_json)
        result = WriterContextTools(llm=None, artifact_store=str(root)).create_writing_context(
            task=task_path,
            resource_profiles=profiles_path,
            document=document_path,
        )
        return _json_dumps(_primary_data(result))

    def generate_outline(self, writing_task_json: str, writing_context_json: str) -> str:
        """Generate an outline in the task's selected representation."""
        planning, task_path, context_path = self._outline_planning(
            writing_task_json, writing_context_json,
        )
        result = planning.generate_outline(task=task_path, context=context_path)
        return self._outline_result(result)

    def _outline_planning(
        self,
        writing_task_json: str,
        writing_context_json: str,
    ) -> tuple[WriterPlanningTools, str, str]:
        root = _temp_root()
        task_path = _write_input_artifact(
            root, 'writing_task.json', _json_loads(writing_task_json, {}), writer_schema('task.WritingTask'),
        )
        context_path = _write_input_artifact(
            root, 'writing_context.json', _json_loads(writing_context_json, {}),
            writer_schema('context.WritingContext'),
        )
        planning = WriterPlanningTools(
            llm=AutoModel(model='llm'), artifact_store=str(root),
        )
        return planning, task_path, context_path

    @staticmethod
    def _outline_result(result: dict) -> str:
        outline = _primary_data(result)
        if isinstance(outline, str):
            return outline
        return _set_document_editable(outline, stage='outline').model_dump_json(exclude_defaults=True)

    def stream_outline(
        self,
        writing_task_json: str,
        writing_context_json: str,
        on_delta: Callable[[str], None],
    ) -> str:
        """Generate an outline while exposing its Markdown preview deltas."""
        planning, task_path, context_path = self._outline_planning(
            writing_task_json, writing_context_json,
        )
        with planning.stream_outline(task=task_path, context=context_path) as stream:
            for delta in stream:
                try:
                    on_delta(delta)
                except Exception as exc:  # noqa: BLE001 - preview forwarding is best effort.
                    LOG.warning('[Writer] Outline delta callback failed: %s', exc)
            result = stream.result()
        return self._outline_result(result)

    def generate_rewrite_outline(
        self,
        writing_task_json: str,
        source_document_json: str,
        writing_context_json: str,
    ) -> str:
        """Generate an internal outline for a complete WriterDocument rewrite."""
        root = _temp_root()
        task_path = _write_input_artifact(
            root, 'writing_task.json', _json_loads(writing_task_json, {}), writer_schema('task.WritingTask'),
        )
        source_path = _write_document_input(root, 'source_document', source_document_json)
        context_path = _write_input_artifact(
            root, 'writing_context.json', _json_loads(writing_context_json, {}),
            writer_schema('context.WritingContext'),
        )
        result = WriterPlanningTools(
            llm=AutoModel(model='llm'), artifact_store=str(root),
        ).generate_rewrite_outline(
            task=task_path,
            source_document=source_path,
            context=context_path,
        )
        return WriterDocument.model_validate(_primary_data(result)).model_dump_json(exclude_defaults=True)

    def generate_rewrite_section_instructions(
        self,
        writing_task_json: str,
        source_document_json: str,
        writing_context_json: str,
    ) -> str:
        """Plan a complete IR or Markdown rewrite without generating an outline."""
        root = _temp_root()
        writing_task = _json_loads(writing_task_json, {})
        require_input_image_reuse = _requires_input_image_reuse(writing_task)
        task_path = _write_input_artifact(
            root, 'writing_task.json', writing_task, writer_schema('task.WritingTask'),
        )
        source_path = _write_document_input(root, 'source_document', source_document_json)
        context_path = _write_input_artifact(
            root, 'writing_context.json', _json_loads(writing_context_json, {}),
            writer_schema('context.WritingContext'),
        )
        planning = WriterPlanningTools(
            llm=AutoModel(model='llm'), artifact_store=str(root),
        )
        result = planning.generate_rewrite_section_instructions(
            task=task_path,
            source_document=source_path,
            context=context_path,
        )
        instructions = SectionInstructionList.model_validate(_primary_data(result))
        representation = str(instructions.meta.get('representation') or 'markdown')
        document_title = str(instructions.meta.get('document_title') or '').strip()
        visual_plan: dict[str, Any] = VisualPlan().model_dump()
        warnings = []
        if (
            representation == 'markdown'
            and _markdown_media_is_explicitly_disabled(_json_loads(writing_task_json, {}))
        ):
            return _json_dumps({
                'section_instructions': instructions.model_dump(exclude_defaults=True),
                'visual_plan': visual_plan,
                'document_title': document_title,
                'warnings': warnings,
            })
        if representation == 'ir':
            transient_outline = WriterDocument(
                document_id=f'{instructions.instruction_set_id}-visual-outline',
                stage='outline',
                title=document_title,
                blocks=[
                    WriterBlock(
                        node_id=instruction.content_ref.node_id or f'rewrite-section-{index}',
                        type='heading',
                        content=instruction.section_title,
                        stage='outline',
                        numbering={'level': 1},
                    )
                    for index, instruction in enumerate(instructions.instructions, start=1)
                ],
            )
            outline_path = _write_input_artifact(
                root,
                'rewrite_visual_outline.lmd',
                transient_outline.model_dump(exclude_defaults=True),
                self.WRITER_IR_SCHEMA,
            )
        else:
            transient_markdown = f'# {document_title}\n' + '\n'.join(
                f'## {instruction.section_title}'
                for instruction in instructions.instructions
            )
            outline_path = _write_document_input(
                root,
                'rewrite_visual_outline',
                transient_markdown,
            )
        try:
            visual_result = planning.generate_visual_plan(
                task=task_path,
                outline=outline_path,
                context=context_path,
            )
            visual_plan = _primary_data(visual_result)
            warnings.extend((visual_result.get('metadata') or {}).get('warnings') or [])
        except Exception as exc:
            if require_input_image_reuse:
                raise RuntimeError(
                    f'Required visual planning failed: {type(exc).__name__}: {exc}'
                ) from exc
            warnings.append(f'Visual planning failed: {type(exc).__name__}: {exc}')
        _ensure_required_visual_plan(
            visual_plan, required=require_input_image_reuse,
        )
        return _json_dumps({
            'section_instructions': instructions.model_dump(exclude_defaults=True),
            'visual_plan': visual_plan,
            'document_title': document_title,
            'warnings': warnings,
        })

    def prepare_outline(self, source_document_json: str) -> str:
        """Normalize a supplied document into an editable outline."""
        source = _document_value(source_document_json)
        if isinstance(source, str):
            return source
        document = WriterDocument.model_validate(source)
        if not any(block.type == 'heading' for block in document.blocks):
            for block in document.blocks:
                if block.type != 'paragraph':
                    continue
                lines = [line.strip() for line in block.content.splitlines() if line.strip()]
                if not lines:
                    continue
                block.type = 'heading'
                block.content = lines[0]
                block.spans = []
                block.numbering['level'] = 1
                if len(lines) > 1:
                    block.children.insert(0, WriterBlock(
                        node_id=f'{block.node_id}-description',
                        type='paragraph',
                        content='\n'.join(lines[1:]),
                        stage='outline',
                    ))
        return _set_document_editable(
            document, stage='outline',
        ).model_dump_json(exclude_defaults=True)

    def generate_section_instructions(
        self,
        writing_task_json: str,
        outline_json: str,
        writing_context_json: str,
    ) -> str:
        """Generate section instructions from an IR or Markdown outline."""
        root = _temp_root()
        writing_task = _json_loads(writing_task_json, {})
        require_input_image_reuse = _requires_input_image_reuse(writing_task)
        task_path = _write_input_artifact(
            root, 'writing_task.json', writing_task, writer_schema('task.WritingTask'),
        )
        outline_path = _write_document_input(root, 'outline', outline_json)
        context_path = _write_input_artifact(
            root, 'writing_context.json', _json_loads(writing_context_json, {}),
            writer_schema('context.WritingContext'),
        )
        planning = WriterPlanningTools(
            llm=AutoModel(model='llm'), artifact_store=str(root),
        )
        warnings = []
        visual_plan = VisualPlan().model_dump()
        if not (
            isinstance(_document_value(outline_json), str)
            and _markdown_media_is_explicitly_disabled(_json_loads(writing_task_json, {}))
        ):
            try:
                visual_result = planning.generate_visual_plan(
                    task=task_path,
                    outline=outline_path,
                    context=context_path,
                )
                visual_plan = _primary_data(visual_result)
                warnings.extend((visual_result.get('metadata') or {}).get('warnings') or [])
            except Exception as exc:
                if require_input_image_reuse:
                    raise RuntimeError(
                        f'Required visual planning failed: {type(exc).__name__}: {exc}'
                    ) from exc
                warnings.append(f'Visual planning failed: {type(exc).__name__}: {exc}')
        _ensure_required_visual_plan(
            visual_plan, required=require_input_image_reuse,
        )
        visual_plan_path = _write_input_artifact(
            root,
            'visual_plan.json',
            visual_plan,
            writer_schema('multimodal.VisualPlan'),
        )
        result = planning.generate_section_instructions(
            outline=outline_path,
            context=context_path,
            visual_plan=visual_plan_path,
            task=task_path,
        )
        return _json_dumps({
            'section_instructions': _primary_data(result),
            'visual_plan': visual_plan,
            'warnings': warnings,
        })

    def generate_draft_section(
        self,
        writing_task_json: str,
        section_instruction_json: str,
        writing_context_json: str,
        previous_blocks_json: str = '[]',
        visual_plan_json: str = '',
        media_assets_json: str = '',
    ) -> str:
        """Generate one draft section in the instruction's representation."""
        root = _temp_root()
        task_path = _write_input_artifact(
            root, 'writing_task.json', _json_loads(writing_task_json, {}), writer_schema('task.WritingTask'),
        )
        context_path = _write_input_artifact(
            root, 'writing_context.json', _json_loads(writing_context_json, {}),
            writer_schema('context.WritingContext'),
        )
        instruction = SectionInstruction.model_validate(_json_loads(section_instruction_json, {}))
        previous_blocks = _json_loads(previous_blocks_json, [])
        visual_plan_path = None
        if visual_plan_json:
            visual_plan_path = _write_input_artifact(
                root,
                'visual_plan.json',
                _json_loads(visual_plan_json, {}),
                writer_schema('multimodal.VisualPlan'),
            )
        media_assets_path = None
        if media_assets_json:
            media_assets_path = _write_input_artifact(
                root,
                'media_assets.json',
                _json_loads(media_assets_json, {}),
                writer_schema('multimodal.MediaAssetLibrary'),
            )
        result = WriterDraftingTools(
            llm=AutoModel(model='llm'), artifact_store=str(root),
        ).generate_draft_section(
            task=task_path,
            section_instruction=instruction,
            context=context_path,
            previous_blocks=previous_blocks,
            visual_plan=visual_plan_path,
            media_assets=media_assets_path,
        )
        return _json_dumps(_primary_data(result))

    def generate_draft_section_markdown(
        self,
        writing_task_json: str,
        section_instruction_json: str,
        writing_context_json: str,
        previous_markdown: str = '',
    ) -> str:
        """Generate one Markdown draft section through the unified drafting API."""
        return _json_loads(self.generate_draft_section(
            writing_task_json=writing_task_json,
            section_instruction_json=section_instruction_json,
            writing_context_json=writing_context_json,
            previous_blocks_json=_json_dumps([previous_markdown] if previous_markdown else []),
        ), '')

    def generate_draft_blocks(
        self,
        writing_task_json: str,
        section_instructions_json: str,
        writing_context_json: str,
        visual_plan_json: str = '',
        media_assets_json: str = '',
    ) -> str:
        """Generate every planned draft section in order."""
        instructions_data = _json_loads(section_instructions_json, {})
        instructions = (
            instructions_data.get('instructions')
            if isinstance(instructions_data, dict) else None
        )
        if not isinstance(instructions, list):
            raise TypeError('section_instructions_json must contain instructions.')
        _bind_document_cross_reference_targets(instructions)

        blocks: list[Any] = []
        for instruction in instructions:
            block = _json_loads(self.generate_draft_section(
                writing_task_json=writing_task_json,
                section_instruction_json=_json_dumps(instruction),
                writing_context_json=writing_context_json,
                previous_blocks_json='[]',
                visual_plan_json=visual_plan_json,
                media_assets_json=media_assets_json,
            ), {})
            blocks.append(block)
        return _json_dumps(blocks)

    def generate_draft_blocks_markdown(
        self,
        writing_task_json: str,
        section_instructions_json: str,
        writing_context_json: str,
        visual_plan_json: str = '',
    ) -> str:
        """Generate every planned draft section in Markdown, in order."""
        return self.generate_draft_blocks(
            writing_task_json=writing_task_json,
            section_instructions_json=section_instructions_json,
            writing_context_json=writing_context_json,
            visual_plan_json=visual_plan_json,
        )

    def stream_draft_blocks_markdown(
        self,
        writing_task_json: str,
        section_instructions_json: str,
        writing_context_json: str,
        on_delta: Callable[[str], None],
        on_section_end: Callable[[], None] | None = None,
        on_progress: Callable[[dict[str, Any]], None] | None = None,
        visual_plan_json: str = '',
        checkpoint_dir: str = '',
    ) -> str:
        """Generate Markdown sections through LazyLLM's non-tool streaming API."""
        return self._stream_draft_blocks(
            writing_task_json=writing_task_json,
            section_instructions_json=section_instructions_json,
            writing_context_json=writing_context_json,
            representation='markdown',
            on_delta=on_delta,
            on_section_end=on_section_end,
            on_progress=on_progress,
            visual_plan_json=visual_plan_json,
            checkpoint_dir=checkpoint_dir,
        )

    def stream_draft_blocks_ir(
        self,
        writing_task_json: str,
        section_instructions_json: str,
        writing_context_json: str,
        on_delta: Callable[[str], None],
        on_section_end: Callable[[], None] | None = None,
        on_progress: Callable[[dict[str, Any]], None] | None = None,
        visual_plan_json: str = '',
        media_assets_json: str = '',
        checkpoint_dir: str = '',
    ) -> str:
        """Generate IR sections while exposing their Markdown preview deltas."""
        return self._stream_draft_blocks(
            writing_task_json=writing_task_json,
            section_instructions_json=section_instructions_json,
            writing_context_json=writing_context_json,
            representation='ir',
            on_delta=on_delta,
            on_section_end=on_section_end,
            on_progress=on_progress,
            visual_plan_json=visual_plan_json,
            media_assets_json=media_assets_json,
            checkpoint_dir=checkpoint_dir,
        )

    def _stream_draft_blocks(
        self,
        *,
        writing_task_json: str,
        section_instructions_json: str,
        writing_context_json: str,
        representation: str,
        on_delta: Callable[[str], None],
        on_section_end: Callable[[], None] | None,
        on_progress: Callable[[dict[str, Any]], None] | None,
        visual_plan_json: str = '',
        media_assets_json: str = '',
        checkpoint_dir: str = '',
    ) -> str:
        instructions_data = _json_loads(section_instructions_json, {})
        instructions = (
            instructions_data.get('instructions')
            if isinstance(instructions_data, dict) else None
        )
        if not isinstance(instructions, list):
            raise TypeError('section_instructions_json must contain instructions.')
        _bind_document_cross_reference_targets(instructions)

        def forward_delta(delta: str) -> None:
            try:
                on_delta(delta)
            except Exception as exc:  # noqa: BLE001 - preview forwarding is best effort.
                LOG.warning(
                    '[Writer] Draft %s delta callback failed: %s',
                    representation, exc,
                )

        def forward_progress(**payload: Any) -> None:
            if on_progress is None:
                return
            try:
                on_progress(payload)
            except Exception as exc:  # noqa: BLE001 - progress is observability only.
                LOG.warning('[Writer] Draft progress callback failed: %s', exc)

        instruction_list_meta = instructions_data.get('meta')
        if not isinstance(instruction_list_meta, dict):
            instruction_list_meta = {}
        first_instruction = instructions[0] if instructions and isinstance(instructions[0], dict) else {}
        first_instruction_meta = first_instruction.get('meta')
        if not isinstance(first_instruction_meta, dict):
            first_instruction_meta = {}
        document_title = ''
        for candidate in (
            instruction_list_meta.get('document_title'),
            instruction_list_meta.get('outline_title'),
            first_instruction_meta.get('document_title'),
            first_instruction_meta.get('outline_title'),
        ):
            document_title = str(candidate or '').strip()
            if document_title:
                break
        if document_title:
            forward_delta(f'# {document_title}\n\n')

        root = _temp_root()
        task_path = _write_input_artifact(
            root, 'writing_task.json', _json_loads(writing_task_json, {}),
            writer_schema('task.WritingTask'),
        )
        context_path = _write_input_artifact(
            root, 'writing_context.json', _json_loads(writing_context_json, {}),
            writer_schema('context.WritingContext'),
        )
        visual_plan_path = None
        if visual_plan_json:
            visual_plan_path = _write_input_artifact(
                root,
                'visual_plan.json',
                _json_loads(visual_plan_json, {}),
                writer_schema('multimodal.VisualPlan'),
            )
        media_assets_path = None
        if media_assets_json:
            media_assets_path = _write_input_artifact(
                root,
                'media_assets.json',
                _json_loads(media_assets_json, {}),
                writer_schema('multimodal.MediaAssetLibrary'),
            )
        checkpoint_root = Path(checkpoint_dir) if checkpoint_dir else root / 'section-checkpoints'
        checkpoint_root.mkdir(parents=True, exist_ok=True)
        sections: list[Any] = [None] * len(instructions)
        event_queues: list[Queue] = [Queue() for _ in instructions]
        stop_event = Event()
        progress_lock = RLock()
        completed_count = 0
        max_attempts = max(1, int(os.getenv('LAZYMIND_WRITER_SECTION_MAX_ATTEMPTS', '2')))
        section_total_timeout = max(
            1.0, float(os.getenv('LAZYMIND_WRITER_SECTION_TOTAL_TIMEOUT', '600')),
        )
        section_stream_idle_timeout = max(
            1.0, float(os.getenv('LAZYMIND_WRITER_SECTION_STREAM_IDLE_TIMEOUT', '180')),
        )
        first_section_idle_timeout = max(
            section_stream_idle_timeout,
            float(os.getenv('LAZYMIND_WRITER_FIRST_SECTION_IDLE_TIMEOUT', '360')),
        )
        section_started_at: list[float | None] = [None] * len(instructions)
        forward_progress(
            progress=5,
            current_phase='正在优先生成第 1 章',
            section_total=len(instructions),
            section_completed=0,
        )

        def checkpoint_path(index: int, instruction_data: dict[str, Any]) -> Path:
            payload = json.dumps({
                'version': 1,
                'representation': representation,
                'task': _json_loads(writing_task_json, {}),
                'instruction': instruction_data,
                'context': _json_loads(writing_context_json, {}),
                'visual_plan': _json_loads(visual_plan_json, {}),
                'media_assets': _json_loads(media_assets_json, {}),
            }, ensure_ascii=False, sort_keys=True, default=str)
            digest = hashlib.sha256(payload.encode('utf-8')).hexdigest()[:16]
            suffix = '.md' if representation == 'markdown' else '.json'
            return checkpoint_root / f'section-{index + 1:04d}-{digest}{suffix}'

        def load_checkpoint(path: Path, instruction: SectionInstruction) -> Any:
            if not path.is_file():
                return None
            try:
                if representation == 'markdown':
                    value = path.read_text(encoding='utf-8')
                    return _normalize_streamed_markdown_section(value, instruction)
                value = json.loads(path.read_text(encoding='utf-8'))
                return WriterBlock.model_validate(value).model_dump(exclude_defaults=True)
            except (OSError, ValueError, TypeError, json.JSONDecodeError):
                LOG.warning('[Writer] Ignoring invalid draft section checkpoint %s', path)
                return None

        def save_checkpoint(path: Path, section: Any) -> None:
            temporary = path.with_name(f'.{path.name}.{uuid.uuid4().hex}.tmp')
            if representation == 'markdown':
                temporary.write_text(str(section), encoding='utf-8')
            else:
                temporary.write_text(
                    json.dumps(section, ensure_ascii=False, indent=2),
                    encoding='utf-8',
                )
            temporary.replace(path)

        def cached_preview(section: Any) -> str:
            if representation == 'markdown':
                return str(section)
            return render_block_markdown(
                WriterBlock.model_validate(section), level=2,
            ).rstrip() + '\n'

        def preview_has_body(preview: str) -> bool:
            """A generated section title alone is not effective body progress."""
            lines = preview.splitlines()
            while lines and not lines[0].strip():
                lines.pop(0)
            if lines and re.match(r'^#{1,6}\s+\S', lines[0].strip()):
                lines.pop(0)
            remainder = '\n'.join(lines)
            # Markdown structure alone (another heading/list prefix, fence,
            # whitespace) is not document body progress.
            visible_text = re.sub(r'[\s#>*_`~+\-\[\](){}|.!:;，。！？、]+', '', remainder)
            return bool(visible_text)

        def mark_completed(index: int, *, cached: bool = False) -> None:
            nonlocal completed_count
            with progress_lock:
                completed_count += 1
                done = completed_count
            if done >= len(instructions):
                phase = f'全部 {len(instructions)} 章已生成，正在按大纲顺序组装'
            elif cached:
                phase = f'已复用 {done}/{len(instructions)} 章 checkpoint，其他章节仍在生成'
            else:
                phase = f'已完成 {done}/{len(instructions)} 章，其他章节仍在后台生成'
            forward_progress(
                progress=5,
                current_phase=phase,
                section_index=index + 1,
                section_total=len(instructions),
                section_completed=done,
                section_state='checkpointed',
            )

        def generate_one(index: int, instruction_data: dict[str, Any]) -> None:
            events = event_queues[index]
            path = checkpoint_path(index, instruction_data)
            section_started_at[index] = time.monotonic()
            try:
                instruction = SectionInstruction.model_validate(instruction_data)
                cached = load_checkpoint(path, instruction)
                if cached is not None:
                    if not stop_event.is_set():
                        events.put(('delta', cached_preview(cached)))
                        events.put(('done', cached))
                        mark_completed(index, cached=True)
                    return
                for attempt in range(1, max_attempts + 1):
                    buffered: list[str] = []
                    section_deltas: list[str] = []
                    body_started = False
                    try:
                        forward_progress(
                            progress=5,
                            current_phase=f'第 {index + 1} 章正在生成',
                            section_index=index + 1,
                            section_total=len(instructions),
                            section_completed=completed_count,
                            section_state='generating',
                            section_attempt=attempt,
                        )
                        section_root = root / f'section-{index + 1:04d}-attempt-{attempt}'
                        section_root.mkdir(parents=True, exist_ok=True)
                        drafting = WriterDraftingTools(
                            llm=AutoModel(model='llm'), artifact_store=str(section_root),
                        )
                        stream_factory = (
                            drafting.stream_draft_section
                            if representation == 'markdown'
                            else drafting.stream_draft_section_ir
                        )
                        stream_kwargs: dict[str, Any] = {
                            'task': task_path,
                            'section_instruction': instruction,
                            'context': context_path,
                        }
                        if visual_plan_path is not None:
                            stream_kwargs['visual_plan'] = visual_plan_path
                        if media_assets_path is not None:
                            stream_kwargs['media_assets'] = media_assets_path
                        stream_kwargs['idle_timeout'] = (
                            first_section_idle_timeout if index == 0 and attempt == 1
                            else section_stream_idle_timeout
                        )
                        try:
                            with stream_factory(**stream_kwargs) as stream:
                                for delta in stream:
                                    if stop_event.is_set():
                                        return
                                    section_deltas.append(delta)
                                    if body_started:
                                        events.put(('delta', delta))
                                        continue
                                    buffered.append(delta)
                                    if preview_has_body(''.join(buffered)):
                                        body_started = True
                                        for pending in buffered:
                                            events.put(('delta', pending))
                                        buffered.clear()
                                        forward_progress(
                                            progress=5,
                                            current_phase=f'第 {index + 1} 章正在输出正文',
                                            section_index=index + 1,
                                            section_total=len(instructions),
                                            section_completed=completed_count,
                                            section_state='streaming',
                                            section_attempt=attempt,
                                        )
                                result = stream.result()
                            section = _primary_data(result)
                        except ValueError as exc:
                            if (
                                representation != 'markdown'
                                or str(exc) != _MARKDOWN_DRAFT_ROOT_ERROR
                                or not section_deltas
                            ):
                                raise
                            section = _normalize_streamed_markdown_section(
                                ''.join(section_deltas), instruction,
                            )
                            LOG.warning(
                                '[Writer] repaired Draft Markdown heading contract without '
                                'regeneration section=%s title=%r',
                                index + 1, instruction.section_title,
                            )
                        if representation == 'markdown' and not isinstance(section, str):
                            raise TypeError('Markdown Draft stream returned a non-Markdown artifact.')
                        if representation == 'ir' and not isinstance(section, dict):
                            raise TypeError('IR Draft stream returned a non-WriterBlock artifact.')
                        if representation == 'markdown':
                            section = _normalize_streamed_markdown_section(section, instruction)
                        if not body_started and preview_has_body(cached_preview(section)):
                            for pending in buffered:
                                events.put(('delta', pending))
                            body_started = True
                        if not body_started:
                            raise TimeoutError('Draft section completed without effective body content.')
                        if stop_event.is_set():
                            return
                        save_checkpoint(path, section)
                        events.put(('done', section))
                        mark_completed(index)
                        return
                    except Exception as exc:
                        if attempt >= max_attempts or body_started or stop_event.is_set():
                            raise
                        LOG.warning(
                            '[Writer] Retrying section %d after no effective body output: %s',
                            index + 1, exc,
                        )
                        forward_progress(
                            progress=5,
                            current_phase=(
                                f'第 {index + 1} 章长时间无有效正文，'
                                f'正在自动重试（{attempt}/{max_attempts - 1}）'
                            ),
                            section_index=index + 1,
                            section_total=len(instructions),
                            section_completed=completed_count,
                            section_state='retrying',
                            section_attempt=attempt + 1,
                        )
            except Exception as exc:  # noqa: BLE001 - propagated on the ordered stream.
                if not stop_event.is_set():
                    events.put(('error', exc))

        executor = ThreadPoolExecutor(max_workers=min(3, max(1, len(instructions))))
        futures: list[Any | None] = [None] * len(instructions)
        futures[0] = executor.submit(generate_one, 0, instructions[0])
        background_started = len(instructions) == 1

        def start_background_sections() -> None:
            nonlocal background_started
            if background_started:
                return
            background_started = True
            for background_index in range(1, len(instructions)):
                futures[background_index] = executor.submit(
                    generate_one,
                    background_index,
                    instructions[background_index],
                )

        try:
            for index, events in enumerate(event_queues):
                if index > 0:
                    start_background_sections()
                future = futures[index]
                if future is None:
                    raise RuntimeError(f'Draft section {index + 1} was not started.')
                wait_started_at = time.monotonic()
                last_wait_progress_at = wait_started_at
                last_stream_progress_at = 0.0
                streamed_chars = 0
                section_stream_started = False
                while True:
                    deadline = (
                        section_started_at[index] or wait_started_at
                    ) + section_total_timeout
                    if time.monotonic() >= deadline and not future.done():
                        raise TimeoutError(
                            f'Draft section {index + 1} exceeded total timeout '
                            f'of {section_total_timeout:g} seconds.'
                        )
                    try:
                        event, payload = events.get(timeout=min(
                            1.0, max(0.001, deadline - time.monotonic()),
                        ))
                    except Empty:
                        if time.monotonic() >= deadline:
                            raise TimeoutError(
                                f'Draft section {index + 1} exceeded total timeout '
                                f'of {section_total_timeout:g} seconds.'
                            )
                        if future.done():
                            future.result()
                            raise RuntimeError(
                                f'Draft section {index + 1} ended without a terminal event.'
                            )
                        now = time.monotonic()
                        if now - last_wait_progress_at >= 10.0:
                            with progress_lock:
                                done = completed_count
                            waiting_for = '后续正文' if section_stream_started else '有效正文'
                            forward_progress(
                                progress=5,
                                current_phase=(
                                    f'第 {index + 1} 章仍在生成，正在等待{waiting_for}'
                                    f'（已完成 {done}/{len(instructions)} 章）'
                                ),
                                section_index=index + 1,
                                section_total=len(instructions),
                                section_completed=done,
                                section_state='streaming' if section_stream_started else 'waiting_for_body',
                            )
                            last_wait_progress_at = now
                        continue
                    if event == 'delta':
                        if index == 0:
                            # Give the first section exclusive access until its
                            # first visible body arrives. Then keep streaming it
                            # while the remaining sections run in the background.
                            start_background_sections()
                        section_stream_started = True
                        streamed_chars += len(str(payload))
                        now = time.monotonic()
                        if last_stream_progress_at == 0.0 or now - last_stream_progress_at >= 2.0:
                            with progress_lock:
                                done = completed_count
                            forward_progress(
                                progress=5,
                                current_phase=(
                                    f'正在流式输出第 {index + 1} 章'
                                    f'（已完成 {done}/{len(instructions)} 章，'
                                    f'已输出 {streamed_chars} 字）'
                                ),
                                section_index=index + 1,
                                section_total=len(instructions),
                                section_completed=done,
                                section_state='streaming',
                                section_output_chars=streamed_chars,
                            )
                            last_stream_progress_at = now
                            last_wait_progress_at = now
                        forward_delta(payload)
                        continue
                    if event == 'error':
                        raise payload
                    if index == 0:
                        start_background_sections()
                    sections[index] = payload
                    if on_section_end is not None:
                        try:
                            on_section_end()
                        except Exception as exc:  # noqa: BLE001 - preview forwarding is best effort.
                            LOG.warning(
                                '[Writer] Draft %s section callback failed: %s',
                                representation, exc,
                            )
                    break
        finally:
            stop_event.set()
            for future in futures:
                if future is not None:
                    future.cancel()
            executor.shutdown(wait=False, cancel_futures=True)
        return _json_dumps(sections)

    def generate_draft_document(
        self,
        draft_blocks_json: str,
        writing_context_json: str,
        outline_json: str = '',
        title: str = '',
    ) -> str:
        """Combine draft sections while preserving their representation."""
        root = _temp_root()
        blocks_data = _json_loads(draft_blocks_json, [])
        if not isinstance(blocks_data, list) or not blocks_data:
            raise ValueError('draft_blocks_json must be a non-empty JSON array.')
        context_path = _write_input_artifact(
            root, 'writing_context.json', _json_loads(writing_context_json, {}),
            writer_schema('context.WritingContext'),
        )
        outline_path = None
        if outline_json:
            outline_path = _write_document_input(root, 'outline', outline_json)
        result = WriterDraftingTools(llm=None, artifact_store=str(root)).generate_draft_document(
            draft_blocks=blocks_data,
            context=context_path,
            outline=outline_path,
            title=title or None,
        )
        return _json_dumps(_primary_data(result))

    def generate_draft_document_markdown(
        self,
        draft_sections_json: str,
        writing_context_json: str,
        outline_json: str = '',
        title: str = '',
    ) -> str:
        """Combine Markdown sections through the unified drafting API."""
        markdown = _json_loads(self.generate_draft_document(
            draft_blocks_json=draft_sections_json,
            writing_context_json=writing_context_json,
            outline_json=outline_json,
            title=title,
        ), '')
        return _json_dumps({
            'draft_document': markdown,
        })

    def update_writing_context(self, content_artifact_json: str, writing_context_json: str) -> str:
        """Update context from IR or Markdown content."""
        root = _temp_root()
        content_data = _document_value(content_artifact_json)
        if isinstance(content_data, str):
            content_path = root / 'writer_content.md'
            content_path.write_text(content_data, encoding='utf-8')
            content_path = str(content_path)
        else:
            schema_name = self.WRITER_IR_SCHEMA if 'document_id' in content_data else self.WRITER_BLOCK_SCHEMA
            content_path = _write_input_artifact(root, 'writer_content.lmd', content_data, schema_name)
        context_path = _write_input_artifact(
            root, 'writing_context.json', _json_loads(writing_context_json, {}),
            writer_schema('context.WritingContext'),
        )
        result = WriterContextTools(llm=None, artifact_store=str(root)).update_writing_context(
            artifacts=content_path,
            context=context_path,
        )
        return _json_dumps(_primary_data(result))

    def check_consistency(self, draft_document_json: str, writing_context_json: str) -> str:
        """Validate an IR or Markdown draft document."""
        root = _temp_root()
        draft_path = _write_document_input(root, 'draft_document', draft_document_json)
        context_path = _write_input_artifact(
            root, 'writing_context.json', _json_loads(writing_context_json, {}),
            writer_schema('context.WritingContext'),
        )
        result = WriterQualityTools(
            llm=AutoModel(model='llm'), artifact_store=str(root),
        ).validate_draft_document(draft_document=draft_path, context=context_path)
        return _json_dumps({
            'review_report': _primary_data(result),
            'review_summary': result.get('summary') or '',
        })

    def generate_final_document(self, draft_document_json: str, writing_context_json: str) -> str:
        """Return the final document without changing its representation."""
        root = _temp_root()
        draft_path = _write_document_input(root, 'draft_document', draft_document_json)
        context_path = _write_input_artifact(
            root, 'writing_context.json', _json_loads(writing_context_json, {}),
            writer_schema('context.WritingContext'),
        )
        result = WriterDraftingTools(llm=None, artifact_store=str(root)).generate_final_document(
            draft=draft_path,
            context=context_path,
        )
        output_path = result.get('output_file_path') or ''
        markdown = ''
        if output_path:
            with open(output_path, 'r', encoding='utf-8') as fh:
                markdown = fh.read()
        final_document = _primary_data(result)
        if not isinstance(final_document, str):
            final_document = _set_document_editable(final_document, stage='final').model_dump(exclude_defaults=True)
        return _json_dumps({
            'final_document': final_document,
            'final_document_md': markdown,
        })

    def render_markdown(self, writer_document_json: str) -> str:
        """Return the current document title and Markdown content."""
        value = _document_value(writer_document_json)
        if isinstance(value, str):
            title_match = re.search(r'^#\s+(.+)$', value, re.MULTILINE)
            return _json_dumps({
                'title': title_match.group(1).strip() if title_match else '',
                'markdown': materialize_markdown(value),
            })
        document = WriterDocument.model_validate(value)
        return _json_dumps({
            'title': document.title,
            'markdown': writer_document_to_markdown(document),
        })

    def locate_revision_target(
        self,
        writing_task_json: str,
        writer_document_json: str,
        writing_context_json: str,
    ) -> str:
        """Locate the IR or Markdown content affected by a revision task."""
        root = _temp_root()
        task_path = _write_input_artifact(
            root, 'writing_task.json', _json_loads(writing_task_json, {}), writer_schema('task.WritingTask'),
        )
        document_path = _write_document_input(root, 'writer_document', writer_document_json)
        context_path = _write_input_artifact(
            root, 'writing_context.json', _json_loads(writing_context_json, {}),
            writer_schema('context.WritingContext'),
        )
        result = WriterRevisionTools(
            llm=AutoModel(model='llm'), artifact_store=str(root),
        ).locate_revision_target(task=task_path, document=document_path, context=context_path)
        return _json_dumps(_primary_data(result))

    def generate_modify_plan(
        self,
        writing_task_json: str,
        writer_document_json: str,
        locate_result_json: str,
        writing_context_json: str,
    ) -> str:
        """Generate a structured modification plan for the located targets."""
        root = _temp_root()
        task_path = _write_input_artifact(
            root, 'writing_task.json', _json_loads(writing_task_json, {}), writer_schema('task.WritingTask'),
        )
        document_path = _write_document_input(root, 'writer_document', writer_document_json)
        locate_path = _write_input_artifact(
            root, 'locate_result.json', _json_loads(locate_result_json, {}),
            writer_schema('revision.LocateResult'),
        )
        context_path = _write_input_artifact(
            root, 'writing_context.json', _json_loads(writing_context_json, {}),
            writer_schema('context.WritingContext'),
        )
        result = WriterRevisionTools(
            llm=AutoModel(model='llm'), artifact_store=str(root),
        ).generate_modify_plan(
            task=task_path,
            document=document_path,
            locate_result=locate_path,
            context=context_path,
        )
        return _json_dumps(_primary_data(result))

    def build_revision_visual_plan(self, modify_plan_json: str) -> str:
        """Extract the explicit visual needs from a structured revision plan."""
        plan = ModifyPlan.model_validate(_json_loads(modify_plan_json, {}))
        instructions: list[VisualInstruction] = []
        for instruction in plan.instructions:
            visual = instruction.visual_instruction
            if visual is None:
                continue
            if instruction.modify_type != 'create':
                raise ValueError('visual_instruction is only valid for create instructions.')
            if visual.visual_type != 'image':
                raise ValueError('revision visual_instruction.visual_type must be "image".')
            if visual.need_id != instruction.instruction_id:
                raise ValueError('visual_instruction.need_id must equal instruction_id.')
            if visual.content_ref != instruction.content_ref:
                raise ValueError(
                    'visual_instruction.content_ref must equal content_ref.'
                )
            if not visual.purpose.strip() or not visual.required:
                raise ValueError('revision image visual_instruction must be required and non-empty.')
            if visual.preferred_strategy not in {None, 'image_generation'}:
                raise ValueError(
                    'revision image preferred_strategy must be null or image_generation.',
                )
            instructions.append(visual)
        return _json_dumps(VisualPlan(instructions=instructions).model_dump(exclude_defaults=True))

    def generate_patch_set(
        self,
        writer_document_json: str,
        modify_plan_json: str,
        writing_context_json: str,
        media_assets_json: str = '',
    ) -> str:
        """Generate a WriterDocument patch set from a modification plan."""
        root = _temp_root()
        document_path = _write_input_artifact(
            root, 'writer_document.json', _json_loads(writer_document_json, {}), self.WRITER_IR_SCHEMA,
        )
        plan_path = _write_input_artifact(
            root, 'modify_plan.json', _json_loads(modify_plan_json, {}),
            writer_schema('revision.ModifyPlan'),
        )
        context_path = _write_input_artifact(
            root, 'writing_context.json', _json_loads(writing_context_json, {}),
            writer_schema('context.WritingContext'),
        )
        media_assets_path = ''
        if media_assets_json.strip():
            media_assets_path = _write_input_artifact(
                root,
                'media_assets.json',
                _json_loads(media_assets_json, {}),
                writer_schema('multimodal.MediaAssetLibrary'),
            )
        result = WriterRevisionTools(
            llm=AutoModel(model='llm'), artifact_store=str(root),
        ).generate_patch_set(
            document=document_path,
            modify_plan=plan_path,
            context=context_path,
            media_assets=media_assets_path or None,
        )
        return _json_dumps(_primary_data(result))

    def generate_string_replace_set(
        self,
        markdown_document: str,
        modify_plan_json: str,
        writing_context_json: str,
    ) -> str:
        """Generate Markdown string replacements from a modification plan."""
        root = _temp_root()
        document_path = _write_document_input(root, 'document', markdown_document)
        plan_path = _write_input_artifact(
            root, 'modify_plan.json', _json_loads(modify_plan_json, {}),
            writer_schema('revision.ModifyPlan'),
        )
        context_path = _write_input_artifact(
            root, 'writing_context.json', _json_loads(writing_context_json, {}),
            writer_schema('context.WritingContext'),
        )
        result = WriterRevisionTools(
            llm=AutoModel(model='llm'), artifact_store=str(root),
        ).generate_string_replace_set(
            document=document_path,
            modify_plan=plan_path,
            context=context_path,
        )
        return _json_dumps(_primary_data(result))

    def plan_revision(
        self,
        writing_task_json: str,
        writer_document_json: str,
        writing_context_json: str,
        media_assets_json: str = '',
    ) -> str:
        """Locate targets, build a modification plan, and generate a PatchSet."""
        located = self.locate_revision_target(
            writing_task_json=writing_task_json,
            writer_document_json=writer_document_json,
            writing_context_json=writing_context_json,
        )
        plan = self.generate_modify_plan(
            writing_task_json=writing_task_json,
            writer_document_json=writer_document_json,
            locate_result_json=located,
            writing_context_json=writing_context_json,
        )
        patch_set = self.generate_patch_set(
            writer_document_json=writer_document_json,
            modify_plan_json=plan,
            writing_context_json=writing_context_json,
            media_assets_json=media_assets_json,
        )
        return _json_dumps({
            'locate_result': _json_loads(located, {}),
            'modify_plan': _json_loads(plan, {}),
            'patch_set': _json_loads(patch_set, {}),
        })

    def apply_patch(
        self,
        writer_document_json: str,
        patch_set_json: str,
        writing_context_json: str,
        media_assets_json: str = '',
    ) -> str:
        """Apply a validated patch set and return the revised WriterDocument."""
        root = _temp_root()
        document_path = _write_input_artifact(
            root, 'writer_document.json', _json_loads(writer_document_json, {}), self.WRITER_IR_SCHEMA,
        )
        patch_path = _write_input_artifact(
            root, 'patch_set.json', _json_loads(patch_set_json, {}), writer_schema('revision.PatchSet'),
        )
        context_path = _write_input_artifact(
            root, 'writing_context.json', _json_loads(writing_context_json, {}),
            writer_schema('context.WritingContext'),
        )
        media_assets_path = ''
        if media_assets_json.strip():
            media_assets_path = _write_input_artifact(
                root,
                'media_assets.json',
                _json_loads(media_assets_json, {}),
                writer_schema('multimodal.MediaAssetLibrary'),
            )
        result = WriterRevisionTools(llm=None, artifact_store=str(root)).apply_patch(
            document=document_path,
            patch_set=patch_path,
            context=context_path,
            media_assets=media_assets_path or None,
        )
        artifact_paths = (result.get('metadata') or {}).get('artifact_paths') or {}
        revised_path = artifact_paths.get('revised_document', '')
        source = WriterDocument.model_validate(
            _json_loads(writer_document_json, {}),
        )
        revised = _set_document_editable(
            _read_artifact_data(revised_path) if revised_path else {},
            stage=source.stage,
        )
        return _json_dumps({
            'patch_result': _primary_data(result),
            'revised_document': revised.model_dump(exclude_defaults=True),
        })

    def apply_string_replace(
        self,
        markdown_document: str,
        string_replace_set_json: str,
        writing_context_json: str,
    ) -> str:
        """Apply replacements and return the revised Markdown document."""
        root = _temp_root()
        document_path = _write_document_input(root, 'document', markdown_document)
        replace_path = _write_input_artifact(
            root, 'string_replace_set.json', _json_loads(string_replace_set_json, {}),
            writer_schema('revision.StringReplaceSet'),
        )
        context_path = _write_input_artifact(
            root, 'writing_context.json', _json_loads(writing_context_json, {}),
            writer_schema('context.WritingContext'),
        )
        result = WriterRevisionTools(llm=None, artifact_store=str(root)).apply_string_replace(
            document=document_path,
            replace_set=replace_path,
            context=context_path,
        )
        artifact_paths = (result.get('metadata') or {}).get('artifact_paths') or {}
        revised_path = artifact_paths.get('revised_document_md', '')
        return _json_dumps({
            'string_replace_result': _primary_data(result),
            'revised_document': _read_artifact_data(revised_path),
        })

    def apply_revision(
        self,
        writer_document_json: str,
        patch_set_json: str,
        writing_context_json: str,
        sync_provider: bool = False,
        allow_outline: bool = True,
        media_assets_json: str = '',
    ) -> str:
        """Apply a local revision and optionally synchronize its bound provider."""
        source = WriterDocument.model_validate(
            _json_loads(writer_document_json, {}),
        )
        if source.stage == 'outline' and not allow_outline:
            raise ValueError(
                'A full-document revision cannot use an outline-stage document.',
            )
        applied = _json_loads(self.apply_patch(
            writer_document_json=writer_document_json,
            patch_set_json=patch_set_json,
            writing_context_json=writing_context_json,
            media_assets_json=media_assets_json,
        ), {})
        output = {
            'patch_result': applied.get('patch_result') or {},
            'revised_document': applied.get('revised_document') or {},
            'write_result': None,
        }
        if not sync_provider or _target_from_document(source) is None:
            return _json_dumps(output)

        published = _json_loads(WriterResourceToolkit().publish_revision(
            source_document_json=writer_document_json,
            patch_set_json=patch_set_json,
            media_assets_json=media_assets_json,
        ), {})
        output['revised_document'] = published.get('draft_document') or {}
        output['write_result'] = published.get('publish_result') or {}
        return _json_dumps(output)

    def load_document(self, user_input: str, stage: str = 'final') -> str:
        """Load a Feishu/Lark document and return its IR and target binding."""
        if stage not in {'outline', 'draft', 'final'}:
            raise ValueError('stage must be outline, draft, or final.')
        root = _temp_root()
        target = TargetDocument(
            uri=_feishu_url(user_input),
            adapter='feishu',
            meta={'stage': stage},
        )
        result = WriterResourceTools(
            llm=None, artifact_store=str(root),
        ).document_to_docir(target)
        return _json_dumps({
            'source_document': _primary_data(result),
            'target_document': target.model_dump(exclude_defaults=True),
        })

    def create_document(self, title: str, parent_uri: str = '') -> str:
        """Create an empty Feishu document and return its target binding."""
        root = _temp_root()
        result = WriterResourceTools(
            llm=None, artifact_store=str(root),
        ).create_document(
            title=title.strip() or '未命名文档',
            parent_uri=parent_uri.strip(),
            adapter='feishu',
        )
        return _json_dumps(_primary_data(result))

    def publish_revision(
        self,
        source_document_json: str,
        patch_set_json: str,
        media_assets_json: str = '',
    ) -> str:
        """Apply a prepared PatchSet to its bound provider document."""
        root = _temp_root()
        source = WriterDocument.model_validate(
            _json_loads(source_document_json, {}),
        )
        target = _target_from_document(source)
        if target is None:
            raise ValueError('source document must contain a cloud target binding.')
        result = WriterResourceTools(
            llm=None, artifact_store=str(root),
        ).apply_patch_to_document(
            patch_set=_json_loads(patch_set_json, {}),
            source_document=source,
            media_assets=_json_loads(media_assets_json, {}) if media_assets_json.strip() else None,
        )
        persisted = _set_document_editable(
            _result_data(result, 'persisted_document'),
            stage=source.stage,
        )
        return _json_dumps({
            'publish_result': _primary_data(result),
            'draft_document': persisted.model_dump(exclude_defaults=True),
            'published_link': _published_link(target),
        })

    def replace_document(
        self,
        content_json: str,
        source_document_json: str,
        target_document_json: str = '',
        target_uri: str = '',
        media_assets_json: str = '',
    ) -> str:
        """Replace a provider document with the selected WriterDocument."""
        return self._write_document(
            mode='replace',
            content_json=content_json,
            source_document_json=source_document_json,
            target_document_json=target_document_json,
            target_uri=target_uri,
            media_assets_json=media_assets_json,
        )

    def append_document(
        self,
        content_json: str,
        target_document_json: str = '',
        target_uri: str = '',
        publish_outline: bool = False,
        media_assets_json: str = '',
    ) -> str:
        """Append a WriterDocument to a provider target."""
        document = WriterDocument.model_validate(_json_loads(content_json, {}))
        if document.stage == 'outline' and not publish_outline:
            raise ValueError(
                'Refusing to publish outline IR as the final document. '
                'Set publish_outline=true only for an explicit outline publish.',
            )
        return self._write_document(
            mode='append',
            content_json=content_json,
            source_document_json=content_json,
            target_document_json=target_document_json,
            target_uri=target_uri,
            media_assets_json=media_assets_json,
        )

    def _write_document(
        self,
        *,
        mode: str,
        content_json: str,
        source_document_json: str = '',
        target_document_json: str = '',
        target_uri: str = '',
        media_assets_json: str = '',
    ) -> str:
        root = _temp_root()
        document = WriterDocument.model_validate(_json_loads(content_json, {}))
        source = (
            WriterDocument.model_validate(_json_loads(source_document_json, {}))
            if source_document_json else None
        )
        target = _resolve_target(source, target_document_json, target_uri)
        if target is None:
            raise ValueError('A target provider document is required.')
        publish_document = _set_document_editable(document, stage='final')
        resource = WriterResourceTools(llm=None, artifact_store=str(root))
        media_assets = (
            _json_loads(media_assets_json, {}) if media_assets_json.strip() else None
        )
        write_result = (
            resource.replace_document(publish_document, target, media_assets)
            if mode == 'replace'
            else resource.append_to_document(publish_document, target, media_assets)
        )
        refreshed = resource.document_to_docir(TargetDocument(
            **target.model_dump(exclude={'meta'}),
            meta={**target.meta, 'stage': 'final'},
        ))
        published = _set_document_editable(_primary_data(refreshed), stage='final')
        if mode == 'replace':
            source_images = (
                block for block in publish_document.iter_blocks() if block.type == 'image'
            )
            published_images = (
                block for block in published.iter_blocks() if block.type == 'image'
            )
            for source_image, published_image in zip(source_images, published_images):
                published_image.references = deepcopy(source_image.references)
        return _json_dumps({
            'publish_result': _primary_data(write_result),
            'draft_document': published.model_dump(exclude_defaults=True),
            'published_link': _published_link(target),
        })


class WriterCreateToolkit(WriterToolkitBase):
    """Create long-form writing from source profiling through final output.

    Start with build_writing_task, profile resources and create context. Build
    the outline before drafting sections, assemble the document, then validate
    consistency and generate the final output.
    """

    __public_apis__ = [
        'build_writing_task', 'build_resources', 'profile_resources',
        'create_writing_context', 'prepare_outline', 'generate_outline',
        'generate_rewrite_outline', 'generate_rewrite_section_instructions',
        'generate_section_instructions', 'generate_draft_section',
        'generate_draft_section_markdown',
        'generate_draft_blocks', 'generate_draft_blocks_markdown',
        'generate_draft_document', 'generate_draft_document_markdown',
        'update_writing_context', 'check_consistency',
        'generate_final_document', 'render_markdown',
    ]


class WriterRevisionToolkit(WriterToolkitBase):
    """Revise an existing draft through a validated structured patch workflow.

    Build a revision task against WriterDocument, locate the target, generate
    and validate a patch set, then apply it to produce a revised WriterDocument.
    """

    __public_apis__ = [
        'build_revise_task', 'build_revision_task', 'locate_revision_target',
        'generate_modify_plan', 'build_revision_visual_plan', 'generate_patch_set',
        'generate_string_replace_set',
        'plan_revision', 'validate_patch_set', 'apply_patch',
        'apply_string_replace', 'apply_revision',
    ]


class WriterResourceToolkit(WriterToolkitBase):
    """Load and persist WriterDocuments through provider-neutral resource tools."""

    __public_apis__ = [
        'load_document', 'create_document', 'publish_revision',
        'replace_document', 'append_document',
    ]
