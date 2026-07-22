"""Common writer tools with string/JSON inputs and outputs."""
from __future__ import annotations

import json
import re
import tempfile
import uuid
from pathlib import Path
from typing import Any

from lazyllm import AutoModel
from lazyllm.tools.writer.data_models import (
    InputResource,
    SectionInstruction,
    TargetDocument,
    WriterBlock,
    WriterDocument,
    WritingTask,
)
from lazyllm.tools.writer.tools import (
    WriterContextTools,
    WriterDraftingTools,
    WriterPlanningTools,
    WriterQualityTools,
    WriterResourceTools,
    WriterRevisionTools,
)
from lazyllm.tools.writer.utils import save_artifact_json


WRITER_DATA_MODEL_SCHEMA_PREFIX = 'lazyllm.tools.writer.data_models'


def writer_schema(name: str) -> str:
    return f'{WRITER_DATA_MODEL_SCHEMA_PREFIX}.{name}'


def build_writer_status_ir(status: str, content: str, *, source: str) -> str:
    """Build a non-editable WriterDocument for a UI-visible workflow status."""
    document = WriterDocument(
        document_id=f'{source}-status-{status}',
        stage='final',
        blocks=[WriterBlock(
            node_id=f'{source}-status-{status}-message',
            type='paragraph',
            content=content,
            stage='final',
            editable=False,
        )],
        metadata={'source': source, 'kind': 'step_status', 'status': status},
        ui_editable=False,
    )
    return document.model_dump_json()


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


def _primary_data(result: dict) -> Any:
    artifact_path = result.get('artifact_path')
    if not artifact_path:
        raise ValueError(f'Writer tool did not return artifact_path: {result!r}')
    return _read_artifact_data(artifact_path)


def _extract_feishu_resources(user_input: str) -> list[dict]:
    pattern = re.compile(r'https?://[A-Za-z0-9.\-]+\.feishu\.cn/\S+')
    resources: list[dict] = []
    seen: set[str] = set()
    for idx, match in enumerate(pattern.finditer(user_input or '')):
        url = match.group(0)
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


class WriterToolkitBase:
    """Adapters for LazyLLM's unified WriterDocument/WriterBlock tool APIs."""

    WRITER_IR_SCHEMA = f'{WRITER_DATA_MODEL_SCHEMA_PREFIX}.writer_ir.WriterDocument'
    WRITER_BLOCK_SCHEMA = f'{WRITER_DATA_MODEL_SCHEMA_PREFIX}.writer_ir.WriterBlock'
    __public_apis__: list[str] = []

    def build_writing_task(self, query: str) -> str:
        """Build a writing task from the user's original request."""
        task = WritingTask(query=query, task_type='write')
        return _json_dumps(task.model_dump())

    def profile_resources(self, writing_task_json: str, user_input: str, resources_json: str = '[]') -> str:
        """Profile writing resources."""
        root = _temp_root()
        task_data = _json_loads(writing_task_json, {})
        resources = _json_loads(resources_json, [])
        if resources is None:
            resources = []
        if not isinstance(resources, list):
            raise TypeError('resources_json must be a JSON array.')
        resources = resources + _extract_feishu_resources(user_input)

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
        return _json_dumps(task.model_dump())

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
        """Create context from a task, profiles, and an optional WriterDocument."""
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
            document_path = _write_input_artifact(
                root, 'writer_document.json', _json_loads(writer_document_json, {}), self.WRITER_IR_SCHEMA,
            )
        result = WriterContextTools(llm=None, artifact_store=str(root)).create_writing_context(
            task=task_path,
            resource_profiles=profiles_path,
            document=document_path,
        )
        return _json_dumps(_primary_data(result))

    def generate_outline(self, writing_task_json: str, writing_context_json: str) -> str:
        """Generate an outline-stage WriterDocument as JSON."""
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
        return _json_dumps(_primary_data(result))

    def generate_section_instructions(
        self,
        outline_json: str,
        writing_context_json: str,
    ) -> str:
        """Generate section instructions from an outline-stage WriterDocument."""
        root = _temp_root()
        outline_path = _write_input_artifact(
            root, 'outline.json', _json_loads(outline_json, {}), self.WRITER_IR_SCHEMA,
        )
        context_path = _write_input_artifact(
            root, 'writing_context.json', _json_loads(writing_context_json, {}),
            writer_schema('context.WritingContext'),
        )
        result = WriterPlanningTools(
            llm=AutoModel(model='llm'), artifact_store=str(root),
        ).generate_section_instructions(
            outline=outline_path,
            context=context_path,
        )
        return _json_dumps(_primary_data(result))

    def generate_draft_section(
        self,
        writing_task_json: str,
        section_instruction_json: str,
        writing_context_json: str,
        previous_blocks_json: str = '[]',
    ) -> str:
        """Generate one draft-stage WriterBlock as JSON."""
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
        result = WriterDraftingTools(
            llm=AutoModel(model='llm'), artifact_store=str(root),
        ).generate_draft_section(
            task=task_path,
            section_instruction=instruction,
            context=context_path,
            previous_blocks=previous_blocks,
        )
        return _json_dumps(_primary_data(result))

    def generate_draft_document(
        self,
        draft_blocks_json: str,
        writing_context_json: str,
        outline_json: str = '',
    ) -> str:
        """Combine draft WriterBlocks into a draft-stage WriterDocument."""
        root = _temp_root()
        blocks_data = _json_loads(draft_blocks_json, [])
        if not isinstance(blocks_data, list) or not blocks_data:
            raise ValueError('draft_blocks_json must be a non-empty JSON array.')
        blocks_dir = root / 'draft_blocks'
        blocks_dir.mkdir(parents=True, exist_ok=True)
        block_paths = [
            _write_input_artifact(
                blocks_dir, f'draft_block_{idx}.json', block, self.WRITER_BLOCK_SCHEMA,
            )
            for idx, block in enumerate(blocks_data, start=1)
        ]
        context_path = _write_input_artifact(
            root, 'writing_context.json', _json_loads(writing_context_json, {}),
            writer_schema('context.WritingContext'),
        )
        outline_path = None
        if outline_json:
            outline_path = _write_input_artifact(
                root, 'outline.json', _json_loads(outline_json, {}), self.WRITER_IR_SCHEMA,
            )
        result = WriterDraftingTools(llm=None, artifact_store=str(root)).generate_draft_document(
            draft_blocks=block_paths,
            context=context_path,
            outline=outline_path,
        )
        return _json_dumps(_primary_data(result))

    def update_writing_context(self, content_artifact_json: str, writing_context_json: str) -> str:
        """Update context from a WriterDocument or WriterBlock."""
        root = _temp_root()
        content_data = _json_loads(content_artifact_json, {})
        schema_name = self.WRITER_IR_SCHEMA if 'document_id' in content_data else self.WRITER_BLOCK_SCHEMA
        content_path = _write_input_artifact(root, 'writer_content.json', content_data, schema_name)
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
        """Validate a draft-stage WriterDocument."""
        root = _temp_root()
        draft_path = _write_input_artifact(
            root, 'draft_document.json', _json_loads(draft_document_json, {}), self.WRITER_IR_SCHEMA,
        )
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
        """Return both a final WriterDocument and its rendered Markdown."""
        root = _temp_root()
        draft_path = _write_input_artifact(
            root, 'draft_document.json', _json_loads(draft_document_json, {}), self.WRITER_IR_SCHEMA,
        )
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
        return _json_dumps({
            'final_document': _primary_data(result),
            'final_document_md': markdown,
        })

    def locate_revision_target(
        self,
        writing_task_json: str,
        writer_document_json: str,
        writing_context_json: str,
    ) -> str:
        """Locate the WriterDocument blocks affected by a revision task."""
        root = _temp_root()
        task_path = _write_input_artifact(
            root, 'writing_task.json', _json_loads(writing_task_json, {}), writer_schema('task.WritingTask'),
        )
        document_path = _write_input_artifact(
            root, 'writer_document.json', _json_loads(writer_document_json, {}), self.WRITER_IR_SCHEMA,
        )
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
        document_path = _write_input_artifact(
            root, 'writer_document.json', _json_loads(writer_document_json, {}), self.WRITER_IR_SCHEMA,
        )
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

    def generate_patch_set(
        self,
        writer_document_json: str,
        modify_plan_json: str,
        writing_context_json: str,
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
        result = WriterRevisionTools(
            llm=AutoModel(model='llm'), artifact_store=str(root),
        ).generate_patch_set(document=document_path, modify_plan=plan_path, context=context_path)
        return _json_dumps(_primary_data(result))

    def apply_patch(
        self,
        writer_document_json: str,
        patch_set_json: str,
        writing_context_json: str,
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
        result = WriterRevisionTools(llm=None, artifact_store=str(root)).apply_patch(
            document=document_path,
            patch_set=patch_path,
            context=context_path,
        )
        artifact_paths = (result.get('metadata') or {}).get('artifact_paths') or {}
        revised_path = artifact_paths.get('revised_document', '')
        return _json_dumps({
            'patch_result': _primary_data(result),
            'revised_document': _read_artifact_data(revised_path) if revised_path else {},
        })


class WriterCreateToolkit(WriterToolkitBase):
    """Create long-form writing from source profiling through final output.

    Start with build_writing_task, profile resources and create context. Build
    the outline before drafting sections, assemble the document, then validate
    consistency and generate the final output.
    """

    __public_apis__ = [
        'build_writing_task', 'profile_resources', 'create_writing_context',
        'generate_outline', 'generate_section_instructions',
        'generate_draft_section', 'generate_draft_document',
        'update_writing_context', 'check_consistency',
        'generate_final_document',
    ]


class WriterRevisionToolkit(WriterToolkitBase):
    """Revise an existing draft through a validated structured patch workflow.

    Build a revision task against WriterDocument, locate the target, generate
    and validate a patch set, then apply it to produce a revised WriterDocument.
    """

    __public_apis__ = [
        'build_revise_task', 'locate_revision_target',
        'generate_modify_plan', 'generate_patch_set', 'validate_patch_set',
        'apply_patch',
    ]
