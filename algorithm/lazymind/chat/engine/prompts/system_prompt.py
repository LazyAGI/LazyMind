from __future__ import annotations

from datetime import datetime, timezone as datetime_timezone
import re
from zoneinfo import ZoneInfo, ZoneInfoNotFoundError

from lazymind.chat.engine.agent_runtime import AgentRole, PromptBuilder

from .guidance import (
    ANALYSIS_GUIDANCE,
    CLARIFICATION_GUIDANCE,
    DECISION_PLANNING_GUIDANCE,
    DEFAULT_SYSTEM_PROMPT,
    DELIVERABLE_GUIDANCE,
    FRESH_RESEARCH_GUIDANCE,
    LEARNING_GUIDANCE,
    REQUEST_ANALYSIS_GUIDANCE,
    RESPONSE_LANGUAGE_GUIDANCE,
    SKILL_RESTRAINT_GUIDANCE,
    TRANSFORMATION_GUIDANCE,
)
from .task_profile import TaskProfile

_DEFAULT_UI_LOCALE = 'zh-CN'
_CJK_PATTERN = re.compile(r'[\u3400-\u9fff]')
_LATIN_PATTERN = re.compile(r'[A-Za-z]')
_URL_PATTERN = re.compile(r'https?://\S+|www\.\S+', re.IGNORECASE)
_EXPLICIT_LANGUAGE_PATTERNS = (
    (
        'Chinese',
        re.compile(
            r'(?:请|始终|默认|务必|改为|切换到|使用|用|以).{0,16}'
            r'(?:中文|汉语|普通话|Chinese|Mandarin)|'
            r'(?:语言偏好|首选语言|默认语言).{0,8}(?:中文|汉语|普通话|Chinese|Mandarin)|'
            r'(?:preferred language|language preference).{0,8}(?:Chinese|Mandarin)|'
            r'(?:reply|answer|respond|write|speak|use|using|in).{0,16}'
            r'(?:in\s+)?(?:Chinese|Mandarin)|'
            r'(?:Chinese|Mandarin)\s+please',
            re.IGNORECASE,
        ),
    ),
    (
        'English',
        re.compile(
            r'(?:请|始终|默认|务必|改为|切换到|使用|用|以).{0,16}'
            r'(?:英文|英语|English)|'
            r'(?:语言偏好|首选语言|默认语言).{0,8}(?:英文|英语|English)|'
            r'(?:preferred language|language preference).{0,8}English|'
            r'(?:reply|answer|respond|write|speak|use|using|in).{0,16}'
            r'(?:in\s+)?English|'
            r'English\s+please',
            re.IGNORECASE,
        ),
    ),
)


def _get_ui_locale(environment_context: dict | None = None) -> str:
    if isinstance(environment_context, dict):
        locale = str(environment_context.get('locale') or '').strip()
        if locale:
            return locale
    return _DEFAULT_UI_LOCALE


def _explicit_language(text: object) -> str:
    value = str(text or '').strip()
    if not value:
        return ''
    for language, pattern in _EXPLICIT_LANGUAGE_PATTERNS:
        if pattern.search(value):
            return language
    return ''


def _dominant_language(text: object) -> str:
    # URLs are identifiers, not natural-language evidence. In particular, long
    # Feishu/Notion links must not make a Chinese request look English.
    value = _URL_PATTERN.sub(' ', str(text or '')[:2000])
    cjk_count = len(_CJK_PATTERN.findall(value))
    latin_count = len(_LATIN_PATTERN.findall(value))
    if cjk_count >= 2 and cjk_count * 2 >= latin_count:
        return 'Chinese'
    if latin_count >= 4 and latin_count > cjk_count * 2:
        return 'English'
    return ''


def _conversation_language(history: list[dict] | None = None) -> str:
    recent_user_messages = []
    for message in reversed(history or []):
        if not isinstance(message, dict) or message.get('role') != 'user':
            continue
        content = message.get('content')
        if isinstance(content, str) and content.strip():
            recent_user_messages.append(content)
        if len(recent_user_messages) >= 3:
            break
    return _dominant_language('\n'.join(reversed(recent_user_messages)))


def _locale_language(locale: str) -> str:
    normalized = locale.strip().lower()
    if normalized.startswith('zh'):
        return 'Chinese'
    if normalized.startswith('en'):
        return 'English'
    return locale.strip() or _DEFAULT_UI_LOCALE


def _resolve_response_language(
    *,
    current_query: str | None = None,
    conversation_history: list[dict] | None = None,
    user_preference: str | None = None,
    environment_context: dict | None = None,
) -> tuple[str, str]:
    current_instruction = _explicit_language(current_query)
    if current_instruction:
        return current_instruction, 'explicit instruction in the current request'

    saved_preference = _explicit_language(user_preference)
    if saved_preference:
        return saved_preference, 'explicit saved user preference'

    request_language = _dominant_language(current_query)
    if request_language:
        return request_language, 'dominant language of the current request'

    history_language = _conversation_language(conversation_history)
    if history_language:
        return history_language, 'dominant language of recent user messages'

    locale = _get_ui_locale(environment_context)
    return _locale_language(locale), f'default UI locale {locale}'


def _build_response_language_prompt(
    environment_context: dict | None = None,
    *,
    current_query: str | None = None,
    conversation_history: list[dict] | None = None,
    user_preference: str | None = None,
) -> str:
    language, source = _resolve_response_language(
        current_query=current_query,
        conversation_history=conversation_history,
        user_preference=user_preference,
        environment_context=environment_context,
    )
    return (
        f'{RESPONSE_LANGUAGE_GUIDANCE}\n'
        f'Default UI locale for this request: {_get_ui_locale(environment_context)}.\n'
        f'Selected response language for this turn: {language} ({source}).\n'
        f'Use {language} for all user-visible natural-language text in this turn.'
    )


def _format_user_time(time_now: object, timezone: object) -> str:
    raw_time = str(time_now).strip()
    if not raw_time:
        return ''

    timezone_name = str(timezone).strip() if timezone is not None else ''
    try:
        normalized_time = raw_time[:-1] + '+00:00' if raw_time.endswith('Z') else raw_time
        parsed_time = datetime.fromisoformat(normalized_time)
        if parsed_time.tzinfo is None:
            parsed_time = parsed_time.replace(tzinfo=datetime_timezone.utc)
        if timezone_name:
            user_time = parsed_time.astimezone(ZoneInfo(timezone_name))
            return f'{user_time:%Y-%m-%d %H:%M:%S} ({timezone_name})'
        return parsed_time.isoformat()
    except (ValueError, TypeError, ZoneInfoNotFoundError):
        return raw_time


def _build_environment_context_prompt(environment_context: dict | None = None) -> str:
    time_now = None
    timezone = None
    if isinstance(environment_context, dict):
        time_info = environment_context.get('time') or {}
        if isinstance(time_info, dict):
            time_now = time_info.get('now')
            timezone = time_info.get('timezone')

    user_time = _format_user_time(time_now, timezone) if time_now else ''
    if not user_time:
        return ''

    return f'## Environment Context\nCurrent user time: {user_time}'


_TOOL_APPENDIX_SECTION_TITLES = {
    'tool_policy': 'Tool-specific policies',
    'safety': 'Tool-specific safety constraints',
    'output_contract': 'Tool output contracts',
    'response_policy': 'Tool-specific response policies',
}


def _build_tool_appendix_prompt(appendices: dict[str, list[str]] | None = None) -> str:
    blocks = []
    for section, title in _TOOL_APPENDIX_SECTION_TITLES.items():
        entries = [item.strip() for item in (appendices or {}).get(section, []) if item.strip()]
        if entries:
            blocks.append(f'## {title}\n' + '\n\n'.join(entries))
    return '\n\n'.join(blocks)


def add_standard_system_sections(
    builder: PromptBuilder,
    has_tools: bool,
    *,
    environment_context: dict | None = None,
    use_memory: bool = True,
    user_preference: str | None = None,
    memory: str | None = None,
    current_query: str | None = None,
    conversation_history: list[dict] | None = None,
    tool_prompt_appendices: dict[str, list[str]] | None = None,
    show_tool_status: bool = True,
    task_profile: TaskProfile | None = None,
    dynamic_prompt_modules: bool = False,
) -> PromptBuilder:
    builder.system(
        'platform_identity', '', DEFAULT_SYSTEM_PROMPT, 'platform.guidance', priority=10,
    ).system(
        'response_language', '', _build_response_language_prompt(
            environment_context,
            current_query=current_query,
            conversation_history=conversation_history,
            user_preference=user_preference,
        ),
        'platform.language', priority=20,
    )

    environment_prompt = _build_environment_context_prompt(environment_context)
    builder.system(
        'environment', '', environment_prompt, 'request.environment', priority=30,
    )

    if dynamic_prompt_modules and task_profile is not None:
        outcomes = {task_profile.primary_outcome, *task_profile.secondary_outcomes}
        builder.system(
            'task_learning', '', LEARNING_GUIDANCE, 'platform.task.learning', priority=32,
            skip_if='learn' not in outcomes,
        ).system(
            'task_fresh_research', '', FRESH_RESEARCH_GUIDANCE,
            'platform.task.research', priority=33,
            skip_if=not task_profile.research_required and task_profile.freshness != 'current',
        ).system(
            'task_decision_planning', '', DECISION_PLANNING_GUIDANCE,
            'platform.task.decision', priority=34,
            skip_if=not outcomes.intersection({'decide', 'plan'}),
        ).system(
            'task_analysis', '', ANALYSIS_GUIDANCE,
            'platform.task.analysis', priority=34,
            skip_if='analyze' not in outcomes,
        ).system(
            'task_transformation', '', TRANSFORMATION_GUIDANCE,
            'platform.task.transformation', priority=34,
            skip_if='transform' not in outcomes,
        )
        deliverables = [task_profile.deliverable_kind, *task_profile.secondary_deliverables][:2]
        contracts = [DELIVERABLE_GUIDANCE[item] for item in deliverables if item in DELIVERABLE_GUIDANCE]
        builder.system(
            'task_deliverable', '# Deliverable contract', '\n'.join(contracts),
            'platform.task.deliverable', priority=35,
            skip_if=not contracts or (
                task_profile.complexity == 'simple' and task_profile.deliverable_kind == 'direct_answer'
            ),
        ).system(
            'task_skill_restraint', '', SKILL_RESTRAINT_GUIDANCE,
            'platform.task.skills', priority=36,
            skip_if=task_profile.skill_mode == 'explicit',
        ).system(
            'task_request_analysis', '', REQUEST_ANALYSIS_GUIDANCE,
            'platform.task.request_analysis', priority=37,
            skip_if=(
                task_profile.request_assessment.status == 'ready'
                and task_profile.complexity != 'compound'
            ),
        ).system(
            'task_clarification', '', CLARIFICATION_GUIDANCE,
            'platform.task.clarification', priority=38,
            skip_if=task_profile.request_assessment.interaction_need != 'blocking',
        )
        assessment = task_profile.request_assessment
        excluded = task_profile.excluded_resources
        excluded_lines = [
            *(f'- Skill: {value}' for value in excluded.skill_names),
            *(f'- Knowledge base: {value}' for value in excluded.knowledge_base_ids),
            *(f'- Workflow: {value}' for value in excluded.plugin_refs),
        ]
        if excluded_lines:
            builder.runtime(
                'task_resource_policy', 'Resource Usage Policy',
                '\n'.join([
                    'Do not use, invoke, cite, or rely on these resources in this turn, even if '
                    'their content appears elsewhere in the assembled context:',
                    *excluded_lines,
                ]),
                'runtime.task.resources', priority=4, authoritative=True, content_kind='instruction',
            )
        if assessment.status != 'ready':
            issue_lines = [
                f'- {issue.issue_type} ({issue.impact}): {issue.description} '
                f'[evidence: {issue.evidence}]'
                for issue in assessment.issues
            ]
            question_lines = [
                f'- {question.question}'
                + (f' Options: {", ".join(question.options)}.' if question.options else '')
                + (f' Recommended: {question.recommended}.' if question.recommended else '')
                for question in assessment.clarification_questions
            ]
            builder.runtime(
                'task_request_assessment', 'Request Assessment',
                '\n'.join([
                    f'Status: {assessment.status}',
                    f'Interaction need: {assessment.interaction_need}',
                    *issue_lines,
                    *question_lines,
                ]),
                'runtime.task.assessment', priority=5, authoritative=True, content_kind='state',
            )

    if use_memory:
        if isinstance(user_preference, str) and user_preference.strip():
            preference_block = (
                '## User Profile / Preferences\n'
                "The following profile entries describe the user's long-term preferences"
                ' and identity.\n'
                '`agent_persona` describes the identity, responsibilities, and boundaries'
                ' the assistant should maintain when replying. If `agent_persona` is'
                ' set, use it as the assistant identity, including for questions such'
                ' as "who are you" or "what is your name". If `agent_persona` is not'
                ' set, the assistant may identify itself as LAZYMIND.\n'
                '`preferred_name` is how replies should address the user.\n'
                '`response_style` describes expression habits, length preference, and'
                ' structure preference.\n'
                "Apply a preference **only when it is relevant to the user's current"
                ' intent**.\n'
                'If a preference conflicts with or is unrelated to what the user is'
                ' actually asking for in this turn, ignore it.\n'
                'Do not force-apply style, format, or persona preferences when the'
                " user's question is factual, technical, or unrelated to that"
                ' preference.\n\n'
                + user_preference.strip()
                + '\n\n<!-- end of User Profile / Preferences -->'
            )
            builder.system(
                'user_preferences', '', preference_block, 'user.profile', priority=40,
            )
        if isinstance(memory, str) and memory.strip():
            builder.system(
                'working_memory', '', f'## Agent Working Memory\n{memory.strip()}',
                'user.memory', priority=50,
            )

    if has_tools:
        tool_policy = (
            '# Tool use policy\n'
            'First decide whether tools are needed. A tool named get_*Toolkit_methods '
            'is a Toolkit gateway: call it before using that Toolkit. Confirm before '
            'destructive or externally visible actions unless the user '
            'already requested that exact action.'
        )
        if show_tool_status:
            tool_policy = (
                '# Tool call status\n'
                'Before calling a tool, write one concise, user-visible sentence explaining '
                'what you are about to do. Keep it action-oriented and do not reveal hidden '
                'reasoning. Then make the tool call in the same response.\n'
                "CRITICAL: Never write a status sentence (e.g. '正在…', 'I am now checking…', "
                "'Activating…') without immediately following it with an actual tool call in the "
                'same response. If you cannot call a tool, do not pretend you are doing so — '
                'answer directly instead.\n\n'
                + tool_policy
            )
        builder.system(
            'tool_policy', '', tool_policy, 'platform.tools', priority=60,
        )
        appendix_prompt = _build_tool_appendix_prompt(tool_prompt_appendices)
        builder.system(
            'tool_appendices', '', appendix_prompt, 'tool.registry', priority=70,
        )

    return builder


def build_system_prompt(has_tools: bool, **kwargs) -> str:
    """Render standard system sections for direct consumers and focused tests."""
    builder = PromptBuilder.for_role(AgentRole.CHAT)
    return add_standard_system_sections(builder, has_tools, **kwargs).build().system_prompt
