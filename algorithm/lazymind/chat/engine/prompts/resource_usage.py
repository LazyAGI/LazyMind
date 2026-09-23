from __future__ import annotations

import re
from dataclasses import dataclass
from typing import Any, Literal


@dataclass(frozen=True)
class ResourceMention:
    resource_type: Literal['skill', 'knowledge_base', 'workflow']
    resource_ref: str
    display_name: str = ''


@dataclass(frozen=True)
class ExplicitResourceBindings:
    skill_names: tuple[str, ...] = ()
    knowledge_base_ids: tuple[str, ...] = ()
    workflow_refs: tuple[str, ...] = ()
    mentions: tuple[ResourceMention, ...] = ()


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
        if resource_type in {'skill', 'knowledge_base', 'workflow'} and resource_ref:
            mentions.append(ResourceMention(
                resource_type=resource_type,
                resource_ref=resource_ref[:240],
                display_name=str(item.get('display_name') or '').strip()[:120],
            ))
    return ExplicitResourceBindings(
        skill_names=strings('skill_names'),
        knowledge_base_ids=strings('knowledge_base_ids'),
        workflow_refs=strings('workflow_refs'),
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
    excluded: dict[str, set[str]] = {'skill': set(), 'knowledge_base': set(), 'workflow': set()}
    ambiguous = False
    for mention in resources.mentions:
        # Core already resolves Skill selection/exclusion and persists it for the
        # conversation. Task classification must not reinterpret that decision.
        if mention.resource_type == 'skill':
            continue
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
        workflow_refs=remaining(resources.workflow_refs, 'workflow'),
        mentions=active_mentions,
    )
    denied = ExplicitResourceBindings(
        skill_names=tuple(value for value in resources.skill_names if value in excluded['skill']),
        knowledge_base_ids=tuple(
            value for value in resources.knowledge_base_ids if value in excluded['knowledge_base']
        ),
        workflow_refs=tuple(value for value in resources.workflow_refs if value in excluded['workflow']),
        mentions=excluded_mentions,
    )
    return active, denied, ambiguous
