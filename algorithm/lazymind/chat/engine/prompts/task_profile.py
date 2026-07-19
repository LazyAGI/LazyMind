from __future__ import annotations

import json
import re
import time
from dataclasses import asdict, dataclass, replace
from typing import Any, Callable, Literal


Outcome = Literal['answer', 'learn', 'research', 'decide', 'plan', 'create', 'execute', 'diagnose']
Complexity = Literal['simple', 'compound', 'open_ended']
Freshness = Literal['stable', 'current', 'unknown']
Deliverable = Literal[
    'direct_answer', 'tutorial', 'research_report', 'comparison', 'decision_brief',
    'action_plan', 'diagnostic_report', 'artifact', 'execution_result',
]
SkillMode = Literal['suppress', 'candidates', 'explicit']

OUTCOMES = {'answer', 'learn', 'research', 'decide', 'plan', 'create', 'execute', 'diagnose'}
COMPLEXITIES = {'simple', 'compound', 'open_ended'}
FRESHNESS = {'stable', 'current', 'unknown'}
DELIVERABLES = {
    'direct_answer', 'tutorial', 'research_report', 'comparison', 'decision_brief',
    'action_plan', 'diagnostic_report', 'artifact', 'execution_result',
}
SKILL_MODES = {'suppress', 'candidates', 'explicit'}


@dataclass(frozen=True)
class TaskProfile:
    primary_outcome: Outcome = 'answer'
    secondary_outcomes: tuple[Outcome, ...] = ()
    complexity: Complexity = 'simple'
    freshness: Freshness = 'stable'
    research_required: bool = False
    deliverable_kind: Deliverable = 'direct_answer'
    secondary_deliverables: tuple[Deliverable, ...] = ()
    skill_mode: SkillMode = 'suppress'
    confidence: float = 1.0
    reasons: tuple[str, ...] = ()
    source: Literal['rules', 'llm', 'fallback'] = 'rules'
    router_latency_ms: int = 0
    router_error: str = ''

    def to_trace_dict(self) -> dict[str, Any]:
        result = asdict(self)
        result.pop('router_error', None)
        return result


_SIGNALS: tuple[tuple[Outcome, re.Pattern[str]], ...] = (
    ('learn', re.compile(r'教我|入门|学会|从零(?:到一|开始)|零基础|教程|how\s+to|teach\s+me|learn', re.I)),
    ('research', re.compile(r'调研|调查|研究一下|深入研究|资料汇总|research|investigate|deep\s+dive', re.I)),
    ('decide', re.compile(
        r'怎么选|如何选|值不值得|哪个好|哪一个好|是否应该|要不要|该不该|.{1,20}还是.{1,20}|'
        r'compare|versus|\bvs\b|should\s+i', re.I,
    )),
    ('plan', re.compile(r'制定.{0,8}计划|规划|路线图|实施步骤|行动方案|roadmap|action\s+plan', re.I)),
    ('diagnose', re.compile(
        r'为什么.{0,20}(?:失败|不行|很差|变慢|下降)|怎么排查|排障|定位.{0,20}问题|diagnos|troubleshoot', re.I,
    )),
    ('execute', re.compile(
        r'替我|帮我(?:发送|发布|修改|运行|删除|安装|部署)|直接(?:发送|发布|修改|运行|部署)|execute|deploy\s+it', re.I,
    )),
    ('create', re.compile(r'创建|生成|写一份|制作一份|产出|create|generate|draft', re.I)),
)
_CURRENT = re.compile(
    r'最新|现在|当前|今年|近期|主流|价格|政策|法规|排名|榜单|可用|202[4-9]|latest|current|today|recent|price', re.I,
)
_EXPLICIT_WEB = re.compile(r'联网|网上搜索|搜索资料|查资料|查一下|web\s+search|browse|look\s+up', re.I)
_SKILL_EXPLICIT = re.compile(r'skill|技能(?:库|包|文件|管理)?|SKILL\.md', re.I)
_OPEN_ENDED = re.compile(r'如何|怎么|有哪些|帮我看看|给我.*方案|what\s+should|how\s+can', re.I)
_SIMPLE_FACT = re.compile(r'^(?:什么是|解释一下|定义|谁是|多少|what\s+is|define)', re.I)

_DELIVERABLE_BY_OUTCOME: dict[Outcome, Deliverable] = {
    'answer': 'direct_answer',
    'learn': 'tutorial',
    'research': 'research_report',
    'decide': 'decision_brief',
    'plan': 'action_plan',
    'create': 'artifact',
    'execute': 'execution_result',
    'diagnose': 'diagnostic_report',
}


def _rule_profile(query: str) -> tuple[TaskProfile, bool]:
    text = str(query or '').strip()
    matches = [outcome for outcome, pattern in _SIGNALS if pattern.search(text)]
    if re.search(r'(?:如何|怎么).{0,12}(?:制作|搭建|学习|学|做出|使用)|how\s+(?:do|can)\s+i', text, re.I):
        matches.insert(0, 'learn')
    matches = list(dict.fromkeys(matches))
    explicit_skill = bool(_SKILL_EXPLICIT.search(text))
    current = bool(_CURRENT.search(text) or _EXPLICIT_WEB.search(text))
    # Fast-moving AI product/how-to requests require current evidence even without "latest".
    ai_how_to = bool(re.search(r'(?:AI|人工智能|大模型).{0,12}(?:视频|工具|产品|平台)', text, re.I))
    current = current or ai_how_to
    if current and re.search(r'有哪些|主流|现状|发展到哪|landscape', text, re.I):
        matches.insert(0, 'research')
    matches = list(dict.fromkeys(matches))

    if matches:
        primary = matches[0]
        secondary = tuple(matches[1:2])
    else:
        primary, secondary = 'answer', ()

    is_simple_fact = bool(_SIMPLE_FACT.search(text)) and not matches and not current
    open_ended = bool(_OPEN_ENDED.search(text)) and not is_simple_fact
    complexity: Complexity = 'compound' if len(matches) > 1 else 'open_ended' if open_ended else 'simple'
    confidence = 0.92 if matches or is_simple_fact else 0.55 if open_ended else 0.8
    deliverable = _DELIVERABLE_BY_OUTCOME[primary]
    secondary_deliverables = tuple(_DELIVERABLE_BY_OUTCOME[item] for item in secondary)
    research_required = current or primary == 'research'
    skill_mode: SkillMode = 'explicit' if explicit_skill else (
        'suppress' if primary == 'learn' or is_simple_fact else 'candidates'
    )
    reasons = []
    if matches:
        reasons.append('explicit outcome wording')
    if current:
        reasons.append('current-information signal')
    if explicit_skill:
        reasons.append('explicit skill wording')
    if is_simple_fact:
        reasons.append('simple factual form')

    profile = TaskProfile(
        primary_outcome=primary,
        secondary_outcomes=secondary,
        complexity=complexity,
        freshness='current' if current else 'stable' if is_simple_fact else 'unknown',
        research_required=research_required,
        deliverable_kind=deliverable,
        secondary_deliverables=secondary_deliverables,
        skill_mode=skill_mode,
        confidence=confidence,
        reasons=tuple(reasons[:4]),
    )
    needs_llm = confidence < 0.75 or len(matches) > 1
    return profile, needs_llm


_CLASSIFIER_PROMPT = '''Classify the user's desired outcome. Return JSON only, with keys:
primary_outcome, secondary_outcomes, complexity, freshness, research_required,
deliverable_kind, secondary_deliverables, skill_mode, confidence, reasons.
Use only the allowed enum values supplied in this schema:
primary_outcome/secondary_outcomes: answer, learn, research, decide, plan, create, execute, diagnose;
complexity: simple, compound, open_ended; freshness: stable, current, unknown;
deliverable_kind/secondary_deliverables: direct_answer, tutorial, research_report, comparison,
decision_brief, action_plan, diagnostic_report, artifact, execution_result;
skill_mode: suppress, candidates, explicit. Use suppress for ordinary learning/how-to requests.
Reasons must be short observable labels, not private reasoning. Maximum two secondary items and four reasons.'''


def _classifier_input(query: str, history: list[dict] | None, intent: Any) -> str:
    recent = [
        str(item.get('content') or '')[:1000]
        for item in (history or []) if isinstance(item, dict) and item.get('role') == 'user'
    ][-3:]
    return (
        f'{_CLASSIFIER_PROMPT}\n\nExplicit conversation intent:\n'
        f'{json.dumps(intent or {}, ensure_ascii=False)[:2000]}\n\n'
        f'Recent user messages:\n{json.dumps(recent, ensure_ascii=False)}\n\n'
        f'Current request:\n{query[:3000]}'
    )


def _extract_json(value: Any) -> dict[str, Any]:
    text = str(value or '').strip()
    fenced = re.search(r'```(?:json)?\s*([\s\S]*?)```', text, re.I)
    if fenced:
        text = fenced.group(1).strip()
    start, end = text.find('{'), text.rfind('}')
    if start < 0 or end <= start:
        raise ValueError('classifier returned no JSON object')
    raw = json.loads(text[start:end + 1])
    if not isinstance(raw, dict):
        raise ValueError('classifier JSON must be an object')
    return raw


def _validate_llm_profile(raw: dict[str, Any], rule: TaskProfile) -> TaskProfile:
    primary = str(raw.get('primary_outcome') or '')
    complexity = str(raw.get('complexity') or '')
    freshness = str(raw.get('freshness') or '')
    deliverable = str(raw.get('deliverable_kind') or '')
    skill_mode = str(raw.get('skill_mode') or '')
    if primary not in OUTCOMES or complexity not in COMPLEXITIES or freshness not in FRESHNESS:
        raise ValueError('classifier returned an invalid task enum')
    if deliverable not in DELIVERABLES or skill_mode not in SKILL_MODES:
        raise ValueError('classifier returned an invalid delivery enum')
    secondary = tuple(str(x) for x in (raw.get('secondary_outcomes') or [])[:2])
    secondary_deliverables = tuple(str(x) for x in (raw.get('secondary_deliverables') or [])[:2])
    if any(x not in OUTCOMES for x in secondary) or any(x not in DELIVERABLES for x in secondary_deliverables):
        raise ValueError('classifier returned an invalid secondary enum')
    reasons = tuple(str(x).strip()[:80] for x in (raw.get('reasons') or [])[:4] if str(x).strip())
    confidence = min(1.0, max(0.0, float(raw.get('confidence', 0.5))))
    # Explicit freshness and skill wording are authoritative deterministic signals.
    if rule.freshness == 'current':
        freshness = 'current'
    if rule.skill_mode == 'explicit':
        skill_mode = 'explicit'
    return TaskProfile(
        primary_outcome=primary, secondary_outcomes=secondary, complexity=complexity,
        freshness=freshness, research_required=bool(raw.get('research_required')) or rule.research_required,
        deliverable_kind=deliverable, secondary_deliverables=secondary_deliverables,
        skill_mode=skill_mode, confidence=confidence, reasons=reasons, source='llm',
    )


def resolve_task_profile(
    query: str,
    *,
    history: list[dict] | None = None,
    intent: Any = None,
    classifier: Callable[[str], Any] | None = None,
    enable_llm_fallback: bool = True,
) -> TaskProfile:
    rule, needs_llm = _rule_profile(query)
    if not needs_llm or not enable_llm_fallback or classifier is None:
        return rule
    started = time.monotonic()
    try:
        result = classifier(_classifier_input(query, history, intent))
        profile = _validate_llm_profile(_extract_json(result), rule)
        return replace(profile, router_latency_ms=int((time.monotonic() - started) * 1000))
    except Exception as exc:
        return fallback_task_profile(
            query,
            error=exc,
            latency_ms=int((time.monotonic() - started) * 1000),
        )


def fallback_task_profile(query: str, *, error: Any, latency_ms: int = 0) -> TaskProfile:
    rule, _ = _rule_profile(query)
    return replace(
        rule,
        primary_outcome='answer',
        secondary_outcomes=(),
        deliverable_kind='direct_answer',
        secondary_deliverables=(),
        skill_mode='suppress',
        source='fallback',
        router_latency_ms=max(0, int(latency_ms)),
        router_error=f'{type(error).__name__}: {error}'[:240],
    )


def selected_prompt_modules(profile: TaskProfile) -> list[str]:
    modules = []
    outcomes = {profile.primary_outcome, *profile.secondary_outcomes}
    if 'learn' in outcomes:
        modules.append('learning')
    if profile.research_required or profile.freshness == 'current':
        modules.append('fresh_research')
    if outcomes.intersection({'decide', 'plan'}):
        modules.append('decision_planning')
    if not (profile.complexity == 'simple' and profile.deliverable_kind == 'direct_answer'):
        modules.extend([profile.deliverable_kind, *profile.secondary_deliverables[:1]])
    if profile.skill_mode != 'explicit':
        modules.append('skill_restraint')
    return list(dict.fromkeys(modules))


_SKILL_OUTCOME_TERMS: dict[Outcome, tuple[str, ...]] = {
    'research': ('research', 'review', 'search', '调研', '研究'),
    'decide': ('decision', 'comparison', 'compare', '决策', '对比'),
    'plan': ('planning', 'plan', 'roadmap', '规划', '计划'),
    'create': ('create', 'writing', 'generation', '创作', '生成'),
    'execute': ('automation', 'operation', 'deploy', '执行', '自动化'),
    'diagnose': ('diagnose', 'debug', 'review', '排障', '诊断'),
    'answer': ('answer',),
    'learn': ('learning', 'tutorial'),
}


def _selection_tokens(value: str) -> set[str]:
    text = str(value or '').lower()
    latin = re.findall(r'[a-z0-9][a-z0-9_-]{1,}', text)
    cjk = re.findall(r'[\u3400-\u9fff]{2,}', text)
    bigrams = [token[index:index + 2] for token in cjk for index in range(len(token) - 1)]
    return set(latin + cjk + bigrams)


def select_skill_candidates(
    available_skills: list[str] | None,
    query: str,
    profile: TaskProfile,
    *,
    limit: int = 5,
) -> list[str] | None:
    if profile.skill_mode == 'suppress':
        return []
    if profile.skill_mode == 'explicit':
        return available_skills
    available = [str(item) for item in (available_skills or []) if str(item).strip()]
    query_tokens = _selection_tokens(query)
    query_tokens.update(_SKILL_OUTCOME_TERMS[profile.primary_outcome])
    ranked = []
    for index, skill in enumerate(available):
        score = len(query_tokens & _selection_tokens(skill))
        ranked.append((score, index, skill))
    ranked.sort(key=lambda item: (-item[0], item[1]))
    return [skill for score, _, skill in ranked if score > 0][:max(1, min(limit, 5))]
