"""Academic workflow adapters around LazyMind's shared Writer capability."""

from __future__ import annotations

import hashlib
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
)


HAN = re.compile(r'[\u3400-\u4dbf\u4e00-\u9fff]')
WORD = re.compile(r"\b[\w'-]+\b", re.UNICODE)
MARKDOWN_HEADING = re.compile(r'^(#{1,6})\s+(.+?)\s*$')
BOLD_LEAD = re.compile(r'^(\s*)\*\*(.+?)\*\*(.*)$')
EVIDENCE_ID = re.compile(r'\b(?:SRC|KB)-\d{3}\b')
EDITABLE_SLOTS = {
    'outline_document', 'draft_document', 'revised_document', 'second_revised_document',
}


def _workspace_root() -> Path:
    context = require_context()
    if not context.workspace_path:
        raise RuntimeError('The active Workflow workspace is unavailable.')
    root = Path(context.workspace_path)
    root.mkdir(parents=True, exist_ok=True)
    return root


def _run_root(name: str) -> Path:
    root = _workspace_root() / 'academic-writer' / f'{name}-{uuid.uuid4().hex}'
    root.mkdir(parents=True, exist_ok=True)
    return root


def _read_text(path: str) -> str:
    source = Path(str(path or '')).expanduser().resolve()
    if not source.is_file():
        raise FileNotFoundError(f'Writer artifact does not exist: {path}')
    return source.read_text(encoding='utf-8')


def _json_value(value: Any, default: Any = None) -> Any:
    current = value
    for _ in range(5):
        if isinstance(current, dict):
            nested = next((
                current[key] for key in ('data', 'text') if key in current
            ), current)
            if nested is current:
                return current
            current = nested
            continue
        if isinstance(current, list):
            return current
        text = str(current or '').strip()
        if not text:
            return default
        try:
            current = json.loads(text)
        except json.JSONDecodeError:
            return current
    return current


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


def _preserve_research_evidence(writing_context_json: str, evidence: str) -> str:
    context = _json_value(writing_context_json, {})
    if not isinstance(context, dict):
        context = {}
    facts = [
        fact for fact in list(context.get('facts') or [])
        if not isinstance(fact, dict) or fact.get('fact_id') != 'academic-registered-evidence'
    ]
    facts.append({
        'fact_id': 'academic-registered-evidence',
        'key': 'registered_research_evidence',
        'value': str(evidence or '').strip(),
        'source': ['academic_search', 'selected_knowledge_bases'],
        'applies_to': [],
        'locked': True,
    })
    context['facts'] = facts
    context.setdefault('meta', {})['academic_registered_evidence_preserved'] = True
    return json.dumps(context, ensure_ascii=False)


def academic_writer_prepare_context(
    user_request: str,
    generation_parameters_json: str,
    research_question_brief: str,
    methodology_blueprint: str,
    annotated_bibliography: str,
    research_synthesis: str,
    literature_evidence: str,
) -> dict[str, str]:
    """Create the shared Writer task and retain the complete registered evidence corpus."""
    parameters = _json_value(generation_parameters_json, {})
    if not isinstance(parameters, dict):
        parameters = {}
    target = int(parameters.get('word_target') or 0)
    language = str(parameters.get('paper_language') or 'zh-CN')
    style = str(parameters.get('citation_style') or 'APA 7')
    query = f"""{str(user_request or '').strip()}

撰写一篇完整学术论文。主题：{parameters.get('research_topic')}；类型：{parameters.get('paper_type')}；
目标篇幅：{target}；正文语言：{language}；引用格式：{style}。
必须依据研究问题、方法蓝图、已注册证据和综合报告写作。正文中的外部事实和学术主张只能引用
literature_evidence 中真实存在的 SRC-NNN/KB-NNN，并在正常学术引用后保留对应证据编号以便审计。
不存在检索证据时，必须明确研究边界，不得生成看似真实的作者、题名、期刊、年份、DOI 或 URL。
初次生成大纲时应根据论文类型建议摘要、引言、方法、分析/结果、讨论、结论、局限、参考文献及适用的完整性声明。
用户在大纲审批节点修改后，最新批准的 Markdown 标题层级是唯一正文结构；后续写作不得静默补回、删除或重排标题。
若批准大纲保留中英文摘要，则应独立撰写而非机械翻译。""".strip()
    toolkit = WriterCreateToolkit()
    task = _json_value(toolkit.build_writing_task(
        query=query,
        task_id=str(require_context().params.get('session_id') or uuid.uuid4().hex),
    ), {})
    if not isinstance(task, dict):
        task = {}
    task['output'] = {**dict(task.get('output') or {}), 'representation': 'markdown'}
    task['academic_parameters'] = parameters
    task_json = json.dumps(task, ensure_ascii=False)
    evidence = '\n\n'.join([
        '# 研究问题简报\n' + str(research_question_brief or '').strip(),
        '# 方法论蓝图\n' + str(methodology_blueprint or '').strip(),
        '# 已注册检索证据\n' + str(literature_evidence or '').strip(),
        '# 注释书目\n' + str(annotated_bibliography or '').strip(),
        '# 证据综合\n' + str(research_synthesis or '').strip(),
    ])
    resources = toolkit.build_resources(knowledge_text=evidence)
    profiles = toolkit.profile_resources(
        writing_task_json=task_json,
        user_input=query,
        resources_json=resources,
    )
    writing_context = toolkit.create_writing_context(
        writing_task_json=task_json,
        resource_profiles_json=profiles,
    )
    writing_context = _preserve_research_evidence(writing_context, evidence)
    root = _run_root('prepare')
    return {
        'writing_task': _write_json(root, 'writing_task', task_json),
        'resource_profiles': _write_json(root, 'resource_profiles', profiles),
        'writing_context': _write_json(root, 'writing_context', writing_context),
    }


def academic_writer_generate_outline(writing_task_path: str, writing_context_path: str) -> str:
    generated = WriterCreateToolkit().generate_outline(
        writing_task_json=_read_json(writing_task_path),
        writing_context_json=_read_json(writing_context_path),
    )
    try:
        parsed = _json_value(generated, None)
    except json.JSONDecodeError:
        parsed = generated
    if isinstance(parsed, dict):
        for key in ('outline_document', 'outline', 'markdown', 'text', 'content'):
            if isinstance(parsed.get(key), str) and parsed[key].strip():
                parsed = parsed[key]
                break
        else:
            if isinstance(parsed.get('chapters'), list):
                parsed = _render_outline_markdown(parsed)
    if isinstance(parsed, list):
        titles = [str(item).strip() for item in parsed if str(item).strip()]
        parsed = '\n\n'.join(
            [f'# {titles[0]}', *[f'## {title}' for title in titles[1:]]]
        ) if len(titles) >= 2 else ''
    if not isinstance(parsed, str) or not parsed.strip():
        raise ValueError('Shared Writer returned no usable outline content.')
    return _write_markdown(_run_root('outline-seed'), 'outline_seed', parsed)


def _render_outline_markdown(outline: dict[str, Any]) -> str:
    if not isinstance(outline, dict) or not isinstance(outline.get('chapters'), list):
        raise ValueError('Academic outline must contain chapters.')
    title = str(outline.get('paper_title') or outline.get('research_topic') or 'Academic Paper').strip()
    lines = [f'# {title}', '']

    def render(nodes: list[Any], level: int = 1) -> None:
        for node in nodes:
            if not isinstance(node, dict):
                continue
            node_title = str(node.get('title') or '').strip()
            if not node_title:
                continue
            lines.extend([f"{'#' * min(level + 1, 5)} {node_title}", ''])
            children = node.get('children') if isinstance(node.get('children'), list) else []
            if children:
                render(children, level + 1)

    render(outline['chapters'])
    return '\n'.join(lines)


def _markdown_heading_signature(
    markdown: str, fallback_title: str = '',
) -> list[tuple[int, str]]:
    """Return a repaired H1–H5 structure from imperfect editable Markdown."""
    rows: list[tuple[int, str]] = []
    for line in str(markdown or '').splitlines():
        match = MARKDOWN_HEADING.match(line.strip())
        if match and match.group(2).strip():
            rows.append((len(match.group(1)), match.group(2).strip()))
    if not rows:
        raise ValueError('Approved Markdown outline contains no usable headings.')

    if rows[0][0] == 1:
        title = rows[0][1]
        body = rows[1:]
    elif str(fallback_title or '').strip():
        title = str(fallback_title).strip()
        body = rows
    else:
        # A missing H1 is recoverable: treat the first heading as the title rather than
        # rejecting a human edit for one Markdown marker.
        title = rows[0][1]
        body = rows[1:]
    if not body:
        raise ValueError('Approved Markdown outline contains no chapter headings.')

    base_level = min(level for level, _ in body if level > 1) if any(
        level > 1 for level, _ in body
    ) else 1
    signature: list[tuple[int, str]] = [(1, title)]
    previous = 1
    for raw_level, heading_title in body:
        candidate = 2 if raw_level == 1 else raw_level - base_level + 2
        level = min(5, max(2, min(candidate, previous + 1)))
        signature.append((level, heading_title))
        previous = level
    return signature


def _assert_outline_matches_markdown(outline: dict[str, Any], outline_markdown: str) -> None:
    """Keep the editable Markdown as the sole authoritative outline structure."""
    fallback_title = str(
        outline.get('paper_title') or outline.get('research_topic') or outline.get('title') or ''
    )
    approved = _markdown_heading_signature(outline_markdown, fallback_title)
    derived = _markdown_heading_signature(_render_outline_markdown(outline))
    if derived == approved:
        return
    limit = max(len(approved), len(derived))
    mismatch = next((index for index in range(limit)
                     if index >= len(approved) or index >= len(derived)
                     or approved[index] != derived[index]), 0)
    expected = approved[mismatch] if mismatch < len(approved) else '<end>'
    actual = derived[mismatch] if mismatch < len(derived) else '<end>'
    raise ValueError(
        'Structured outline must exactly match the latest user-approved Markdown headings; '
        f'first mismatch at position {mismatch + 1}: approved={expected!r}, derived={actual!r}. '
        'Do not add, remove, rename, or reorder headings.'
    )


def _outline_nodes_by_path(
    nodes: list[Any],
    parent: tuple[tuple[str, int], ...] = (),
) -> tuple[
    dict[tuple[tuple[str, int], ...], dict[str, Any]],
    dict[str, list[dict[str, Any]]],
]:
    """Index old constraints without treating their numbering as authoritative."""
    by_path: dict[tuple[tuple[str, int], ...], dict[str, Any]] = {}
    by_title: dict[str, list[dict[str, Any]]] = {}

    def visit(items: list[Any], prefix: tuple[tuple[str, int], ...]) -> None:
        occurrences: dict[str, int] = {}
        for raw in items:
            if not isinstance(raw, dict):
                continue
            title = re.sub(r'\s+', ' ', str(raw.get('title') or '')).strip()
            if not title:
                continue
            occurrence = occurrences.get(title, 0) + 1
            occurrences[title] = occurrence
            key = prefix + ((title, occurrence),)
            by_path[key] = raw
            by_title.setdefault(title, []).append(raw)
            children = raw.get('children') if isinstance(raw.get('children'), list) else []
            visit(children, key)

    visit(nodes, parent)
    return by_path, by_title


def _synchronize_outline_with_markdown(
    outline: dict[str, Any], outline_markdown: str,
) -> dict[str, Any]:
    """Derive the transient constraints from the approved Markdown heading tree.

    Existing targets and evidence mappings are retained only for unambiguous matching
    headings. New or renamed headings receive fresh deterministic word allocation and no
    inherited evidence, so an old JSON outline can never override a user edit.
    """
    if not isinstance(outline, dict):
        raise ValueError('Structured outline must be a JSON object.')
    fallback_title = str(
        outline.get('paper_title') or outline.get('research_topic') or outline.get('title') or ''
    )
    approved = _markdown_heading_signature(outline_markdown, fallback_title)
    old_chapters = outline.get('chapters') if isinstance(outline.get('chapters'), list) else []
    old_by_path, old_by_title = _outline_nodes_by_path(old_chapters)
    roots: list[dict[str, Any]] = []
    root_occurrences: dict[str, int] = {}
    stack: list[
        tuple[int, dict[str, Any], tuple[tuple[str, int], ...], dict[str, int]]
    ] = []

    for heading_level, title in approved[1:]:
        while stack and stack[-1][0] >= heading_level:
            stack.pop()
        # _markdown_heading_signature has already repaired skipped levels.
        parent_key = stack[-1][2] if stack else ()
        occurrences = stack[-1][3] if stack else root_occurrences
        occurrence = occurrences.get(title, 0) + 1
        occurrences[title] = occurrence
        key = parent_key + ((title, occurrence),)
        previous = old_by_path.get(key)
        if previous is None and len(old_by_title.get(title, [])) == 1:
            previous = old_by_title[title][0]
        node = dict(previous or {})
        node.update({
            'title': title,
            'level': heading_level - 1,
            'number': '',
            'target_words': max(0, int((previous or {}).get('target_words') or 0)),
            'source_refs': list((previous or {}).get('source_refs') or []),
            'children': [],
        })
        if stack:
            stack[-1][1]['children'].append(node)
        else:
            roots.append(node)
        stack.append((heading_level, node, key, {}))

    if not roots:
        raise ValueError('Approved Markdown outline must contain at least one H2 chapter.')

    def number_nodes(nodes: list[dict[str, Any]], prefix: str = '', level: int = 1) -> None:
        for index, node in enumerate(nodes, 1):
            number = str(index) if not prefix else f'{prefix}.{index}'
            node['number'] = number
            node['level'] = level
            number_nodes(node['children'], number, level + 1)

    number_nodes(roots)
    leaves = _leaves(roots)
    total = max(
        1,
        int(outline.get('total_word_target') or 0),
        sum(int(leaf.get('target_words') or 0) for leaf in leaves),
    )
    known_weights = [int(leaf.get('target_words') or 0) for leaf in leaves
                     if int(leaf.get('target_words') or 0) > 0]
    fallback = (sum(known_weights) / len(known_weights)) if known_weights else 1.0
    weights = [float(int(leaf.get('target_words') or 0) or fallback) for leaf in leaves]
    scaled = [total * weight / sum(weights) for weight in weights]
    allocations = [int(value) for value in scaled]
    for index in sorted(
        range(len(leaves)), key=lambda item: scaled[item] - int(scaled[item]), reverse=True,
    )[:total - sum(allocations)]:
        allocations[index] += 1
    for leaf, target in zip(leaves, allocations):
        leaf['target_words'] = target

    def set_parent_targets(nodes: list[dict[str, Any]]) -> int:
        subtotal = 0
        for node in nodes:
            children = node['children']
            if children:
                node['target_words'] = set_parent_targets(children)
            subtotal += int(node.get('target_words') or 0)
        return subtotal

    set_parent_targets(roots)
    synchronized = dict(outline)
    synchronized.update({
        'paper_title': approved[0][1],
        'total_word_target': total,
        'chapters': roots,
    })
    _assert_outline_matches_markdown(synchronized, outline_markdown)
    return synchronized


def academic_writer_save_validated_outline(outline_json: Any) -> str:
    outline = _json_value(outline_json, {})
    return _write_markdown(
        _run_root('validated-outline'), 'outline_document', _render_outline_markdown(outline),
    )


def academic_writer_save_effective_outline(
    outline_json: dict[str, Any], outline_document_path: str,
) -> str:
    """Persist a temporary machine contract derived from the approved Markdown."""
    if not isinstance(outline_json, dict):
        outline_json = {}
    synchronized = _synchronize_outline_with_markdown(
        outline_json, _read_text(outline_document_path),
    )
    return _write_json(_run_root('effective-outline'), 'effective_outline', synchronized)


def academic_writer_normalize_outline(
    outline_document_path: str, generation_parameters_path: str,
) -> dict[str, str]:
    """Derive canonical Markdown and a private contract without an LLM JSON round trip."""
    parameters = _json_value(_read_text(generation_parameters_path), {})
    if not isinstance(parameters, dict):
        parameters = {}
    try:
        target = max(1, int(parameters.get('word_target') or 1))
    except (TypeError, ValueError):
        target = 1
    count_unit = str(parameters.get('count_unit') or 'chinese_characters')
    topic = str(parameters.get('research_topic') or 'Academic Paper').strip()
    synchronized = _synchronize_outline_with_markdown({
        'paper_title': topic,
        'research_topic': topic,
        'total_word_target': target,
        'count_unit': count_unit,
        'chapters': [],
    }, _read_text(outline_document_path))
    chapters = list(synchronized.get('chapters') or [])
    leaves = _leaves(chapters)
    titles = ' '.join(str(item.get('title') or '').lower() for item in chapters)
    conventional = {
        '摘要': ('摘要', 'abstract'),
        '引言': ('引言', '绪论', 'introduction'),
        '方法': ('方法', 'method'),
        '讨论': ('讨论', 'discussion'),
        '结论': ('结论', 'conclusion'),
        '参考文献': ('参考文献', 'references'),
    }
    omitted = [
        label for label, aliases in conventional.items()
        if not any(alias in titles for alias in aliases)
    ]
    report = '\n'.join([
        '# 大纲检查报告', '', '## PASS', '',
        f'- 论文标题：{synchronized["paper_title"]}',
        f'- 目标总量：{target}',
        f'- 一级章节：{len(chapters)}',
        f'- 叶子章节：{len(leaves)}',
        f'- 叶子分配合计：{sum(int(item.get("target_words") or 0) for item in leaves)}',
        '- Markdown 标题级别、编号和篇幅分配已确定性归一化。',
        *(
            ['', '## 非阻断提醒', '- 当前批准大纲未包含常规章节：' + '、'.join(omitted)]
            if omitted else []
        ),
    ])
    root = _run_root('normalized-outline')
    return {
        'outline_document': _write_markdown(
            root, 'outline_document', _render_outline_markdown(synchronized),
        ),
        'effective_outline': _write_json(root, 'effective_outline', synchronized),
        'outline_check_report': report,
    }


def academic_writer_read_markdown(document_path: str) -> str:
    path = Path(str(document_path or ''))
    if path.suffix.lower() not in {'.md', '.markdown', '.txt'}:
        raise ValueError('The academic Writer bridge accepts Markdown artifacts only.')
    return _read_text(str(path))


def academic_writer_update_context(content_path: str, writing_context_path: str) -> str:
    original = _json_value(_read_text(writing_context_path), {})
    try:
        updated = WriterCreateToolkit().update_writing_context(
            content_artifact_json=_read_text(content_path),
            writing_context_json=json.dumps(original, ensure_ascii=False),
        )
    except Exception as exc:  # noqa: BLE001 - context enrichment must not discard a document.
        fallback = dict(original) if isinstance(original, dict) else {}
        meta = dict(fallback.get('meta') or {})
        meta['context_update_warning'] = f'{type(exc).__name__}: {exc}'
        fallback['meta'] = meta
        updated = fallback
    return _write_json(_run_root('update-context'), 'writing_context', updated)


def _leaves(nodes: list[Any]) -> list[dict[str, Any]]:
    leaves: list[dict[str, Any]] = []
    for node in nodes:
        if not isinstance(node, dict):
            continue
        children = node.get('children') if isinstance(node.get('children'), list) else []
        if children:
            leaves.extend(_leaves(children))
        else:
            leaves.append(node)
    return leaves


def _leaf_heading_level(leaf: dict[str, Any]) -> int:
    return min(5, max(2, int(leaf.get('level') or 2) + 1))


def _normalize_section_instructions(
    raw: Any, outline: dict[str, Any],
) -> tuple[dict[str, Any], list[str]]:
    """Build a stable section contract from the approved outline.

    The generic Writer planner is generative, while chapter identity, hierarchy, target and
    evidence constraints are deterministic. Rebuilding this small contract avoids making a
    costly draft—and its checkpoint key—depend on random planning JSON.
    """
    chapters = [
        item for item in list(outline.get('chapters') or []) if isinstance(item, dict)
    ]
    normalized: list[dict[str, Any]] = []
    paper_title = str(outline.get('paper_title') or outline.get('research_topic') or '').strip()

    for chapter_index, chapter in enumerate(chapters, 1):
        title = str(chapter.get('title') or '').strip() or f'章节 {chapter_index}'
        leaves = _leaves([chapter]) or [chapter]
        contract = [{
            'number': str(leaf.get('number') or ''),
            'title': str(leaf.get('title') or '').strip() or title,
            'markdown_level': _leaf_heading_level(leaf),
            'target_words': max(0, int(leaf.get('target_words') or 0)),
            'source_refs': list(leaf.get('source_refs') or []),
        } for leaf in leaves]
        normalized.append({
            'instruction_id': f'academic-section-{chapter_index}',
            'content_ref': {'heading_path': [paper_title, title]},
            'section_title': title,
            'section_goal': f'依据写作任务、批准大纲和注册证据完成“{title}”章节。',
            'required_points': [
                '结构指引：' + '；'.join(
                    f"输出 `{'#' * item['markdown_level']} {item['title']}`，"
                    f"建议篇幅约 {item['target_words']}"
                    for item in contract
                ),
                (
                    '证据指引：source_refs 是优先候选而非排他授权；外部事实必须来自写作'
                    '上下文中的注册证据，并保留对应 SRC-NNN/KB-NNN。不得发明参考文献。'
                ),
            ],
            'fact_constraints': ['不得虚构数据、实验、来源或引用。'],
            'style_constraints': [
                '使用学科惯用学术语体，避免模板化套话、重复内容和机械段落节奏。',
            ],
            'expected_blocks': [item['title'] for item in contract],
            'meta': {
                'academic_leaf_contract': contract,
                'academic_count_unit': outline.get('count_unit'),
                'academic_total_target': int(outline.get('total_word_target') or 0),
            },
        })
    fingerprint = hashlib.sha256(json.dumps(
        normalized, ensure_ascii=False, sort_keys=True,
    ).encode('utf-8')).hexdigest()[:20]
    payload = {
        'instruction_set_id': f'academic-{fingerprint}',
        'instructions': normalized,
        'meta': {
            'representation': 'markdown',
            'document_title': paper_title,
            'deterministically_normalized': True,
        },
    }
    source_instructions = raw.get('instructions') if isinstance(raw, dict) else None
    warnings = []
    if isinstance(source_instructions, list) and source_instructions:
        warnings.append('已按最新批准大纲重建稳定章节规划，未沿用可能过期的生成式规划。')
    return payload, warnings


def academic_writer_plan_sections(
    writing_task_path: str,
    outline_document_path: str,
    writing_context_path: str,
    effective_outline_path: str,
) -> dict[str, Any]:
    """Attach academic length and registered-evidence constraints to Writer plans."""
    approved_markdown = _read_text(outline_document_path)
    outline = _synchronize_outline_with_markdown(
        _json_value(_read_text(effective_outline_path), {}), approved_markdown,
    )
    # JSON carries only transient word/evidence constraints. Its headings are an
    # exact projection of the user-approved Markdown, which remains the source of truth.
    # Keep the public signature aligned with shared Writer, but planning itself is local and
    # deterministic. The drafting call still receives writing_task and writing_context.
    _read_text(writing_task_path)
    _read_text(writing_context_path)
    instructions, warnings = _normalize_section_instructions({}, outline)
    root = _run_root('section-plan')
    return {
        'section_instructions': _write_json(root, 'section_instructions', instructions),
        'warnings': warnings,
    }


def _assert_section_instructions_match_outline(
    section_instructions: dict[str, Any], outline: dict[str, Any],
) -> None:
    """Compatibility check retained for callers; variable plans are repaired, not rejected."""
    normalized, _ = _normalize_section_instructions(section_instructions, outline)
    section_instructions.clear()
    section_instructions.update(normalized)


def _normalized_title(value: str) -> str:
    text = re.sub(r'^\s*\d+(?:\.\d+)*[、.．\s　]+', '', str(value or ''))
    return re.sub(r'[\s\W_]+', '', text, flags=re.UNICODE).lower()


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
        expected_level = _leaf_heading_level(leaf)
        best: tuple[float, int, str] | None = None
        for position in range(cursor, len(candidates)):
            _, actual, kind = candidates[position]
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


def _normalize_section_root(markdown: str, chapter: dict[str, Any]) -> str:
    """Map an arbitrary relative heading hierarchy onto one academic H2 section."""
    title = str(chapter.get('title') or '').strip()
    if not title:
        raise ValueError('Academic top-level chapter title must not be empty.')
    lines = str(markdown or '').splitlines()
    headings = [
        (index, len(match.group(1)), match.group(2).strip())
        for index, line in enumerate(lines)
        if (match := MARKDOWN_HEADING.match(line.strip()))
    ]
    root = next((
        item for item in headings
        if _normalized_title(item[2]) == _normalized_title(title)
    ), None)
    root_index = root[0] if root else -1
    if root_index < 0:
        lines = [f'## {title}', '', *lines]
        root_index = 0
        headings = [(index + 2, level, heading_title) for index, level, heading_title in headings]
        child_levels = [level for _, level, _ in headings]
        shift = 3 - min(child_levels) if child_levels else 0
    else:
        lines[root_index] = f'## {title}'
        shift = 2 - int(root[1])

    normalized: list[str] = []
    seen_titles: set[str] = set()
    for index, line in enumerate(lines):
        match = MARKDOWN_HEADING.match(line.strip())
        if not match:
            normalized.append(line)
            continue
        heading_title = _normalized_title(match.group(2))
        if heading_title in seen_titles:
            continue
        seen_titles.add(heading_title)
        if index == root_index:
            normalized.append(f'## {title}')
        else:
            level = min(6, max(3, len(match.group(1)) + shift))
            normalized.append(f"{'#' * level} {match.group(2).strip()}")
    # The transformation above always creates one canonical H2 and demotes every other
    # heading. Do not reject already-generated prose merely because the model varied its
    # original Markdown hierarchy.
    return '\n'.join(normalized).strip()


def _count_units(text: str, count_unit: str) -> int:
    if count_unit == 'words':
        return len(WORD.findall(str(text or '')))
    value = str(text or '')
    return len(HAN.findall(value)) + len(re.findall(r'\b[A-Za-z][A-Za-z0-9_-]*\b', value))


def _align_sections_to_chapters(
    sections: list[str], chapters: list[dict[str, Any]],
) -> tuple[list[str], list[str]]:
    """Best-effort align variable Writer output without throwing completed prose away."""
    usable = [str(item or '').strip() for item in sections if str(item or '').strip()]
    remaining = set(range(len(usable)))
    aligned: list[str] = []
    warnings: list[str] = []
    for chapter_index, chapter in enumerate(chapters, 1):
        title = str(chapter.get('title') or '').strip() or f'章节 {chapter_index}'
        match_index: int | None = None
        scored: list[tuple[float, int]] = []
        for index in remaining:
            headings = [
                match.group(2) for line in usable[index].splitlines()
                if (match := MARKDOWN_HEADING.match(line.strip()))
            ]
            score = max((_title_similarity(title, heading) for heading in headings), default=0.0)
            scored.append((score, index))
        if scored:
            score, candidate = max(scored)
            if score >= 0.58:
                match_index = candidate
        if match_index is None and remaining:
            match_index = min(remaining)
            warnings.append(f'章节“{title}”按生成顺序完成了对齐。')
        if match_index is None:
            aligned.append(f'## {title}\n\n> 本章未获得独立生成内容，请在人工检查点补充。')
            warnings.append(f'章节“{title}”没有独立生成内容，已保留可编辑占位。')
        else:
            remaining.remove(match_index)
            aligned.append(usable[match_index])
    if remaining:
        warnings.append(f'忽略了 {len(remaining)} 个无法对应批准大纲的多余生成块。')
    return aligned, warnings


def _enforce_draft_contract(
    sections: list[str], outline: dict[str, Any],
) -> list[str]:
    """Normalize generated sections; only absence of a usable outline remains fatal.

    Length, missing headings and extra/missing model blocks are quality findings for the
    later audit and human checkpoint. They must never discard an expensive completed draft.
    """
    chapters = outline.get('chapters') if isinstance(outline, dict) else None
    if not isinstance(chapters, list) or not chapters:
        raise ValueError('The approved academic outline contains no usable chapters.')
    sections, _ = _align_sections_to_chapters(
        sections, [item for item in chapters if isinstance(item, dict)],
    )
    normalized: list[str] = []
    for chapter, section in zip(chapters, sections):
        leaves = _leaves([chapter])
        value = _canonicalize_leaf_headings(str(section), leaves)
        value = _normalize_section_root(value, chapter)
        lines = value.splitlines()
        positions: list[int] = []
        cursor = 0
        for leaf in leaves:
            title = str(leaf.get('title') or '').strip()
            level = _leaf_heading_level(leaf)
            position = next((
                index for index in range(cursor, len(lines))
                if (match := MARKDOWN_HEADING.match(lines[index].strip()))
                and len(match.group(1)) == level
                and _normalized_title(match.group(2)) == _normalized_title(title)
            ), -1)
            positions.append(position)
            if position < 0:
                # Preserve the prose and make the approved structure editable. A neutral
                # marker is safer than inventing academic content or re-running the model.
                lines.extend([
                    '', f"{'#' * level} {title}", '',
                    '> 本小节未被模型单独分隔；请结合本章前文在人工检查点复核。',
                ])
            else:
                cursor = position + 1
        normalized.append('\n'.join(lines).strip())
    # Deliberately do not enforce the 90% target here. The deterministic audit reports a
    # short draft as FAIL, but the generated document remains available for editing/revision.
    return normalized


def academic_writer_write_sections(
    writing_task_path: str,
    section_instructions_path: str,
    writing_context_path: str,
    effective_outline_path: str,
    outline_document_path: str,
) -> list[str]:
    """Write or resume the initial draft from completed section checkpoints.

    A later Workflow attempt may invoke this function with the same inputs after a runtime
    interruption; completed sections are reused without model regeneration. Do not call it
    twice inside one SubAgent attempt after a validation or assembly error. Never substitute
    academic_writer_revise_markdown for initial drafting.
    """
    outline = _synchronize_outline_with_markdown(
        _json_value(_read_text(effective_outline_path), {}),
        _read_text(outline_document_path),
    )
    section_instructions = _json_value(_read_text(section_instructions_path), {})
    section_instructions, _ = _normalize_section_instructions(section_instructions, outline)
    normalized_plan_path = _write_json(
        _run_root('normalized-section-plan'), 'section_instructions', section_instructions,
    )
    ctx = require_context()
    events = DraftMarkdownStreamEventEmitter(ctx.emit, slot='draft_document')
    try:
        sections = _json_value(WriterCreateToolkit().stream_draft_blocks_markdown(
            writing_task_json=_read_json(writing_task_path),
            section_instructions_json=_read_json(normalized_plan_path),
            writing_context_json=_read_json(writing_context_path),
            on_delta=events.feed,
            on_section_end=events.flush,
            on_progress=lambda payload: ctx.emit({'type': 'progress', **payload}),
            checkpoint_dir=str(_workspace_root() / 'academic-writer' / 'draft-checkpoints'),
        ), [])
        if not isinstance(sections, list) or not sections:
            raise ValueError('Shared Writer returned no academic draft sections.')
        sections = _enforce_draft_contract([str(item) for item in sections], outline)
        root = _run_root('draft-sections')
        paths = [
            _write_markdown(root, f'draft_section_{index:04d}', section)
            for index, section in enumerate(sections, 1)
        ]
    except Exception as exc:
        events.abort(str(exc))
        raise
    events.end()
    return paths


def academic_writer_assemble_draft(
    draft_sections_anchor_path: str,
    writing_context_path: str,
    outline_document_path: str,
) -> str:
    anchor = Path(str(draft_sections_anchor_path or '')).resolve()
    directory = anchor if anchor.is_dir() else anchor.parent
    paths = sorted(directory.glob('draft_section_*.md'))
    if not paths:
        raise ValueError('No Writer draft section files were found.')
    sections = [_read_text(str(path)) for path in paths]
    approved_outline = _read_text(outline_document_path)
    title = _markdown_heading_signature(approved_outline)[0][1]
    try:
        payload = _json_value(WriterCreateToolkit().generate_draft_document_markdown(
            draft_sections_json=json.dumps(sections, ensure_ascii=False),
            writing_context_json=_read_json(writing_context_path),
            outline_json=approved_outline,
            title=title,
        ), {})
    except Exception:  # noqa: BLE001 - deterministic concatenation preserves completed work.
        payload = {}
    draft = str(payload.get('draft_document') or '').strip() if isinstance(payload, dict) else ''
    if not draft:
        draft = '\n\n'.join([f'# {title}', *sections])
    return _write_markdown(
        _run_root('draft-document'), 'draft_document', draft,
    )


def academic_writer_revise_markdown(
    base_document_path: str,
    writing_context_path: str,
    instruction: str,
    document_slot: str,
) -> dict[str, str]:
    if document_slot not in EDITABLE_SLOTS:
        raise ValueError(f'document_slot must be one of {sorted(EDITABLE_SLOTS)}.')
    document = _read_text(base_document_path)
    if document_slot != 'outline_document' and not any(
        line.strip() and not re.match(r'^#{1,6}\s+', line.strip())
        for line in document.splitlines()
    ):
        raise ValueError(
            'Initial academic drafting cannot be replaced by revising an outline; '
            'call academic_writer_write_sections with the same inputs to resume checkpoints.'
        )
    root = _run_root(f'revise-{document_slot}')
    instruction = str(instruction or '').strip()
    context_text = _read_text(writing_context_path)
    registered_ids = sorted(set(EVIDENCE_ID.findall(context_text)))
    revision_task = {
        'query': instruction,
        'strategy': 'single_pass_complete_markdown',
        'document_slot': document_slot,
    }
    locate = {
        'status': 'DETERMINISTIC',
        'scope': 'document_root',
        'reason': 'The workflow already selected the complete editable manuscript.',
    }
    plan = {
        'status': 'DETERMINISTIC',
        'scope': 'document',
        'strategy': 'single_pass_complete_markdown',
    }
    warning = ''
    try:
        if not instruction:
            raise ValueError('instruction must not be empty.')
        evidence_boundary = ', '.join(registered_ids) if registered_ids else '(none registered)'
        prompt = f'''Revise the complete Markdown document once according to the instruction.

Return only the complete revised Markdown document. Do not return JSON, a patch, a change
plan, analysis, commentary, or an outer Markdown code fence. The source document is the
only document source of truth. Preserve unaffected content and the user's latest edits.
Apply the requested changes directly; do not merely describe them. Do not invent facts,
data, methods, results, citations, or source metadata. Evidence IDs allowed by the locked
Writer context are: {evidence_boundary}.

Revision instruction:
{instruction}

Source Markdown:
{document}
'''
        revision = WriterRevisionTools(
            llm=AutoModel(model='llm'), artifact_store=str(root / 'writer'),
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
        changed = revised != document.strip()
        replace_set = {
            'strategy': 'complete_document_rewrite',
            'replacement_count': int(changed),
        }
        applied = {
            'string_replace_result': {
                'status': 'APPLIED' if changed else 'NO_CHANGE',
                'success': changed,
                'message': (
                    'Complete Markdown revision applied in one model call.'
                    if changed else 'The model returned the source document unchanged.'
                ),
            },
        }
        if not changed:
            warning = 'Writer returned an unchanged document; the approved source was preserved.'
    except Exception as exc:  # noqa: BLE001 - never lose the approved manuscript on revision.
        warning = f'{type(exc).__name__}: {exc}'
        revision_task = {
            **revision_task, 'fallback': 'preserve_original_document',
        }
        locate = {'status': 'NOT_COMPLETED', 'warning': warning}
        plan = {'status': 'NOT_COMPLETED', 'warning': warning}
        replace_set = {'replacements': [], 'warning': warning}
        applied = {
            'revised_document': document,
            'string_replace_result': {
                'status': 'NO_OP_FALLBACK',
                'warning': warning,
                'message': '修订工具失败，已保留原稿供人工编辑，未触发全文重写。',
            },
        }
        revised = document
    result = {
        'revision_task': _write_json(root, 'revision_task', revision_task),
        'locate_result': _write_json(root, 'locate_result', locate),
        'modify_plan': _write_json(root, 'modify_plan', plan),
        'revision_set': _write_json(root, 'revision_set', replace_set),
        'revision_result': _write_json(
            root, 'revision_result', applied.get('string_replace_result') or {},
        ),
        document_slot: _write_markdown(root, document_slot, revised),
    }
    if warning:
        result['warning'] = warning
    return result


def academic_writer_revise_from_feedback(
    base_document_path: str,
    writing_context_path: str,
    feedback_path: str,
    document_slot: str,
    user_instruction: str = '',
) -> dict[str, str]:
    feedback_value = _json_value(_read_text(feedback_path), '')
    feedback = (
        feedback_value if isinstance(feedback_value, str)
        else json.dumps(feedback_value, ensure_ascii=False)
    )
    instruction = f"""依据以下评审材料直接修订论文正文。不得虚构新数据、方法、实验结果或
参考文献；仅修改评审涉及的范围。无法安全落实的意见保留对应原文，不要把解释、响应表或
修订过程写入论文；意见处理状态由工作流另行生成。

用户补充要求：{str(user_instruction or '').strip() or '无'}

评审材料：
{feedback}""".strip()
    return academic_writer_revise_markdown(
        base_document_path, writing_context_path, instruction, document_slot,
    )


def academic_writer_preview_selection_rewrite(
    artifact: Any,
    instruction: str,
    selection: Mapping[str, Any],
    artifact_store: str = '',
    slot: str = '',
) -> dict[str, Any]:
    if slot not in EDITABLE_SLOTS:
        raise ValueError('Selection rewrite is supported only for academic Writer documents.')
    if str((selection or {}).get('type') or '') != 'markdown':
        raise ValueError("The academic workflow requires selection.type='markdown'.")
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
    root = root / 'academic-writer-selection' / uuid.uuid4().hex
    root.mkdir(parents=True, exist_ok=True)
    context = {'context_id': f'academic-selection-{uuid.uuid4().hex}', 'meta': {'slot': slot}}
    revision = WriterRevisionTools(llm=AutoModel(model='llm'), artifact_store=str(root))
    replace_set = StringReplaceSet.model_validate(
        revision.build_selected_markdown_replace_set(
            document, instruction, str(selection.get('selected_text') or ''), context,
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
        'preview': {'old_text': replacement.old_string, 'new_text': replacement.new_string},
        'patch': {'type': 'string_replace_set', 'payload': replace_set.model_dump()},
        'artifact': {
            'content_type': 'file',
            'value': {
                'path': str(canonical), 'filename': canonical.name,
                'size': canonical.stat().st_size,
            },
        },
    }
