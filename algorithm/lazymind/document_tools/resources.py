"""Provider-neutral document loading, creation, and write-back."""

from __future__ import annotations
import re
from collections.abc import Mapping
from copy import deepcopy
from pathlib import Path
from typing import Any
from lazyllm.tools.agent import ToolExecutionError
from lazyllm.tools.writer.data_models import (
    MediaAssetLibrary,
    PatchResult,
    PatchSet,
    TargetDocument,
    WriterDocument,
)
from lazyllm.tools.writer.provider import (
    WriterProviderBase,
    WriterProviderDocument,
    WriterProviderWriteOutcomeError,
    get_writer_provider,
    is_ambiguous_write_error,
    list_writer_providers,
    match_writer_provider,
    resolve_writer_create_target,
)
from lazyllm.tools.writer.tools import WriterResourceTools, WriterRevisionTools
from lazyllm.tools.writer.tools.revision_tools import apply_patch_to_ir
from .artifacts import (
    WRITER_BLOCK_SCHEMA,
    WRITER_IR_SCHEMA,
    _document_value,
    _json_dumps,
    _json_loads,
    _primary_data,
    _result_data,
    _set_document_editable,
    _temp_root,
)

_PROVIDER_LOCATOR_RE = re.compile(
    r"(?:https?://|[a-z][a-z0-9_+.-]*:(?://)?)[^\s<>\"'，。；！？、（）【】《》「」『』]+",
    re.IGNORECASE,
)


def list_document_providers() -> list[dict[str, Any]]:
    return list_writer_providers()


def _provider_name_from_document(document: WriterDocument) -> str:
    return str(document.provider_binding.get('provider') or '').strip().lower()


def _merge_provider_state(
    document: WriterDocument,
    persisted: WriterDocument,
) -> WriterDocument:
    merged = document.model_copy(deep=True)
    local_blocks = list(merged.iter_blocks())
    persisted_blocks = list(persisted.iter_blocks())
    if len(local_blocks) != len(persisted_blocks):
        raise ToolExecutionError(
            'Provider document block count does not match the written WriterDocument.'
        )
    for local, remote in zip(local_blocks, persisted_blocks):
        if local.type != remote.type:
            raise ToolExecutionError(
                'Provider document block order does not match the written WriterDocument.'
            )
        local.provider_binding = deepcopy(remote.provider_binding)
        local.provider_payload = deepcopy(remote.provider_payload)
        local.editable = remote.editable
    merged.revision = persisted.revision
    merged.provider_binding = deepcopy(persisted.provider_binding)
    for key in ('source', 'provider_metadata', 'block_count', 'source_block_count'):
        if key in persisted.metadata:
            merged.metadata[key] = deepcopy(persisted.metadata[key])
        else:
            merged.metadata.pop(key, None)
    return merged


def sync_writer_documents(
    source_value: Any,
    revised_value: Any,
    media_assets: Any = None,
    artifact_store: str = '',
) -> dict[str, Any]:
    """Persist one WriterDocument delta and bind its semantic IR to the provider."""
    source = WriterDocument.model_validate(source_value)
    revised = WriterDocument.model_validate(revised_value)
    provider_name = _provider_name_from_document(source)
    target = _target_from_document(source)
    if not provider_name or target is None:
        raise ToolExecutionError(
            'Bound write-back requires a source provider binding and target identity.'
        )
    if source.document_id != revised.document_id:
        raise ToolExecutionError('WriterDocument document_id values must match.')
    for field in ('stage', 'revision', 'provider_binding'):
        if getattr(source, field) != getattr(revised, field):
            raise ToolExecutionError(f'WriterDocument {field} values must match.')
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
            patch_id=f'patch-{source.document_id}',
            target_doc_id=source.document_id,
        )
    else:
        revision = WriterRevisionTools(llm=None, artifact_store=str(root))
        patch = PatchSet.model_validate(
            _primary_data(
                revision.build_patch_set_from_documents(source, revised, library),
            )
        )
    changed = bool(patch.hunks or patch.new_title is not None)
    if changed:
        output = WriterResourceTools(
            llm=None,
            artifact_store=str(root),
        ).apply_patch_to_document(patch, source, target, media_assets=library)
        persisted = WriterDocument.model_validate(
            _result_data(output, 'persisted_document')
        )
        result = PatchResult.model_validate(_result_data(output, 'patch_result'))
    else:
        persisted = source
        result = PatchResult(
            patch_id=patch.patch_id,
            success=True,
            message='No document changes.',
        )
    candidate = _merge_provider_state(revised, persisted)
    candidate.ui_editable = True
    return {
        'success': result.success,
        'changed': changed,
        'provider_synced': result.success,
        'patch_set': patch.model_dump(),
        'patch_result': result.model_dump(),
        'persisted_document': candidate.model_dump(),
    }


def sync_document(
    source_document: Mapping[str, Any],
    revised_document: Mapping[str, Any],
    media_assets: Mapping[str, Any] | None = None,
    artifact_store: str = '',
) -> dict[str, Any]:
    """Synchronize one edited, provider-bound WriterDocument through a PatchSet."""
    return sync_writer_documents(
        source_document,
        revised_document,
        media_assets,
        artifact_store,
    )


def convert_document(
    content: str | Mapping[str, Any] | WriterDocument,
    provider: str = '',
    media_assets: Mapping[str, Any] | None = None,
    target_document: Mapping[str, Any] | None = None,
    *,
    output_format: str = 'native',
    template: str = '',
) -> dict[str, Any]:
    """Purely convert canonical Writer content to one provider's copyable format."""
    if output_format != 'native':
        library = MediaAssetLibrary.model_validate(media_assets) if media_assets else None
        source = WriterDocument.model_validate(content) if isinstance(content, Mapping) else content
        return WriterProviderBase.convert_common_document(
            source, output_format=output_format, media_assets=library,
        ).model_dump()
    provider_name = provider.strip().lower()
    if not provider_name:
        raise ToolExecutionError('provider is required for document conversion.')
    if isinstance(content, Mapping):
        document = WriterDocument.model_validate(content)
        if _provider_name_from_document(document) not in {'', provider_name}:
            from .artifacts import detach_provider_binding

            document = WriterDocument.model_validate(detach_provider_binding(document))
        content = document
    target = (
        TargetDocument.model_validate(target_document) if target_document else None
    )
    if target is not None and target.adapter and target.adapter != provider_name:
        raise ToolExecutionError('target_document provider does not match provider.')
    media_library = MediaAssetLibrary.model_validate(media_assets) if media_assets else None
    writer_provider = get_writer_provider(provider_name)
    converted = writer_provider.convert_document_with_template(
        content,
        target=target,
        media_assets=media_library,
        template=template,
    )
    return converted.model_dump()


def write_document(
    converted_document: Mapping[str, Any] | WriterProviderDocument,
    target_document: Mapping[str, Any] | None = None,
    media_assets: Mapping[str, Any] | None = None,
    title: str = '',
    parent_uri: str = '',
    mode: str = 'replace',
) -> dict[str, Any]:
    """Write an already converted provider document, then read back confirmation."""
    if mode not in {'replace', 'append'}:
        raise ToolExecutionError('mode must be replace or append.')
    converted = WriterProviderDocument.model_validate(converted_document)
    if not converted.provider:
        raise ToolExecutionError('Portable document conversions cannot be written to a provider.')
    provider = get_writer_provider(converted.provider)
    target = TargetDocument.model_validate(target_document) if target_document else None
    if target is None:
        provider.require_capability('create')
        target = provider.create_document(
            title.strip() or converted.source_document.title or '未命名文档',
            parent_uri.strip(),
        )
    if target.adapter and target.adapter != converted.provider:
        raise ToolExecutionError(
            'target_document provider does not match converted_document provider.'
        )
    target.adapter = converted.provider
    media_library = MediaAssetLibrary.model_validate(media_assets) if media_assets else None
    provider.require_capability(mode)
    try:
        write_result = provider.write_document(
            converted,
            target,
            media_assets=media_library,
            mode=mode,
        )
    except WriterProviderWriteOutcomeError:
        raise
    except Exception as exc:
        if is_ambiguous_write_error(exc):
            raise WriterProviderWriteOutcomeError(converted.provider, mode) from exc
        raise

    refreshed_target = TargetDocument(
        doc_id=str(write_result.get('doc_id') or target.doc_id or '') or None,
        uri=str(write_result.get('locator') or target.uri or '') or None,
        adapter=str(write_result.get('adapter') or converted.provider),
        title=target.title or converted.source_document.title or None,
        meta=deepcopy(target.meta),
    )
    persisted_value = write_result.get('persisted_document')
    representation = str(write_result.get('representation') or '')
    if persisted_value is None:
        loaded = provider.load_document(refreshed_target)
        persisted_value = loaded.get('source_document')
        representation = str(loaded.get('representation') or representation)
        loaded_target = loaded.get('target_document')
        if loaded_target:
            refreshed_target = TargetDocument.model_validate(loaded_target)
    if isinstance(persisted_value, WriterDocument):
        persisted = persisted_value
    elif representation == 'ir' or isinstance(persisted_value, Mapping):
        persisted = WriterDocument.model_validate(persisted_value)
    else:
        persisted = persisted_value
    if isinstance(persisted, WriterDocument):
        persisted = _set_document_editable(
            _merge_provider_state(converted.source_document, persisted),
            stage='final',
        ).model_dump()
    normalized_write_result = deepcopy(write_result)
    if isinstance(normalized_write_result.get('persisted_document'), WriterDocument):
        normalized_write_result['persisted_document'] = normalized_write_result[
            'persisted_document'
        ].model_dump()
    patch_result = PatchResult(
        success=True,
        message='Document written to provider and read back successfully.',
        meta={'mode': mode, 'write_result': normalized_write_result},
    )
    return {
        'success': True,
        'changed': True,
        'provider_synced': True,
        'patch_result': patch_result.model_dump(),
        'persisted_document': persisted,
        'representation': representation,
        'provider': converted.provider,
        'write_result': normalized_write_result,
        'target_document': refreshed_target.model_dump(exclude_defaults=True),
    }


def _provider_locator_matches(user_input: str):
    # Existing Writer sessions retain requests serialized before mention chips
    # had text separators ("AI Writerobsidian://..."). Keep those locators usable
    # without rewriting the stored request or guessing arbitrary URI prefixes.
    return _PROVIDER_LOCATOR_RE.finditer((user_input or '').removeprefix('AI Writer'))


def _provider_targets(
    user_input: str, *, stage: str | None = None
) -> list[TargetDocument]:
    targets: list[TargetDocument] = []
    seen: set[str] = set()
    for match in _provider_locator_matches(user_input):
        locator = match.group(0).rstrip(').,;!?]}，。；！？】》」』')
        if locator in seen:
            continue
        try:
            target = match_writer_provider(locator).resolve(locator)
        except ValueError:
            continue
        seen.add(locator)
        if stage is not None:
            target.meta = {**target.meta, 'stage': stage}
        targets.append(target)
    return targets


def find_provider_locator(user_input: str) -> str:
    """Return the first locator handled by a registered Writer provider."""
    for match in _provider_locator_matches(user_input):
        locator = match.group(0).rstrip(').,;!?]}，。；！？】》」』')
        try:
            match_writer_provider(locator)
        except ValueError:
            continue
        return locator
    return ''


def _provider_target(user_input: str, *, stage: str | None = None) -> TargetDocument:
    targets = _provider_targets(user_input, stage=stage)
    if not targets:
        raise ToolExecutionError('A supported provider document locator is required.')
    if len(targets) > 1:
        raise ToolExecutionError('Exactly one provider document locator is required.')
    return targets[0]


def _source_document_target(user_input: str, *, stage: str = 'final') -> TargetDocument:
    targets = _provider_targets(user_input, stage=stage)
    if len(targets) > 1:
        raise ToolExecutionError('Exactly one provider document locator is required.')
    if targets:
        return targets[0]
    try:
        provider = match_writer_provider(user_input)
    except ValueError as exc:
        raise ToolExecutionError(
            'A supported provider document locator is required.'
        ) from exc
    try:
        target = provider.resolve(user_input)
    except ValueError as exc:
        raise ToolExecutionError(str(exc)) from exc
    target.meta = {**target.meta, 'stage': stage}
    return target


def _provider_create_target(user_input: str) -> tuple[str, TargetDocument] | None:
    for match in _provider_locator_matches(user_input):
        locator = match.group(0).rstrip(').,;!?]}，。；！？】》」』')
        try:
            target = resolve_writer_create_target(locator)
        except ValueError:
            continue
        if target.meta.get('create_pending'):
            return locator, target
    return None


def _extract_provider_resources(user_input: str) -> list[dict]:
    resources: list[dict] = []
    for idx, target in enumerate(_provider_targets(user_input)):
        provider = str(target.adapter or '')
        resources.append(
            {
                'resource_id': f'{provider}_{idx}',
                'resource_type': 'url',
                'uri': target.uri,
                'title': None,
                'mime_type': None,
                'summary': None,
                'meta': {'provider': provider, 'role': 'background'},
            }
        )
    return resources


def _target_from_document(value: Any) -> TargetDocument | None:
    document = WriterDocument.model_validate(value)
    binding = document.provider_binding
    target = TargetDocument(
        doc_id=binding.get('document_id'),
        uri=binding.get('uri'),
        adapter=binding.get('provider'),
        title=document.title or None,
        meta={
            key: binding[key]
            for key in ('article_index', 'thumb_media_id', 'browser_url')
            if binding.get(key) is not None
        },
    )
    if target.uri or target.doc_id:
        return target
    source = document.metadata.get('source')
    if not isinstance(source, dict):
        return None
    try:
        target = TargetDocument.model_validate(source)
    except Exception:
        return None
    return target if target.uri or target.doc_id else None


def _published_link(target: TargetDocument) -> str:
    link = str(
        target.meta.get('browser_url')
        or (
            target.uri if (target.uri or '').startswith(('http://', 'https://')) else ''
        )
    ).strip()
    if not link:
        raise ToolExecutionError(
            'Provider write succeeded but no browser URL was returned.'
        )
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
        target = _provider_target(target_uri.strip())
    return target


class WriterResourceCapabilities:
    WRITER_IR_SCHEMA = WRITER_IR_SCHEMA
    WRITER_BLOCK_SCHEMA = WRITER_BLOCK_SCHEMA

    def load_document(self, user_input: str, stage: str = 'final') -> str:
        """Load a provider document without changing its Writer representation."""
        if stage not in {'outline', 'draft', 'final'}:
            raise ToolExecutionError('stage must be outline, draft, or final.')
        root = _temp_root()
        target = _source_document_target(user_input, stage=stage)
        result = WriterResourceTools(
            llm=None,
            artifact_store=str(root),
        ).load_document(target)
        artifact_paths = (result.get('metadata') or {}).get('artifact_paths') or {}
        return _json_dumps(
            {
                'source_document': _primary_data(result),
                'target_document': _result_data(result, 'target_document'),
                'representation': result.get('representation'),
                'input_resources': (
                    _result_data(result, 'input_resources')
                    if artifact_paths.get('input_resources')
                    else []
                ),
                'resource_warnings': (result.get('metadata') or {}).get('warnings')
                or [],
            }
        )

    def resolve_create_target(self, user_input: str) -> str:
        """Resolve an optional deferred provider target for a new document."""
        resolved = _provider_create_target(user_input)
        if resolved is None:
            return _json_dumps({})
        locator, target = resolved
        return _json_dumps(
            {
                'target_ref': locator,
                'target_document': target.model_dump(exclude_defaults=True),
            }
        )

    def prepare_markdown_for_editor(
        self, markdown: str, target_document_json: str
    ) -> str:
        """Prepare Markdown through an optional provider capability."""
        target = TargetDocument.model_validate(_json_loads(target_document_json, {}))
        provider_name = str(target.adapter or '').strip()
        if not provider_name and not str(target.uri or '').strip():
            return _json_dumps({'markdown': markdown, 'target_document': None})
        provider = (
            get_writer_provider(provider_name)
            if provider_name
            else match_writer_provider(str(target.uri or ''))
        )
        original_target = target.model_dump()
        prepared = provider.prepare_markdown_for_editor(markdown, target)
        changed = prepared != markdown or target.model_dump() != original_target
        return _json_dumps(
            {
                'markdown': prepared,
                'target_document': (
                    target.model_dump(exclude_defaults=True) if changed else None
                ),
            }
        )

    def create_document(
        self, title: str, parent_uri: str = '', adapter: str = ''
    ) -> str:
        """Create an empty provider document and return its target binding."""
        root = _temp_root()
        provider = adapter.strip()
        if not provider and parent_uri.strip():
            provider = str(_provider_target(parent_uri.strip()).adapter or '')
        if not provider:
            raise ToolExecutionError(
                'adapter is required when parent_uri cannot identify a provider.'
            )
        result = WriterResourceTools(
            llm=None,
            artifact_store=str(root),
        ).create_document(
            title=title.strip() or '未命名文档',
            parent_uri=parent_uri.strip(),
            adapter=provider,
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
        patch = PatchSet.model_validate(_json_loads(patch_set_json, {}))
        media_assets = (
            MediaAssetLibrary.model_validate(_json_loads(media_assets_json, {}))
            if media_assets_json.strip()
            else None
        )
        revised, _ = apply_patch_to_ir(source, patch, media_assets=media_assets)
        target = _target_from_document(source)
        if target is None:
            raise ToolExecutionError(
                'source document must contain a cloud target binding.'
            )
        result = WriterResourceTools(
            llm=None,
            artifact_store=str(root),
        ).apply_patch_to_document(
            patch_set=patch,
            source_document=source,
            media_assets=media_assets,
        )
        persisted = WriterDocument.model_validate(
            _result_data(result, 'persisted_document')
        )
        published = _set_document_editable(
            _merge_provider_state(revised, persisted),
            stage=source.stage,
        )
        return _json_dumps(
            {
                'publish_result': _primary_data(result),
                'draft_document': published.model_dump(exclude_defaults=True),
                'provider': str(target.adapter or ''),
                'representation': 'ir',
                'published_link': _published_link(target),
            }
        )

    def convert_document(
        self,
        content_json: str,
        provider: str = '',
        target_document_json: str = '',
        media_assets_json: str = '',
        output_format: str = 'native',
        template: str = '',
    ) -> str:
        """Convert Writer content without provider IO."""
        return _json_dumps(convert_document(
            _document_value(content_json),
            provider,
            output_format=output_format,
            media_assets=(
                _json_loads(media_assets_json, {}) if media_assets_json.strip() else None
            ),
            target_document=(
                _json_loads(target_document_json, {})
                if target_document_json.strip()
                else None
            ),
            template=template,
        ))

    def write_document(
        self,
        converted_document_json: str,
        target_document_json: str = '',
        media_assets_json: str = '',
        title: str = '',
        parent_uri: str = '',
        mode: str = 'replace',
    ) -> str:
        """Persist an explicit provider conversion artifact."""
        payload = write_document(
            _json_loads(converted_document_json, {}),
            target_document=(
                _json_loads(target_document_json, {})
                if target_document_json.strip()
                else None
            ),
            media_assets=(
                _json_loads(media_assets_json, {}) if media_assets_json.strip() else None
            ),
            title=title,
            parent_uri=parent_uri,
            mode=mode,
        )
        target = TargetDocument.model_validate(payload['target_document'])
        write_result = payload.get('write_result')
        published_link = (
            str(write_result.get('published_link') or '').strip()
            if isinstance(write_result, Mapping) and 'published_link' in write_result
            else _published_link(target)
        )
        return _json_dumps({
            'publish_result': payload['write_result'],
            'draft_document': payload['persisted_document'],
            'representation': payload['representation'],
            'provider': payload['provider'],
            'published_link': published_link,
            'target_document': payload['target_document'],
        })


def resolve_provider_targets(
    user_input: str, *, stage: str | None = None
) -> list[TargetDocument]:
    return _provider_targets(user_input, stage=stage)


def resolve_provider_target(
    user_input: str, *, stage: str | None = None
) -> TargetDocument:
    return _provider_target(user_input, stage=stage)


def provider_reference(value: str) -> str:
    """Return a concrete provider locator or the matched provider name."""
    locator = find_provider_locator(value)
    if locator:
        return locator
    try:
        return match_writer_provider(value).provider
    except ValueError:
        return ''


def extract_provider_resources(user_input: str) -> list[dict]:
    return _extract_provider_resources(user_input)


def resolve_document_target(
    source_document: WriterDocument | None = None,
    target_document_json: str = '',
    target_uri: str = '',
) -> TargetDocument | None:
    return _resolve_target(source_document, target_document_json, target_uri)


__all__ = [
    'WriterResourceCapabilities',
    'convert_document',
    'extract_provider_resources',
    'list_document_providers',
    'provider_reference',
    'resolve_provider_target',
    'resolve_provider_targets',
    'sync_document',
    'sync_writer_documents',
    'write_document',
]
