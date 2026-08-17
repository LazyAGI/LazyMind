"""Thin adapters from the bid workflow to LazyMind's shared Writer capability.

Only bid-specific orchestration and file persistence live here. Outline planning,
section planning, drafting, revision, and selection rewriting are delegated to the
Writer toolkits shipped by LazyMind.
"""

from __future__ import annotations

import json
import re
import uuid
from pathlib import Path
from typing import Any, Mapping

from lazyllm import AutoModel
from lazyllm.tools.writer.data_models import StringReplaceSet
from lazyllm.tools.writer.tools import WriterRevisionTools
from lazymind.chat.engine.subagent.context import require_context
from lazymind.chat.engine.tools.writer import (
    DraftMarkdownStreamEventEmitter,
    WriterCreateToolkit,
    WriterRevisionToolkit,
)


def _workspace_root() -> Path:
    context = require_context()
    root = Path(context.workspace_path) if context.workspace_path else Path('/tmp')
    root.mkdir(parents=True, exist_ok=True)
    return root


def _run_root(name: str) -> Path:
    root = _workspace_root() / 'bid-writer' / f'{name}-{uuid.uuid4().hex}'
    root.mkdir(parents=True, exist_ok=True)
    return root


def _read_text(path: str) -> str:
    source = Path(str(path or '')).expanduser().resolve()
    if not source.is_file():
        raise FileNotFoundError(f'Writer artifact does not exist: {path}')
    return source.read_text(encoding='utf-8')


def _json_value(value: str, default: Any = None) -> Any:
    text = str(value or '').strip()
    if not text:
        return default
    parsed = json.loads(text)
    if isinstance(parsed, dict) and 'data' in parsed:
        return parsed['data']
    return parsed


def _read_json(path: str) -> str:
    return json.dumps(_json_value(_read_text(path), {}), ensure_ascii=False)


def _write_json(root: Path, name: str, value: str | Mapping[str, Any] | list[Any]) -> str:
    payload = _json_value(value, {}) if isinstance(value, str) else value
    path = root / f'{name}.json'
    path.write_text(json.dumps(payload, ensure_ascii=False, indent=2), encoding='utf-8')
    return str(path)


def _write_markdown(root: Path, name: str, value: str) -> str:
    text = str(value or '').strip()
    if not text:
        raise ValueError(f'{name} Markdown must not be empty.')
    path = root / f'{name}.md'
    path.write_text(text + '\n', encoding='utf-8')
    return str(path)


def _preserve_bid_knowledge_context(writing_context_json: str, knowledge_text: str) -> str:
    """Keep selected-KB evidence intact after the shared Writer's compact profiling."""
    writing_context = _json_value(writing_context_json, {})
    knowledge = str(knowledge_text or '').strip()
    if not knowledge:
        return json.dumps(writing_context, ensure_ascii=False)

    facts = list(writing_context.get('facts') or [])
    facts = [
        fact for fact in facts
        if not isinstance(fact, dict)
        or fact.get('fact_id') != 'bid-selected-knowledge-evidence'
    ]
    facts.append({
        'fact_id': 'bid-selected-knowledge-evidence',
        'key': 'selected_knowledge_base_evidence',
        'value': knowledge,
        'source': ['knowledge_base_evidence'],
        'applies_to': [],
        'locked': True,
    })
    writing_context['facts'] = facts
    writing_context.setdefault('meta', {})['bid_knowledge_evidence_preserved'] = True
    return json.dumps(writing_context, ensure_ascii=False)


def bid_writer_prepare_context(
    user_request: str,
    requirements_markdown: str,
    disqualification_markdown: str,
    word_target: str,
    knowledge_text: str = '',
) -> dict[str, str]:
    """Create Writer task, resource profile, and context for one bid proposal."""
    context = require_context()
    toolkit = WriterCreateToolkit()
    target = re.sub(r'[^0-9]', '', str(word_target or '')) or str(word_target or '')
    query = f"""{str(user_request or '').strip()}

编写中文投标技术方案，目标约 {target} 个中文字符。大纲不超过四级，标题使用简短中文名词短语。
正文采用解决方案专家口吻，逐项覆盖资料中的技术要求与废标项 ID；所有数字、标准和承诺必须可追溯，
不得虚构。每个章节末尾保留“追溯：ID...”行。如果上下文包含指定知识库检索资料，
必须在相关章节实际吸收其中与本项目相关的产品能力和方案做法，不得只写成泛化摘要；
标记为模拟数据、参考指标或非官方承诺的内容不得转化为本项目承诺。""".strip()
    task = _json_value(toolkit.build_writing_task(
        query=query,
        task_id=str(context.params.get('session_id') or uuid.uuid4().hex),
    ), {})
    task['output'] = {**dict(task.get('output') or {}), 'representation': 'markdown'}
    task_json = json.dumps(task, ensure_ascii=False)
    evidence = (
        '# 招标技术要求\n\n' + str(requirements_markdown or '').strip()
        + '\n\n# 废标与高风险条款\n\n' + str(disqualification_markdown or '').strip()
    )
    if str(knowledge_text or '').strip():
        evidence += '\n\n# 指定知识库检索资料\n\n' + str(knowledge_text).strip()
    resources_json = toolkit.build_resources(knowledge_text=evidence)
    profiles_json = toolkit.profile_resources(
        writing_task_json=task_json,
        user_input=query,
        resources_json=resources_json,
    )
    writing_context_json = toolkit.create_writing_context(
        writing_task_json=task_json,
        resource_profiles_json=profiles_json,
    )
    # The shared Writer intentionally condenses resources into a compact set of facts.
    # For bid writing that can erase the concrete, selected-KB product facts before the
    # section planner sees them. Keep the original retrieved evidence as one locked,
    # workflow-specific fact while retaining the rest of the shared Writer context.
    writing_context_json = _preserve_bid_knowledge_context(
        writing_context_json, knowledge_text,
    )
    root = _run_root('prepare')
    return {
        'writing_task': _write_json(root, 'writing_task', task_json),
        'resource_profiles': _write_json(root, 'resource_profiles', profiles_json),
        'writing_context': _write_json(root, 'writing_context', writing_context_json),
    }


def bid_writer_generate_outline(writing_task_path: str, writing_context_path: str) -> str:
    """Generate a Markdown outline with LazyMind's shared Writer planner."""
    generated = WriterCreateToolkit().generate_outline(
        writing_task_json=_read_json(writing_task_path),
        writing_context_json=_read_json(writing_context_path),
    )
    try:
        parsed = _json_value(generated, None)
    except json.JSONDecodeError:
        parsed = generated
    if not isinstance(parsed, str):
        raise TypeError('The bid workflow requires the shared Writer planner to return Markdown.')
    return _write_markdown(_run_root('outline-seed'), 'outline_seed', parsed)


def bid_writer_save_validated_outline(outline_json: str) -> str:
    """Render the validated bid outline as an editable Writer Markdown artifact."""
    outline = _json_value(outline_json, {})
    if not isinstance(outline, dict) or not isinstance(outline.get('chapters'), list):
        raise ValueError('outline_json must contain chapters.')
    document_title = str(
        outline.get('project_full_name') or outline.get('project_name') or '投标技术方案'
    ).strip()
    lines: list[str] = [f'# {document_title}', '']

    def render(nodes: list[Any], level: int = 1) -> None:
        for node in nodes:
            if not isinstance(node, dict):
                continue
            title = str(node.get('title') or '').strip()
            if not title:
                continue
            # Shared Writer Markdown reserves H1 for the single document title.
            # Bid chapter levels 1..4 therefore map to Markdown H2..H5.
            lines.extend([f"{'#' * min(level + 1, 5)} {title}"])
            children = node.get('children') if isinstance(node.get('children'), list) else []
            if children:
                lines.append('')
                render(children, level + 1)
            else:
                # Keep the user-facing Writer document as ordinary Markdown. MDX-based
                # editors do not accept HTML comments reliably, and the authoritative
                # trace/word metadata already remains in the structured outline slot.
                lines.append('')
    render(outline['chapters'])
    return _write_markdown(_run_root('validated-outline'), 'outline_document', '\n'.join(lines))


def bid_writer_read_markdown(document_path: str) -> str:
    """Read an editable Markdown artifact selected by the Workflow runtime."""
    path = Path(str(document_path or ''))
    if path.suffix.lower() not in {'.md', '.markdown', '.txt'}:
        raise ValueError('The bid Writer bridge accepts Markdown artifacts only.')
    return _read_text(str(path))


def bid_writer_update_context(content_path: str, writing_context_path: str) -> str:
    """Update Writer context from the latest selected outline or draft."""
    updated = WriterCreateToolkit().update_writing_context(
        content_artifact_json=_read_text(content_path),
        writing_context_json=_read_json(writing_context_path),
    )
    return _write_json(_run_root('update-context'), 'writing_context', updated)


def bid_writer_plan_sections(
    writing_task_path: str,
    outline_document_path: str,
    writing_context_path: str,
) -> dict[str, Any]:
    """Generate section instructions with LazyMind's shared Writer planner."""
    payload = _json_value(WriterCreateToolkit().generate_section_instructions(
        writing_task_json=_read_json(writing_task_path),
        outline_json=_read_text(outline_document_path),
        writing_context_json=_read_json(writing_context_path),
    ), {})
    root = _run_root('section-plan')
    return {
        'section_instructions': _write_json(
            root, 'section_instructions', payload.get('section_instructions') or {},
        ),
        'warnings': list(payload.get('warnings') or []),
    }


def bid_writer_write_sections(
    writing_task_path: str,
    section_instructions_path: str,
    writing_context_path: str,
) -> list[str]:
    """Stream and persist one Markdown artifact per planned proposal section."""
    events = DraftMarkdownStreamEventEmitter(require_context().emit)
    try:
        sections = _json_value(WriterCreateToolkit().stream_draft_blocks_markdown(
            writing_task_json=_read_json(writing_task_path),
            section_instructions_json=_read_json(section_instructions_path),
            writing_context_json=_read_json(writing_context_path),
            on_delta=events.feed,
            on_section_end=events.flush,
        ), [])
        if not isinstance(sections, list) or not sections:
            raise ValueError('Shared Writer returned no draft sections.')
        root = _run_root('draft-sections')
        paths = [
            _write_markdown(root, f'draft_section_{index:04d}', str(section))
            for index, section in enumerate(sections, start=1)
        ]
    except Exception as exc:
        events.abort(str(exc))
        raise
    events.end()
    return paths


def bid_writer_assemble_draft(
    draft_sections_anchor_path: str,
    writing_context_path: str,
    outline_document_path: str,
    document_title: str = '',
) -> str:
    """Assemble Writer-generated sections into one editable Markdown draft."""
    anchor = Path(str(draft_sections_anchor_path or '')).resolve()
    directory = anchor if anchor.is_dir() else anchor.parent
    paths = sorted(directory.glob('draft_section_*.md'))
    if not paths:
        raise ValueError('No Writer draft section files were found.')
    sections = [_read_text(str(path)) for path in paths]
    payload = _json_value(WriterCreateToolkit().generate_draft_document_markdown(
        draft_sections_json=json.dumps(sections, ensure_ascii=False),
        writing_context_json=_read_json(writing_context_path),
        outline_json=_read_text(outline_document_path),
        title=str(document_title or '').strip(),
    ), {})
    markdown = str(payload.get('draft_document') or '').strip()
    return _write_markdown(_run_root('draft-document'), 'draft_document', markdown)


def bid_writer_revise_markdown(
    base_document_path: str,
    writing_context_path: str,
    instruction: str,
    document_slot: str,
) -> dict[str, str]:
    """Apply LazyMind's structured Markdown revision pipeline to a bid artifact."""
    if document_slot not in {'outline_document', 'draft_document'}:
        raise ValueError('document_slot must be outline_document or draft_document.')
    toolkit = WriterRevisionToolkit()
    document = _read_text(base_document_path)
    context_json = _read_json(writing_context_path)
    revision_task = toolkit.build_revision_task(
        query=str(instruction or '').strip(),
        writer_document_json=document,
        allow_outline=True,
    )
    locate = toolkit.locate_revision_target(
        writing_task_json=revision_task,
        writer_document_json=document,
        writing_context_json=context_json,
    )
    plan = toolkit.generate_modify_plan(
        writing_task_json=revision_task,
        writer_document_json=document,
        locate_result_json=locate,
        writing_context_json=context_json,
    )
    replace_set = toolkit.generate_string_replace_set(
        markdown_document=document,
        modify_plan_json=plan,
        writing_context_json=context_json,
    )
    applied = _json_value(toolkit.apply_string_replace(
        markdown_document=document,
        string_replace_set_json=replace_set,
        writing_context_json=context_json,
    ), {})
    revised = str(applied.get('revised_document') or '').strip()
    if not revised:
        raise ValueError('Shared Writer revision returned no revised document.')
    root = _run_root(f'revise-{document_slot}')
    return {
        'revision_task': _write_json(root, 'revision_task', revision_task),
        'locate_result': _write_json(root, 'locate_result', locate),
        'modify_plan': _write_json(root, 'modify_plan', plan),
        'revision_set': _write_json(root, 'revision_set', replace_set),
        'revision_result': _write_json(
            root, 'revision_result', applied.get('string_replace_result') or {},
        ),
        document_slot: _write_markdown(root, document_slot, revised),
    }


def bid_writer_preview_selection_rewrite(
    artifact: Any,
    instruction: str,
    selection: Mapping[str, Any],
    artifact_store: str = '',
    slot: str = '',
) -> dict[str, Any]:
    """Preview a Writer-powered rewrite of one selected Markdown paragraph."""
    if slot not in {'outline_document', 'draft_document'}:
        raise ValueError('Selection rewrite is supported only for Writer document slots.')
    if str((selection or {}).get('type') or '') != 'markdown':
        raise ValueError("The bid workflow requires selection.type='markdown'.")
    instruction = str(instruction or '').strip()
    if not instruction:
        raise ValueError('instruction must not be empty.')
    if isinstance(artifact, Mapping):
        if isinstance(artifact.get('data'), str):
            document = str(artifact['data'])
        elif artifact.get('path'):
            document = _read_text(str(artifact['path']))
        else:
            raise ValueError('Markdown artifact path is missing.')
    elif isinstance(artifact, str) and Path(artifact).is_file():
        document = _read_text(artifact)
    else:
        document = str(artifact or '')
    root = Path(artifact_store) if artifact_store else _run_root('selection-preview')
    root = root / 'bid-writer-selection' / uuid.uuid4().hex
    root.mkdir(parents=True, exist_ok=True)
    context = {'context_id': f'bid-selection-{uuid.uuid4().hex}', 'meta': {'slot': slot}}
    revision = WriterRevisionTools(llm=AutoModel(model='llm'), artifact_store=str(root))
    replace_set = StringReplaceSet.model_validate(
        revision.build_selected_markdown_replace_set(
            document,
            instruction,
            str(selection.get('selected_text') or ''),
            context,
        ),
    )
    replacement = replace_set.replacements[0]
    output = revision.apply_string_replace(document, replace_set, context)
    candidate = Path(str(output['revised_document_md']))
    canonical = root / f'{slot}.md'
    if candidate.resolve() != canonical.resolve():
        canonical.write_bytes(candidate.read_bytes())
    return {
        'representation': 'markdown',
        'target': {'type': 'block', 'block_type': 'paragraph'},
        'preview': {
            'old_text': replacement.old_string,
            'new_text': replacement.new_string,
        },
        'patch': {'type': 'string_replace_set', 'payload': replace_set.model_dump()},
        'artifact': {
            'content_type': 'file',
            'value': {
                'path': str(canonical),
                'filename': canonical.name,
                'size': canonical.stat().st_size,
            },
        },
    }
