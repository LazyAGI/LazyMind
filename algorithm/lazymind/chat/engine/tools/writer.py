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
    WritingTask,
)
from lazyllm.tools.writer.tools import (
    WriterContextTools,
    WriterDraftingTools,
    WriterPlanningTools,
    WriterQualityTools,
    WriterResourceTools,
)
from lazyllm.tools.writer.utils import save_artifact_json


WRITER_DATA_MODEL_SCHEMA_PREFIX = 'lazyllm.tools.writer.data_models'
WRITER_ARTIFACT_SCHEMA_PREFIX = 'lazyllm.tools.writer.artifacts'


def writer_schema(name: str) -> str:
    return f'{WRITER_DATA_MODEL_SCHEMA_PREFIX}.{name}'


def writer_artifact_schema(name: str) -> str:
    return f'{WRITER_ARTIFACT_SCHEMA_PREFIX}.{name}'


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
        created_by='WriterToolGroup',
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


def _infer_content_schema(data: Any) -> str:
    if isinstance(data, dict):
        if 'outline_id' in data and 'nodes' in data:
            return writer_schema('writing.WritingOutline')
        if 'draft_id' in data and 'sections' in data:
            return writer_schema('writing.DraftDocument')
        if 'section_id' in data and 'blocks' in data:
            return writer_schema('writing.DraftSection')
        if 'output_id' in data and 'content' in data:
            return writer_schema('writing.WritingOutput')
        if 'target' in data and 'result' in data:
            return writer_schema('quality.ReviewReport')
    return writer_artifact_schema('content')


class WriterToolGroup:
    """Common writer tools.

    All public methods accept text or JSON strings and return text or JSON strings.
    File paths are an internal implementation detail used only to call the
    underlying lazyllm writer tools.
    """

    __public_apis__ = [
        'build_writing_task',
        'profile_resources',
        'create_writing_context',
        'generate_outline',
        'generate_section_instructions',
        'generate_draft_section',
        'assemble_draft_document',
        'update_writing_context',
        'check_consistency',
        'generate_writing_output',
    ]

    def __key_source__(self) -> bool:
        return True

    def build_writing_task(self, query: str) -> str:
        """Build a writing task from the user's original request.

        Args:
            query: The user's writing request.

        Returns:
            WritingTask as a JSON string.
        """
        task = WritingTask(query=query, task_type='write')
        return _json_dumps(task.model_dump())

    def profile_resources(self, writing_task_json: str, user_input: str, resources_json: str = '[]') -> str:
        """Profile writing resources.

        Args:
            writing_task_json: WritingTask JSON string.
            user_input: Original user request, used to discover Feishu links.
            resources_json: Optional JSON array of InputResource objects.

        Returns:
            ResourceProfile list as a JSON string.
        """
        root = _temp_root()
        task_data = _json_loads(writing_task_json, {})
        resources = _json_loads(resources_json, [])
        if resources is None:
            resources = []
        if not isinstance(resources, list):
            raise TypeError('resources_json must be a JSON array.')
        resources = resources + _extract_feishu_resources(user_input)

        task_path = _write_input_artifact(root, 'writing_task.json', task_data, writer_schema('task.WritingTask'))
        input_resources = [InputResource.model_validate(item) for item in resources]
        result = WriterResourceTools(
            llm=AutoModel(model='llm'),
            artifact_store=str(root),
        ).profile_resources(task=task_path, input_resources=input_resources)
        return _json_dumps(_primary_data(result))

    def create_writing_context(self, writing_task_json: str, resource_profiles_json: str) -> str:
        """Create writing context from task and resource profile JSON strings."""
        root = _temp_root()
        task_path = _write_input_artifact(
            root, 'writing_task.json', _json_loads(writing_task_json, {}), writer_schema('task.WritingTask'),
        )
        profiles_path = _write_input_artifact(
            root, 'resource_profiles.json', _json_loads(resource_profiles_json, []),
            writer_schema('resource.ResourceProfile'),
        )
        result = WriterContextTools(
            llm=None,
            artifact_store=str(root),
        ).create_writing_context(task=task_path, resource_profiles=profiles_path)
        return _json_dumps(_primary_data(result))

    def generate_outline(self, writing_task_json: str, writing_context_json: str) -> str:
        """Generate a writing outline as JSON."""
        root = _temp_root()
        task_path = _write_input_artifact(
            root, 'writing_task.json', _json_loads(writing_task_json, {}), writer_schema('task.WritingTask'),
        )
        context_path = _write_input_artifact(
            root, 'writing_context.json', _json_loads(writing_context_json, {}),
            writer_schema('context.WritingContext'),
        )
        result = WriterPlanningTools(
            llm=AutoModel(model='llm'),
            artifact_store=str(root),
        ).generate_outline(task=task_path, context=context_path)
        return _json_dumps(_primary_data(result))

    def generate_section_instructions(
        self,
        outline_json: str,
        writing_context_json: str,
        review_report_json: str = '',
    ) -> str:
        """Generate section instructions as JSON."""
        root = _temp_root()
        outline_path = _write_input_artifact(
            root, 'outline.json', _json_loads(outline_json, {}), writer_schema('writing.WritingOutline'),
        )
        context_path = _write_input_artifact(
            root, 'writing_context.json', _json_loads(writing_context_json, {}),
            writer_schema('context.WritingContext'),
        )
        execution_results = _json_loads(review_report_json, None) if review_report_json else None
        result = WriterPlanningTools(
            llm=AutoModel(model='llm'),
            artifact_store=str(root),
        ).generate_section_instructions(
            outline=outline_path,
            context=context_path,
            execution_results=execution_results,
        )
        return _json_dumps(_primary_data(result))

    def generate_draft_section(
        self,
        writing_task_json: str,
        section_instruction_json: str,
        writing_context_json: str,
        previous_sections_json: str = '[]',
    ) -> str:
        """Generate one draft section as JSON."""
        root = _temp_root()
        task_path = _write_input_artifact(
            root, 'writing_task.json', _json_loads(writing_task_json, {}), writer_schema('task.WritingTask'),
        )
        context_path = _write_input_artifact(
            root, 'writing_context.json', _json_loads(writing_context_json, {}),
            writer_schema('context.WritingContext'),
        )
        instruction_data = _json_loads(section_instruction_json, {})
        instruction = SectionInstruction.model_validate(instruction_data)
        previous_sections = _json_loads(previous_sections_json, [])
        result = WriterDraftingTools(
            llm=AutoModel(model='llm'),
            artifact_store=str(root),
        ).generate_draft_section(
            task=task_path,
            section_instruction=instruction,
            context=context_path,
            previous_sections=previous_sections,
        )
        return _json_dumps(_primary_data(result))

    def assemble_draft_document(
        self,
        draft_sections_json: str,
        writing_context_json: str,
        outline_json: str = '',
    ) -> str:
        """Assemble draft sections into a draft document JSON string."""
        root = _temp_root()
        sections_data = _json_loads(draft_sections_json, [])
        if not isinstance(sections_data, list) or not sections_data:
            raise ValueError('draft_sections_json must be a non-empty JSON array.')
        section_paths = []
        sections_dir = root / 'draft_sections'
        sections_dir.mkdir(parents=True, exist_ok=True)
        for idx, section in enumerate(sections_data, start=1):
            section_paths.append(_write_input_artifact(
                sections_dir, f'draft_section_{idx}.json', section, writer_schema('writing.DraftSection'),
            ))
        context_path = _write_input_artifact(
            root, 'writing_context.json', _json_loads(writing_context_json, {}),
            writer_schema('context.WritingContext'),
        )
        outline_path = None
        if outline_json:
            outline_path = _write_input_artifact(
                root, 'outline.json', _json_loads(outline_json, {}), writer_schema('writing.WritingOutline'),
            )
        result = WriterDraftingTools(
            llm=None,
            artifact_store=str(root),
        ).generate_draft_document(
            draft_sections=section_paths,
            context=context_path,
            outline=outline_path,
        )
        return _json_dumps(_primary_data(result))

    def update_writing_context(self, content_artifact_json: str, writing_context_json: str) -> str:
        """Update writing context from a content artifact JSON string."""
        root = _temp_root()
        content_data = _json_loads(content_artifact_json, {})
        content_path = _write_input_artifact(
            root, 'content_artifact.json', content_data, _infer_content_schema(content_data),
        )
        context_path = _write_input_artifact(
            root, 'writing_context.json', _json_loads(writing_context_json, {}),
            writer_schema('context.WritingContext'),
        )
        result = WriterContextTools(
            llm=None,
            artifact_store=str(root),
        ).update_writing_context(artifacts=content_path, context=context_path)
        return _json_dumps(_primary_data(result))

    def check_consistency(self, draft_document_json: str, writing_context_json: str) -> str:
        """Review a draft document.

        Returns:
            JSON string with `review_report` and `review_summary`.
        """
        root = _temp_root()
        draft_path = _write_input_artifact(
            root, 'draft_document.json', _json_loads(draft_document_json, {}),
            writer_schema('writing.DraftDocument'),
        )
        context_path = _write_input_artifact(
            root, 'writing_context.json', _json_loads(writing_context_json, {}),
            writer_schema('context.WritingContext'),
        )
        result = WriterQualityTools(
            llm=AutoModel(model='llm'),
            artifact_store=str(root),
        ).validate_draft_document(
            draft_document=draft_path,
            context=context_path,
        )
        return _json_dumps({
            'review_report': _primary_data(result),
            'review_summary': result.get('summary') or '',
        })

    def generate_writing_output(
        self,
        draft_document_json: str,
        review_report_json: str,
        writing_context_json: str,
    ) -> str:
        """Generate final writing output.

        Returns:
            JSON string with `writing_output` and `writing_output_md`.
        """
        root = _temp_root()
        draft_path = _write_input_artifact(
            root, 'draft_document.json', _json_loads(draft_document_json, {}),
            writer_schema('writing.DraftDocument'),
        )
        _json_loads(review_report_json, {})
        context_path = _write_input_artifact(
            root, 'writing_context.json', _json_loads(writing_context_json, {}),
            writer_schema('context.WritingContext'),
        )
        result = WriterDraftingTools(
            llm=None,
            artifact_store=str(root),
        ).generate_writing_output(
            draft=draft_path,
            context=context_path,
        )
        output_path = result.get('output_file_path') or ''
        markdown = ''
        if output_path:
            with open(output_path, 'r', encoding='utf-8') as fh:
                markdown = fh.read()
        return _json_dumps({
            'writing_output': _primary_data(result),
            'writing_output_md': markdown,
        })
