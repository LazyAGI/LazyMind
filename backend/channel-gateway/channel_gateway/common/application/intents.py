from __future__ import annotations

import re
from dataclasses import dataclass, field
from typing import Any, Sequence

from pydantic import ValidationError

from channel_gateway.common.domain.commands import (
    COMMAND_ADAPTER,
    RESOURCE_CHANGE_ADAPTER,
    RESOLVED_CONVERSATION_TARGET_KEY,
    RESOLVED_RESOURCE_SELECTIONS_KEY,
    SCHEMA_VERSION,
    CapabilityConfigureCommand,
    CapabilityConfigureParameters,
    ChatCommand,
    CommandEnvelope,
    ConversationSwitchCommand,
    ConversationSwitchParameters,
    ConversationSettingsUpdateCommand,
    ConversationSettingsUpdateParameters,
    IndexTarget,
    ResourceChange,
    ResourceIndexSelector,
    SelectionContinuation,
    SelectionChooseCommand,
)
from channel_gateway.common.errors import LazyMindError
from channel_gateway.common.ports.repository import IntentRepository


_PLAIN_INDEX = re.compile(r'^\s*(\d{1,2})\s*$')
_CHINESE_INDEXES = {
    '1': '一',
    '2': '二两',
    '3': '三',
    '4': '四',
    '5': '五',
    '6': '六',
    '7': '七',
    '8': '八',
    '9': '九',
    '10': '十',
}


@dataclass(frozen=True, slots=True)
class ShortcutMatch:
    command: CommandEnvelope
    grounding_messages: tuple[str, ...]
    prepared_catalog: dict[str, Any] = field(default_factory=dict)


class ExactShortcutParser:
    """Resolves only a pure numeric answer to an active selection."""

    def __init__(self, store: IntentRepository):
        self._store = store

    def parse(
        self,
        *,
        account_id: str,
        external_address_hash: str,
        text: str,
    ) -> ShortcutMatch | None:
        value = text.strip()
        match = _PLAIN_INDEX.fullmatch(value)
        if not match:
            return None
        selection = self._store.get_selection_context(
            account_id,
            external_address_hash,
        )
        if selection is None:
            return None
        kind = str(selection.get('kind') or '')
        if kind == 'conversation':
            return self._selection(
                index=match.group(1),
                evidence=value,
                selection=selection,
            )
        if kind == 'workflow':
            return self._workflow_selection(
                index=match.group(1),
                evidence=value,
                selection=selection,
            )
        if kind in ('knowledge_base', 'skill', 'tool', 'personalization'):
            return self._capability_selection(
                kind=kind,
                index=match.group(1),
                evidence=value,
                selection=selection,
            )
        return None

    @staticmethod
    def _workflow_selection(
        *,
        index: str,
        evidence: str,
        selection: dict[str, Any],
    ) -> ShortcutMatch | None:
        items = selection.get('items')
        position = int(index) - 1
        if not isinstance(items, list) or not 0 <= position < len(items):
            return None
        item = items[position]
        if not isinstance(item, dict):
            return None
        workflow_ref = str(item.get('id') or item.get('name') or '').strip()
        if not workflow_ref:
            return None
        return ExactShortcutParser._match(
            ConversationSettingsUpdateCommand(
                schema_version=SCHEMA_VERSION,
                command='conversation.settings.update',
                parameters=ConversationSettingsUpdateParameters(
                    change={
                        'setting': 'workflow',
                        'workflow_ref': workflow_ref,
                        'enabled': True,
                    },
                    evidence=[evidence],
                ),
            ),
            evidence,
        )

    @staticmethod
    def _switch(index: str, evidence: str) -> ShortcutMatch:
        return ExactShortcutParser._match(
            ConversationSwitchCommand(
                schema_version=SCHEMA_VERSION,
                command='conversation.switch',
                parameters=ConversationSwitchParameters(
                    target=IndexTarget(kind='index', value=index),
                    evidence=[evidence],
                ),
            ),
            evidence,
        )

    @staticmethod
    def _capability_selection(
        *,
        kind: str,
        index: str,
        evidence: str,
        selection: dict[str, Any],
    ) -> ShortcutMatch:
        continuation = selection.get('continuation')
        resumed = _resume_continuation(continuation, index, evidence)
        if resumed is not None:
            return resumed
        raw_change: dict[str, Any]
        if kind == 'personalization':
            raw_change = {
                'resource_type': 'personalization',
                'operation': 'use',
                'scope': 'turn',
                'evidence': evidence,
            }
        else:
            raw_change = {
                'resource_type': kind,
                'selector': {'kind': 'index', 'value': index},
                'operation': 'use',
                'scope': 'turn',
                'evidence': evidence,
            }
        change = RESOURCE_CHANGE_ADAPTER.validate_python(raw_change)
        return ExactShortcutParser._match(
            CapabilityConfigureCommand(
                schema_version=SCHEMA_VERSION,
                command='capability.configure',
                parameters=CapabilityConfigureParameters(
                    resource_changes=[change],
                    evidence=[evidence],
                ),
            ),
            evidence,
        )

    @staticmethod
    def _selection(
        *,
        index: str,
        evidence: str,
        selection: dict[str, Any],
    ) -> ShortcutMatch:
        resumed = _resume_continuation(
            selection.get('continuation'),
            index,
            evidence,
        )
        return resumed or ExactShortcutParser._switch(index, evidence)

    @staticmethod
    def _match(command: CommandEnvelope, *messages: str) -> ShortcutMatch:
        return ShortcutMatch(command=command, grounding_messages=tuple(messages))


def _resume_continuation(
    raw: object,
    index: str,
    evidence: str,
) -> ShortcutMatch | None:
    if raw is None:
        return None
    if not isinstance(raw, dict):
        raise LazyMindError('Saved channel selection is invalid')
    try:
        continuation = SelectionContinuation.model_validate(raw)
        command = COMMAND_ADAPTER.validate_python(continuation.command)
        parameters = command.parameters
        if continuation.selection_field == 'conversation_target':
            if not isinstance(command, ConversationSwitchCommand):
                raise ValueError('invalid conversation continuation')
            combined_evidence = list(parameters.evidence[:7])
            if evidence not in combined_evidence:
                combined_evidence.append(evidence)
            parameters = parameters.model_copy(
                update={
                    'target': IndexTarget(kind='index', value=index),
                    'evidence': combined_evidence,
                }
            )
        else:
            position = continuation.resource_change_index
            changes = list(getattr(parameters, 'resource_changes', []))
            if position is None or position >= len(changes):
                raise ValueError('invalid resource continuation')
            changes[position] = changes[position].model_copy(
                update={
                    'selector': ResourceIndexSelector(kind='index', value=index),
                    'evidence': evidence,
                }
            )
            parameters = parameters.model_copy(update={'resource_changes': changes})
        resumed = COMMAND_ADAPTER.validate_python(
            command.model_copy(update={'parameters': parameters}).model_dump(mode='json')
        )
    except (ValidationError, ValueError) as exc:
        raise LazyMindError('Saved channel selection is invalid') from exc
    grounding_messages = list(
        dict.fromkeys([*continuation.grounding_messages, evidence])
    )
    if len(grounding_messages) > 10:
        grounding_messages = [grounding_messages[0], *grounding_messages[-9:]]
    prepared_catalog: dict[str, Any] = {}
    if continuation.prepared_resources:
        prepared_catalog[RESOLVED_RESOURCE_SELECTIONS_KEY] = {
            position: {
                'resource_type': selection.resource_type,
                'items': [selection.item.model_dump(mode='json')],
            }
            for position, selection in continuation.prepared_resources.items()
        }
    if continuation.prepared_conversation_target is not None:
        prepared_catalog[RESOLVED_CONVERSATION_TARGET_KEY] = (
            continuation.prepared_conversation_target.model_dump(mode='json')
        )
    return ShortcutMatch(
        command=resumed,
        grounding_messages=tuple(grounding_messages),
        prepared_catalog=prepared_catalog,
    )


def resolve_pending_selection(
    command: CommandEnvelope,
    selection: dict[str, Any] | None,
    current_message: str,
) -> ShortcutMatch | None:
    """Resume a suspended typed command from the canonical selection command."""

    if not isinstance(command, SelectionChooseCommand):
        return None
    if not selection or not isinstance(selection.get('continuation'), dict):
        raise LazyMindError('There is no pending channel selection')
    index = command.parameters.index
    evidence_values = command.parameters.evidence
    evidence = next(
        (
            item
            for item in evidence_values
            if item in current_message and _index_has_evidence(index, item)
        ),
        None,
    )
    if evidence is None:
        raise LazyMindError('Selection index is not grounded in the current message')
    resumed = _resume_continuation(
        selection.get('continuation'),
        index,
        evidence,
    )
    if resumed is not None:
        return resumed
    raise LazyMindError('Saved channel selection has no continuation')


def canonicalize_command(
    command: CommandEnvelope,
    current_message: str,
) -> CommandEnvelope:
    """Apply parameter identities declared by the command contract."""

    if isinstance(command, ChatCommand) and not command.parameters.resource_changes:
        parameters = command.parameters.model_copy(
            update={'message': current_message}
        )
        return command.model_copy(update={'parameters': parameters})
    return command


def validate_command(
    command: CommandEnvelope,
    messages: str | Sequence[str],
) -> CommandEnvelope:
    """Applies the same grounding checks to model and exact-shortcut commands."""

    grounding_messages = (
        (messages,) if isinstance(messages, str) else tuple(messages)
    )
    parameters = command.parameters
    evidence = list(getattr(parameters, 'evidence', []))
    if len(set(evidence)) != len(evidence):
        raise LazyMindError('Channel command evidence contains duplicates')
    for item in evidence:
        if not _is_grounded(item, grounding_messages):
            raise LazyMindError('Channel command evidence is not from the user message')

    task = str(getattr(parameters, 'message', '') or '')
    if task and not _is_grounded(task, grounding_messages):
        raise LazyMindError('Task text is not a verbatim user-message substring')

    changes = list(getattr(parameters, 'resource_changes', []))
    _validate_resource_changes(changes, grounding_messages)

    if (
        isinstance(command, ChatCommand)
        and not changes
        and task != grounding_messages[-1]
    ):
        raise LazyMindError('Plain chat must preserve the complete user message')
    if isinstance(command, ConversationSwitchCommand):
        target = parameters.target
        if target.kind == 'index':
            if not any(_index_has_evidence(target.value, item) for item in evidence):
                raise LazyMindError('Conversation index does not match its evidence')
        elif not _is_grounded(target.value, grounding_messages):
            raise LazyMindError('Conversation name is not from the user message')
    if isinstance(command, SelectionChooseCommand) and not any(
        _index_has_evidence(command.parameters.index, item)
        for item in command.parameters.evidence
    ):
        raise LazyMindError('Selection index does not match its evidence')
    if hasattr(parameters, 'capabilities'):
        capabilities = list(parameters.capabilities)
        if len(set(capabilities)) != len(capabilities):
            raise LazyMindError('Capability categories contain duplicates')
    return command


def _validate_resource_changes(
    changes: list[ResourceChange],
    messages: Sequence[str],
) -> None:
    seen: set[tuple[str, str, str, str]] = set()
    for change in changes:
        if not _is_grounded(change.evidence, messages):
            raise LazyMindError('Resource action evidence is not from the user message')
        selector = getattr(change, 'selector', None)
        if selector is not None:
            if selector.kind == 'name' and not _is_grounded(selector.value, messages):
                raise LazyMindError('Resource selector is not from the user message')
            if selector.kind == 'index' and not _index_has_evidence(
                selector.value,
                change.evidence,
            ):
                raise LazyMindError('Resource index does not match its evidence')
        key = (
            change.resource_type,
            (
                f'{selector.kind}:{selector.value}'
                if selector is not None
                else ''
            ),
            change.operation,
            change.scope,
        )
        if key in seen:
            raise LazyMindError('Resource action contains duplicate changes')
        seen.add(key)


def _is_grounded(value: str, messages: Sequence[str]) -> bool:
    return bool(value) and any(value in message for message in messages)


def _index_has_evidence(value: str, evidence: str) -> bool:
    if re.search(rf'(?<!\d){re.escape(value)}(?!\d)', evidence):
        return True
    chinese = _CHINESE_INDEXES.get(value, '')
    return bool(chinese and any(f'第{item}' in evidence for item in chinese))
