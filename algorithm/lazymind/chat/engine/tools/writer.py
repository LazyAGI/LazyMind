"""Common writer tools with string/JSON inputs and outputs."""
from __future__ import annotations

import json
import os
import re
import tempfile
import uuid
from collections.abc import Callable
from copy import deepcopy
from pathlib import Path
from threading import RLock
from typing import Any, ClassVar

from lazyllm import LOG, AutoModel
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
from lazyllm.tools.writer.utils import render_document_markdown, save_artifact_json

WRITER_DATA_MODEL_SCHEMA_PREFIX = 'lazyllm.tools.writer.data_models'
_FEISHU_URL_RE = re.compile(
    r"https?://[^\s<>\"']*(?:feishu\.(?:cn|com)|larksuite\.com)/"
    r"[^\s<>\"'，。；！？、（）【】《》「」『』]+",
    re.IGNORECASE,
)


class DraftMarkdownStreamEventEmitter:
    """Publish each Draft Markdown delta in one attempt-scoped event stream."""

    EVENT_TYPES: ClassVar[dict[str, str]] = {
        'start': 'artifact_stream_start',
        'delta': 'artifact_stream',
        'end': 'artifact_stream_end',
        'abort': 'artifact_stream_abort',
    }

    def __init__(
        self,
        emit: Callable[[dict[str, Any]], None],
    ) -> None:
        self._emit = emit
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
            self._publish_locked('delta', delta=delta)

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
            'slot': 'draft_document',
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
            LOG.warning('[Writer] failed to forward Draft Markdown stream event: %s', exc)


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
        """Build a writing task from the user's original request."""
        task = WritingTask(task_id=task_id.strip() or None, query=query, task_type='write')
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
        artifact_store = Path(media_store.strip()) if media_store.strip() else root
        artifact_store.mkdir(parents=True, exist_ok=True)
        result = WriterMultimodalTools(
            llm=AutoModel(model='vlm') if use_vision_model else None,
            artifact_store=str(artifact_store),
        ).collect_available_media(task=task_path, input_resources=resources_path)
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
        root = _temp_root()
        task_path = _write_input_artifact(
            root, 'writing_task.json', _json_loads(writing_task_json, {}), writer_schema('task.WritingTask'),
        )
        context_path = _write_input_artifact(
            root, 'writing_context.json', _json_loads(writing_context_json, {}),
            writer_schema('context.WritingContext'),
        )
        result = WriterPlanningTools(
            llm=AutoModel(model='llm'), artifact_store=str(root),
        ).generate_outline(task=task_path, context=context_path)
        outline = _primary_data(result)
        if isinstance(outline, str):
            return outline
        return _set_document_editable(outline, stage='outline').model_dump_json(exclude_defaults=True)

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
        task_path = _write_input_artifact(
            root, 'writing_task.json', _json_loads(writing_task_json, {}), writer_schema('task.WritingTask'),
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
            try:
                visual_result = planning.generate_visual_plan(
                    task=task_path,
                    outline=outline_path,
                    context=context_path,
                )
                visual_plan = _primary_data(visual_result)
                warnings.extend((visual_result.get('metadata') or {}).get('warnings') or [])
            except Exception as exc:
                warnings.append(f'Visual planning failed: {type(exc).__name__}: {exc}')
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
        task_path = _write_input_artifact(
            root, 'writing_task.json', _json_loads(writing_task_json, {}), writer_schema('task.WritingTask'),
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
        try:
            visual_result = planning.generate_visual_plan(
                task=task_path,
                outline=outline_path,
                context=context_path,
            )
            visual_plan = _primary_data(visual_result)
            warnings.extend((visual_result.get('metadata') or {}).get('warnings') or [])
        except Exception as exc:
            visual_plan = VisualPlan().model_dump()
            warnings.append(f'Visual planning failed: {type(exc).__name__}: {exc}')
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

        blocks: list[Any] = []
        for instruction in instructions:
            block = _json_loads(self.generate_draft_section(
                writing_task_json=writing_task_json,
                section_instruction_json=_json_dumps(instruction),
                writing_context_json=writing_context_json,
                previous_blocks_json=_json_dumps(blocks),
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
    ) -> str:
        """Generate every planned draft section in Markdown, in order."""
        return self.generate_draft_blocks(
            writing_task_json=writing_task_json,
            section_instructions_json=section_instructions_json,
            writing_context_json=writing_context_json,
        )

    def stream_draft_blocks_markdown(
        self,
        writing_task_json: str,
        section_instructions_json: str,
        writing_context_json: str,
        on_delta: Callable[[str], None],
        on_section_end: Callable[[], None] | None = None,
    ) -> str:
        """Generate Markdown sections through LazyLLM's non-tool streaming API."""
        return self._stream_draft_blocks(
            writing_task_json=writing_task_json,
            section_instructions_json=section_instructions_json,
            writing_context_json=writing_context_json,
            representation='markdown',
            on_delta=on_delta,
            on_section_end=on_section_end,
        )

    def stream_draft_blocks_ir(
        self,
        writing_task_json: str,
        section_instructions_json: str,
        writing_context_json: str,
        on_delta: Callable[[str], None],
        on_section_end: Callable[[], None] | None = None,
        visual_plan_json: str = '',
        media_assets_json: str = '',
    ) -> str:
        """Generate IR sections while exposing their Markdown preview deltas."""
        return self._stream_draft_blocks(
            writing_task_json=writing_task_json,
            section_instructions_json=section_instructions_json,
            writing_context_json=writing_context_json,
            representation='ir',
            on_delta=on_delta,
            on_section_end=on_section_end,
            visual_plan_json=visual_plan_json,
            media_assets_json=media_assets_json,
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
        visual_plan_json: str = '',
        media_assets_json: str = '',
    ) -> str:
        instructions_data = _json_loads(section_instructions_json, {})
        instructions = (
            instructions_data.get('instructions')
            if isinstance(instructions_data, dict) else None
        )
        if not isinstance(instructions, list):
            raise TypeError('section_instructions_json must contain instructions.')

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
        drafting = WriterDraftingTools(
            llm=AutoModel(model='llm'), artifact_store=str(root),
        )
        sections: list[Any] = []
        for instruction_data in instructions:
            instruction = SectionInstruction.model_validate(instruction_data)
            stream_factory = (
                drafting.stream_draft_section
                if representation == 'markdown'
                else drafting.stream_draft_section_ir
            )
            stream_kwargs: dict[str, Any] = {
                'task': task_path,
                'section_instruction': instruction,
                'context': context_path,
                'previous_blocks': sections,
            }
            if representation == 'ir':
                if visual_plan_path is not None:
                    stream_kwargs['visual_plan'] = visual_plan_path
                if media_assets_path is not None:
                    stream_kwargs['media_assets'] = media_assets_path
            with stream_factory(
                **stream_kwargs,
            ) as stream:
                for delta in stream:
                    try:
                        on_delta(delta)
                    except Exception as exc:  # noqa: BLE001 - preview forwarding is best effort.
                        LOG.warning(
                            '[Writer] Draft %s delta callback failed: %s',
                            representation, exc,
                        )
                result = stream.result()
            section = _primary_data(result)
            if representation == 'markdown' and not isinstance(section, str):
                raise TypeError(
                    'Markdown Draft stream returned a non-Markdown artifact.',
                )
            if representation == 'ir' and not isinstance(section, dict):
                raise TypeError('IR Draft stream returned a non-WriterBlock artifact.')
            sections.append(section)
            if on_section_end is not None:
                try:
                    on_section_end()
                except Exception as exc:  # noqa: BLE001 - preview forwarding is best effort.
                    LOG.warning(
                        '[Writer] Draft %s section callback failed: %s',
                        representation, exc,
                    )
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
                'markdown': value,
            })
        document = WriterDocument.model_validate(value)
        return _json_dumps({
            'title': document.title,
            'markdown': render_document_markdown(document),
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
