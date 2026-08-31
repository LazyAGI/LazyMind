"""Deterministic business rules for the built-in academic research pipeline."""

from __future__ import annotations

import json
import re
from pathlib import Path
from typing import Any, Iterable


HAN = re.compile(r'[\u3400-\u4dbf\u4e00-\u9fff]')
WORD = re.compile(r"\b[\w'-]+\b", re.UNICODE)
EVIDENCE_ID = re.compile(r'\b(?:SRC|KB)-\d{3}\b')
PARENTHETICAL = re.compile(r'\([^)]*\)|（[^）]*）|\[[^]]*\]|【[^】]*】')
REGISTERED_EVIDENCE_ID = re.compile(
    r'^\s*(?:#{1,6}\s+|[-*+]\s+|\|\s*)?(?:\*\*)?((?:SRC|KB)-\d{3})'
    r'(?:\*\*)?(?=\s*(?:\||[:：—-]|$))',
    re.MULTILINE,
)
HEADING = re.compile(r'^#{1,6}\s+(.+?)\s*$', re.MULTILINE)

PAPER_TYPES = {
    'empirical': 'empirical', 'empirical research': 'empirical', '实证研究': 'empirical',
    'research': 'research', 'research paper': 'research', '研究型论文': 'research',
    '论文': 'research', '学术论文': 'research', '研究论文': 'research',
    'literature_review': 'literature_review', 'literature review': 'literature_review',
    '综述': 'literature_review', '文献综述': 'literature_review',
    'theoretical': 'theoretical', 'theoretical paper': 'theoretical', '理论研究': 'theoretical',
    '理论探讨': 'theoretical', '思辨论文': 'theoretical',
    '理论探讨/思辨论文': 'theoretical', '理论探讨_思辨论文': 'theoretical',
    'case_study': 'case_study', 'case study': 'case_study', '案例研究': 'case_study',
    'conference': 'conference', 'conference paper': 'conference', '会议论文': 'conference',
}
CITATION_STYLES = {
    'apa': 'APA 7', 'apa7': 'APA 7', 'apa 7': 'APA 7', 'apa 7.0': 'APA 7',
    'chicago': 'Chicago Author-Date', 'chicago author-date': 'Chicago Author-Date',
    'mla': 'MLA 9', 'mla9': 'MLA 9', 'mla 9': 'MLA 9',
    'ieee': 'IEEE', 'vancouver': 'Vancouver',
    'gbt7714': 'GB/T 7714', 'gb/t 7714': 'GB/T 7714', 'gb/t7714': 'GB/T 7714',
    'gb 7714': 'GB/T 7714', '国标': 'GB/T 7714',
}


def _json_object(value: Any, name: str) -> dict[str, Any]:
    current = value
    for _ in range(5):
        if isinstance(current, dict):
            nested = next((
                current[key] for key in ('data', 'outline', 'normalized_outline')
                if key in current
            ), current)
            if nested is current:
                return current
            current = nested
            continue
        text = str(current or '').strip()
        fenced = re.search(r'```(?:json)?\s*(\{.*\})\s*```', text, re.DOTALL | re.IGNORECASE)
        if fenced:
            text = fenced.group(1)
        try:
            current = json.loads(text)
        except (TypeError, json.JSONDecodeError) as exc:
            raise ValueError(f'{name} must be a valid JSON object.') from exc
    if not isinstance(current, dict):
        raise ValueError(f'{name} must be a JSON object.')
    return current


def _positive_int(value: Any, name: str) -> int:
    match = re.search(r'\d+', str(value or '').replace(',', ''))
    if not match or int(match.group()) < 1:
        raise ValueError(f'{name} must contain a positive integer.')
    return int(match.group())


def normalize_academic_parameters(
    research_topic: str,
    word_target: str,
    paper_type: str,
    paper_language: str,
    citation_style: str,
    output_format: str,
) -> dict[str, Any]:
    """Normalize the mandatory preflight answers without inventing defaults."""
    topic = re.sub(r'\s+', ' ', str(research_topic or '')).strip()
    if not topic:
        raise ValueError('research_topic must not be empty.')
    target = _positive_int(word_target, 'word_target')
    type_text = str(paper_type or '').strip().lower()
    raw_type = re.sub(r'[-\s]+', '_', type_text)
    base_type = re.sub(r'\s*[（(][^）)]*[）)]\s*$', '', type_text)
    normalized_type = (
        PAPER_TYPES.get(raw_type)
        or PAPER_TYPES.get(type_text)
        or PAPER_TYPES.get(base_type)
    )
    if not normalized_type:
        raise ValueError(
            'paper_type must be research, empirical, literature_review, theoretical, '
            'case_study, or conference.'
        )
    raw_language = str(paper_language or '').strip().lower()
    if raw_language in {'中文', '汉语', '简体中文', 'chinese', 'zh', 'zh-cn'}:
        language, count_unit = 'zh-CN', 'chinese_characters'
    elif raw_language in {'英文', '英语', 'english', 'en', 'en-us'}:
        language, count_unit = 'en', 'words'
    else:
        raise ValueError('paper_language must be Chinese or English.')
    raw_style = re.sub(r'\s+', ' ', str(citation_style or '').strip().lower())
    style = CITATION_STYLES.get(raw_style)
    if not style:
        raise ValueError(
            'citation_style must be APA 7, Chicago, MLA 9, IEEE, Vancouver, or GB/T 7714.'
        )
    raw_format = str(output_format or '').strip().lower().lstrip('.')
    if raw_format in {'markdown'}:
        raw_format = 'md'
    if raw_format in {'word', 'word (.docx)', 'word（.docx）', 'microsoft word'}:
        raw_format = 'docx'
    if raw_format not in {'md', 'docx'}:
        raise ValueError('output_format must be md or docx.')
    return {
        'research_topic': topic,
        'word_target': target,
        'paper_type': normalized_type,
        'paper_language': language,
        'count_unit': count_unit,
        'citation_style': style,
        'output_format': raw_format,
        'source_skill': {
            'name': 'academic-pipeline',
            'version': '3.21.0',
            'adaptation': 'LazyMind built-in workflow',
        },
        'display_summary': (
            f'主题：{topic}；类型：{normalized_type}；目标：{target}；'
            f'语言：{language}；引用：{style}；导出：{raw_format.upper()}'
        ),
    }


def _walk(nodes: list[Any]) -> Iterable[dict[str, Any]]:
    for node in nodes:
        if not isinstance(node, dict):
            continue
        yield node
        children = node.get('children') if isinstance(node.get('children'), list) else []
        yield from _walk(children)


def _leaves(nodes: list[Any]) -> list[dict[str, Any]]:
    return [
        node for node in _walk(nodes)
        if not (isinstance(node.get('children'), list) and node['children'])
    ]


def _registered_evidence_ids(value: str) -> set[str]:
    """Read actual evidence record keys without treating protocol examples as records."""
    text = str(value or '').strip()
    try:
        payload = json.loads(text)
    except (TypeError, json.JSONDecodeError):
        payload = None
    if isinstance(payload, dict):
        for key in ('data', 'text', 'content'):
            if isinstance(payload.get(key), str):
                text = payload[key]
                break
    return set(REGISTERED_EVIDENCE_ID.findall(text))


def _cited_evidence_ids(value: str) -> set[str]:
    """Extract IDs used in citation-like parentheses, including author-year citations."""
    return {
        item.upper()
        for group in PARENTHETICAL.findall(str(value or ''))
        for item in EVIDENCE_ID.findall(group)
    }


def _set_parent_targets(nodes: list[Any]) -> int:
    total = 0
    for node in nodes:
        if not isinstance(node, dict):
            continue
        children = node.get('children') if isinstance(node.get('children'), list) else []
        if children:
            node['target_words'] = _set_parent_targets(children)
        total += max(0, int(node.get('target_words') or 0))
    return total


def _allocate_targets(leaves: list[dict[str, Any]], target: int) -> None:
    if not leaves:
        return
    weights = [max(1, int(leaf.get('target_words') or 1)) for leaf in leaves]
    weight_sum = sum(weights)
    minimum = min(120, target // len(leaves))
    remaining = max(0, target - minimum * len(leaves))
    extras = [(remaining * weight) // weight_sum for weight in weights]
    remainder = remaining - sum(extras)
    ranked = sorted(
        range(len(weights)),
        key=lambda index: ((remaining * weights[index]) % weight_sum, -index),
        reverse=True,
    )
    for index in ranked[:remainder]:
        extras[index] += 1
    for leaf, amount in zip(leaves, extras):
        leaf['target_words'] = minimum + amount


def validate_and_allocate_academic_outline(
    outline_json: str,
    literature_evidence: str,
    word_target: str,
    paper_language: str,
) -> dict[str, Any]:
    """Validate editable academic outline structure and allocate an exact word budget."""
    outline = _json_object(outline_json, 'outline_json')
    target = _positive_int(word_target, 'word_target')
    chapters = outline.get('chapters')
    if not isinstance(chapters, list) and isinstance(outline.get('children'), list):
        chapters = outline.pop('children')
        outline['chapters'] = chapters
        outline.setdefault('paper_title', str(outline.get('title') or '').strip())
    if not isinstance(chapters, list) or not chapters:
        return {
            'valid': False,
            'normalized_outline': outline,
            'report': '# 大纲检查报告\n\nFAIL：chapters 必须是非空数组。',
            'errors': ['chapters must be a non-empty list'],
            'warnings': [],
        }
    errors: list[str] = []
    warnings: list[str] = []
    available_refs = _registered_evidence_ids(literature_evidence)

    def visit(nodes: list[Any], parent: str = '', level: int = 1) -> None:
        for index, raw in enumerate(nodes, 1):
            if not isinstance(raw, dict):
                title = re.sub(r'\s+', ' ', str(raw or '')).strip()
                raw = {'title': title or f'未命名章节 {index}', 'children': []}
                nodes[index - 1] = raw
                warnings.append(f'{parent or "root"} 第 {index} 项已转换为章节对象')
            number = str(index) if not parent else f'{parent}.{index}'
            title = re.sub(r'\s+', ' ', str(raw.get('title') or '')).strip()
            try:
                raw_level = int(raw.get('level') or level)
            except (TypeError, ValueError):
                raw_level = level
            normalized_level = min(4, level)
            if raw_level != normalized_level or level > 4:
                warnings.append(
                    f'{number} level 已从 {raw_level} 归一化为 {normalized_level}'
                )
            if str(raw.get('number') or number) != number:
                warnings.append(f'{number} 编号已自动归一化')
            if not title:
                title = f'未命名章节 {number}'
                warnings.append(f'{number} 空标题已自动补为“{title}”')
            elif len(title) > 80:
                title = title[:80].rstrip()
                warnings.append(f'{number} 过长标题已截断为 80 个字符')
            raw['level'], raw['number'], raw['title'] = normalized_level, number, title
            children = raw.get('children')
            if children is None:
                raw['children'] = []
            elif not isinstance(children, list):
                warnings.append(f'{number} children 不是数组，已按叶子章节处理')
                raw['children'] = []
            elif children:
                visit(children, number, level + 1)
            refs_value = raw.get('source_refs') or []
            if isinstance(refs_value, str):
                refs_value = EVIDENCE_ID.findall(refs_value)
            elif not isinstance(refs_value, list):
                refs_value = []
            refs = list(dict.fromkeys(
                str(item).strip() for item in refs_value if str(item).strip()
            ))
            unknown = [item for item in refs if item not in available_refs]
            if unknown:
                warnings.append(
                    f'{number} 已移除不存在的候选证据编号：{"、".join(unknown)}'
                )
            raw['source_refs'] = [item for item in refs if item in available_refs]

    visit(chapters)
    leaves = _leaves(chapters)
    if len(leaves) < 3:
        warnings.append('叶子章节少于 3 个；保留用户/模型给定的精简结构')
    if len(leaves) > 40:
        warnings.append('叶子章节超过 40 个；建议在人工大纲节点合并过细章节')
    _allocate_targets(leaves, target)
    _set_parent_targets(chapters)
    outline['total_word_target'] = target
    raw_language = str(paper_language or '').strip().lower()
    outline['count_unit'] = (
        'words'
        if raw_language in {'英文', '英语', 'english'} or raw_language.startswith('en')
        else 'chinese_characters'
    )
    outline.setdefault('$schema', 'academic-research-pipeline/outline.schema.json')

    titles = ' '.join(str(node.get('title') or '').lower() for node in _walk(chapters))
    section_groups = {
        'abstract': ('abstract', '摘要'),
        'introduction': ('introduction', '引言', '绪论'),
        'methodology': ('method', 'methodology', '研究方法', '方法'),
        'discussion': ('discussion', '讨论'),
        'conclusion': ('conclusion', '结论'),
        'references': ('references', '参考文献'),
        'declarations': ('declaration', '声明', '披露'),
    }
    missing = [
        name for name, aliases in section_groups.items()
        if not any(alias in titles for alias in aliases)
    ]
    if missing:
        warnings.append('建议补充标准学术章节：' + '、'.join(missing))
    mapped_refs = set(
        ref for leaf in leaves for ref in (leaf.get('source_refs') or [])
    )
    unused = sorted(available_refs - mapped_refs)
    if unused:
        warnings.append('未映射到大纲的候选证据：' + '、'.join(unused))
    report_status = 'PASS' if not errors else 'FAIL'
    lines = [
        '# 大纲检查报告', '', f'## {report_status}', '',
        f'- 目标总量：{target}',
        f'- 一级章节：{len(chapters)}',
        f'- 叶子章节：{len(leaves)}',
        f'- 叶子分配合计：{sum(int(item.get("target_words") or 0) for item in leaves)}',
        f'- 可用证据：{len(available_refs)}',
        f'- 已映射证据：{len(mapped_refs)}',
    ]
    if errors:
        lines.extend(['', '## 必须修复'] + [f'- {item}' for item in errors])
    if warnings:
        lines.extend(['', '## 建议检查'] + [f'- {item}' for item in warnings])
    return {
        'valid': not errors,
        'normalized_outline': outline,
        'report': '\n'.join(lines),
        'errors': errors,
        'warnings': warnings,
    }


def validate_and_allocate_academic_outline_from_inputs(
    outline_json: str,
    word_target: str,
    paper_language: str,
) -> dict[str, Any]:
    """Validate against the bound evidence artifact without copying it through the model."""
    values = _remote_inputs()
    evidence_path = str(values.get('literature_evidence') or '')
    evidence = str(_artifact_payload(evidence_path)) if evidence_path else ''
    return validate_and_allocate_academic_outline(
        outline_json, evidence, word_target, paper_language,
    )


def normalize_academic_review_decision(decision_text: str) -> str:
    """Return a deterministic route token from variable LLM review wording."""
    text = str(decision_text or '').strip().upper().replace('-', '_').replace(' ', '_')
    if 'MAJOR_REVISION' in text or '重大修改' in str(decision_text):
        return 'MAJOR_REVISION'
    if 'MINOR_REVISION' in text or '小修' in str(decision_text):
        return 'MINOR_REVISION'
    if 'ACCEPT' in text or '接受' in str(decision_text):
        return 'ACCEPT'
    # Ambiguous/reject wording takes the safe review route without making routing depend on
    # the model reproducing one exact token.
    return 'MAJOR_REVISION'


def count_academic_units(text: str, count_unit: str) -> int:
    """Count Chinese characters or English words using the declared paper language."""
    value = str(text or '')
    if count_unit == 'words':
        return len(WORD.findall(value))
    return len(HAN.findall(value)) + len(re.findall(r'\b[A-Za-z][A-Za-z0-9_-]*\b', value))


def audit_academic_manuscript(
    markdown_text: str,
    literature_evidence: str,
    parameters_json: str,
    stage: str = 'pre_review',
    approved_outline_markdown: str = '',
) -> dict[str, Any]:
    """Run reproducible checks relative to the user-approved outline when supplied."""
    parameters = _json_object(parameters_json, 'parameters_json')
    manuscript = str(markdown_text or '')
    target = _positive_int(parameters.get('word_target'), 'word_target')
    count_unit = str(parameters.get('count_unit') or 'chinese_characters')
    actual = count_academic_units(manuscript, count_unit)
    lower_bound = max(1, int(target * 0.9))
    upper_bound = int(target * 1.1)
    available = _registered_evidence_ids(literature_evidence)
    # Count IDs inside citation-like parentheses, including “(Author, 2025; SRC-001)”. A
    # bare ID in a verification table is provenance, not necessarily a manuscript citation.
    cited = _cited_evidence_ids(manuscript)
    unknown = sorted(cited - available)
    uncited = sorted(available - cited)
    headings = ' '.join(HEADING.findall(manuscript)).lower()
    required = {
        'Abstract/摘要': ('abstract', '摘要'),
        'Introduction/引言': ('introduction', '引言', '绪论'),
        'Methodology/研究方法': ('method', 'methodology', '研究方法', '方法'),
        'Discussion/讨论': ('discussion', '讨论'),
        'Conclusion/结论': ('conclusion', '结论'),
        'References/参考文献': ('references', '参考文献'),
        'Limitations/局限': ('limitation', '局限', '限制'),
        'Data Availability/数据可用性': ('data availability', '数据可用'),
        'Ethics Declaration/伦理声明': ('ethics', '伦理'),
        'Author Contributions/作者贡献': ('author contribution', '作者贡献'),
        'Conflict of Interest/利益冲突': ('conflict of interest', '利益冲突'),
        'Funding/资金声明': ('funding', '资助', '资金'),
        'AI Disclosure/AI 使用声明': ('ai disclosure', 'ai 使用', '人工智能使用'),
    }
    conventionally_missing = [
        label for label, aliases in required.items()
        if not any(alias in headings for alias in aliases)
    ]
    approved_headings = ' '.join(HEADING.findall(str(approved_outline_markdown or ''))).lower()
    if approved_headings:
        required_by_approved_outline = {
            label for label, aliases in required.items()
            if any(alias in approved_headings for alias in aliases)
        }
        missing_sections = [
            label for label in conventionally_missing if label in required_by_approved_outline
        ]
        outline_omitted_sections = [
            label for label in conventionally_missing if label not in required_by_approved_outline
        ]
    else:
        # Backward-compatible strict audit for callers that do not provide an approved outline.
        missing_sections = conventionally_missing
        outline_omitted_sections = []
    failures: list[str] = []
    warnings: list[str] = []
    if not manuscript.strip():
        failures.append('论文正文为空')
    if actual < lower_bound:
        failures.append(f'篇幅不足：{actual}/{target}，低于 90% 下限 {lower_bound}')
    elif actual > upper_bound:
        warnings.append(f'篇幅超出目标 10%：{actual}/{target}')
    if unknown:
        failures.append('正文引用了不存在的证据编号：' + '、'.join(unknown))
    if missing_sections:
        failures.append('缺少已批准大纲中的章节或声明：' + '、'.join(missing_sections))
    if outline_omitted_sections:
        warnings.append(
            '用户批准的大纲未包含以下常规学术章节，本次不作阻断：'
            + '、'.join(outline_omitted_sections)
        )
    if available and not cited:
        failures.append('检索到证据但正文没有任何可审计的证据编号')
    if uncited:
        warnings.append('候选证据未在正文使用：' + '、'.join(uncited))
    if not available:
        warnings.append('本轮没有可验证检索证据；不得据此声称完成系统性文献覆盖')
    status = 'FAIL' if failures else 'WARN' if warnings else 'PASS'
    summary = {
        'stage': str(stage or 'pre_review'),
        'status': status,
        'target': target,
        'actual': actual,
        'count_unit': count_unit,
        'lower_bound': lower_bound,
        'upper_bound': upper_bound,
        'available_evidence_count': len(available),
        'cited_evidence_count': len(cited & available),
        'unknown_citations': unknown,
        'uncited_evidence': uncited,
        'missing_sections': missing_sections,
        'outline_omitted_sections': outline_omitted_sections,
        'failures': failures,
        'warnings': warnings,
        'deterministic_scope': [
            'document structure', 'declared length', 'registered evidence IDs',
        ],
        'semantic_checks_required': [
            'claim-source alignment', 'methodological validity', 'reported-data fidelity',
            'originality and plagiarism', 'AI research failure modes',
        ],
    }
    lines = [
        '# 学术完整性检查报告', '', f'## 总体结论：{status}', '',
        '## 可重复确定性检查',
        f'- 篇幅：{actual}/{target}（计量：{count_unit}，允许区间 {lower_bound}–{upper_bound}）',
        f'- 已注册证据：{len(available)}',
        f'- 正文已用证据：{len(cited & available)}',
        f'- 未知引用：{len(unknown)}',
        f'- 缺少已批准章节：{len(missing_sections)}',
        f'- 批准大纲主动省略的常规章节：{len(outline_omitted_sections)}',
        '',
        '## 语义检查边界',
        '- 本工具不声称自动完成事实真伪、主张与来源对齐、统计有效性或抄袭判定。',
        '- 后续审查必须基于具体段落和已注册来源给出 PASS/FAIL/UNKNOWN，不得把 UNKNOWN 当作 PASS。',
    ]
    if failures:
        lines.extend(['', '## 阻断项'] + [f'- {item}' for item in failures])
    if warnings:
        lines.extend(['', '## 警告'] + [f'- {item}' for item in warnings])
    return {'report': '\n'.join(lines), 'summary': summary}


def _artifact_payload(path: str) -> Any:
    source = Path(str(path or '')).expanduser().resolve()
    if not source.is_file():
        raise FileNotFoundError(f'Workflow input does not exist: {path}')
    text = source.read_text(encoding='utf-8')
    try:
        value = json.loads(text)
    except json.JSONDecodeError:
        return text
    if isinstance(value, dict):
        if 'data' in value:
            return value['data']
        if 'text' in value:
            return value['text']
    return value


def _remote_inputs() -> dict[str, Any]:
    from lazymind.chat.engine.subagent.context import require_context

    values = require_context().params.get('remote_inputs') or {}
    if not isinstance(values, dict):
        raise RuntimeError('Workflow input paths are unavailable in this Attempt.')
    return values


def _latest_manuscript(values: dict[str, Any]) -> str:
    for slot in ('second_revised_document', 'revised_document', 'draft_document'):
        path = values.get(slot)
        if isinstance(path, str) and path.strip():
            return str(_artifact_payload(path))
    raise ValueError('No manuscript artifact is available for integrity validation.')


def audit_academic_manuscript_from_inputs(stage: str = 'final') -> dict[str, Any]:
    """Audit the latest immutable Workflow manuscript without copying it through the model."""
    values = _remote_inputs()
    evidence = str(_artifact_payload(str(values.get('literature_evidence') or '')))
    parameters = _artifact_payload(str(values.get('generation_parameters') or ''))
    outline_path = str(values.get('outline_document') or '')
    approved_outline = str(_artifact_payload(outline_path)) if outline_path else ''
    return audit_academic_manuscript(
        _latest_manuscript(values), evidence,
        json.dumps(parameters, ensure_ascii=False), stage=stage,
        approved_outline_markdown=approved_outline,
    )
