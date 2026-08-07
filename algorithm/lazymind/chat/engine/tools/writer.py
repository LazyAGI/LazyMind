"""Common writer tools with string/JSON inputs and outputs."""
from __future__ import annotations

import json
import os
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
from lazyllm.tools.writer.utils import render_document_markdown, save_artifact_json


WRITER_DATA_MODEL_SCHEMA_PREFIX = 'lazyllm.tools.writer.data_models'
_FEISHU_URL_RE = re.compile(
    r"https?://[^\s<>\"']*(?:feishu\.(?:cn|com)|larksuite\.com)/"
    r"[^\s<>\"'，。；！？、（）【】《》「」『』]+",
    re.IGNORECASE,
)


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
    return document.model_dump_json(exclude_defaults=True)


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


def _result_data(result: dict, key: str) -> Any:
    path = ((result.get('metadata') or {}).get('artifact_paths') or {}).get(key)
    if not path:
        raise ValueError(f'Writer tool did not return artifact {key!r}: {result!r}')
    return _read_artifact_data(path)


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

    def build_writing_task(self, query: str) -> str:
        """Build a writing task from the user's original request."""
        task = WritingTask(query=query, task_type='write')
        return _json_dumps(task.model_dump(exclude_defaults=True))

    def build_resources(
        self,
        file_paths_json: str = '[]',
        source_document_json: str = '',
        knowledge_text: str = '',
    ) -> str:
        """Build normalized InputResource data from plugin runtime inputs."""
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
            document = WriterDocument.model_validate(
                _json_loads(source_document_json, {}),
            )
            target = _target_from_document(document)
            resources.append({
                'resource_id': 'source_document',
                'resource_type': 'text',
                'inline_text': _document_text(document),
                'title': document.title or None,
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
        """Build a revision task directly from its current WriterDocument."""
        document = WriterDocument.model_validate(
            _json_loads(writer_document_json, {}),
        )
        if document.stage == 'outline' and not allow_outline:
            raise ValueError(
                'A full-document revision cannot use an outline-stage document.',
            )
        target = _target_from_document(document)
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
        return _set_document_editable(
            _primary_data(result), stage='outline',
        ).model_dump_json(exclude_defaults=True)

    def prepare_outline(self, source_document_json: str) -> str:
        """Normalize a supplied document into an editable outline."""
        document = WriterDocument.model_validate(
            _json_loads(source_document_json, {}),
        )
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

    def generate_draft_section_markdown(
        self,
        writing_task_json: str,
        section_instruction_json: str,
        writing_context_json: str,
        previous_markdown: str = '',
    ) -> str:
        """Generate one draft section as Markdown while preserving the IR API."""
        root = _temp_root()
        task_path = _write_input_artifact(
            root, 'writing_task.json', _json_loads(writing_task_json, {}), writer_schema('task.WritingTask'),
        )
        context_path = _write_input_artifact(
            root, 'writing_context.json', _json_loads(writing_context_json, {}),
            writer_schema('context.WritingContext'),
        )
        instruction = SectionInstruction.model_validate(_json_loads(section_instruction_json, {}))
        result = WriterDraftingTools(
            llm=AutoModel(model='llm'), artifact_store=str(root),
        ).generate_draft_section_markdown(
            task=task_path,
            section_instruction=instruction,
            context=context_path,
            previous_markdown=previous_markdown,
        )
        with open(result['artifact_path'], 'r', encoding='utf-8') as fh:
            return fh.read()

    def generate_draft_blocks(
        self,
        writing_task_json: str,
        section_instructions_json: str,
        writing_context_json: str,
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
        instructions_data = _json_loads(section_instructions_json, {})
        instructions = (
            instructions_data.get('instructions')
            if isinstance(instructions_data, dict) else None
        )
        if not isinstance(instructions, list):
            raise TypeError('section_instructions_json must contain instructions.')

        sections: list[str] = []
        for instruction in instructions:
            sections.append(self.generate_draft_section_markdown(
                writing_task_json=writing_task_json,
                section_instruction_json=_json_dumps(instruction),
                writing_context_json=writing_context_json,
                previous_markdown='\n\n'.join(sections),
            ))
        return _json_dumps(sections)

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

    def generate_draft_document_markdown(
        self,
        draft_sections_json: str,
        writing_context_json: str,
        outline_json: str = '',
    ) -> str:
        """Combine Markdown sections and convert the completed draft once to IR."""
        root = _temp_root()
        sections = _json_loads(draft_sections_json, [])
        if not isinstance(sections, list) or not sections:
            raise ValueError('draft_sections_json must be a non-empty JSON array.')
        context_path = _write_input_artifact(
            root, 'writing_context.json', _json_loads(writing_context_json, {}),
            writer_schema('context.WritingContext'),
        )
        outline_path = None
        if outline_json:
            outline_path = _write_input_artifact(
                root, 'outline.lmd', _json_loads(outline_json, {}), self.WRITER_IR_SCHEMA,
            )
        result = WriterDraftingTools(
            llm=None, artifact_store=str(root),
        ).generate_draft_document_markdown(
            draft_sections=sections,
            context=context_path,
            outline=outline_path,
        )
        with open(result['draft_document_md'], 'r', encoding='utf-8') as fh:
            markdown = fh.read()
        return _json_dumps({
            'draft_document': _primary_data(result),
            'draft_document_md': markdown,
        })

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
        final_document = _set_document_editable(
            _primary_data(result),
            stage='final',
        )
        return _json_dumps({
            'final_document': final_document.model_dump(exclude_defaults=True),
            'final_document_md': markdown,
        })

    def render_markdown(self, writer_document_json: str) -> str:
        """Return the current WriterDocument title and rendered Markdown."""
        document = WriterDocument.model_validate(
            _json_loads(writer_document_json, {}),
        )
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

    def plan_revision(
        self,
        writing_task_json: str,
        writer_document_json: str,
        writing_context_json: str,
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

    def apply_revision(
        self,
        writer_document_json: str,
        patch_set_json: str,
        writing_context_json: str,
        sync_provider: bool = False,
        allow_outline: bool = True,
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
        ), {})
        output['revised_document'] = published.get('published_document') or {}
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
        )
        persisted = _set_document_editable(
            _result_data(result, 'persisted_document'),
            stage=source.stage,
        )
        return _json_dumps({
            'publish_result': _primary_data(result),
            'published_document': persisted.model_dump(exclude_defaults=True),
            'published_link': _published_link(target),
        })

    def replace_document(
        self,
        content_json: str,
        source_document_json: str,
        target_document_json: str = '',
        target_uri: str = '',
    ) -> str:
        """Replace a provider document with the selected WriterDocument."""
        return self._write_document(
            mode='replace',
            content_json=content_json,
            source_document_json=source_document_json,
            target_document_json=target_document_json,
            target_uri=target_uri,
        )

    def append_document(
        self,
        content_json: str,
        target_document_json: str = '',
        target_uri: str = '',
        publish_outline: bool = False,
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
        )

    def _write_document(
        self,
        *,
        mode: str,
        content_json: str,
        source_document_json: str = '',
        target_document_json: str = '',
        target_uri: str = '',
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
        write_result = (
            resource.replace_document(publish_document, target)
            if mode == 'replace'
            else resource.append_to_document(publish_document, target)
        )
        refreshed = resource.document_to_docir(TargetDocument(
            **target.model_dump(exclude={'meta'}),
            meta={**target.meta, 'stage': 'final'},
        ))
        published = _set_document_editable(_primary_data(refreshed), stage='final')
        return _json_dumps({
            'publish_result': _primary_data(write_result),
            'published_document': published.model_dump(exclude_defaults=True),
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
        'generate_modify_plan', 'generate_patch_set', 'plan_revision',
        'validate_patch_set', 'apply_patch', 'apply_revision',
    ]


class WriterResourceToolkit(WriterToolkitBase):
    """Load and persist WriterDocuments through provider-neutral resource tools."""

    __public_apis__ = [
        'load_document', 'create_document', 'publish_revision',
        'replace_document', 'append_document',
    ]
