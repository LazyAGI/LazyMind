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


HAN = re.compile(r'[\u3400-\u4dbf\u4e00-\u9fff]')
MARKDOWN_HEADING = re.compile(r'^(#{3,5})\s+(.+?)\s*$')
BOLD_LEAD = re.compile(r'^(\s*)\*\*(.+?)\*\*(.*)$')
TRACE_LINE = re.compile(r'^(\s*(?:[-*]\s*)?追溯\s*[：:]\s*)(.*?)\s*$')
BID_TRACE_ID = re.compile(r'\b(?:(?:BG|FUNC|PERF|SEC|SVC|IMPL)|D)-\d{3}\b')


def _workspace_root() -> Path:
    context = require_context()
    if not context.workspace_path:
        raise RuntimeError('The active Workflow workspace is unavailable.')
    root = Path(context.workspace_path)
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


def bid_writer_save_effective_outline(outline_json: dict[str, Any]) -> str:
    """Persist the approved structured outline for path-based Writer tools."""
    if not isinstance(outline_json, dict) or not isinstance(outline_json.get('chapters'), list):
        raise ValueError('outline_json must contain chapters.')
    return _write_json(_run_root('effective-outline'), 'effective_outline', outline_json)


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
    effective_outline_path: str,
) -> dict[str, Any]:
    """Generate Writer plans and attach the authoritative bid leaf contract."""
    payload = _json_value(WriterCreateToolkit().generate_section_instructions(
        writing_task_json=_read_json(writing_task_path),
        outline_json=_read_text(outline_document_path),
        writing_context_json=_read_json(writing_context_path),
    ), {})
    outline = _json_value(_read_text(effective_outline_path), {})
    chapters = outline.get('chapters') if isinstance(outline, dict) else None
    instructions = payload.get('section_instructions', {}).get('instructions') \
        if isinstance(payload, dict) else None
    if not isinstance(chapters, list) or not isinstance(instructions, list):
        raise ValueError('Bid section planning requires a validated effective outline.')
    by_title = {
        str(chapter.get('title') or '').strip(): chapter
        for chapter in chapters if isinstance(chapter, dict)
    }
    for instruction in instructions:
        if not isinstance(instruction, dict):
            continue
        chapter = by_title.get(str(instruction.get('section_title') or '').strip())
        if not chapter:
            raise ValueError(
                f"Writer section {instruction.get('section_title')!r} is absent from effective_outline."
            )
        leaves = _bid_leaves([chapter])
        contract = [{
            'number': str(leaf.get('number') or ''),
            'title': str(leaf.get('title') or '').strip(),
            'markdown_level': _leaf_markdown_level(leaf),
            'target_words': int(leaf.get('target_words') or 0),
            'bid_requirements_refs': list(leaf.get('bid_requirements_refs') or []),
            'disqualification_refs': list(leaf.get('disqualification_refs') or []),
        } for leaf in leaves if isinstance(leaf, dict) and str(leaf.get('title') or '').strip()]
        if not contract:
            raise ValueError(f"Bid section {instruction.get('section_title')!r} has no leaf contract.")
        heading_lines = '；'.join(
            f"标题行 `{'#' * leaf['markdown_level']} {leaf['title']}` 的正文不少于 "
            f"{leaf['target_words']} 个中文字符"
            for leaf in contract
        )
        required = list(instruction.get('required_points') or [])
        required.insert(0, (
            '结构硬约束：必须按以下顺序逐字输出指定层级的 Markdown 标题，不得改写标题、'
            '添加前后缀、合并叶子或用粗体代替标题：' + heading_lines
        ))
        required.insert(1, (
            '字数硬约束：本章中文字符最低目标为 '
            f"{int(chapter.get('target_words') or sum(item['target_words'] for item in contract))}，"
            '每个叶子标题下的正文不得低于给定目标；达到最低目标后保持简洁，避免重复背景、'
            '泛化说明和边界声明，不得通过重复需求原文凑字数。'
        ))
        styles = list(instruction.get('style_constraints') or [])
        styles.extend([
            '叶子标题必须使用上述层级的独立 Markdown 标题行；不得使用 **粗体** 模拟标题。',
            '每个叶子仅撰写其分配内容与追溯 ID，避免跨叶重复介绍相同背景。',
        ])
        instruction['required_points'] = required
        instruction['style_constraints'] = styles
        meta = dict(instruction.get('meta') or {})
        meta['bid_leaf_contract'] = contract
        meta['bid_chapter_target_words'] = int(chapter.get('target_words') or 0)
        meta['bid_total_word_target'] = int(outline.get('total_word_target') or 0)
        instruction['meta'] = meta
    if len(instructions) != len(chapters):
        raise ValueError(
            f'Writer planned {len(instructions)} top-level sections, but effective_outline '
            f'requires {len(chapters)}.'
        )
    root = _run_root('section-plan')
    return {
        'section_instructions': _write_json(
            root, 'section_instructions', payload.get('section_instructions') or {},
        ),
        'warnings': list(payload.get('warnings') or []),
    }


def _normalized_title(value: str) -> str:
    text = re.sub(r'^\s*\d+(?:\.\d+)*[、.．\s　]+', '', str(value or ''))
    return re.sub(r'[\s\W_]+', '', text, flags=re.UNICODE).lower()


def _bid_leaves(nodes: list[Any]) -> list[dict[str, Any]]:
    leaves: list[dict[str, Any]] = []
    for node in nodes:
        if not isinstance(node, dict):
            continue
        children = node.get('children') if isinstance(node.get('children'), list) else []
        if children:
            leaves.extend(_bid_leaves(children))
        else:
            leaves.append(node)
    return leaves


def _leaf_markdown_level(leaf: dict[str, Any]) -> int:
    return min(5, max(3, int(leaf.get('level') or 2) + 1))


def _title_similarity(expected: str, actual: str) -> float:
    left, right = _normalized_title(expected), _normalized_title(actual)
    if not left or not right:
        return 0.0
    if left == right:
        return 1.0
    if min(len(left), len(right)) >= 4 and (left in right or right in left):
        return 0.95
    left_pairs = {left[index:index + 2] for index in range(max(0, len(left) - 1))}
    right_pairs = {right[index:index + 2] for index in range(max(0, len(right) - 1))}
    if not left_pairs or not right_pairs:
        return 0.0
    return 2 * len(left_pairs & right_pairs) / (len(left_pairs) + len(right_pairs))


def _canonicalize_leaf_headings(markdown: str, leaves: list[dict[str, Any]]) -> str:
    """Normalize obvious Writer heading variants without inventing missing sections."""
    lines = str(markdown or '').splitlines()
    candidates: list[tuple[int, str, str]] = []
    for index, line in enumerate(lines):
        heading = MARKDOWN_HEADING.match(line.strip())
        if heading:
            candidates.append((index, heading.group(2), 'heading'))
            continue
        bold = BOLD_LEAD.match(line)
        if bold:
            candidates.append((index, bold.group(2).rstrip('。；:：'), 'bold'))
    cursor = 0
    for leaf in leaves:
        expected = str(leaf.get('title') or '').strip()
        expected_level = _leaf_markdown_level(leaf)
        best: tuple[float, int, str] | None = None
        for position in range(cursor, len(candidates)):
            line_index, actual, kind = candidates[position]
            score = _title_similarity(expected, actual)
            if score >= 0.58 and (best is None or score > best[0]):
                best = (score, position, kind)
                if score >= 0.95:
                    break
        if best is None:
            continue
        _, position, kind = best
        line_index, _, _ = candidates[position]
        if kind == 'bold':
            match = BOLD_LEAD.match(lines[line_index])
            tail = str(match.group(3) if match else '').strip()
            lines[line_index] = (
                f"{'#' * expected_level} {expected}" + (f'\n\n{tail}' if tail else '')
            )
        else:
            lines[line_index] = f"{'#' * expected_level} {expected}"
        cursor = position + 1
    return '\n'.join(lines).strip()


def _ensure_leaf_trace_refs(markdown: str, leaves: list[dict[str, Any]]) -> str:
    """Complete each leaf's trace line from the authoritative outline mapping."""
    lines = str(markdown or '').splitlines()
    positions: list[int] = []
    cursor = 0
    for leaf in leaves:
        expected = str(leaf.get('title') or '').strip()
        expected_level = _leaf_markdown_level(leaf)
        position = next((
            index for index in range(cursor, len(lines))
            if (match := MARKDOWN_HEADING.match(lines[index].strip()))
            and len(match.group(1)) == expected_level
            and _normalized_title(match.group(2)) == _normalized_title(expected)
        ), -1)
        positions.append(position)
        if position >= 0:
            cursor = position + 1

    # Work backwards so inserting a trace line cannot invalidate earlier boundaries.
    for leaf_index in range(len(leaves) - 1, -1, -1):
        start = positions[leaf_index]
        if start < 0:
            continue
        end = positions[leaf_index + 1] if leaf_index + 1 < len(positions) else len(lines)
        refs = list(dict.fromkeys(
            str(ref).strip()
            for ref in (
                list(leaves[leaf_index].get('bid_requirements_refs') or [])
                + list(leaves[leaf_index].get('disqualification_refs') or [])
            )
            if str(ref).strip()
        ))
        if not refs:
            continue
        trace_index = next((
            index for index in range(start + 1, end)
            if TRACE_LINE.match(lines[index])
        ), -1)
        if trace_index >= 0:
            existing = set(BID_TRACE_ID.findall(lines[trace_index]))
            missing = [ref for ref in refs if ref not in existing]
            if missing:
                base = lines[trace_index].rstrip().rstrip('。；;,，、')
                lines[trace_index] = f"{base}；{'、'.join(missing)}。"
            continue

        insert_at = end
        while insert_at > start + 1 and not lines[insert_at - 1].strip():
            insert_at -= 1
        prefix = [] if insert_at == start + 1 or not lines[insert_at - 1].strip() else ['']
        lines[insert_at:insert_at] = prefix + [f"追溯：{'、'.join(refs)}。", '']

    return '\n'.join(lines).strip()


def _enforce_draft_contract(sections: list[str], effective_outline: dict[str, Any]) -> list[str]:
    chapters = effective_outline.get('chapters') if isinstance(effective_outline, dict) else None
    if not isinstance(chapters, list) or len(chapters) != len(sections):
        raise ValueError('Writer draft sections do not match the effective outline chapter count.')
    normalized: list[str] = []
    missing: list[str] = []
    actual_total = 0
    target_total = max(1, int(effective_outline.get('total_word_target') or 0))
    for chapter, section in zip(chapters, sections):
        leaves = _bid_leaves([chapter])
        value = _canonicalize_leaf_headings(str(section), leaves)
        value = _ensure_leaf_trace_refs(value, leaves)
        headings = {
            (len(match.group(1)), _normalized_title(match.group(2)))
            for line in value.splitlines()
            if (match := re.match(r'^(#{3,5})\s+(.+?)\s*$', line.strip()))
        }
        for leaf in leaves:
            title = str(leaf.get('title') or '').strip()
            if (_leaf_markdown_level(leaf), _normalized_title(title)) not in headings:
                missing.append(f"{leaf.get('number')} {title}")
        actual_total += len(HAN.findall(value))
        normalized.append(value)
    if missing:
        raise ValueError(
            'Writer draft violated the authoritative leaf-heading contract: ' + '、'.join(missing)
        )
    if actual_total < target_total:
        shortfall = target_total - actual_total
        raise ValueError(
            f'Writer draft Chinese-character count {actual_total} is below the minimum '
            f'{target_total} by {shortfall}; regenerate with the injected minimum budgets.'
        )
    return normalized


def bid_writer_write_sections(
    writing_task_path: str,
    section_instructions_path: str,
    writing_context_path: str,
    effective_outline_path: str,
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
        effective_outline = _json_value(_read_text(effective_outline_path), {})
        sections = _enforce_draft_contract(
            [str(section) for section in sections], effective_outline,
        )
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
