"""Product-delivery adapters around LazyMind's shared Writer capability."""

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
    WriterRevisionToolkit,
)


MARKDOWN_HEADING = re.compile(r'^(#{1,6})\s+(.+?)\s*$')
EVIDENCE_ID = re.compile(r'\b(?:WEB|KB)-\d{3}\b')
EDITABLE_SLOTS = {
    'direction_outline', 'direction_document',
    'design_outline', 'design_document',
    'prd_outline', 'prd_document',
    'review_outline', 'review_document',
    'handoff_outline', 'handoff_document',
}

TEXT_STAGE_CONTRACTS: dict[str, dict[str, Any]] = {
    'direction': {
        'artifact_type': 'direction-brief',
        'source_skill': 'shape-product-direction',
        'default_target': 1800,
        'sections': [
            '一句话方向', '背景与证据', '目标用户与场景', '问题与核心任务',
            '目标与成功信号', '范围与非目标', '约束', '关键假设与验证',
            '关键决定与未决问题',
        ],
        'boundary': '只定义方向，不提前设计详细机制、页面、技术架构或排期。',
    },
    'design': {
        'artifact_type': 'product-design-spec',
        'source_skill': 'product-design-full-cycle',
        'default_target': 5000,
        'sections': [
            '结论与决策范围', '现状与证据边界', '对象关系与状态生命周期',
            '行为规则与权限', '信息架构与命名', '用户流程与异常恢复',
            '界面、状态与文案', '竞品启示的采用与拒绝', '验收场景',
            '风险、条件变化与未决问题',
        ],
        'boundary': '只决定产品机制和体验，不代替产品定位、技术架构或研发排期。',
    },
    'prd': {
        'artifact_type': 'prd',
        'source_skill': 'write-prd',
        'default_target': 6000,
        'sections': [
            '文档信息与结论摘要', '背景与目标', '范围与非目标', '用户、角色与权限',
            '领域模型', '状态模型', '用户流程', '功能需求', '页面与交互要求',
            '内容与通知', '数据与可观测性', '非功能约束', '验收标准',
            '依赖、风险与未决问题',
        ],
        'boundary': '表达已确认产品决定；不得静默发明核心机制、技术架构或排期承诺。',
    },
    'review': {
        'artifact_type': 'review-report',
        'source_skill': 'review-product-artifact',
        'default_target': 3000,
        'sections': [
            '评审对象、版本与范围', '评审结论', 'P0 阻断问题', 'P1 关键问题',
            'P2 改进建议', '已通过的关键检查', '未覆盖范围与剩余风险',
            '修复与复验清单',
        ],
        'boundary': '默认只读评审，不静默改写原产物，也不代替用户批准业务决定。',
    },
    'handoff': {
        'artifact_type': 'development-handoff',
        'source_skill': 'prepare-development-handoff',
        'default_target': 5000,
        'sections': [
            '就绪结论与阻塞摘要', '交付包索引', '版本基线', '范围与场景',
            '产品规格', '体验规格', '验收与追溯矩阵', '资源与依赖清单',
            '开放项与风险', '交付确认清单',
        ],
        'boundary': '判断产品信息是否就绪，不代替研发决定架构、工期或发布日期。',
    },
}

ARTIFACT_TITLES = {
    'direction': '产品方向说明',
    'design': '产品方案',
    'prd': '产品需求文档',
    'review': '产品方案评审报告',
    'handoff': '研发交付文档',
}

STAGE_ALIASES = {
    'direction': 'direction',
    'shape-product-direction': 'direction',
    'design': 'design',
    'product-design-full-cycle': 'design',
    'prd': 'prd',
    'write-prd': 'prd',
    'review': 'review',
    'review-product-artifact': 'review',
    'handoff': 'handoff',
    'prepare-development-handoff': 'handoff',
}

UPSTREAM_SLOTS = {
    'direction': (),
    'design': ('direction_document', 'competitive_analysis'),
    'prd': ('design_document',),
    'review': (
        'direction_document', 'competitive_analysis', 'design_document',
        'prd_document', 'prototype',
    ),
    'handoff': ('design_document', 'prd_document', 'prototype', 'review_document'),
}


def _workspace_root() -> Path:
    context = require_context()
    if not context.workspace_path:
        raise RuntimeError('The active Workflow workspace is unavailable.')
    root = Path(context.workspace_path)
    root.mkdir(parents=True, exist_ok=True)
    return root


def _run_root(name: str) -> Path:
    root = _workspace_root() / 'product-writer' / f'{name}-{uuid.uuid4().hex}'
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
        text = re.sub(
            r'^```(?:json)?\s*|\s*```$', '', str(current or '').strip(), flags=re.I,
        )
        if not text:
            return default
        try:
            current = json.loads(text)
        except json.JSONDecodeError:
            return current
    return current


def _json_content_or_path(value: Any, default: Any = None) -> Any:
    """Accept Workflow JSON content or the path of a JSON artifact."""
    if isinstance(value, (dict, list)):
        return _json_value(value, default)
    text = str(value or '').strip()
    if not text:
        return default
    if '\n' not in text and len(text) < 4096 and text[:1] not in {'{', '[', '"'}:
        candidate = Path(text).expanduser()
        try:
            if candidate.is_file():
                text = _read_text(str(candidate))
        except OSError:
            pass
    return _json_value(text, default)


def _text_content_or_path(value: Any) -> str:
    """Accept inline evidence text or the path returned by an earlier Workflow step."""
    text = str(value or '').strip()
    if '\n' not in text and len(text) < 4096 and ('/' in text or '\\' in text):
        candidate = Path(text).expanduser()
        try:
            if candidate.is_file():
                payload = _json_value(_read_text(str(candidate)), '')
                if isinstance(payload, str):
                    return payload.strip()
                return json.dumps(payload, ensure_ascii=False)
        except OSError:
            pass
    payload = _json_value(value, value)
    return payload if isinstance(payload, str) else json.dumps(payload, ensure_ascii=False)


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


def _stage_contract(stage_id: str) -> dict[str, Any]:
    stage = _normalize_stage_id(stage_id)
    if stage not in TEXT_STAGE_CONTRACTS:
        raise ValueError(f'stage_id must be one of {sorted(TEXT_STAGE_CONTRACTS)}.')
    return TEXT_STAGE_CONTRACTS[stage]


def _normalize_stage_id(stage_id: Any) -> str:
    raw = str(stage_id or '').strip().lower().replace('_', '-')
    return STAGE_ALIASES.get(raw, raw)


def _runtime_stage() -> str:
    context = require_context()
    step_id = str((context.params or {}).get('step_id') or '').strip().lower()
    match = re.match(
        r'(?:build_(direction|design|prd|review|handoff)_outline|'
        r'write_(direction|design|prd|review|handoff)_document)$',
        step_id,
    )
    return next((value for value in match.groups() if value), '') if match else ''


def _remote_inputs() -> dict[str, Any]:
    values = (require_context().params or {}).get('remote_inputs') or {}
    return values if isinstance(values, dict) else {}


def _remote_path_set() -> set[Path]:
    paths: set[Path] = set()

    def collect(value: Any) -> None:
        if isinstance(value, (list, tuple)):
            for item in value:
                collect(item)
            return
        if isinstance(value, dict):
            for key in ('path', 'value', 'data'):
                if key in value:
                    collect(value[key])
            return
        text = str(value or '').strip()
        if not text:
            return
        try:
            candidate = Path(text).expanduser().resolve()
            if candidate.is_file():
                paths.add(candidate)
        except OSError:
            return

    for remote_value in _remote_inputs().values():
        collect(remote_value)
    return paths


def _target_words(raw: str, default: int) -> int:
    match = re.search(r'\d+', str(raw or '').replace(',', ''))
    if not match:
        return default
    return max(300, int(match.group()))


def _source_paths(value: Any) -> list[str]:
    parsed = _json_content_or_path(value, [])
    if isinstance(parsed, dict):
        parsed = next((
            parsed[key] for key in ('file_list', 'paths', 'files', 'value')
            if key in parsed
        ), [])
    if not isinstance(parsed, list):
        raise ValueError('source_material_paths_json must be a JSON array.')
    workspace = _workspace_root().resolve()
    remote_paths = _remote_path_set()
    paths: list[str] = []
    pending = list(parsed)
    while pending:
        item = pending.pop(0)
        if isinstance(item, (list, tuple)):
            pending[0:0] = list(item)
            continue
        if isinstance(item, dict):
            pending[0:0] = [
                item[key] for key in ('path', 'value', 'data', 'file_list', 'paths', 'files')
                if key in item
            ]
            continue
        raw_path = item
        text = str(raw_path or '').strip()
        if text.startswith('file://'):
            text = text[7:]
        if not text:
            continue
        path = Path(text).expanduser().resolve()
        if not path.is_file():
            raise FileNotFoundError(f'Product source material does not exist: {path}')
        try:
            path.relative_to(workspace)
        except ValueError:
            if path not in remote_paths:
                raise ValueError(
                    'Source material must be in this workspace or an exact bound Workflow input.'
                )
        paths.append(str(path))
    return list(dict.fromkeys(paths))


def _preserve_business_contract(
    writing_context_json: str,
    stage_id: str,
    contract: dict[str, Any],
    research_evidence: str,
) -> str:
    context = _json_value(writing_context_json, {})
    if not isinstance(context, dict):
        context = {}
    facts = [
        fact for fact in list(context.get('facts') or [])
        if not isinstance(fact, dict) or fact.get('fact_id') != 'product-stage-contract'
    ]
    contract_value = {
        'stage_id': stage_id,
        'artifact_type': contract['artifact_type'],
        'source_skill': contract['source_skill'],
        'required_sections': contract['sections'],
        'boundary': contract['boundary'],
        'registered_evidence': str(research_evidence or '').strip(),
    }
    facts.append({
        'fact_id': 'product-stage-contract',
        'key': 'product_stage_contract_and_registered_evidence',
        # Shared Writer's DocumentFact schema requires a string value. Keep the
        # business contract structured without violating that generic contract.
        'value': json.dumps(contract_value, ensure_ascii=False),
        'source': ['product-solution-delivery', 'web_search', 'selected_knowledge_bases'],
        'applies_to': [],
        'locked': True,
    })
    context['facts'] = facts
    context.setdefault('meta', {})['product_stage_contract_preserved'] = True
    return json.dumps(context, ensure_ascii=False)


def _business_contract(context: Mapping[str, Any]) -> dict[str, Any]:
    for fact in list(context.get('facts') or []):
        if not isinstance(fact, dict) or fact.get('fact_id') != 'product-stage-contract':
            continue
        value = fact.get('value')
        if isinstance(value, dict):  # Compatibility with artifacts from older runs.
            return value
        try:
            parsed = _json_value(str(value or ''), {})
        except (TypeError, ValueError, json.JSONDecodeError):
            return {}
        return parsed if isinstance(parsed, dict) else {}
    return {}


def product_writer_prepare_context(
    user_request: str,
    stage_id: str,
    routing_record_json: Any = '',
    research_evidence: Any = '',
    source_material_paths_json: str = '[]',
    resource_profiles_path: str = '',
    upstream_artifact_paths_json: str = '[]',
    word_target: str = '',
) -> dict[str, str]:
    """Create Writer context from authoritative bindings for one product stage.

    Explicit arguments remain supported for tests and non-Workflow callers. During a
    Workflow step, omitted or empty values are resolved from immutable ``remote_inputs`` so
    the SubAgent never needs to read and re-serialize routing JSON or upstream path lists.
    """
    remote = _remote_inputs()
    runtime_stage = _runtime_stage()
    stage = runtime_stage or _normalize_stage_id(stage_id)
    contract = _stage_contract(stage)
    routing = _json_content_or_path(routing_record_json, {})
    if not isinstance(routing, dict) or not routing:
        routing = _json_content_or_path(remote.get('execution_plan'), {})
    if isinstance(routing, dict):
        nested_plan = next((
            routing[key] for key in ('execution_plan', 'plan', 'routing_record')
            if isinstance(routing.get(key), dict)
        ), None)
        if nested_plan is not None:
            routing = nested_plan
    if not isinstance(routing, dict):
        routing = {}
    if not str(research_evidence or '').strip():
        research_evidence = remote.get('research_evidence') or ''
    evidence = _text_content_or_path(research_evidence)
    approved_chain = routing.get('stage_chain') if isinstance(routing, dict) else None
    if isinstance(approved_chain, list) and approved_chain:
        normalized_chain = {_normalize_stage_id(item) for item in approved_chain}
        if stage not in normalized_chain:
            raise ValueError('stage_id must belong to execution_plan.stage_chain.')
    elif routing and _normalize_stage_id(routing.get('selected_stage')) != stage \
            and not runtime_stage:
        raise ValueError('stage_id must match the approved execution plan.')

    target_source: Any = word_target
    parsed_target = _json_content_or_path(word_target, word_target)
    if isinstance(parsed_target, dict):
        target_source = parsed_target.get('word_target')
    if not str(target_source or '').strip():
        target_source = routing.get('word_target')
    target = _target_words(str(target_source or ''), int(contract['default_target']))

    if not str(resource_profiles_path or '').strip():
        resource_profiles_path = remote.get('resource_profiles') or ''
    if _json_content_or_path(source_material_paths_json, []) in (None, []):
        bound_sources = [
            remote[slot] for slot in ('product_materials', 'reference_sample')
            if remote.get(slot)
        ]
        source_material_paths_json = bound_sources
    if _json_content_or_path(upstream_artifact_paths_json, []) in (None, []):
        upstream_artifact_paths_json = [
            remote[slot] for slot in UPSTREAM_SLOTS.get(stage, ()) if remote.get(slot)
        ]
    sections = '、'.join(str(item) for item in contract['sections'])
    query = f"""{str(user_request or '').strip()}

生成 {contract['artifact_type']}，来源业务合同为 {contract['source_skill']}。
建议篇幅约 {target} 个中文字符，可根据实际信息密度合理增多，不得因资料不足填充虚构事实。
初始大纲应覆盖：{sections}。{contract['boundary']}
正文必须区分已核验事实、用户陈述、推断、拟议决定、已接受决定和未知项。
引用检索资料时保留真实 WEB-NNN/KB-NNN 证据编号；不得伪造来源、现状、指标或接受状态。
用户批准大纲后，最新 Markdown 标题层级是唯一正文结构，不得从旧模板补回已删除章节。
样例仅影响结构、字段、语气和颗粒度，不得把样例业务事实当作当前项目事实。""".strip()

    toolkit = WriterCreateToolkit()
    task = _json_value(toolkit.build_writing_task(
        query=query,
        task_id=str(require_context().params.get('session_id') or uuid.uuid4().hex),
    ), {})
    if not isinstance(task, dict):
        task = {}
    task['output'] = {**dict(task.get('output') or {}), 'representation': 'markdown'}
    task['product_parameters'] = {
        'stage_id': stage,
        'artifact_type': contract['artifact_type'],
        'source_skill': contract['source_skill'],
        'word_target': target,
    }
    task_json = json.dumps(task, ensure_ascii=False)
    profiles_value: list[Any] = []
    profiled_paths: set[str] = set()
    if str(resource_profiles_path or '').strip():
        stored_profiles = _json_content_or_path(resource_profiles_path, [])
        if isinstance(stored_profiles, dict):
            stored_profiles = next((
                stored_profiles[key] for key in ('profiles', 'resource_profiles', 'data')
                if isinstance(stored_profiles.get(key), list)
            ), [])
        if not isinstance(stored_profiles, list):
            stored_profiles = []
        profiles_value.extend(stored_profiles)
    else:
        paths = _source_paths(source_material_paths_json)
        profiled_paths.update(paths)
        resources = toolkit.build_resources(
            file_paths_json=json.dumps(paths, ensure_ascii=False),
            knowledge_text=evidence,
        )
        generated_profiles = _json_value(toolkit.profile_resources(
            writing_task_json=task_json,
            user_input=query,
            resources_json=resources,
        ), [])
        if isinstance(generated_profiles, dict):
            generated_profiles = generated_profiles.get('profiles') or []
        if not isinstance(generated_profiles, list):
            generated_profiles = []
        profiles_value.extend(generated_profiles)

    upstream_paths = [
        path for path in _source_paths(upstream_artifact_paths_json)
        if path not in profiled_paths
    ]
    if upstream_paths:
        upstream_resources = toolkit.build_resources(
            file_paths_json=json.dumps(upstream_paths, ensure_ascii=False),
        )
        upstream_profiles = _json_value(toolkit.profile_resources(
            writing_task_json=task_json,
            user_input=(
                '提取这些已批准上游产品产物中的目标、范围、对象、规则、状态、流程、'
                '验收、决定状态、版本和未决项，作为当前阶段的受约束输入。'
            ),
            resources_json=upstream_resources,
        ), [])
        if isinstance(upstream_profiles, dict):
            upstream_profiles = upstream_profiles.get('profiles') or []
        if not isinstance(upstream_profiles, list):
            upstream_profiles = []
        profiles_value.extend(upstream_profiles)

    profiles = json.dumps(profiles_value, ensure_ascii=False)
    writing_context = toolkit.create_writing_context(
        writing_task_json=task_json,
        resource_profiles_json=profiles,
    )
    writing_context = _preserve_business_contract(
        writing_context, stage, contract, evidence,
    )
    root = _run_root('prepare')
    return {
        'writing_task': _write_json(root, 'writing_task', task_json),
        'resource_profiles': _write_json(root, 'resource_profiles', profiles),
        'writing_context': _write_json(root, 'writing_context', writing_context),
    }


def profile_product_materials(
    user_request: str,
    source_material_paths_json: str = '[]',
) -> dict[str, Any]:
    """Read workflow-bound product files through Writer's shared resource profiler."""
    paths = _source_paths(source_material_paths_json)
    profiles: Any = []
    if paths:
        toolkit = WriterCreateToolkit()
        task_json = toolkit.build_writing_task(
            query=(
                '仅分析这些产品材料中明确存在的目标、范围、对象、规则、状态、流程、'
                '验收、版本和未知项，不补写材料中没有的事实。\n\n'
                + str(user_request or '').strip()
            ),
            task_id=str(require_context().params.get('session_id') or uuid.uuid4().hex),
        )
        resources = toolkit.build_resources(
            file_paths_json=json.dumps(paths, ensure_ascii=False),
        )
        profiles = _json_value(toolkit.profile_resources(
            writing_task_json=task_json,
            user_input=str(user_request or '').strip(),
            resources_json=resources,
        ), [])
    root = _run_root('resource-profiles')
    return {
        'profiles': profiles if isinstance(profiles, list) else [],
        'resource_profiles': _write_json(
            root, 'resource_profiles', profiles if isinstance(profiles, list) else [],
        ),
        'files': [Path(path).name for path in paths],
        'message': (
            'Profiles contain model-extracted summaries; unresolved details remain unknown.'
            if paths else 'No product material files were bound.'
        ),
    }


def product_writer_generate_outline(
    writing_task_path: str,
    writing_context_path: str,
) -> str:
    task = _json_value(_read_text(writing_task_path), {})
    if not isinstance(task, dict):
        task = {}
    stage = str((task.get('product_parameters') or {}).get('stage_id') or '')
    try:
        generated = WriterCreateToolkit().generate_outline(
            writing_task_json=_read_json(writing_task_path),
            writing_context_json=_read_json(writing_context_path),
        )
    except Exception:  # A canonical editable outline is safer than failing the stage.
        generated = ''
    try:
        parsed = _json_value(generated, None)
    except json.JSONDecodeError:
        parsed = generated
    if isinstance(parsed, dict):
        parsed = next((
            parsed.get(key) for key in ('outline_document', 'outline', 'markdown', 'content')
            if isinstance(parsed.get(key), str) and parsed.get(key).strip()
        ), '')
    if not isinstance(parsed, str) or not parsed.strip():
        parsed = ''
    normalized = _normalize_generated_outline(stage, parsed)
    return _write_markdown(_run_root('outline'), 'outline_document', normalized)


def product_writer_read_markdown(document_path: str) -> str:
    path = Path(str(document_path or ''))
    if path.suffix.lower() not in {'.md', '.markdown', '.txt'}:
        raise ValueError('The product Writer bridge accepts Markdown artifacts only.')
    return _read_text(str(path))


def _heading_signature(markdown: str) -> list[tuple[int, str]]:
    return [
        (len(match.group(1)), match.group(2).strip())
        for line in str(markdown or '').splitlines()
        if (match := MARKDOWN_HEADING.match(line.strip()))
    ]


def _normalize_generated_outline(stage_id: str, markdown: str) -> str:
    """Recover a usable relative Markdown hierarchy from imperfect model output."""
    contract = _stage_contract(stage_id)
    text = re.sub(
        r'^```(?:markdown|md)?\s*|\s*```$', '', str(markdown or '').strip(), flags=re.I,
    )
    headings = _heading_signature(text)
    if headings:
        has_title = headings[0][0] == 1
        if has_title:
            title = headings[0][1]
            body = headings[1:]
        else:
            title = ARTIFACT_TITLES[stage_id]
            body = headings
        if not body:
            return '\n\n'.join([
                f'# {title}',
                *[f'## {section}' for section in contract['sections']],
            ])
        base = min(level for level, _ in body)
        normalized_body: list[tuple[int, str]] = []
        previous = 1
        for raw_level, heading_title in body:
            candidate = max(2, raw_level - base + 2)
            level = min(5, candidate, previous + 1)
            normalized_body.append((level, heading_title))
            previous = level
        replacements = iter(normalized_body)
        output = [f'# {title}', ''] if not has_title else []
        heading_index = 0
        for line in text.splitlines():
            match = MARKDOWN_HEADING.match(line.strip())
            if not match:
                output.append(line)
                continue
            if has_title and heading_index == 0:
                output.append(f'# {title}')
            else:
                level, heading_title = next(replacements)
                output.append(f"{'#' * level} {heading_title}")
            heading_index += 1
        return '\n'.join(output).strip()
    else:
        candidates = []
        for line in text.splitlines():
            value = re.sub(r'^\s*(?:[-*+] |\d+[.)、]\s*)', '', line).strip()
            if value and len(value) <= 80:
                candidates.append(value)
        body_titles = candidates or list(contract['sections'])
        return '\n\n'.join([
            f'# {ARTIFACT_TITLES[stage_id]}',
            *[f'## {item}' for item in body_titles],
        ])


def _normalize_approved_outline(stage_id: str, markdown: str) -> str:
    """Normalize user-approved structure without restoring deleted template sections."""
    text = re.sub(
        r'^```(?:markdown|md)?\s*|\s*```$', '', str(markdown or '').strip(), flags=re.I,
    )
    headings = _heading_signature(text)
    if len(headings) >= 2 or (headings and headings[0][0] != 1):
        return _normalize_generated_outline(stage_id, text)
    if headings:
        title = headings[0][1]
        non_heading = '\n'.join(
            line for line in text.splitlines()
            if not MARKDOWN_HEADING.match(line.strip())
        ).strip()
    else:
        title = ARTIFACT_TITLES[stage_id]
        non_heading = text
    body = '## 正文'
    if non_heading:
        body += f'\n\n{non_heading}'
    return f'# {title}\n\n{body}'.strip()


def validate_product_outline(stage_id: str, outline_markdown: str) -> dict[str, Any]:
    """Validate structure deterministically while leaving content decisions to the user."""
    contract = _stage_contract(stage_id)
    headings = _heading_signature(outline_markdown)
    errors: list[str] = []
    warnings: list[str] = []
    if not headings or headings[0][0] != 1:
        errors.append('大纲必须以一个 H1 文档标题开始')
    if sum(1 for level, _ in headings if level == 1) != 1:
        errors.append('大纲必须且只能包含一个 H1 标题')
    if not any(level == 2 for level, _ in headings):
        errors.append('大纲至少需要一个 H2 正文章节')
    previous = 0
    for index, (level, title) in enumerate(headings, 1):
        if not title:
            errors.append(f'第 {index} 个标题为空')
        if previous and level > previous + 1:
            errors.append(f'标题“{title}”发生层级跳跃')
        previous = level
    normalized_titles = ''.join(title.lower() for _, title in headings)
    missing = [
        title for title in contract['sections']
        if re.sub(r'[\s、，,：:与和/]', '', title.lower())
        not in re.sub(r'[\s、，,：:与和/]', '', normalized_titles)
    ]
    if missing:
        warnings.append('建议确认原业务合同章节：' + '、'.join(missing))
    status = 'PASS' if not errors else 'FAIL'
    report = [
        '# 产品文档大纲检查', '', f'## {status}', '',
        f'- 业务阶段：{stage_id}',
        f'- H2 章节数：{sum(1 for level, _ in headings if level == 2)}',
        '- 结构来源：当前可编辑 Markdown（唯一权威）',
    ]
    if errors:
        report.extend(['', '## 必须修复', *[f'- {item}' for item in errors]])
    if warnings:
        report.extend(['', '## 建议确认', *[f'- {item}' for item in warnings]])
    return {'valid': not errors, 'errors': errors, 'warnings': warnings, 'report': '\n'.join(report)}


def product_writer_update_context(content_path: str, writing_context_path: str) -> str:
    original = _json_value(_read_text(writing_context_path), {})
    try:
        updated = _json_value(WriterCreateToolkit().update_writing_context(
            content_artifact_json=_read_text(content_path),
            writing_context_json=json.dumps(original, ensure_ascii=False),
        ), {})
        if not isinstance(updated, dict):
            raise ValueError('Writer returned a non-object writing context.')
    except Exception as exc:  # Context refresh must not discard a completed artifact.
        updated = dict(original) if isinstance(original, dict) else {}
        meta = updated.get('meta')
        if not isinstance(meta, dict):
            meta = {}
            updated['meta'] = meta
        meta['context_update_warning'] = str(exc)[:500]
    return _write_json(_run_root('context'), 'writing_context', updated)


def _required_bound_file(slot: str) -> str:
    """Resolve one immutable Workflow input without asking the SubAgent to read it."""
    value = _remote_inputs().get(slot)
    paths = _source_paths([value]) if value not in (None, '', []) else []
    if not paths:
        raise ValueError(f"Required Workflow input '{slot}' is missing or is not a file artifact.")
    return paths[0]


def product_writer_generate_outline_from_inputs(
    user_request: str,
    stage_id: str,
) -> dict[str, Any]:
    """Generate one canonical outline without exposing Workflow file bindings to the Agent."""
    stage = _runtime_stage() or _normalize_stage_id(stage_id)
    prepared = product_writer_prepare_context(user_request=user_request, stage_id=stage)
    outline = product_writer_generate_outline(
        prepared['writing_task'], prepared['writing_context'],
    )
    validation = validate_product_outline(stage, _read_text(outline))
    if not validation['valid']:
        raise ValueError(
            'Generated product outline is structurally invalid after normalization: '
            + '; '.join(validation['errors'])
        )
    approved_context = product_writer_update_context(outline, prepared['writing_context'])
    return {
        'writing_task': prepared['writing_task'],
        'writing_context': prepared['writing_context'],
        'outline': outline,
        'outline_report': validation['report'],
        'approved_context': approved_context,
        'warnings': list(validation.get('warnings') or []),
    }


def product_writer_generate_document_from_inputs(stage_id: str) -> dict[str, Any]:
    """Run the shared Writer pipeline directly from authoritative bound artifacts.

    The shared Writer still emits section deltas and resumes its stage checkpoint. This business
    adapter only removes error-prone file lookup and intermediate-path plumbing from the SubAgent.
    """
    stage = _runtime_stage() or _normalize_stage_id(stage_id)
    _stage_contract(stage)
    task_path = _required_bound_file(f'{stage}_task')
    outline_path = _required_bound_file(f'{stage}_outline')
    context_path = _required_bound_file(f'{stage}_context_approved')

    plan = product_writer_plan_sections(task_path, outline_path, context_path)
    section_plan = str(plan['section_instructions'])
    chapters = product_writer_write_sections(task_path, section_plan, context_path)
    document = product_writer_assemble_draft(chapters[0], context_path, outline_path)
    final_context = product_writer_update_context(document, context_path)
    return {
        'section_plan': section_plan,
        'chapter_files': chapters,
        'document': document,
        'writing_context': final_context,
        'warnings': list(plan.get('warnings') or []),
    }


def product_writer_plan_sections(
    writing_task_path: str,
    outline_document_path: str,
    writing_context_path: str,
) -> dict[str, Any]:
    """Build a stable Writer section contract from the approved Markdown outline."""
    task = _json_value(_read_text(writing_task_path), {})
    if not isinstance(task, dict):
        task = {}
    stage = str((task.get('product_parameters') or {}).get('stage_id') or '')
    outline = _normalize_approved_outline(stage, _read_text(outline_document_path))
    context = _json_value(_read_text(writing_context_path), {})
    if not isinstance(context, dict):
        context = {}
    validation = validate_product_outline(stage, outline)
    if not validation['valid']:
        raise ValueError('Approved product outline is structurally invalid: ' + '; '.join(validation['errors']))
    headings = _heading_signature(outline)
    document_title = next((title for level, title in headings if level == 1), ARTIFACT_TITLES[stage])
    contract = _business_contract(context)
    instructions: list[dict[str, Any]] = []
    for index, (level, title) in enumerate(headings):
        if level != 2:
            continue
        descendants: list[tuple[int, str]] = []
        for child_level, child_title in headings[index + 1:]:
            if child_level <= 2:
                break
            descendants.append((child_level, child_title))
        expected = [child_title for _, child_title in descendants] or [title]
        structure = '；'.join(
            f"输出 `{'#' * child_level} {child_title}`"
            for child_level, child_title in descendants
        ) or '直接完成本 H2 章节正文。'
        instructions.append({
            'instruction_id': f'product-{stage}-section-{len(instructions) + 1}',
            'content_ref': {'heading_path': [document_title, title]},
            'section_title': title,
            'section_goal': f'依据写作任务、批准大纲和已注册证据完成“{title}”章节。',
            'required_points': [
                '标题硬约束：正文必须以当前 section_title 作为唯一 H2 根标题。',
                f'子结构指引：{structure}',
                (
                    '证据约束：只能使用写作上下文中的用户材料和已注册 WEB-NNN/KB-NNN；'
                    '区分事实、推断、建议和未知，不得虚构来源或接受状态。'
                ),
                '不得增加批准大纲之外的 H1/H2 章节。',
            ],
            'fact_constraints': ['不得虚构来源、指标、接受状态或业务事实。'],
            'style_constraints': ['使用正式、清晰、可执行的产品文档语言，避免空泛套话。'],
            'expected_blocks': expected,
            'meta': {
                'product_stage': stage,
                'source_skill': str(contract.get('source_skill') or ''),
                'deterministically_normalized': True,
            },
        })
    fingerprint = hashlib.sha256(json.dumps(
        instructions, ensure_ascii=False, sort_keys=True,
    ).encode('utf-8')).hexdigest()[:20]
    instructions_doc = {
        'instruction_set_id': f'product-{stage}-{fingerprint}',
        'instructions': instructions,
        'meta': {
            'representation': 'markdown',
            'document_title': document_title,
            'deterministically_normalized': True,
        },
    }
    root = _run_root('section-plan')
    return {
        'section_instructions': _write_json(root, 'section_instructions', instructions_doc),
        'warnings': [],
    }


def product_writer_write_sections(
    writing_task_path: str,
    section_instructions_path: str,
    writing_context_path: str,
) -> list[str]:
    """Stream or resume product-document sections using shared Writer checkpoints."""
    ctx = require_context()
    task_json = _read_json(writing_task_path)
    task = _json_value(task_json, {})
    if not isinstance(task, dict):
        task = {}
    stage = str((task.get('product_parameters') or {}).get('stage_id') or 'unknown')
    events = DraftMarkdownStreamEventEmitter(ctx.emit, slot=f'{stage}_document')
    try:
        sections = _json_value(WriterCreateToolkit().stream_draft_blocks_markdown(
            writing_task_json=task_json,
            section_instructions_json=_read_json(section_instructions_path),
            writing_context_json=_read_json(writing_context_path),
            on_delta=events.feed,
            on_section_end=events.flush,
            on_progress=lambda payload: ctx.emit({'type': 'progress', **payload}),
            checkpoint_dir=str(
                _workspace_root() / 'product-writer' / 'draft-checkpoints' / stage
            ),
        ), [])
        if not isinstance(sections, list) or not sections:
            plan = _json_value(_read_text(section_instructions_path), {})
            instructions = list(plan.get('instructions') or []) if isinstance(plan, dict) else []
            sections = [
                '## {}\n\n本节生成未返回有效正文，请在审批时补充。'.format(
                    str(item.get('section_title') or '待补充章节').strip(),
                )
                for item in instructions if isinstance(item, dict)
            ]
        if not sections:
            sections = ['## 待补充章节\n\n本阶段生成未返回有效正文，请在审批时补充。']
        root = _run_root('draft-sections')
        paths = [
            _write_markdown(root, f'draft_section_{index:04d}', str(section))
            for index, section in enumerate(sections, 1)
        ]
    except Exception as exc:
        events.abort(str(exc))
        raise
    events.end()
    return paths


def _normalized_title(value: str) -> str:
    return re.sub(r'[\s\W_]+', '', str(value or ''), flags=re.UNICODE).lower()


def _align_draft_headings(draft: str, outline: str) -> str:
    """Preserve approved headings and demote model-added headings without losing prose."""
    approved = _heading_signature(outline)
    indices_by_title: dict[str, list[int]] = {}
    for index, (_, title) in enumerate(approved):
        indices_by_title.setdefault(_normalized_title(title), []).append(index)
    used: set[int] = set()
    chunks: dict[int, list[str]] = {index: [] for index in range(len(approved))}
    preamble: list[str] = []
    current: int | None = None
    for line in str(draft or '').splitlines():
        match = MARKDOWN_HEADING.match(line.strip())
        if not match:
            (chunks[current] if current is not None else preamble).append(line)
            continue
        key = _normalized_title(match.group(2))
        candidate = next((index for index in indices_by_title.get(key, []) if index not in used), None)
        if candidate is None:
            (chunks[current] if current is not None else preamble).append(
                f'**{match.group(2).strip()}**',
            )
            continue
        current = candidate
        used.add(candidate)

    rendered: list[str] = []
    for index, (level, title) in enumerate(approved):
        if rendered:
            rendered.append('')
        rendered.append(f"{'#' * level} {title}")
        content = list(chunks[index])
        if index == 0 and preamble:
            content = [*preamble, *content]
        if content:
            rendered.extend(content)
        elif level > 1:
            rendered.extend(['', '本节尚未生成有效正文，请在审批时补充。'])
    return '\n'.join(rendered).strip()


def product_writer_assemble_draft(
    draft_sections_anchor_path: str,
    writing_context_path: str,
    outline_document_path: str,
) -> str:
    anchor = Path(str(draft_sections_anchor_path or '')).resolve()
    directory = anchor if anchor.is_dir() else anchor.parent
    paths = sorted(directory.glob('draft_section_*.md'))
    if not paths:
        raise ValueError('No Writer product section files were found.')
    raw_outline = _read_text(outline_document_path)
    context = _json_value(_read_text(writing_context_path), {})
    if not isinstance(context, dict):
        context = {}
    stage = str(_business_contract(context).get('stage_id') or '')
    outline = _normalize_approved_outline(stage, raw_outline)
    section_markdown = [_read_text(str(path)) for path in paths]
    try:
        payload = _json_value(WriterCreateToolkit().generate_draft_document_markdown(
            draft_sections_json=json.dumps(section_markdown, ensure_ascii=False),
            writing_context_json=_read_json(writing_context_path),
            outline_json=outline,
            title='',
        ), {})
        generated = str(payload.get('draft_document') or '').strip()
    except Exception:
        generated = ''
    draft = _align_draft_headings(generated or '\n\n'.join(section_markdown), outline)
    return _write_markdown(_run_root('draft-document'), 'draft_document', draft)


def product_writer_revise_markdown(
    base_document_path: str,
    writing_context_path: str,
    instruction: str,
    document_slot: str,
) -> dict[str, str]:
    if document_slot not in EDITABLE_SLOTS:
        raise ValueError(f'document_slot must be one of {sorted(EDITABLE_SLOTS)}.')
    document = _read_text(base_document_path)
    toolkit = WriterRevisionToolkit()
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
    if document_slot.endswith('_outline'):
        revised = _normalize_approved_outline(document_slot.removesuffix('_outline'), revised)
    root = _run_root(f'revise-{document_slot}')
    return {
        'revision_task': _write_json(root, 'revision_task', revision_task),
        'locate_result': _write_json(root, 'locate_result', locate),
        'modify_plan': _write_json(root, 'modify_plan', plan),
        'revision_set': _write_json(root, 'revision_set', replace_set),
        'revision_result': _write_json(root, 'revision_result', applied),
        document_slot: _write_markdown(root, document_slot, revised),
    }


def product_writer_preview_selection_rewrite(
    artifact: Any,
    instruction: str,
    selection: Mapping[str, Any],
    artifact_store: str = '',
    slot: str = '',
) -> dict[str, Any]:
    if slot not in EDITABLE_SLOTS:
        raise ValueError('Selection rewrite is supported only for product Writer documents.')
    if str((selection or {}).get('type') or '') != 'markdown':
        raise ValueError("The product workflow requires selection.type='markdown'.")
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
    root = root / 'product-writer-selection' / uuid.uuid4().hex
    root.mkdir(parents=True, exist_ok=True)
    context = {'context_id': f'product-selection-{uuid.uuid4().hex}', 'meta': {'slot': slot}}
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
