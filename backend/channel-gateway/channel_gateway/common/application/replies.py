from dataclasses import dataclass
from typing import Any

from channel_gateway.common.application.conversations import ConversationResult
from channel_gateway.common.domain.chat import CoreEvent
from channel_gateway.common.domain.commands import (
    ActionKind,
    CommandEnvelope,
    command_kind,
)
from channel_gateway.common.domain.outbound import (
    AskPresentation,
    AskQuestionPresentation,
    ReplyPresentation,
    SelectionOption,
    SelectionPresentation,
    TaskPresentation,
    optional_int,
)
from channel_gateway.common.ports.repository import NavigationRepository


def project_core_presentations(
    events: tuple[CoreEvent, ...],
) -> tuple[ReplyPresentation, ...]:
    presentations: list[ReplyPresentation] = []
    for event in events:
        if event.type == 'ask_pending':
            presentation = _ask(event.payload)
        elif event.type == 'task_created':
            presentation = _task(event.payload)
        else:
            presentation = None
        if presentation is not None:
            presentations.append(presentation)
    return tuple(presentations)


def _ask(payload: dict) -> AskPresentation | None:
    ask_id = str(payload.get('ask_id') or '')
    raw_questions = payload.get('questions')
    questions = tuple(
        AskQuestionPresentation(
            text=str(question.get('text') or ''),
            type=str(question.get('type') or 'text'),
            choices=tuple(
                str(choice)
                for choice in (
                    question.get('choices')
                    if isinstance(question.get('choices'), list)
                    else []
                )
                if str(choice)
            ),
        )
        for question in (
            raw_questions
            if isinstance(raw_questions, list)
            else []
        )
        if isinstance(question, dict) and question.get('text')
    )
    if not ask_id or not questions:
        return None
    return AskPresentation(
        kind='ask',
        ask_id=ask_id,
        title=str(payload.get('title') or ''),
        description=str(payload.get('description') or ''),
        questions=questions,
    )


def _task(payload: dict) -> TaskPresentation | None:
    task_id = str(payload.get('task_id') or '')
    if not task_id:
        return None
    return TaskPresentation(
        kind='task',
        task_id=task_id,
        conversation_id=str(payload.get('conversation_id') or ''),
        title=str(payload.get('title') or '后台任务'),
        mode=str(payload.get('mode') or ''),
        status=str(payload.get('status') or '已创建'),
        agent_type=str(payload.get('agent_type') or ''),
        progress=optional_int(
            payload.get('progress', payload.get('progress_pct'))
        ),
        current_phase=str(payload.get('current_phase') or ''),
        estimated_sec=optional_int(payload.get('estimated_sec')),
        summary=str(payload.get('summary') or ''),
    )


@dataclass(frozen=True)
class ChannelReply:
    intent_kind: ActionKind
    text: str
    core_events: tuple[dict[str, Any], ...] = ()
    sources: tuple[Any, ...] = ()
    presentations: tuple[ReplyPresentation, ...] = ()
    suppress_text_when_presented: bool = False


class ChannelReplyBuilder:
    """Converts action results into the provider-neutral reply model."""

    def __init__(self, store: NavigationRepository):
        self._store = store

    def build(
        self,
        *,
        command: CommandEnvelope,
        result: str | ConversationResult,
        account_id: str,
        external_address_hash: str,
        extra_presentations: tuple[ReplyPresentation, ...] = (),
    ) -> ChannelReply:
        selection = self._selection(
            account_id,
            external_address_hash,
        )
        if isinstance(result, ConversationResult):
            core_presentations = project_core_presentations(
                result.turn.events
                if result.turn is not None
                else ()
            )
            presentations = (
                *extra_presentations,
                *result.presentations,
                *core_presentations,
            )
            if selection is not None:
                presentations = (*presentations, selection)
            return ChannelReply(
                intent_kind=command_kind(command),
                text=result.text,
                core_events=tuple(
                    event.to_dict()
                    for event in (
                        result.turn.events
                        if result.turn is not None
                        else ()
                    )
                ),
                sources=(
                    result.turn.sources
                    if result.turn is not None
                    else ()
                ),
                presentations=presentations,
                suppress_text_when_presented=(
                    result.suppress_text_when_presented
                ),
            )
        intent_kind = command_kind(command)
        presentations = extra_presentations
        if selection is not None:
            presentations = (*presentations, selection)
        return ChannelReply(
            intent_kind=intent_kind,
            text=result,
            presentations=presentations,
            suppress_text_when_presented=(
                bool(presentations)
                and intent_kind
                in {
                    ActionKind.CAPABILITY_LIST,
                    ActionKind.CONVERSATION_SETTINGS,
                    ActionKind.CONVERSATION_SETTINGS_UPDATE,
                }
            ),
        )

    def _selection(
        self,
        account_id: str,
        external_address_hash: str,
    ) -> SelectionPresentation | None:
        selection = self._store.get_selection_context(
            account_id,
            external_address_hash,
        )
        if not selection:
            return None
        raw_items = selection.get('items')
        if not isinstance(raw_items, list):
            return None
        options = tuple(
            SelectionOption(
                label=self._selection_label(item, index),
                value=str(index),
            )
            for index, item in enumerate(raw_items, start=1)
            if isinstance(item, dict)
        )
        if not options:
            return None
        labels = {
            'conversation': '选择要继续的会话',
            'knowledge_base': '选择知识库',
            'skill': '选择 Skill',
            'tool': '选择工具',
            'personalization': '选择个人习惯',
        }
        kind = str(selection.get('kind') or '')
        return SelectionPresentation(
            kind='selection',
            selection_id=str(selection.get('id') or ''),
            title=labels.get(kind, '请选择'),
            options=options,
        )

    @staticmethod
    def _selection_label(item: dict[str, Any], index: int) -> str:
        return str(
            item.get('display_name')
            or item.get('name')
            or f'选项 {index}'
        )
