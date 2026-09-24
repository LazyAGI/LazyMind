"""AI rewrite and validated document patch capabilities."""

from __future__ import annotations
import re
from pathlib import Path
from typing import Any

from lazyllm import AutoModel
from lazyllm.tools.agent import ToolExecutionError
from lazyllm.tools.writer.data_models import (
    ModifyPlan,
    TargetDocument,
    VisualInstruction,
    VisualPlan,
    WriterDocument,
    WritingTask,
)
from lazyllm.tools.writer.tools import WriterQualityTools, WriterRevisionTools
from lazyllm.tools.writer.utils.serialization import markdown_image_sources
from .artifacts import (
    WRITER_BLOCK_SCHEMA,
    WRITER_IR_SCHEMA,
    _document_value,
    _json_dumps,
    _json_loads,
    _primary_data,
    _read_artifact_data,
    _set_document_editable,
    _temp_root,
    _write_document_input,
    _write_input_artifact,
    writer_schema,
)
from .resources import _target_from_document, sync_writer_documents


def revise_markdown_document(
    document: str,
    instruction: str,
    *,
    constraints: str = '',
    artifact_store: str,
) -> str:
    """Revise complete Markdown in one model call, without Workflow context."""
    instruction = str(instruction or '').strip()
    if not instruction:
        raise ValueError('instruction must not be empty.')
    prompt = f'''Revise the complete Markdown document once according to the instruction.

Return only the complete revised Markdown document. Do not return JSON, a patch, a change
plan, analysis, commentary, or an outer Markdown code fence. The source document is the
only document source of truth. Preserve unaffected content and the user's latest edits.
Apply the requested changes directly; do not merely describe them. Do not invent facts,
data, methods, results, citations, or source metadata.

Constraints:
{constraints}

Revision instruction:
{instruction}

Source Markdown:
{document}
'''
    revision = WriterRevisionTools(
        llm=AutoModel(model='llm'), artifact_store=artifact_store,
    )
    revised = str(revision._call_llm_text(prompt) or '').strip()  # noqa: SLF001
    outer_fence = re.fullmatch(
        r'```(?:markdown|md)?\s*\n?(.*?)\n?```', revised,
        flags=re.DOTALL | re.IGNORECASE,
    )
    if outer_fence:
        revised = outer_fence.group(1).strip()
    if re.search(r'^#{1,6}\s+', document, flags=re.MULTILINE):
        first_heading = re.search(r'^#{1,6}\s+', revised, flags=re.MULTILINE)
        if first_heading and first_heading.start() > 0:
            revised = revised[first_heading.start():].strip()
    if not revised:
        raise ValueError('Shared Writer returned no revised Markdown document.')
    return revised


def modify_plan_needs_media(plan: dict[str, Any]) -> bool:
    """Return whether a normalized revision plan requests visual changes."""
    return any(
        isinstance(instruction, dict) and bool(instruction.get('visual_instruction'))
        for instruction in (plan.get('instructions') or [])
    )


def finalize_markdown_revision(
    markdown: str, resolved_media_assets: Any = None, *, source: str | None = None,
) -> str:
    """Resolve media placeholders introduced by a Markdown revision."""
    from .writing import fill_markdown_media_placeholders

    library = resolved_media_assets or {}
    needs = library.get('visual_need_asset_ids') or {}
    assets = library.get('assets') or {}
    images = markdown_image_sources(markdown)
    existing = markdown_image_sources(source) if source is not None else set()
    placeholders = {f'media-placeholder://{need_id}' for need_id in needs}
    if source is not None:
        unknown = images - existing - placeholders
        if unknown:
            raise ValueError(f'Unregistered revision images: {sorted(unknown)}')
    for target in images:
        if not target.startswith('media-placeholder://'):
            continue
        need_id = target.removeprefix('media-placeholder://')
        asset_ids = needs.get(need_id) or []
        asset = assets.get(asset_ids[0], {}) if asset_ids else {}
        path = str(asset.get('local_path') or asset.get('uri') or '')
        if not path or (not path.startswith(('https://', 'http://')) and not Path(path).is_file()):
            raise ValueError(f'Revision image is unavailable: {need_id}')
    filled = (fill_markdown_media_placeholders(markdown, library)
              if any(target.startswith('media-placeholder://') for target in images) else markdown)
    remaining = markdown_image_sources(filled)
    if any(target.startswith('media-placeholder://') for target in remaining):
        raise ValueError('Revision contains unresolved image placeholders.')
    for need_id, asset_ids in needs.items():
        if not asset_ids:
            continue
        asset = assets.get(asset_ids[0]) or {}
        paths = {
            value if value.startswith(('https://', 'http://')) else Path(value).as_posix()
            for key in ('local_path', 'uri')
            if (value := str(asset.get(key) or ''))
        }
        if not paths.intersection(remaining):
            raise ValueError(f'Resolved revision image was not inserted: {need_id}')
    return filled


def generate_revision_set(
    document: Any,
    writing_context: Any,
    modify_plan: dict[str, Any],
    *,
    media_assets: Any = None,
) -> dict[str, Any]:
    """Generate the representation-appropriate deterministic revision set."""
    toolkit = WriterRevisionCapabilities()
    if isinstance(document, str):
        return {
            'revision_set': _json_loads(
                toolkit.generate_string_replace_set(
                    markdown_document=document,
                    modify_plan_json=_json_dumps(modify_plan),
                    writing_context_json=_json_dumps(writing_context),
                ),
                {},
            ),
            'schema_name': writer_schema('revision.StringReplaceSet'),
        }
    return {
        'revision_set': _json_loads(
            toolkit.generate_patch_set(
                writer_document_json=_json_dumps(document),
                modify_plan_json=_json_dumps(modify_plan),
                writing_context_json=_json_dumps(writing_context),
                media_assets_json=(
                    _json_dumps(media_assets) if media_assets is not None else ''
                ),
            ),
            {},
        ),
        'schema_name': writer_schema('revision.PatchSet'),
    }


def apply_document_revision(
    document: Any,
    writing_context: Any,
    revision_set: dict[str, Any],
    *,
    media_assets: Any = None,
    sync_provider: bool = False,
    allow_outline: bool = True,
) -> dict[str, Any]:
    """Apply the representation-appropriate revision and normalize its result."""
    toolkit = WriterRevisionCapabilities()
    if isinstance(document, str):
        payload = _json_loads(
            toolkit.apply_string_replace(
                markdown_document=document,
                string_replace_set_json=_json_dumps(revision_set),
                writing_context_json=_json_dumps(writing_context),
            ),
            {},
        )
        payload['revised_document'] = finalize_markdown_revision(
            payload.get('revised_document') or '', media_assets, source=document
        )
        return {
            'payload': payload,
            'result': payload.get('string_replace_result') or {},
            'schema_name': writer_schema('revision.StringReplaceResult'),
        }
    payload = _json_loads(
        toolkit.apply_revision(
            writer_document_json=_json_dumps(document),
            patch_set_json=_json_dumps(revision_set),
            writing_context_json=_json_dumps(writing_context),
            media_assets_json=(
                _json_dumps(media_assets) if media_assets is not None else ''
            ),
            sync_provider=sync_provider,
            allow_outline=allow_outline,
        ),
        {},
    )
    return {
        'payload': payload,
        'result': payload.get('patch_result') or {},
        'schema_name': writer_schema('revision.PatchResult'),
    }


def preview_selection_rewrite(
    document: str | dict,
    instruction: str,
    selection_ranges: list[dict],
    context: Any,
    *,
    artifact_store: str,
) -> dict[str, Any]:
    """Generate whole-block candidates using the original Writer patch contracts."""
    instruction = str(instruction or '').strip()
    if not instruction:
        raise ValueError('instruction must not be empty.')
    from .selection import preview_ir, preview_markdown
    if isinstance(document, str):
        return preview_markdown(document, instruction, selection_ranges, artifact_store=artifact_store)
    return preview_ir(document, instruction, selection_ranges, context, artifact_store=artifact_store)


class WriterRevisionCapabilities:
    WRITER_IR_SCHEMA = WRITER_IR_SCHEMA
    WRITER_BLOCK_SCHEMA = WRITER_BLOCK_SCHEMA

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
        document = (
            WriterDocument.model_validate(source) if isinstance(source, dict) else None
        )
        if document and document.stage == 'outline' and not allow_outline:
            raise ToolExecutionError(
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
            root,
            'patch_set.json',
            _json_loads(patch_set_json, {}),
            writer_schema('revision.PatchSet'),
        )
        context_path = _write_input_artifact(
            root,
            'writing_context.json',
            _json_loads(writing_context_json, {}),
            writer_schema('context.WritingContext'),
        )
        task_path = _write_input_artifact(
            root,
            'writing_task.json',
            _json_loads(writing_task_json, {}),
            writer_schema('task.WritingTask'),
        )
        result = WriterQualityTools(
            llm=AutoModel(model='llm'),
            artifact_store=str(root),
        ).validate_patch_set(
            patch_set=patch_set_path,
            context=context_path,
            task=task_path,
        )
        return _json_dumps(
            {
                'patch_set_review': _primary_data(result),
                'patch_set_review_summary': result.get('summary') or '',
            }
        )

    def locate_revision_target(
        self,
        writing_task_json: str,
        writer_document_json: str,
        writing_context_json: str,
    ) -> str:
        """Locate the IR or Markdown content affected by a revision task."""
        root = _temp_root()
        task_path = _write_input_artifact(
            root,
            'writing_task.json',
            _json_loads(writing_task_json, {}),
            writer_schema('task.WritingTask'),
        )
        document_path = _write_document_input(
            root, 'writer_document', writer_document_json
        )
        context_path = _write_input_artifact(
            root,
            'writing_context.json',
            _json_loads(writing_context_json, {}),
            writer_schema('context.WritingContext'),
        )
        result = WriterRevisionTools(
            llm=AutoModel(model='llm'),
            artifact_store=str(root),
        ).locate_revision_target(
            task=task_path, document=document_path, context=context_path
        )
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
            root,
            'writing_task.json',
            _json_loads(writing_task_json, {}),
            writer_schema('task.WritingTask'),
        )
        document_path = _write_document_input(
            root, 'writer_document', writer_document_json
        )
        locate_path = _write_input_artifact(
            root,
            'locate_result.json',
            _json_loads(locate_result_json, {}),
            writer_schema('revision.LocateResult'),
        )
        context_path = _write_input_artifact(
            root,
            'writing_context.json',
            _json_loads(writing_context_json, {}),
            writer_schema('context.WritingContext'),
        )
        result = WriterRevisionTools(
            llm=AutoModel(model='llm'),
            artifact_store=str(root),
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
                raise ToolExecutionError(
                    'visual_instruction is only valid for create instructions.'
                )
            if visual.visual_type not in {'image', 'diagram', 'chart', 'table'}:
                raise ToolExecutionError(
                    'revision visual_instruction.visual_type must be image, diagram, chart, or table.'
                )
            if visual.need_id != instruction.instruction_id:
                raise ToolExecutionError(
                    'visual_instruction.need_id must equal instruction_id.'
                )
            if visual.content_ref != instruction.content_ref:
                raise ToolExecutionError(
                    'visual_instruction.content_ref must equal content_ref.'
                )
            if not visual.purpose.strip() or not visual.required:
                raise ToolExecutionError(
                    'revision image visual_instruction must be required and non-empty.'
                )
            allowed_strategies = {None, 'image_generation'}
            if visual.visual_type in {'image', 'diagram'}:
                allowed_strategies.add('web_search')
            if visual.preferred_strategy not in allowed_strategies:
                raise ToolExecutionError(
                    'revision visual preferred_strategy is not supported for its visual_type.'
                )
            instructions.append(visual)
        return _json_dumps(
            VisualPlan(instructions=instructions).model_dump(exclude_defaults=True)
        )

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
            root,
            'writer_document.json',
            _json_loads(writer_document_json, {}),
            self.WRITER_IR_SCHEMA,
        )
        plan_path = _write_input_artifact(
            root,
            'modify_plan.json',
            _json_loads(modify_plan_json, {}),
            writer_schema('revision.ModifyPlan'),
        )
        context_path = _write_input_artifact(
            root,
            'writing_context.json',
            _json_loads(writing_context_json, {}),
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
            llm=AutoModel(model='llm'),
            artifact_store=str(root),
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
            root,
            'modify_plan.json',
            _json_loads(modify_plan_json, {}),
            writer_schema('revision.ModifyPlan'),
        )
        context_path = _write_input_artifact(
            root,
            'writing_context.json',
            _json_loads(writing_context_json, {}),
            writer_schema('context.WritingContext'),
        )
        result = WriterRevisionTools(
            llm=AutoModel(model='llm'),
            artifact_store=str(root),
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
        return _json_dumps(
            {
                'locate_result': _json_loads(located, {}),
                'modify_plan': _json_loads(plan, {}),
                'patch_set': _json_loads(patch_set, {}),
            }
        )

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
            root,
            'writer_document.json',
            _json_loads(writer_document_json, {}),
            self.WRITER_IR_SCHEMA,
        )
        patch_path = _write_input_artifact(
            root,
            'patch_set.json',
            _json_loads(patch_set_json, {}),
            writer_schema('revision.PatchSet'),
        )
        context_path = _write_input_artifact(
            root,
            'writing_context.json',
            _json_loads(writing_context_json, {}),
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
        return _json_dumps(
            {
                'patch_result': _primary_data(result),
                'revised_document': revised.model_dump(exclude_defaults=True),
            }
        )

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
            root,
            'string_replace_set.json',
            _json_loads(string_replace_set_json, {}),
            writer_schema('revision.StringReplaceSet'),
        )
        context_path = _write_input_artifact(
            root,
            'writing_context.json',
            _json_loads(writing_context_json, {}),
            writer_schema('context.WritingContext'),
        )
        result = WriterRevisionTools(
            llm=None, artifact_store=str(root)
        ).apply_string_replace(
            document=document_path,
            replace_set=replace_path,
            context=context_path,
        )
        artifact_paths = (result.get('metadata') or {}).get('artifact_paths') or {}
        revised_path = artifact_paths.get('revised_document_md', '')
        return _json_dumps(
            {
                'string_replace_result': _primary_data(result),
                'revised_document': _read_artifact_data(revised_path),
            }
        )

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
            raise ToolExecutionError(
                'A full-document revision cannot use an outline-stage document.',
            )
        applied = _json_loads(
            self.apply_patch(
                writer_document_json=writer_document_json,
                patch_set_json=patch_set_json,
                writing_context_json=writing_context_json,
                media_assets_json=media_assets_json,
            ),
            {},
        )
        output = {
            'patch_result': applied.get('patch_result') or {},
            'revised_document': applied.get('revised_document') or {},
            'write_result': None,
        }
        if not sync_provider or _target_from_document(source) is None:
            return _json_dumps(output)

        from .toolkits import WriterResourceToolkit

        published = _json_loads(
            WriterResourceToolkit().publish_revision(
                source_document_json=writer_document_json,
                patch_set_json=patch_set_json,
                media_assets_json=media_assets_json,
            ),
            {},
        )
        output['revised_document'] = published.get('draft_document') or {}
        output['write_result'] = published.get('publish_result') or {}
        return _json_dumps(output)


__all__ = [
    'WriterRevisionCapabilities',
    'apply_document_revision',
    'finalize_markdown_revision',
    'generate_revision_set',
    'modify_plan_needs_media',
    'sync_writer_documents',
]
