from __future__ import annotations

import json
import re
import time
from dataclasses import asdict, dataclass, field, replace
from typing import Any, Callable, Literal


Outcome = Literal[
    'answer', 'learn', 'research', 'analyze', 'transform',
    'decide', 'plan', 'create', 'execute', 'diagnose',
]
Complexity = Literal['simple', 'compound', 'open_ended']
Freshness = Literal['stable', 'current', 'unknown']
Deliverable = Literal[
    'direct_answer', 'tutorial', 'research_report', 'comparison', 'decision_brief',
    'analysis_report', 'transformed_content', 'action_plan', 'diagnostic_report',
    'artifact', 'execution_result',
]
SkillMode = Literal['suppress', 'candidates', 'explicit']

OUTCOMES = {
    'answer', 'learn', 'research', 'analyze', 'transform',
    'decide', 'plan', 'create', 'execute', 'diagnose',
}
COMPLEXITIES = {'simple', 'compound', 'open_ended'}
FRESHNESS = {'stable', 'current', 'unknown'}
DELIVERABLES = {
    'direct_answer', 'tutorial', 'research_report', 'comparison', 'decision_brief',
    'analysis_report', 'transformed_content', 'action_plan', 'diagnostic_report',
    'artifact', 'execution_result',
}
SKILL_MODES = {'suppress', 'candidates', 'explicit'}


@dataclass(frozen=True)
class RequestIssue:
    issue_type: str
    description: str
    evidence: str
    impact: Literal['low', 'medium', 'high'] = 'medium'


@dataclass(frozen=True)
class ClarificationQuestion:
    question: str
    options: tuple[str, ...] = ()
    recommended: str = ''


@dataclass(frozen=True)
class RequestAssessment:
    status: Literal[
        'ready', 'underspecified', 'ambiguous', 'contradictory', 'infeasible', 'unsafe',
    ] = 'ready'
    issues: tuple[RequestIssue, ...] = ()
    interaction_need: Literal['none', 'optional', 'blocking'] = 'none'
    assumptions_allowed: bool = True
    recommended_assumptions: tuple[str, ...] = ()
    clarification_questions: tuple[ClarificationQuestion, ...] = ()


@dataclass(frozen=True)
class ResourceMention:
    resource_type: Literal['skill', 'knowledge_base', 'plugin']
    resource_ref: str
    display_name: str = ''


@dataclass(frozen=True)
class ExplicitResourceBindings:
    skill_names: tuple[str, ...] = ()
    knowledge_base_ids: tuple[str, ...] = ()
    plugin_refs: tuple[str, ...] = ()
    mentions: tuple[ResourceMention, ...] = ()


@dataclass(frozen=True)
class TaskProfile:
    primary_outcome: Outcome = 'answer'
    secondary_outcomes: tuple[Outcome, ...] = ()
    outcome_subtype: str | None = None
    secondary_subtypes: tuple[str | None, ...] = ()
    subject_kind: str = 'topic'
    input_mode: str = 'query_only'
    source_strategy: str = 'model_knowledge'
    complexity: Complexity = 'simple'
    freshness: Freshness = 'stable'
    research_required: bool = False
    deliverable_kind: Deliverable = 'direct_answer'
    secondary_deliverables: tuple[Deliverable, ...] = ()
    execution_scope: str = 'chat_only'
    request_assessment: RequestAssessment = field(default_factory=RequestAssessment)
    explicit_resources: ExplicitResourceBindings = field(default_factory=ExplicitResourceBindings)
    excluded_resources: ExplicitResourceBindings = field(default_factory=ExplicitResourceBindings)
    skill_mode: SkillMode = 'suppress'
    confidence: float = 1.0
    reasons: tuple[str, ...] = ()
    source: Literal['rules', 'llm', 'fallback'] = 'rules'
    router_latency_ms: int = 0
    router_error: str = ''
    routing_review_required: bool = False
    routing_review_reason: str = ''

    def to_trace_dict(self) -> dict[str, Any]:
        result = asdict(self)
        result.pop('router_error', None)
        return result


_SIGNALS: tuple[tuple[Outcome, re.Pattern[str]], ...] = (
    ('answer', re.compile(r'解释一下|回答我|告诉我什么是|explain\s+(?:what|why|how)', re.I)),
    ('learn', re.compile(r'教我|入门|学会|从零(?:到一|开始)|零基础|教程|how\s+to|teach\s+me|learn', re.I)),
    ('research', re.compile(r'调研|调查|研究一下|深入研究|资料汇总|research|investigate|deep\s+dive', re.I)),
    ('analyze', re.compile(
        r'分析|评价|评估|审查|审阅|批评|比较|对比|找出.{0,12}(?:规律|模式)|'
        r'analy[sz]e|review|critique|compare', re.I,
    )),
    ('transform', re.compile(
        r'总结|摘要|翻译|改写|重写|润色|提取|抽取|转成|转换成|格式化|整理(?:这|以下|附件|文件)|'
        r'summari[sz]e|translate|rewrite|extract|reformat|convert', re.I,
    )),
    ('decide', re.compile(
        r'怎么选|如何选|值不值得|哪个好|哪一个好|是否应该|要不要|该不该|.{1,20}还是.{1,20}|'
        r'帮我选择|推荐哪个|versus|\bvs\b|should\s+i', re.I,
    )),
    ('plan', re.compile(r'制定.{0,8}计划|规划|路线图|实施步骤|行动方案|roadmap|action\s+plan', re.I)),
    ('diagnose', re.compile(
        r'为什么.{0,20}(?:失败|不行|很差|变慢|下降)|排查|排障|定位.{0,20}问题|'
        r'(?:变慢|失败|下降|异常).{0,12}(?:原因|问题)|找出.{0,12}原因|diagnos|troubleshoot', re.I,
    )),
    ('execute', re.compile(
        r'替我|帮我(?:发送|发布|修改|运行|删除|安装|部署)|直接(?:发送|发布|修改|运行|部署)|'
        r'^(?:发送|发布|修改|运行|删除|安装|部署)|(?:然后|再|并且|并)(?:发送|发布|修改|运行|删除|安装|部署)|'
        r'execute|deploy\s+it', re.I,
    )),
    ('create', re.compile(r'创建|生成|写一份|制作一份|产出|create|generate|draft', re.I)),
)
_CURRENT = re.compile(
    r'最新|现在|当前|今年|近期|主流|价格|政策|法规|排名|榜单|可用|202[4-9]|latest|current|today|recent|price', re.I,
)
_EXPLICIT_WEB = re.compile(r'联网|网上搜索|搜索资料|查资料|查一下|web\s+search|browse|look\s+up', re.I)
_SKILL_EXPLICIT = re.compile(r'skill|技能(?:库|包|文件|管理)?|SKILL\.md', re.I)
_OPEN_ENDED = re.compile(
    r'如何|怎么|有哪些|帮我看看|给我.*方案|'
    r'我(?:现在|目前)?想(?:搞|做|弄|了解|看看).{0,20}(?:相关|方面|方向|东西|内容)|'
    r'what\s+should|how\s+can|i(?:\s+am|\'m)?\s+interested\s+in',
    re.I,
)
_SIMPLE_FACT = re.compile(r'^(?:什么是|解释一下|定义|谁是|多少|what\s+is|define)', re.I)
_PLATFORM_CAPABILITY_QUERY = re.compile(
    r'(?:你|你们|平台|系统|LazyMind).{0,12}(?:有|支持|具备|提供|能做).{0,12}'
    r'(?:能力|功能|资源|技能|skill|知识库|文档|数据集|数据库|db|工具)?|'
    r'(?:有哪些|有什么|支持哪些).{0,8}'
    r'(?:能力|功能|资源|技能|skill|知识库|文档|数据集|数据库|db|工具)|'
    r'what\s+(?:can\s+you\s+do|skills|capabilities|resources).{0,12}',
    re.I,
)
_REQUEST_REVIEW_HINT = re.compile(
    r'全部|所有|必须|不能|至少|最多|保证|确保|同时|既要|又要|all|must|never|at\s+least|at\s+most|guarantee',
    re.I,
)

_DELIVERABLE_BY_OUTCOME: dict[Outcome, Deliverable] = {
    'answer': 'direct_answer',
    'learn': 'tutorial',
    'research': 'research_report',
    'analyze': 'analysis_report',
    'transform': 'transformed_content',
    'decide': 'decision_brief',
    'plan': 'action_plan',
    'create': 'artifact',
    'execute': 'execution_result',
    'diagnose': 'diagnostic_report',
}


def _ordered_outcomes(text: str, *, current: bool) -> list[Outcome]:
    found: list[tuple[int, int, Outcome]] = []
    priority = {
        'execute': 0, 'diagnose': 1, 'transform': 2, 'research': 3, 'analyze': 4,
        'decide': 5, 'plan': 6, 'learn': 7, 'create': 8, 'answer': 9,
    }
    for outcome, pattern in _SIGNALS:
        for match in pattern.finditer(text):
            found.append((match.start(), priority[outcome], outcome))
            break
    learn_match = re.search(
        r'(?:如何|怎么).{0,12}(?:制作|搭建|学习|学|做出|使用)|how\s+(?:do|can)\s+i', text, re.I,
    )
    if learn_match:
        found.append((learn_match.start(), priority['learn'], 'learn'))
    research_match = re.search(r'有哪些|主流|现状|发展到哪|landscape', text, re.I)
    if current and research_match:
        found.append((research_match.start(), priority['research'], 'research'))
    found.sort(key=lambda item: (item[0], item[1]))
    outcomes = list(dict.fromkeys(outcome for _, _, outcome in found))
    if 'create' in outcomes and 'plan' in outcomes and re.search(
        r'(?:create|创建|生成|写)(?:一个|一份|\s+a|\s+an)?\s*(?:launch\s+)?'
        r'(?:plan|roadmap|计划|路线图|方案)', text, re.I,
    ):
        outcomes.remove('create')
    return outcomes


def _outcome_subtype(outcome: Outcome, text: str) -> str | None:
    patterns: dict[Outcome, tuple[tuple[str, str], ...]] = {
        'answer': (
            ('calculation', r'计算|算一下|多少|calculate'), ('definition', r'什么是|定义|define'),
            ('lookup', r'谁是|哪年|何时|where|when|who'), ('explanation', r'解释|为什么|explain'),
        ),
        'learn': (
            ('curriculum', r'课程|学习计划|curriculum'), ('exercise', r'练习|题目|exercise'),
            ('coaching', r'陪我|辅导|coach'), ('tutorial', r'教程|教我|从零|如何|怎么|how\s+to'),
        ),
        'research': (
            ('literature_review', r'文献|论文|literature'), ('fact_verification', r'核实|验证|verify'),
            ('landscape', r'全景|版图|赛道|主流|landscape'), ('deep_research', r'深入|全面|deep'),
        ),
        'analyze': (
            ('risk_assessment', r'风险|risk'), ('compare', r'比较|对比|compare'),
            ('critique', r'批评|不足|问题|critique'), ('review', r'审查|审阅|评价|review'),
            ('pattern_discovery', r'规律|模式|pattern'),
        ),
        'transform': (
            ('summarize', r'总结|摘要|summari'), ('translate', r'翻译|translate'),
            ('rewrite', r'改写|重写|润色|rewrite'), ('extract', r'提取|抽取|extract'),
            ('convert', r'转成|转换|convert'), ('organize', r'整理|organize'),
        ),
        'decide': (
            ('go_no_go', r'值不值得|要不要|该不该|go.?no.?go'),
            ('select', r'怎么选|哪个好|哪一个|versus|\bvs\b'), ('prioritize', r'优先|排序|prioriti'),
        ),
        'plan': (
            ('roadmap', r'路线图|roadmap'), ('schedule', r'日程|排期|schedule'),
            ('strategy', r'战略|策略|strategy'), ('learning_plan', r'学习计划'),
            ('implementation_plan', r'实施|落地|implementation'),
        ),
        'create': (
            ('code', r'代码|程序|code|app|网站'), ('image', r'图片|海报|logo|image'),
            ('video', r'视频|video'), ('presentation', r'演示|PPT|presentation'),
            ('spreadsheet', r'表格|spreadsheet'), ('document', r'文档|报告|合同|document'),
        ),
        'execute': (
            ('send', r'发送|send'), ('publish', r'发布|publish'), ('delete', r'删除|delete'),
            ('install', r'安装|install'), ('deploy', r'部署|deploy'), ('run', r'运行|execute|run'),
            ('schedule', r'日程|定时|schedule'), ('modify', r'修改|edit|modify'),
        ),
        'diagnose': (
            ('performance', r'慢|性能|performance'), ('quality', r'效果|质量|答非所问|quality'),
            ('incident', r'故障|事故|incident'), ('debug', r'错误|报错|bug|debug'),
            ('process', r'延期|流程|process'),
        ),
    }
    for subtype, pattern in patterns.get(outcome, ()):
        if re.search(pattern, text, re.I):
            return subtype
    defaults = {
        'answer': 'fact', 'learn': 'overview', 'research': 'quick_research',
        'analyze': 'inspect', 'transform': 'reformat', 'decide': 'recommend',
        'plan': 'action_plan', 'create': 'text', 'execute': 'modify',
        'diagnose': 'root_cause',
    }
    return defaults[outcome]


def _subject_and_input(text: str, has_attachments: bool) -> tuple[str, str]:
    subjects = (
        ('code', r'代码|程序|仓库|code|repository'), ('data', r'数据|表格|CSV|data'),
        ('image', r'图片|图像|截图|image'), ('video', r'视频|video'),
        ('audio', r'音频|播客|audio'),
        ('conversation', r'对话|聊天记录|会议记录|conversation'),
        ('document', r'财报|文档|文章|论文|报告|合同|文件|document|paper'),
        ('system', r'系统|服务|网站|API|system'),
    )
    subject = next((kind for kind, pattern in subjects if re.search(pattern, text, re.I)), 'topic')
    if has_attachments:
        input_mode = 'attachment'
    elif re.search(r'https?://|www\.', text, re.I):
        input_mode = 'url'
    elif re.search(r'知识库|knowledge\s*base', text, re.I):
        input_mode = 'knowledge_base'
    elif re.search(r'以下|这段|如下|```|<[^>]+>', text, re.I):
        input_mode = 'inline_content'
    else:
        input_mode = 'query_only'
    return subject, input_mode


def _assess_request(
    text: str,
    outcomes: list[Outcome],
    *,
    has_attachments: bool,
) -> RequestAssessment:
    issues: list[RequestIssue] = []
    questions: list[ClarificationQuestion] = []
    objective_match = re.search(r'(\d+)\s*个?(?:目标|意图)', text)
    case_match = re.search(r'(\d+)\s*个?(?:测例|测试用例|测试场景|场景)', text)
    all_combinations = re.search(r'(?:全部|所有).{0,12}组合|all.{0,12}combinations', text, re.I)
    if objective_match and case_match and all_combinations:
        objectives, cases = int(objective_match.group(1)), int(case_match.group(1))
        required = 2 ** objectives - 1 if objectives < 20 else cases + 1
        if required > cases:
            evidence = all_combinations.group(0)
            issues.append(RequestIssue(
                'mathematical_inconsistency',
                f'{objectives} objectives have {required} non-empty unordered combinations, exceeding {cases} cases.',
                evidence,
                'high',
            ))
            questions.append(ClarificationQuestion(
                'How should combination coverage be defined?',
                ('all ordered pairs plus representative triples', f'raise the suite above {required} cases'),
                'all ordered pairs plus representative triples',
            ))
    contradictions = (
        (r'不要联网|不联网', r'最新|当前|现在|联网搜索', 'offline requirement conflicts with current research'),
        (r'简短|一句话', r'完整|全面|详细|从零到一', 'brevity conflicts with requested completeness'),
        (r'不要修改|只读', r'修改|删除|部署|发布', 'read-only constraint conflicts with execution'),
    )
    for left, right, description in contradictions:
        left_match, right_match = re.search(left, text), re.search(right, text)
        if left_match and right_match and left_match.group(0) != right_match.group(0):
            issues.append(RequestIssue(
                'conflicting_requirements', description,
                f'{left_match.group(0)} / {right_match.group(0)}', 'high',
            ))
    attachment_reference = re.search(
        r'这个文件|这份(?:文档|财报|论文|报告|合同|文件)|这篇(?:文章|论文|报告)|附件|这张图|'
        r'attached\s+(?:file|document|image)', text, re.I,
    )
    if attachment_reference and not has_attachments:
        issues.append(RequestIssue(
            'missing_input', 'The request references an attachment that is not available.',
            attachment_reference.group(0), 'high',
        ))
        questions.append(ClarificationQuestion('Please attach the referenced file before I continue.'))
    ambiguous_target = re.search(r'旧文件|那些文件|把它处理掉|do\s+it', text, re.I)
    if 'execute' in outcomes and ambiguous_target:
        issues.append(RequestIssue(
            'ambiguous_term', 'The external action target is not uniquely identified.',
            ambiguous_target.group(0), 'high',
        ))
        questions.append(ClarificationQuestion('Which exact target should the action affect?'))
    guarantee = re.search(r'保证|确保.{0,8}一定|100%|guarantee', text, re.I)
    if guarantee:
        issues.append(RequestIssue(
            'unverifiable_success_criterion', 'The requested guaranteed outcome cannot be verified or assured.',
            guarantee.group(0), 'medium',
        ))
    if not issues:
        return RequestAssessment()
    blocking = any(issue.impact == 'high' for issue in issues)
    status = 'contradictory' if any(
        issue.issue_type in {'conflicting_requirements', 'mathematical_inconsistency'} for issue in issues
    ) else 'underspecified' if any(issue.issue_type == 'missing_input' for issue in issues) else 'ambiguous'
    return RequestAssessment(
        status=status,
        issues=tuple(issues[:4]),
        interaction_need='blocking' if blocking else 'optional',
        assumptions_allowed=not blocking,
        recommended_assumptions=() if blocking else ('Treat the requested outcome as a target, not a guarantee.',),
        clarification_questions=tuple(questions[:2]),
    )


def _rule_profile(query: str, *, has_attachments: bool = False) -> tuple[TaskProfile, bool]:
    text = str(query or '').strip()
    platform_capability_query = bool(_PLATFORM_CAPABILITY_QUERY.search(text))
    explicit_skill = bool(_SKILL_EXPLICIT.search(text))
    current = bool(_CURRENT.search(text) or _EXPLICIT_WEB.search(text))
    # Fast-moving AI product/how-to requests require current evidence even without "latest".
    ai_how_to = bool(re.search(r'(?:AI|人工智能|大模型).{0,12}(?:视频|工具|产品|平台)', text, re.I))
    current = current or ai_how_to
    matches = _ordered_outcomes(text, current=current)

    if matches:
        primary = matches[0]
        secondary = tuple(matches[1:3])
    else:
        primary, secondary = 'answer', ()

    is_simple_fact = platform_capability_query or (
        bool(_SIMPLE_FACT.search(text)) and matches in ([], ['answer']) and not current
    )
    open_ended = bool(_OPEN_ENDED.search(text)) and not is_simple_fact
    complexity: Complexity = 'compound' if len(matches) > 1 else 'open_ended' if open_ended else 'simple'
    confidence = (
        0.96 if platform_capability_query
        else 0.92 if matches or is_simple_fact
        else 0.55 if open_ended else 0.8
    )
    deliverable = _DELIVERABLE_BY_OUTCOME[primary]
    secondary_deliverables = tuple(_DELIVERABLE_BY_OUTCOME[item] for item in secondary)
    research_required = current or 'research' in matches
    skill_mode: SkillMode = 'explicit' if explicit_skill else (
        'suppress' if primary in {'learn', 'transform'} or is_simple_fact else 'candidates'
    )
    subject_kind, input_mode = _subject_and_input(text, has_attachments)
    source_strategy = (
        'provided_content_only' if primary in {'analyze', 'transform'} and input_mode != 'query_only'
        else 'web' if current or 'research' in matches
        else 'model_knowledge'
    )
    execution_scope = (
        'external_action' if primary == 'execute'
        else 'create_artifact' if primary == 'create'
        else 'chat_only'
    )
    assessment = _assess_request(text, matches, has_attachments=has_attachments)
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
        outcome_subtype=_outcome_subtype(primary, text),
        secondary_subtypes=tuple(_outcome_subtype(item, text) for item in secondary),
        subject_kind=subject_kind,
        input_mode=input_mode,
        source_strategy=source_strategy,
        complexity=complexity,
        freshness='current' if current else 'stable' if is_simple_fact else 'unknown',
        research_required=research_required,
        deliverable_kind=deliverable,
        secondary_deliverables=secondary_deliverables,
        execution_scope=execution_scope,
        request_assessment=assessment,
        skill_mode=skill_mode,
        confidence=confidence,
        reasons=tuple(reasons[:4]),
    )
    needs_llm = (
        confidence < 0.75 or len(matches) > 1 or assessment.status != 'ready'
        or bool(_REQUEST_REVIEW_HINT.search(text))
    )
    return profile, needs_llm


def _normalize_explicit_resources(value: Any) -> ExplicitResourceBindings:
    if isinstance(value, ExplicitResourceBindings):
        return value
    if not isinstance(value, dict):
        return ExplicitResourceBindings()

    def strings(key: str) -> tuple[str, ...]:
        raw = value.get(key) or []
        if not isinstance(raw, (list, tuple)):
            return ()
        return tuple(dict.fromkeys(str(item).strip() for item in raw if str(item).strip()))

    raw_mentions = value.get('mentions') or []
    mentions = []
    for item in raw_mentions[:12]:
        if not isinstance(item, dict):
            continue
        resource_type = str(item.get('resource_type') or '').strip()
        resource_ref = str(item.get('resource_ref') or '').strip()
        if resource_type in {'skill', 'knowledge_base', 'plugin'} and resource_ref:
            mentions.append(ResourceMention(
                resource_type=resource_type,
                resource_ref=resource_ref[:240],
                display_name=str(item.get('display_name') or '').strip()[:120],
            ))
    return ExplicitResourceBindings(
        skill_names=strings('skill_names'),
        knowledge_base_ids=strings('knowledge_base_ids'),
        plugin_refs=strings('plugin_refs'),
        mentions=tuple(mentions),
    )


_RESOURCE_DENY = re.compile(
    r'不要(?:使用|调用|加载|启用|查询|搜索|检索|用)?|别(?:再)?(?:使用|调用|用)|'
    r'不想(?:使用|调用|用)|无需|不用|禁止|排除|忽略|跳过|避免使用|'
    r'do\s+not\s+use|don[’\']t\s+use|without|exclude|ignore', re.I,
)
_RESOURCE_ALLOW = re.compile(
    r'可以使用|可以用|可使用|可用|请使用|请用|使用|优先使用|启用|调用|'
    r'may\s+use|can\s+use|please\s+use|use', re.I,
)
_RESOURCE_POLICY_HINT = re.compile(
    r'不要|别(?:再)?用|不想用|无需|不用|禁止|排除|忽略|跳过|避免|尽量|'
    r'do\s+not|don[’\']t|without|exclude|ignore|avoid', re.I,
)


def _resource_usage_policy(
    query: str, resources: ExplicitResourceBindings,
) -> tuple[ExplicitResourceBindings, ExplicitResourceBindings, bool]:
    """Split current-turn mentions into usable/excluded sets; return whether intent is ambiguous."""
    excluded: dict[str, set[str]] = {'skill': set(), 'knowledge_base': set(), 'plugin': set()}
    ambiguous = False
    for mention in resources.mentions:
        labels = [label for label in (mention.display_name, mention.resource_ref) if label]
        positions = [query.lower().find(label.lower()) for label in labels]
        positions = [position for position in positions if position >= 0]
        if not positions:
            ambiguous = ambiguous or bool(_RESOURCE_POLICY_HINT.search(query))
            continue
        position = min(positions)
        prefix = query[max(0, position - 28):position]
        deny = list(_RESOURCE_DENY.finditer(prefix))
        allow = list(_RESOURCE_ALLOW.finditer(prefix))
        if deny and (not allow or deny[-1].end() >= allow[-1].end()):
            excluded[mention.resource_type].add(mention.resource_ref)
        elif _RESOURCE_POLICY_HINT.search(prefix) and not allow:
            ambiguous = True

    def remaining(values: tuple[str, ...], kind: str) -> tuple[str, ...]:
        return tuple(value for value in values if value not in excluded[kind])

    active_mentions = tuple(
        item for item in resources.mentions
        if item.resource_ref not in excluded[item.resource_type]
    )
    excluded_mentions = tuple(
        item for item in resources.mentions
        if item.resource_ref in excluded[item.resource_type]
    )
    active = ExplicitResourceBindings(
        skill_names=remaining(resources.skill_names, 'skill'),
        knowledge_base_ids=remaining(resources.knowledge_base_ids, 'knowledge_base'),
        plugin_refs=remaining(resources.plugin_refs, 'plugin'),
        mentions=active_mentions,
    )
    denied = ExplicitResourceBindings(
        skill_names=tuple(value for value in resources.skill_names if value in excluded['skill']),
        knowledge_base_ids=tuple(
            value for value in resources.knowledge_base_ids if value in excluded['knowledge_base']
        ),
        plugin_refs=tuple(value for value in resources.plugin_refs if value in excluded['plugin']),
        mentions=excluded_mentions,
    )
    return active, denied, ambiguous


def _apply_explicit_resources(
    profile: TaskProfile,
    resources: ExplicitResourceBindings,
    query: str,
    model_excluded_refs: tuple[str, ...] = (),
) -> TaskProfile:
    resources, excluded, _ = _resource_usage_policy(query, resources)
    if model_excluded_refs:
        all_resources = ExplicitResourceBindings(
            skill_names=resources.skill_names + excluded.skill_names,
            knowledge_base_ids=resources.knowledge_base_ids + excluded.knowledge_base_ids,
            plugin_refs=resources.plugin_refs + excluded.plugin_refs,
            mentions=resources.mentions + excluded.mentions,
        )
        allowed_refs = {
            *all_resources.skill_names,
            *all_resources.knowledge_base_ids,
            *all_resources.plugin_refs,
        }
        denied_refs = set(model_excluded_refs)
        if not denied_refs.issubset(allowed_refs):
            raise ValueError('classifier excluded an unbound resource')
        resources = ExplicitResourceBindings(
            skill_names=tuple(x for x in all_resources.skill_names if x not in denied_refs),
            knowledge_base_ids=tuple(x for x in all_resources.knowledge_base_ids if x not in denied_refs),
            plugin_refs=tuple(x for x in all_resources.plugin_refs if x not in denied_refs),
            mentions=tuple(x for x in all_resources.mentions if x.resource_ref not in denied_refs),
        )
        excluded = ExplicitResourceBindings(
            skill_names=tuple(x for x in all_resources.skill_names if x in denied_refs),
            knowledge_base_ids=tuple(x for x in all_resources.knowledge_base_ids if x in denied_refs),
            plugin_refs=tuple(x for x in all_resources.plugin_refs if x in denied_refs),
            mentions=tuple(x for x in all_resources.mentions if x.resource_ref in denied_refs),
        )
    updates: dict[str, Any] = {
        'explicit_resources': resources,
        'excluded_resources': excluded,
    }
    reasons = list(profile.reasons)
    if resources.skill_names:
        updates['skill_mode'] = 'explicit'
        reasons.append('explicit skill selection')
    elif excluded.skill_names:
        updates['skill_mode'] = 'suppress'
    if resources.knowledge_base_ids:
        updates['source_strategy'] = 'mixed' if _EXPLICIT_WEB.search(query) else 'knowledge_base'
        reasons.append('explicit knowledge-base selection')
    elif excluded.knowledge_base_ids:
        updates['source_strategy'] = 'web' if _EXPLICIT_WEB.search(query) else 'model_knowledge'
    if resources.plugin_refs:
        reasons.append('explicit workflow selection')
    assessment = profile.request_assessment
    issues = list(assessment.issues)
    questions = list(assessment.clarification_questions)
    if model_excluded_refs:
        issues = [issue for issue in issues if issue.issue_type != 'ambiguous_resource_policy']
        questions = [
            question for question in questions
            if question.question != 'Which mentioned resources should I use, and which should I avoid?'
        ]
        if not issues and assessment.status == 'ambiguous':
            updates['request_assessment'] = RequestAssessment()
    updates['reasons'] = tuple(dict.fromkeys(reasons))[:6]
    return replace(profile, **updates)


_CLASSIFIER_PROMPT = '''Resolve only the uncertain parts of a rule-generated task profile.
Return one compact JSON object and nothing else. Do not output reasoning, analysis, markdown, or
fields whose rule-proposed value is acceptable. Allowed optional keys:
primary_outcome, secondary_outcomes, complexity, freshness, skill_mode, request_status,
interaction_need, confidence.
Use only these enum values:
primary_outcome/secondary_outcomes: answer, learn, research, analyze, transform, decide, plan,
create, execute, diagnose;
complexity: simple, compound, open_ended; freshness: stable, current, unknown;
request_status: ready, underspecified, ambiguous, contradictory,
infeasible, unsafe; interaction_need: none, optional, blocking.
skill_mode: suppress, candidates, explicit. Keep the response under 80 tokens. An empty object is
valid when no override is needed.'''


def _classifier_input(
    query: str, history: list[dict] | None, intent: Any, has_attachments: bool,
    resources: ExplicitResourceBindings, rule: TaskProfile, review_reasons: list[str],
) -> str:
    recent = [
        str(item.get('content') or '')[:1000]
        for item in (history or []) if isinstance(item, dict) and item.get('role') == 'user'
    ][-3:]
    proposed = {
        'primary_outcome': rule.primary_outcome,
        'secondary_outcomes': rule.secondary_outcomes,
        'complexity': rule.complexity,
        'freshness': rule.freshness,
        'skill_mode': rule.skill_mode,
        'request_status': rule.request_assessment.status,
        'interaction_need': rule.request_assessment.interaction_need,
    }
    return (
        f'{_CLASSIFIER_PROMPT}\n\nUnresolved questions:\n'
        f'{json.dumps(review_reasons, ensure_ascii=False)}\n\n'
        f'Rule-proposed profile:\n{json.dumps(proposed, ensure_ascii=False)}\n\n'
        f'Explicit conversation intent:\n'
        f'{json.dumps(intent or {}, ensure_ascii=False)[:2000]}\n\n'
        f'Recent user messages:\n{json.dumps(recent, ensure_ascii=False)}\n\n'
        f'Attachments available: {has_attachments}\n\n'
        f'Explicit resource bindings:\n{json.dumps(asdict(resources), ensure_ascii=False)[:3000]}\n\n'
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


def _validate_llm_profile(
    raw: dict[str, Any], rule: TaskProfile, resources: ExplicitResourceBindings, query: str,
) -> TaskProfile:
    primary = str(raw.get('primary_outcome') or rule.primary_outcome)
    complexity = str(raw.get('complexity') or rule.complexity)
    freshness = str(raw.get('freshness') or rule.freshness)
    deliverable = _DELIVERABLE_BY_OUTCOME[primary] if primary in OUTCOMES else ''
    skill_mode = str(raw.get('skill_mode') or rule.skill_mode)
    if primary not in OUTCOMES or complexity not in COMPLEXITIES or freshness not in FRESHNESS:
        raise ValueError('classifier returned an invalid task enum')
    if deliverable not in DELIVERABLES or skill_mode not in SKILL_MODES:
        raise ValueError('classifier returned an invalid delivery enum')
    secondary = tuple(str(x) for x in raw.get('secondary_outcomes', rule.secondary_outcomes)[:2])
    secondary_deliverables = tuple(_DELIVERABLE_BY_OUTCOME[x] for x in secondary if x in OUTCOMES)
    if any(x not in OUTCOMES for x in secondary) or any(x not in DELIVERABLES for x in secondary_deliverables):
        raise ValueError('classifier returned an invalid secondary enum')
    reasons = rule.reasons
    confidence = min(1.0, max(0.0, float(raw.get('confidence', rule.confidence))))
    subject_kind = rule.subject_kind
    input_mode = rule.input_mode
    source_strategy = rule.source_strategy
    execution_scope = rule.execution_scope
    allowed_subjects = {
        'topic', 'document', 'code', 'data', 'image', 'audio', 'video',
        'conversation', 'system', 'external_resource',
    }
    allowed_inputs = {
        'query_only', 'inline_content', 'attachment', 'url', 'knowledge_base',
        'conversation_context', 'mixed',
    }
    allowed_sources = {
        'model_knowledge', 'provided_content_only', 'knowledge_base', 'web',
        'academic', 'connected_source', 'mixed',
    }
    if subject_kind not in allowed_subjects or input_mode not in allowed_inputs:
        raise ValueError('classifier returned an invalid subject or input enum')
    if source_strategy not in allowed_sources:
        raise ValueError('classifier returned an invalid source strategy')
    if execution_scope not in {'chat_only', 'create_artifact', 'workspace_change', 'external_action'}:
        raise ValueError('classifier returned an invalid execution scope')
    assessment = rule.request_assessment
    request_status = str(raw.get('request_status') or 'ready')
    interaction_need = str(raw.get('interaction_need') or 'none')
    if assessment.status == 'ready' and request_status != 'ready':
        if request_status not in {
            'underspecified', 'ambiguous', 'contradictory', 'infeasible', 'unsafe',
        } or interaction_need not in {'none', 'optional', 'blocking'}:
            raise ValueError('classifier returned an invalid request assessment')
        raw_issues = [str(item).strip()[:160] for item in (raw.get('request_issues') or [])[:4]]
        raw_questions = [
            str(item).strip()[:240] for item in (raw.get('clarification_questions') or [])[:2]
        ]
        assessment = RequestAssessment(
            status=request_status,
            issues=tuple(RequestIssue('model_detected_issue', item, item) for item in raw_issues if item),
            interaction_need=interaction_need,
            assumptions_allowed=interaction_need != 'blocking',
            clarification_questions=tuple(
                ClarificationQuestion(item) for item in raw_questions if item
            ),
        )
    # Explicit freshness and skill wording are authoritative deterministic signals.
    if rule.freshness == 'current':
        freshness = 'current'
    if rule.skill_mode == 'explicit':
        skill_mode = 'explicit'
    profile = TaskProfile(
        primary_outcome=primary, secondary_outcomes=secondary, complexity=complexity,
        outcome_subtype=str(raw.get('outcome_subtype') or rule.outcome_subtype)[:40] or None,
        secondary_subtypes=tuple(str(x)[:40] for x in (raw.get('secondary_subtypes') or [])[:2]),
        subject_kind=subject_kind, input_mode=input_mode, source_strategy=source_strategy,
        freshness=freshness, research_required=bool(raw.get('research_required')) or rule.research_required,
        deliverable_kind=deliverable, secondary_deliverables=secondary_deliverables,
        execution_scope=execution_scope, request_assessment=assessment,
        skill_mode=skill_mode, confidence=confidence, reasons=reasons, source='llm',
    )
    return _apply_explicit_resources(profile, resources, query)


def resolve_task_profile(
    query: str,
    *,
    history: list[dict] | None = None,
    intent: Any = None,
    classifier: Callable[[str], Any] | None = None,
    enable_llm_fallback: bool = True,
    has_attachments: bool = False,
    explicit_resources: ExplicitResourceBindings | dict[str, Any] | None = None,
) -> TaskProfile:
    rule, needs_llm = _rule_profile(query, has_attachments=has_attachments)
    resources = _normalize_explicit_resources(explicit_resources)
    rule = _apply_explicit_resources(rule, resources, query)
    review_reasons = []
    if len({rule.primary_outcome, *rule.secondary_outcomes}) > 1:
        review_reasons.append('请求包含多个可能竞争的目标')
    if rule.confidence < 0.75:
        review_reasons.append('规则无法高置信度确定主要目标')
    if rule.request_assessment.status != 'ready' and not review_reasons:
        review_reasons.append('请求约束需要进一步分析')
    if needs_llm:
        rule = replace(
            rule,
            routing_review_required=True,
            routing_review_reason='；'.join(review_reasons) or '开放请求需要进一步判断交付目标',
        )
    if not needs_llm or not enable_llm_fallback or classifier is None:
        return rule
    started = time.monotonic()
    try:
        result = classifier(_classifier_input(
            query, history, intent, has_attachments, resources, rule, review_reasons,
        ))
        profile = _validate_llm_profile(_extract_json(result), rule, resources, query)
        return replace(
            profile,
            router_latency_ms=int((time.monotonic() - started) * 1000),
            routing_review_required=False,
            routing_review_reason='',
        )
    except Exception as exc:
        return fallback_task_profile(
            query,
            error=exc,
            latency_ms=int((time.monotonic() - started) * 1000),
            explicit_resources=resources,
        )


def fallback_task_profile(
    query: str,
    *,
    error: Any,
    latency_ms: int = 0,
    has_attachments: bool = False,
    explicit_resources: ExplicitResourceBindings | dict[str, Any] | None = None,
) -> TaskProfile:
    rule, needed_llm = _rule_profile(query, has_attachments=has_attachments)
    resources = _normalize_explicit_resources(explicit_resources)
    rule = _apply_explicit_resources(rule, resources, query)
    return replace(
        rule,
        primary_outcome='answer',
        secondary_outcomes=(),
        outcome_subtype='fact',
        secondary_subtypes=(),
        deliverable_kind='direct_answer',
        secondary_deliverables=(),
        skill_mode='explicit' if rule.explicit_resources.skill_names else 'suppress',
        source='fallback',
        router_latency_ms=max(0, int(latency_ms)),
        router_error=f'{type(error).__name__}: {error}'[:240],
        routing_review_required=needed_llm,
        routing_review_reason=(
            '模型路由失败，当前结果使用规则安全回退' if needed_llm else ''
        ),
    )


def selected_prompt_modules(profile: TaskProfile) -> list[str]:
    modules = []
    outcomes = {profile.primary_outcome, *profile.secondary_outcomes}
    if 'learn' in outcomes:
        modules.append('learning')
    if profile.research_required or profile.freshness == 'current':
        modules.append('fresh_research')
    if 'analyze' in outcomes:
        modules.append('analysis')
    if 'transform' in outcomes:
        modules.append('transformation')
    if outcomes.intersection({'decide', 'plan'}):
        modules.append('decision_planning')
    if not (profile.complexity == 'simple' and profile.deliverable_kind == 'direct_answer'):
        modules.extend([profile.deliverable_kind, *profile.secondary_deliverables[:1]])
    if profile.skill_mode != 'explicit':
        modules.append('skill_restraint')
    assessment = profile.request_assessment
    hard_constraint = assessment.status != 'ready' or profile.complexity == 'compound'
    if hard_constraint:
        modules.append('request_analysis')
    if assessment.interaction_need == 'blocking':
        modules.append('clarification')
    return list(dict.fromkeys(modules))


_SKILL_OUTCOME_TERMS: dict[Outcome, tuple[str, ...]] = {
    'research': ('research', 'review', 'search', '调研', '研究'),
    'analyze': ('analysis', 'review', 'critique', '分析', '审查'),
    'transform': ('transform', 'rewrite', 'translate', 'summary', '转换', '改写'),
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
        selected = profile.explicit_resources.skill_names
        if not selected:
            return available_skills
        available = set(available_skills or [])
        return [skill for skill in selected if skill in available]
    available = [str(item) for item in (available_skills or []) if str(item).strip()]
    query_tokens = _selection_tokens(query)
    query_tokens.update(_SKILL_OUTCOME_TERMS[profile.primary_outcome])
    ranked = []
    for index, skill in enumerate(available):
        score = len(query_tokens & _selection_tokens(skill))
        ranked.append((score, index, skill))
    ranked.sort(key=lambda item: (-item[0], item[1]))
    return [skill for score, _, skill in ranked if score > 0][:max(1, min(limit, 5))]
