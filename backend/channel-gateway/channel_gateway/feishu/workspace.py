from __future__ import annotations

import re
from dataclasses import dataclass, field
from datetime import datetime
from pathlib import PurePath
from typing import Any, Literal


WorkspaceView = Literal[
    'chat',
    'capabilities',
    'conversations',
    'assistant',
    'settings',
    'context',
]
ContextScope = Literal['global', 'conversation', 'turn']

_VIEWS = {
    'chat',
    'capabilities',
    'conversations',
    'assistant',
    'settings',
    'context',
}
_SCOPES = {'global', 'conversation', 'turn'}
_RESOURCE_TYPES = (
    'knowledge_base',
    'skill',
    'plugin',
    'tool',
    'conversation',
)
_CONTEXT_CATEGORIES = (*_RESOURCE_TYPES, 'prompt')
_MAX_CONVERSATION_CONTEXTS = 50
_MAX_CARD_ANSWER_CHARS = 16000
_MAX_SNAPSHOT_PRESENTATIONS = 30
_MAX_WORKSPACE_IMAGES = 6
_MAX_CAPABILITY_ITEMS_PER_GROUP = 6
_CONTEXT_PAGE_SIZE = 10
_CHAT_HISTORY_PAGE_SIZE = 3
_MAX_CHAT_HISTORY_TURNS = 60
_MAX_CHAT_HISTORY_QUERY_CHARS = 300
_MAX_CHAT_HISTORY_ANSWER_CHARS = 800
_ASSISTANT_SESSION_PAGE_SIZE = 5
_ASSISTANT_ANSWER_PAGE_CHARS = 5000
_MAX_ASSISTANT_THREADS = 500
_MAX_ASSISTANT_MESSAGE_CHARS = 64000
_MARKDOWN_IMAGE = re.compile(r'!\[[^\]]*\]\([^)\s]+\)')
_CONTEXT_LABELS = {
    'knowledge_base': '知识库',
    'skill': 'Skill',
    'plugin': 'Workflow',
    'tool': 'Tool',
    'prompt': 'Prompt',
    'conversation': '会话',
}
_TAB_COMMANDS = {
    'chat': {
        'schema_version': '1',
        'command': 'conversation.current',
        'parameters': {'evidence': ['查看对话']},
    },
    'capabilities': {
        'schema_version': '1',
        'command': 'capability.list',
        'parameters': {
            'capabilities': ['knowledge_base', 'skill', 'plugin', 'tool'],
            'evidence': ['查看能力'],
        },
    },
    'settings': {
        'schema_version': '1',
        'command': 'conversation.settings',
        'parameters': {
            'section': 'overview',
            'evidence': ['查看设置'],
        },
    },
}
MENU_EVENT_VIEWS = {
    'lazymind_capabilities': 'capabilities',
    'lazymind_conversations': 'conversations',
    'lazymind_settings': 'settings',
    'lazymind_assistant': 'assistant',
}


def _history_command(evidence: str) -> dict[str, Any]:
    return {
        'schema_version': '1',
        'command': 'conversation.list',
        'parameters': {'evidence': [evidence]},
    }


def menu_command(view: str) -> dict[str, Any] | None:
    if view == 'capabilities':
        return dict(_TAB_COMMANDS['capabilities'])
    if view == 'conversations':
        return _history_command('切换会话')
    if view == 'settings':
        return dict(_TAB_COMMANDS['settings'])
    return None


def _localized(state: FeishuWorkspaceState, zh: str, en: str) -> str:
    return en if state.output_language == 'en' else zh


def is_feishu_image_key(value: Any) -> bool:
    """Reject URLs and local paths before they can poison a CardKit card."""
    key = str(value or '').strip()
    return bool(
        key
        and len(key) <= 1024
        and not any(character.isspace() for character in key)
        and '/' not in key
        and '?' not in key
        and '://' not in key
    )


def _workspace_chat_text(value: Any) -> str:
    return _MARKDOWN_IMAGE.sub('', str(value or '')).strip()[
        :_MAX_CARD_ANSWER_CHARS
    ]


def _workspace_turns(value: Any) -> list[dict[str, str]]:
    turns: list[dict[str, str]] = []
    for item in value if isinstance(value, list) else []:
        if not isinstance(item, dict):
            continue
        query = str(item.get('query') or '').strip()
        answer = _workspace_chat_text(item.get('answer'))
        if not query or not answer:
            continue
        turns.append(
            {
                'query': query[:_MAX_CHAT_HISTORY_QUERY_CHARS],
                'answer': answer[:_MAX_CHAT_HISTORY_ANSWER_CHARS],
            }
        )
    return turns[-_MAX_CHAT_HISTORY_TURNS:]


@dataclass(frozen=True, slots=True)
class WorkspaceResource:
    type: str
    id: str
    name: str

    @classmethod
    def from_dict(cls, value: Any) -> WorkspaceResource | None:
        if not isinstance(value, dict):
            return None
        resource_type = str(value.get('type') or '')
        resource_id = str(value.get('id') or '')
        name = str(value.get('name') or '').strip()
        if (
            resource_type not in _RESOURCE_TYPES
            or not resource_id
            or not name
        ):
            return None
        return cls(resource_type, resource_id, name[:200])

    def to_dict(self) -> dict[str, str]:
        return {'type': self.type, 'id': self.id, 'name': self.name}

    def to_mention(self) -> dict[str, str]:
        return {
            'mention_id': f'feishu_{self.type}_{self.id}',
            'type': self.type,
            'resource_id': self.id,
            'display_name': self.name,
        }


@dataclass(slots=True)
class FeishuWorkspaceState:
    view: WorkspaceView = 'chat'
    message_id: str = ''
    revision: int = 0
    context_scope: ContextScope = 'conversation'
    context_category: str = 'knowledge_base'
    context_page: int = 0
    context_draft: list[WorkspaceResource] = field(default_factory=list)
    defaults: list[WorkspaceResource] = field(default_factory=list)
    conversations: dict[str, list[WorkspaceResource]] = field(default_factory=dict)
    thinking_depth: str = 'medium'
    output_language: str = 'zh'
    show_process: bool = True
    auto_collapse_process: bool = True
    show_sources: bool = True
    ready_marker: bool = True
    restore_last_view: bool = False
    preferences_dirty: bool = False
    unread_results: int = 0
    result_notice_operation_id: str = ''
    new_session_open: bool = False
    new_session_mode: str = 'blank'
    pending_conversation_resources: list[WorkspaceResource] = field(
        default_factory=list
    )
    active_operation_id: str = ''
    generation_history_id: str = ''
    run_status: str = 'idle'
    user_text: str = ''
    chat_text: str = ''
    chat_status: str = ''
    chat_thinking: str = ''
    conversation_title: str = ''
    conversation_switch_index: int = 0
    conversation_switch_status: str = ''
    chat_history: list[dict[str, str]] = field(default_factory=list)
    chat_history_page: int = 0
    chat_history_reached_start: bool = False
    chat_presentations: list[dict[str, Any]] = field(default_factory=list)
    view_snapshots: dict[str, dict[str, Any]] = field(default_factory=dict)
    pending_ask_id: str = ''
    images: list[dict[str, str]] = field(default_factory=list)
    assistant_mode: str = 'projects'
    assistant_provider: str = 'codex'
    assistant_threads: list[dict[str, Any]] = field(default_factory=list)
    assistant_status: str = 'idle'
    assistant_error: str = ''
    assistant_selected_thread_id: str = ''
    assistant_conversation_id: str = ''
    assistant_thread_title: str = ''
    assistant_thread_source: str = ''
    assistant_thread_cwd: str = ''
    assistant_thread_updated_at: str = ''
    assistant_thread_available: bool = False
    assistant_managed: bool = False
    assistant_turns: list[dict[str, str]] = field(default_factory=list)
    assistant_turns_offset: int = 0
    assistant_turns_total: int = 0
    assistant_project_cwd: str = ''
    assistant_project_page: int = 0
    assistant_answer_page: int = 0
    assistant_pending_request: dict[str, Any] | None = None

    @classmethod
    def from_dict(cls, value: Any) -> FeishuWorkspaceState:
        raw = value if isinstance(value, dict) else {}
        view = str(raw.get('view') or 'chat')
        if view == 'history':
            view = 'conversations'
        scope = str(raw.get('context_scope') or 'conversation')
        category = str(raw.get('context_category') or 'knowledge_base')
        conversations = {
            str(conversation_id): _resources(items)
            for conversation_id, items in (
                raw.get('conversations')
                if isinstance(raw.get('conversations'), dict)
                else {}
            ).items()
            if conversation_id
        }
        raw_assistant_mode = str(raw.get('assistant_mode') or 'projects')
        assistant_mode = (
            'projects'
            if raw_assistant_mode == 'list'
            else raw_assistant_mode
            if raw_assistant_mode in {'projects', 'sessions', 'detail'}
            else 'projects'
        )
        return cls(
            view=view if view in _VIEWS else 'chat',
            message_id=str(raw.get('message_id') or ''),
            revision=max(0, _integer(raw.get('revision'))),
            context_scope=scope if scope in _SCOPES else 'conversation',
            context_category=(
                category
                if category in _CONTEXT_CATEGORIES
                else 'knowledge_base'
            ),
            context_page=max(0, _integer(raw.get('context_page'))),
            context_draft=_resources(raw.get('context_draft')),
            defaults=_resources(raw.get('defaults')),
            conversations=dict(
                list(conversations.items())[-_MAX_CONVERSATION_CONTEXTS:]
            ),
            thinking_depth=(
                str(raw.get('thinking_depth'))
                if str(raw.get('thinking_depth'))
                in {'low', 'medium', 'high', 'max'}
                else 'medium'
            ),
            output_language=(
                str(raw.get('output_language'))
                if str(raw.get('output_language')) in {'zh', 'en'}
                else 'zh'
            ),
            show_process=bool(raw.get('show_process', True)),
            auto_collapse_process=bool(
                raw.get('auto_collapse_process', True)
            ),
            show_sources=bool(raw.get('show_sources', True)),
            ready_marker=bool(raw.get('ready_marker', True)),
            restore_last_view=bool(raw.get('restore_last_view', False)),
            preferences_dirty=bool(raw.get('preferences_dirty', False)),
            unread_results=max(0, _integer(raw.get('unread_results'))),
            result_notice_operation_id=str(
                raw.get('result_notice_operation_id') or ''
            )[:128],
            new_session_open=bool(raw.get('new_session_open', False)),
            new_session_mode=(
                str(raw.get('new_session_mode'))
                if str(raw.get('new_session_mode')) in {'blank', 'inherit'}
                else 'blank'
            ),
            pending_conversation_resources=_resources(
                raw.get('pending_conversation_resources')
            ),
            active_operation_id=str(raw.get('active_operation_id') or ''),
            generation_history_id=str(
                raw.get('generation_history_id') or ''
            )[:512],
            run_status=(
                str(raw.get('run_status'))
                if str(raw.get('run_status')) in {
                    'idle',
                    'running',
                    'waiting_for_input',
                    'completed',
                    'failed',
                    'cancelled',
                }
                else 'idle'
            ),
            user_text=str(raw.get('user_text') or '')[:4000],
            chat_text=_workspace_chat_text(raw.get('chat_text')),
            chat_status=str(raw.get('chat_status') or '')[:300],
            chat_thinking=str(raw.get('chat_thinking') or '')[:4000],
            conversation_title=str(
                raw.get('conversation_title') or ''
            )[:200],
            conversation_switch_index=max(
                0,
                _integer(raw.get('conversation_switch_index')),
            ),
            conversation_switch_status=(
                str(raw.get('conversation_switch_status') or '')
                if str(raw.get('conversation_switch_status') or '')
                in {'running', 'completed', 'expired'}
                else ''
            ),
            chat_history=_workspace_turns(raw.get('chat_history')),
            chat_history_page=max(
                0,
                _integer(raw.get('chat_history_page')),
            ),
            chat_history_reached_start=bool(
                raw.get('chat_history_reached_start', False)
            ),
            chat_presentations=_presentations(raw.get('chat_presentations')),
            view_snapshots=_view_snapshots(raw.get('view_snapshots')),
            pending_ask_id=str(raw.get('pending_ask_id') or '')[:512],
            images=_workspace_images(raw.get('images')),
            assistant_mode=assistant_mode,
            assistant_provider=str(raw.get('assistant_provider') or 'codex')[:64],
            assistant_threads=_assistant_threads(raw.get('assistant_threads')),
            assistant_status=(
                str(raw.get('assistant_status'))
                if str(raw.get('assistant_status'))
                in {'idle', 'loading', 'error', 'ready'}
                else 'idle'
            ),
            assistant_error=str(raw.get('assistant_error') or '')[:500],
            assistant_selected_thread_id=str(raw.get('assistant_selected_thread_id') or '')[:512],
            assistant_conversation_id=str(raw.get('assistant_conversation_id') or '')[:512],
            assistant_thread_title=str(raw.get('assistant_thread_title') or '')[:200],
            assistant_thread_source=str(raw.get('assistant_thread_source') or '')[:100],
            assistant_thread_cwd=str(raw.get('assistant_thread_cwd') or '')[:500],
            assistant_thread_updated_at=str(raw.get('assistant_thread_updated_at') or '')[:100],
            assistant_thread_available=bool(raw.get('assistant_thread_available', False)),
            assistant_managed=bool(raw.get('assistant_managed', False)),
            assistant_turns=_assistant_turns(raw.get('assistant_turns')),
            assistant_turns_offset=max(0, _integer(raw.get('assistant_turns_offset'))),
            assistant_turns_total=max(0, _integer(raw.get('assistant_turns_total'))),
            assistant_project_cwd=str(raw.get('assistant_project_cwd') or '')[:500],
            assistant_project_page=max(
                0,
                _integer(raw.get('assistant_project_page')),
            ),
            assistant_answer_page=max(
                0,
                _integer(raw.get('assistant_answer_page')),
            ),
            assistant_pending_request=_assistant_request(raw.get('assistant_pending_request')),
        )

    def to_dict(self) -> dict[str, Any]:
        return {
            'version': 1,
            'view': self.view,
            'message_id': self.message_id,
            'revision': self.revision,
            'context_scope': self.context_scope,
            'context_category': self.context_category,
            'context_page': self.context_page,
            'context_draft': [item.to_dict() for item in self.context_draft],
            'defaults': [item.to_dict() for item in self.defaults],
            'conversations': {
                conversation_id: [item.to_dict() for item in items]
                for conversation_id, items in self.conversations.items()
            },
            'thinking_depth': self.thinking_depth,
            'output_language': self.output_language,
            'show_process': self.show_process,
            'auto_collapse_process': self.auto_collapse_process,
            'show_sources': self.show_sources,
            'ready_marker': self.ready_marker,
            'restore_last_view': self.restore_last_view,
            'preferences_dirty': self.preferences_dirty,
            'unread_results': self.unread_results,
            'result_notice_operation_id': self.result_notice_operation_id,
            'new_session_open': self.new_session_open,
            'new_session_mode': self.new_session_mode,
            'pending_conversation_resources': [
                item.to_dict()
                for item in self.pending_conversation_resources
            ],
            'active_operation_id': self.active_operation_id,
            'generation_history_id': self.generation_history_id,
            'run_status': self.run_status,
            'user_text': self.user_text,
            'chat_text': self.chat_text,
            'chat_status': self.chat_status,
            'chat_thinking': self.chat_thinking,
            'conversation_title': self.conversation_title,
            'conversation_switch_index': self.conversation_switch_index,
            'conversation_switch_status': self.conversation_switch_status,
            'chat_history': _workspace_turns(self.chat_history),
            'chat_history_page': self.chat_history_page,
            'chat_history_reached_start': self.chat_history_reached_start,
            'chat_presentations': _presentations(self.chat_presentations),
            'view_snapshots': _view_snapshots(self.view_snapshots),
            'pending_ask_id': self.pending_ask_id,
            'images': _workspace_images(self.images),
            'assistant_mode': self.assistant_mode,
            'assistant_provider': self.assistant_provider,
            'assistant_threads': _assistant_threads(self.assistant_threads),
            'assistant_status': self.assistant_status,
            'assistant_error': self.assistant_error,
            'assistant_selected_thread_id': self.assistant_selected_thread_id,
            'assistant_conversation_id': self.assistant_conversation_id,
            'assistant_thread_title': self.assistant_thread_title,
            'assistant_thread_source': self.assistant_thread_source,
            'assistant_thread_cwd': self.assistant_thread_cwd,
            'assistant_thread_updated_at': self.assistant_thread_updated_at,
            'assistant_thread_available': self.assistant_thread_available,
            'assistant_managed': self.assistant_managed,
            'assistant_turns': _assistant_turns(self.assistant_turns),
            'assistant_turns_offset': self.assistant_turns_offset,
            'assistant_turns_total': self.assistant_turns_total,
            'assistant_project_cwd': self.assistant_project_cwd,
            'assistant_project_page': self.assistant_project_page,
            'assistant_answer_page': self.assistant_answer_page,
            'assistant_pending_request': _assistant_request(self.assistant_pending_request),
        }

    def advance(self) -> None:
        self.revision += 1

    @property
    def assistant_thread_readonly(self) -> bool:
        return not self.assistant_thread_available and not self.assistant_managed

    def bind_message(self, message_id: str) -> None:
        if not self.message_id and message_id:
            self.message_id = message_id

    def begin_operation(self, operation_id: str, user_text: str) -> None:
        self.archive_current_turn()
        self.active_operation_id = operation_id
        self.generation_history_id = ''
        self.run_status = 'running'
        self.user_text = user_text[:4000]
        self.chat_status = '⏳ **正在理解你的问题**'
        self.chat_text = ''
        self.chat_thinking = '正在分析问题…'
        self.chat_presentations = []
        self.pending_ask_id = ''
        self.images = []
        self.chat_history_page = 0
        self.unread_results = 0
        self.new_session_open = False
        self.conversation_switch_status = ''

    def begin_conversation_switch(
        self,
        operation_id: str,
        selection: str,
    ) -> None:
        self.active_operation_id = operation_id
        self.run_status = 'running'
        self.conversation_switch_index = max(0, _integer(selection))
        self.conversation_switch_status = 'running'
        self.view = 'conversations'

    def expire_conversation_switch(self, selection: str) -> None:
        self.run_status = 'completed'
        self.conversation_switch_index = max(0, _integer(selection))
        self.conversation_switch_status = 'expired'
        self.view = 'conversations'

    def cancel_operation(self) -> None:
        if self.run_status != 'running':
            return
        self.run_status = 'cancelled'
        self.generation_history_id = ''
        self.chat_status = (
            '⏹️ **Generation cancelled**'
            if self.output_language == 'en'
            else '⏹️ **已取消本次生成**'
        )
        self.chat_thinking = (
            'The partial answer above has been preserved.'
            if self.output_language == 'en'
            else '已保留取消前生成的内容。'
        )

    def leave_assistant_thread(self) -> None:
        self.assistant_mode = (
            'sessions' if self.assistant_project_cwd else 'projects'
        )
        self.assistant_status = 'ready'
        self.assistant_error = ''
        self.assistant_selected_thread_id = ''
        self.assistant_conversation_id = ''
        self.assistant_thread_title = ''
        self.assistant_thread_source = ''
        self.assistant_thread_cwd = ''
        self.assistant_thread_updated_at = ''
        self.assistant_thread_available = False
        self.assistant_managed = False
        self.assistant_turns = []
        self.assistant_turns_offset = 0
        self.assistant_turns_total = 0
        self.assistant_answer_page = 0
        self.assistant_pending_request = None
        self.run_status = 'idle'
        self.user_text = ''
        self.chat_text = ''
        self.chat_status = ''
        self.chat_thinking = ''

    def archive_current_turn(self) -> None:
        turn = _workspace_turns(
            [{'query': self.user_text, 'answer': self.chat_text}]
        )
        if not turn:
            return
        if not self.chat_history or self.chat_history[-1] != turn[0]:
            self.chat_history = _workspace_turns(
                [*self.chat_history, turn[0]]
            )

    def replace_chat_history(
        self,
        *,
        title: str,
        turns: Any,
        reached_start: bool,
    ) -> None:
        self.conversation_title = title[:200]
        self.chat_history = _workspace_turns(turns)
        self.chat_history_page = 0
        self.chat_history_reached_start = reached_start
        self.user_text = ''
        self.chat_text = ''
        self.chat_status = '✅ **已切换会话**'
        self.chat_thinking = '已恢复所选会话的最近记录。'
        self.conversation_switch_status = 'completed'
        self.chat_presentations = []
        self.pending_ask_id = ''
        self.images = []
        self.run_status = 'completed'
        self.view = 'conversations'

    def prepend_chat_history(
        self,
        *,
        turns: Any,
        reached_start: bool,
    ) -> None:
        older = _workspace_turns(turns)
        combined = [*older, *self.chat_history]
        capped = len(combined) > _MAX_CHAT_HISTORY_TURNS
        self.chat_history = _workspace_turns(combined)
        self.chat_history_page = min(
            self.chat_history_page + 1,
            max(0, (len(self.chat_history) - 1) // _CHAT_HISTORY_PAGE_SIZE),
        )
        self.chat_history_reached_start = reached_start or capped
        self.chat_status = (
            f'ℹ️ **已保留最近 {_MAX_CHAT_HISTORY_TURNS} 轮**'
            if capped
            else '✅ **已加载更早记录**'
        )
        self.chat_thinking = (
            '已达到卡片内历史记录上限。'
            if capped
            else '会话记录已更新。'
        )
        self.run_status = 'completed'
        self.view = 'conversations'

    def navigate(
        self,
        view: str,
        *,
        conversation_id: str,
        pending_turn: dict[str, Any],
    ) -> None:
        if view not in {
            'chat',
            'capabilities',
            'conversations',
            'assistant',
            'settings',
        }:
            return
        self.view = view
        self.new_session_open = False
        if view != 'assistant':
            self.assistant_pending_request = None
        if view == 'chat':
            self.unread_results = 0
        elif view == 'capabilities':
            self.context_scope = 'conversation'
            self.context_draft = self.resources_for_scope(
                'conversation',
                conversation_id=conversation_id,
                pending_turn=pending_turn,
            )

    def save_capabilities(self, conversation_id: str) -> None:
        if conversation_id:
            self.conversations[conversation_id] = list(self.context_draft)
            self.conversations = dict(
                list(self.conversations.items())[-_MAX_CONVERSATION_CONTEXTS:]
            )
        else:
            self.pending_conversation_resources = list(self.context_draft)
        self.view = 'capabilities'

    def open_new_session(self) -> None:
        self.view = 'conversations'
        self.new_session_open = True

    def prepare_new_session(
        self,
        *,
        mode: str,
        conversation_id: str,
    ) -> None:
        self.new_session_mode = mode if mode in {'blank', 'inherit'} else 'blank'
        self.pending_conversation_resources = (
            list(self.conversations.get(conversation_id, []))
            if self.new_session_mode == 'inherit'
            else []
        )
        self.clear_chat()
        self.new_session_open = False
        self.view = 'conversations'

    def complete_new_session(self, conversation_id: str) -> None:
        if conversation_id and self.pending_conversation_resources:
            self.conversations[conversation_id] = list(
                self.pending_conversation_resources
            )
        self.pending_conversation_resources = []

    def mark_result_ready(self, operation_id: str) -> None:
        if (
            not self.ready_marker
            or self.view == 'chat'
            or not operation_id
            or self.result_notice_operation_id == operation_id
        ):
            return
        self.unread_results += 1
        self.result_notice_operation_id = operation_id[:128]

    def reset_preferences(self) -> None:
        self.thinking_depth = 'medium'
        self.output_language = 'zh'
        self.show_process = True
        self.auto_collapse_process = True
        self.show_sources = True
        self.ready_marker = True
        self.restore_last_view = False
        self.preferences_dirty = False

    def clear_chat(self) -> None:
        self.user_text = ''
        self.chat_text = ''
        self.chat_status = ''
        self.chat_thinking = ''
        self.conversation_title = ''
        self.chat_history = []
        self.chat_history_page = 0
        self.chat_history_reached_start = False
        self.chat_presentations = []
        self.pending_ask_id = ''
        self.images = []
        self.generation_history_id = ''
        self.run_status = 'idle'
        self.unread_results = 0

    def cache_view(
        self,
        view: str,
        *,
        text: str,
        presentations: list[dict[str, Any]],
        merge: bool = False,
    ) -> None:
        snapshot_view = 'capabilities' if view == 'context' else view
        if snapshot_view == 'conversations':
            snapshot_view = 'history'
        if snapshot_view not in {'capabilities', 'history', 'settings', 'assistant'}:
            return
        next_presentations = _presentations(presentations)
        if merge:
            current = self.view_snapshots.get(snapshot_view, {})
            current_presentations = _presentations(
                current.get('presentations')
                if isinstance(current, dict)
                else []
            )
            by_kind = {
                str(item.get('kind') or ''): item
                for item in current_presentations
            }
            for item in next_presentations:
                by_kind[str(item.get('kind') or '')] = item
            next_presentations = list(by_kind.values())
        self.view_snapshots[snapshot_view] = {
            'text': text[:_MAX_CARD_ANSWER_CHARS],
            'presentations': next_presentations,
        }

    def snapshot_for_view(self, view: str) -> tuple[str, list[dict[str, Any]]]:
        if view == 'chat':
            return self.chat_text, list(self.chat_presentations)
        snapshot_view = 'capabilities' if view == 'context' else view
        if snapshot_view == 'conversations':
            snapshot_view = 'history'
        snapshot = self.view_snapshots.get(snapshot_view)
        if not isinstance(snapshot, dict):
            return '', []
        return (
            str(snapshot.get('text') or ''),
            _presentations(snapshot.get('presentations')),
        )

    def add_image(
        self,
        *,
        image_key: str,
        caption: str = '',
        identity: str = '',
    ) -> None:
        if not is_feishu_image_key(image_key):
            raise ValueError('Invalid Feishu image key')
        images = {
            str(item.get('identity') or item.get('image_key') or ''): item
            for item in self.images
        }
        images[identity or image_key] = {
            'image_key': image_key,
            'caption': caption[:300],
            'identity': (identity or image_key)[:512],
        }
        self.images = list(images.values())[-_MAX_WORKSPACE_IMAGES:]

    def open_context(
        self,
        *,
        scope: str,
        category: str,
        conversation_id: str,
        pending_turn: dict[str, Any],
    ) -> None:
        if scope in _SCOPES:
            self.context_scope = scope
        if category in _CONTEXT_CATEGORIES:
            self.context_category = category
        self.context_page = 0
        self.view = 'context'
        self.context_draft = self.resources_for_scope(
            self.context_scope,
            conversation_id=conversation_id,
            pending_turn=pending_turn,
        )

    def toggle_context(self, value: Any) -> None:
        resource = WorkspaceResource.from_dict(value)
        if resource is None:
            return
        key = (resource.type, resource.id)
        existing = {
            (item.type, item.id): item
            for item in self.context_draft
        }
        if key in existing:
            existing.pop(key)
        else:
            if resource.type == 'plugin':
                existing = {
                    item_key: item
                    for item_key, item in existing.items()
                    if item.type != 'plugin'
                }
            if resource.type == 'conversation':
                conversation_count = sum(
                    item.type == 'conversation'
                    for item in existing.values()
                )
                if conversation_count >= 3:
                    return
            existing[key] = resource
        self.context_draft = list(existing.values())

    def save_context(self, conversation_id: str) -> list[WorkspaceResource]:
        saved = list(self.context_draft)
        if self.context_scope == 'global':
            self.defaults = saved
        elif self.context_scope == 'conversation' and conversation_id:
            self.conversations[conversation_id] = saved
            self.conversations = dict(
                list(self.conversations.items())[-_MAX_CONVERSATION_CONTEXTS:]
            )
        self.view = (
            'capabilities'
            if self.context_scope == 'conversation'
            else 'chat'
        )
        return saved if self.context_scope == 'turn' else []

    def resources_for_scope(
        self,
        scope: str,
        *,
        conversation_id: str,
        pending_turn: dict[str, Any],
    ) -> list[WorkspaceResource]:
        if scope == 'global':
            return list(self.defaults)
        if scope == 'conversation':
            return list(
                self.conversations.get(conversation_id, [])
                if conversation_id
                else self.pending_conversation_resources
            )
        return _resources_from_mentions(pending_turn.get('mentions'))

    def effective_resources(
        self,
        conversation_id: str,
        pending_turn: dict[str, Any],
    ) -> list[WorkspaceResource]:
        return _merge_resources(
            self.defaults,
            (
                self.conversations.get(conversation_id, [])
                if conversation_id
                else self.pending_conversation_resources
            ),
            _resources_from_mentions(pending_turn.get('mentions')),
        )


class FeishuWorkspaceRenderer:
    @classmethod
    def render(
        cls,
        *,
        provider_context: dict[str, Any],
        text: str,
        presentations: list[dict[str, Any]],
        status: str | None = None,
        thinking: str | None = None,
        streaming: bool = False,
        extra_chat_elements: list[dict[str, Any]] | None = None,
    ) -> dict[str, Any]:
        state = FeishuWorkspaceState.from_dict(
            provider_context.get('workspace_state')
        )
        snapshot_text, snapshot_presentations = state.snapshot_for_view(
            state.view
        )
        if state.view == 'chat':
            rendered_presentations = (
                presentations or snapshot_presentations
            )
        else:
            # Background chat/task updates must never replace the visible
            # capability, history, settings, or context snapshot.
            rendered_presentations = snapshot_presentations
        rendered_streaming = streaming or state.run_status == 'running'
        # Menu cards ignore live chat text; native Feishu messages own that surface.
        _ = (text, snapshot_text)
        chat_id = str(provider_context.get('chat_id') or '')
        elements: list[dict[str, Any]] = []
        if state.view == 'capabilities':
            elements.extend(
                cls._capabilities(
                    state,
                    rendered_presentations,
                    chat_id,
                    has_conversation=bool(
                        provider_context.get('workspace_conversation_id')
                    ),
                )
            )
        elif state.view == 'conversations':
            elements.extend(
                cls._conversation_history(
                    state,
                    rendered_presentations,
                    chat_id,
                )
            )
        elif state.view == 'assistant':
            elements.extend(cls._assistant(state, chat_id))
        elif state.view == 'settings':
            elements.extend(
                cls._settings(state, rendered_presentations, chat_id)
            )
        elif state.view == 'context':
            elements.extend(
                cls._context(state, rendered_presentations, chat_id)
            )
        else:
            # Native Feishu messages own the conversation surface. Management
            # cards only render one of the four bot-menu pages.
            elements.extend(cls._assistant(state, chat_id))
        return {
            'schema': '2.0',
            'config': {
                'wide_screen_mode': True,
                'streaming_mode': rendered_streaming,
                'update_multi': True,
                'streaming_config': {
                    'print_frequency_ms': {
                        'default': 20,
                        'android': 20,
                        'ios': 20,
                        'pc': 20,
                    },
                    'print_step': {
                        'default': 4,
                        'android': 4,
                        'ios': 4,
                        'pc': 4,
                    },
                    'print_strategy': 'fast',
                },
                'summary': {
                    'content': (
                        'LazyMind is loading'
                        if rendered_streaming and state.output_language == 'en'
                        else 'LazyMind 正在加载'
                        if rendered_streaming
                        else 'LazyMind menu'
                        if state.output_language == 'en'
                        else 'LazyMind 菜单'
                    ),
                },
            },
            'header': {
                'title': {
                    'tag': 'plain_text',
                    'content': 'LazyMind',
                },
                'template': 'blue',
            },
            'body': {'elements': elements},
        }

    @staticmethod
    def _capabilities(
        state: FeishuWorkspaceState,
        presentations: list[dict[str, Any]],
        chat_id: str,
        *,
        has_conversation: bool,
    ) -> list[dict[str, Any]]:
        groups = _capability_groups(presentations)
        settings = next(
            (
                item
                for item in presentations
                if item.get('kind') == 'conversation_settings'
            ),
            {},
        )
        selected = {(item.type, item.id) for item in state.context_draft}
        selection_scope_note = _localized(
            state,
            '资源与执行能力仅影响当前会话',
            'Resources and execution options affect only this conversation',
        )
        elements: list[dict[str, Any]] = [
            _heading_action(
                title=(
                    _localized(state, '本会话能力', 'Conversation capabilities')
                    if has_conversation
                    else _localized(state, '新会话能力', 'New conversation capabilities')
                ),
                description=(
                    _localized(
                        state,
                        '选择后持续用于本会话。',
                        'Selections remain active in this conversation.',
                    )
                    if has_conversation
                    else _localized(
                        state,
                        '选择后用于即将开始的会话；发送首条消息后自动绑定。',
                        'Selections bind after the first message in the new conversation.',
                    )
                ),
                button={
                    'label': (
                        _localized(state, '应用到本会话', 'Apply')
                        if has_conversation
                        else _localized(state, '用于首轮对话', 'Apply to first turn')
                    ),
                    'style': 'primary',
                    'action': _command_action(
                        chat_id=chat_id,
                        text='应用能力到本会话',
                        command=_TAB_COMMANDS['chat'],
                        workspace_action={'kind': 'capability.save'},
                    ),
                },
            ),
            {
                'tag': 'markdown',
                'content': (
                    f'<font color="blue">{len(selected)} '
                    f'{_localized(state, "项已选", "selected")}</font>　'
                    f'<font color="grey">{selection_scope_note}</font>'
                ),
            },
        ]
        if not groups:
            elements.append(
                {
                    'tag': 'markdown',
                    'content': _localized(
                        state,
                        '<font color="grey">正在同步能力列表…</font>',
                        '<font color="grey">Syncing capabilities…</font>',
                    ),
                }
            )
        for group in groups:
            items = group.get('items')
            values = items if isinstance(items, list) else []
            resource_type = str(group.get('resource_type') or '')
            label = _localized(
                state,
                str(group.get('label') or '能力'),
                {
                    'knowledge_base': 'Knowledge bases',
                    'skill': 'Skills',
                    'plugin': 'Workflows',
                    'tool': 'Tools',
                    'conversation': 'Conversations',
                }.get(resource_type, 'Capabilities'),
            )
            option_elements: list[dict[str, Any]] = []
            visible_values = values[:_MAX_CAPABILITY_ITEMS_PER_GROUP]
            for start in range(0, len(visible_values), 2):
                option_elements.append(
                    _button_row(
                        [
                            {
                                'label': (
                                    f'{"✓" if (resource_type, str(item.get("id") or "")) in selected else "＋"} '
                                    f'{str(item.get("name") or "未命名")[:28]}'
                                ),
                                'style': (
                                    'primary'
                                    if (
                                        resource_type,
                                        str(item.get('id') or ''),
                                    ) in selected
                                    else 'default'
                                ),
                                'action': _resource_toggle_action(
                                    chat_id=chat_id,
                                    kind='capability.toggle',
                                    scope='conversation',
                                    category=resource_type,
                                    item=item,
                                ),
                            }
                            for item in visible_values[start:start + 2]
                            if isinstance(item, dict) and item.get('id')
                        ]
                    )
                )
            if len(values) > len(visible_values):
                truncated_note = _localized(
                    state,
                    f'仅展示前 {len(visible_values)} 项；可进入分类管理全部资源。',
                    (
                        f'Showing the first {len(visible_values)} items; '
                        'open the category to manage all resources.'
                    ),
                )
                option_elements.extend(
                    [
                        {
                            'tag': 'markdown',
                            'content': (
                                '<font color="grey">'
                                + truncated_note
                                + '</font>'
                            ),
                        },
                        _button_row(
                            [
                                {
                                    'label': _localized(
                                        state,
                                        f'管理全部 {len(values)} 项',
                                        f'Manage all {len(values)}',
                                    ),
                                    'style': 'default',
                                    'action': _local_action(
                                        chat_id=chat_id,
                                        text=f'管理全部{label}',
                                        workspace_action={
                                            'kind': 'context.open',
                                            'scope': 'conversation',
                                            'category': resource_type,
                                        },
                                    ),
                                }
                            ]
                        ),
                    ]
                )
            elements.extend(
                [
                    {
                        'tag': 'markdown',
                        'content': (
                            f'**{label}**　'
                            f'<font color="blue">'
                            f'{sum(item_type == resource_type for item_type, _ in selected)} '
                            f'{_localized(state, "项已选", "selected")}'
                            '</font>'
                        ),
                    },
                    *(
                        option_elements
                        or [
                            {
                                'tag': 'markdown',
                                'content': (
                                    '<font color="grey">'
                                    + _localized(
                                        state,
                                        '当前分类暂无可用资源。',
                                        'No resources are available in this category.',
                                    )
                                    + '</font>'
                                ),
                            }
                        ]
                    ),
                ]
            )
        workflow_enabled = bool(settings.get('workflow_enabled', True))
        subagent_enabled = bool(settings.get('subagent_enabled', True))
        personalization_enabled = bool(
            settings.get('personalization_enabled', True)
        )
        workflow_mode = str(settings.get('workflow_mode') or 'dynamic')
        elements.extend(
            [
                {
                    'tag': 'markdown',
                    'content': _localized(
                        state,
                        '**执行能力**　<font color="grey">点击即可切换</font>',
                        '**Execution**　<font color="grey">Click to toggle</font>',
                    ),
                },
                _button_row(
                    [
                        {
                            'label': (
                                _localized(state, '✓ Workflow 自动执行', '✓ Workflow auto-run')
                                if workflow_enabled and workflow_mode == 'auto'
                                else _localized(
                                    state,
                                    '○ Workflow 执行前确认',
                                    '○ Confirm Workflow runs',
                                )
                            ),
                            'style': (
                                'primary'
                                if workflow_enabled and workflow_mode == 'auto'
                                else 'default'
                            ),
                            'action': _setting_action(
                                chat_id,
                                {
                                    'setting': 'workflow_mode',
                                    'mode': (
                                        'dynamic'
                                        if workflow_mode == 'auto'
                                        else 'auto'
                                    ),
                                },
                                '切换 Workflow 执行方式',
                                view='capabilities',
                            ),
                        },
                        {
                            'label': (
                                '✓ SubAgent'
                                if subagent_enabled
                                else '○ SubAgent'
                            ),
                            'style': (
                                'primary' if subagent_enabled else 'default'
                            ),
                            'action': _setting_action(
                                chat_id,
                                {
                                    'setting': 'subagent',
                                    'enabled': not subagent_enabled,
                                },
                                '切换 SubAgent',
                                view='capabilities',
                            ),
                        },
                    ]
                ),
                _button_row(
                    [
                        {
                            'label': (
                                _localized(
                                    state,
                                    '✓ 使用个人习惯',
                                    '✓ Personalization',
                                )
                                if personalization_enabled
                                else _localized(
                                    state,
                                    '○ 使用个人习惯',
                                    '○ Personalization',
                                )
                            ),
                            'style': (
                                'primary'
                                if personalization_enabled
                                else 'default'
                            ),
                            'action': _setting_action(
                                chat_id,
                                {
                                    'setting': 'personalization',
                                    'enabled': not personalization_enabled,
                                },
                                '切换个人习惯',
                                view='capabilities',
                            ),
                        }
                    ]
                ),
            ]
        )
        return elements

    @staticmethod
    def _conversation_history(
        state: FeishuWorkspaceState,
        presentations: list[dict[str, Any]],
        chat_id: str,
    ) -> list[dict[str, Any]]:
        loading = not presentations
        panel_elements: list[dict[str, Any]] = [
            _heading_action(
                title=_localized(state, '切换会话', 'Switch conversation'),
                description=_localized(
                    state,
                    '选择会话后，后续原生聊天会继续使用对应上下文与能力。',
                    'Select a conversation to continue with its context and capabilities.',
                ),
                button={
                    'label': _localized(state, '＋ 新建', '＋ New'),
                    'style': 'primary',
                    'action': _new_session_action(
                        chat_id,
                        kind='new_session.open',
                        mode=state.new_session_mode,
                    ),
                },
            ),
        ]
        if state.conversation_title:
            title = ' '.join(state.conversation_title.split())
            panel_elements.append(
                {
                    'tag': 'markdown',
                    'content': (
                        f'**{_localized(state, "当前会话", "Current conversation")}**　'
                        f'{title}'
                    ),
                }
            )
        if state.conversation_switch_status:
            index = state.conversation_switch_index
            if state.conversation_switch_status == 'running':
                notice = _localized(
                    state,
                    (
                        f'⏳ **正在切换到第 {index} 个会话…**'
                        if index
                        else '⏳ **正在切换会话…**'
                    ),
                    (
                        f'⏳ **Switching to conversation {index}…**'
                        if index
                        else '⏳ **Switching conversation…**'
                    ),
                )
            elif state.conversation_switch_status == 'completed':
                notice = _localized(
                    state,
                    (
                        f'✅ **第 {index} 个会话已生效**'
                        if index
                        else '✅ **所选会话已生效**'
                    ),
                    (
                        f'✅ **Conversation {index} is now active**'
                        if index
                        else '✅ **The selected conversation is now active**'
                    ),
                )
            else:
                notice = _localized(
                    state,
                    '⚠️ **会话列表已更新，请刷新后重新选择**',
                    '⚠️ **The conversation list changed. Refresh and choose again.**',
                )
            panel_elements.append({'tag': 'markdown', 'content': notice})
            if state.conversation_switch_status == 'expired':
                panel_elements.append(
                    _button_row(
                        [
                            {
                                'label': _localized(
                                    state,
                                    '刷新会话列表',
                                    'Refresh conversations',
                                ),
                                'style': 'primary',
                                'action': _history_refresh_action(chat_id),
                            }
                        ]
                    )
                )
        if state.new_session_open:
            panel_elements.extend(
                [
                    {
                        'tag': 'markdown',
                        'content': _localized(
                            state,
                            '**新建会话起点**',
                            '**New conversation starting point**',
                        ),
                    },
                    {
                        'tag': 'markdown',
                        'content': _localized(
                            state,
                            '<font color="grey">只复制能力选择，不复制聊天记录。</font>',
                            '<font color="grey">Copies capability selections, not chat history.</font>',
                        ),
                    },
                    _button_row(
                        [
                            {
                                'label': (
                                    _localized(state, '✓ 空白会话', '✓ Blank')
                                    if state.new_session_mode == 'blank'
                                    else _localized(state, '○ 空白会话', '○ Blank')
                                ),
                                'style': (
                                    'primary'
                                    if state.new_session_mode == 'blank'
                                    else 'default'
                                ),
                                'action': _new_session_action(
                                    chat_id,
                                    kind='new_session.mode',
                                    mode='blank',
                                ),
                            },
                            {
                                'label': (
                                    _localized(state, '✓ 继承当前能力', '✓ Keep capabilities')
                                    if state.new_session_mode == 'inherit'
                                    else _localized(state, '○ 继承当前能力', '○ Keep capabilities')
                                ),
                                'style': (
                                    'primary'
                                    if state.new_session_mode == 'inherit'
                                    else 'default'
                                ),
                                'action': _new_session_action(
                                    chat_id,
                                    kind='new_session.mode',
                                    mode='inherit',
                                ),
                            },
                        ]
                    ),
                    _button_row(
                        [
                            {
                                'label': _localized(state, '取消', 'Cancel'),
                                'style': 'default',
                                'action': _new_session_action(
                                    chat_id,
                                    kind='new_session.cancel',
                                    mode=state.new_session_mode,
                                ),
                            },
                            {
                                'label': _localized(state, '创建会话', 'Create'),
                                'style': 'primary',
                                'action': _new_session_action(
                                    chat_id,
                                    kind='new_session.create',
                                    mode=state.new_session_mode,
                                    create=True,
                                ),
                            },
                        ]
                    ),
                    {'tag': 'hr'},
                ]
            )
        if loading:
            panel_elements.append(
                _button_row(
                    [
                        {
                            'label': _localized(state, '同步会话', 'Sync conversations'),
                            'style': 'default',
                            'action': _history_refresh_action(chat_id),
                        }
                    ]
                )
            )
        panel_elements.extend(
            FeishuWorkspaceRenderer._selection(
                presentations,
                chat_id,
                workspace_action={
                    'kind': 'history.switch',
                    'view': 'conversations',
                },
                selected_value=(
                    str(state.conversation_switch_index)
                    if state.conversation_switch_index
                    else ''
                ),
                loading=state.conversation_switch_status == 'running',
                empty=(
                    _localized(
                        state,
                        '<font color="grey">尚未同步历史会话。</font>',
                        '<font color="grey">Conversation history has not been synced.</font>',
                    )
                    if loading
                    else _localized(
                        state,
                        '暂时没有历史会话。',
                        'No previous conversations yet.',
                    )
                ),
            )
        )
        return panel_elements

    @staticmethod
    def _assistant(
        state: FeishuWorkspaceState,
        chat_id: str,
    ) -> list[dict[str, Any]]:
        return render_assistant(state, chat_id)

    @staticmethod
    def _settings(
        state: FeishuWorkspaceState,
        presentations: list[dict[str, Any]],
        chat_id: str,
    ) -> list[dict[str, Any]]:
        del presentations
        elements = [
            _heading_action(
                title=_localized(state, '体验设置', 'Experience settings'),
                description=_localized(
                    state,
                    '控制 LazyMind 的思考、语言、呈现和会话行为。',
                    'Control LazyMind thinking, language, presentation, and conversation behavior.',
                ),
                button={
                    'label': (
                        _localized(state, '保存设置', 'Save settings')
                        if state.preferences_dirty
                        else _localized(state, '已保存', 'Saved')
                    ),
                    'style': 'primary',
                    'disabled': not state.preferences_dirty,
                    'action': _preference_action(
                        chat_id,
                        'settings_save',
                        True,
                    ),
                },
            ),
            {
                'tag': 'markdown',
                'content': _localized(
                    state,
                    '**思考深度**',
                    '**Thinking depth**',
                ),
            },
            *[
                _button_row(
                    [
                        {
                            'label': label,
                            'style': (
                                'primary'
                                if state.thinking_depth == value
                                else 'default'
                            ),
                            'action': _preference_action(
                                chat_id,
                                'thinking_depth',
                                value,
                            ),
                        }
                        for label, value in row
                    ]
                )
                for row in (
                    (
                        (_localized(state, '简洁', 'Concise'), 'low'),
                        (_localized(state, '标准', 'Standard'), 'medium'),
                    ),
                    (
                        (_localized(state, '深入', 'In-depth'), 'high'),
                        (_localized(state, '极致（Max）', 'Maximum'), 'max'),
                    ),
                )
            ],
            {'tag': 'hr'},
            {
                'tag': 'markdown',
                'content': _localized(
                    state,
                    '**语言设置**',
                    '**Language settings**',
                ),
            },
            _button_row(
                [
                    {
                        'label': label,
                        'style': (
                            'primary'
                            if state.output_language == value
                            else 'default'
                        ),
                        'action': _preference_action(
                            chat_id,
                            'output_language',
                            value,
                        ),
                    }
                    for label, value in (
                        ('中文', 'zh'),
                        ('English', 'en'),
                    )
                ]
            ),
            {'tag': 'hr'},
            {
                'tag': 'markdown',
                'content': _localized(
                    state,
                    '**呈现与通知**',
                    '**Presentation and notifications**',
                ),
            },
            _button_row(
                [
                    {
                        'label': (
                            _localized(state, '✓ 显示执行摘要', '✓ Show execution summary')
                            if state.show_process
                            else _localized(state, '＋ 显示执行摘要', '＋ Show execution summary')
                        ),
                        'style': 'primary' if state.show_process else 'default',
                        'action': _preference_action(
                            chat_id,
                            'show_process',
                            not state.show_process,
                        ),
                    },
                    {
                        'label': (
                            _localized(state, '✓ 完成后折叠进度', '✓ Collapse progress on completion')
                            if state.auto_collapse_process
                            else _localized(state, '＋ 完成后折叠进度', '＋ Collapse progress on completion')
                        ),
                        'style': (
                            'primary'
                            if state.auto_collapse_process
                            else 'default'
                        ),
                        'action': _preference_action(
                            chat_id,
                            'auto_collapse_process',
                            not state.auto_collapse_process,
                        ),
                    },
                ]
            ),
            _button_row(
                [
                    {
                        'label': (
                            _localized(state, '✓ 显示参考来源', '✓ Show sources')
                            if state.show_sources
                            else _localized(state, '＋ 显示参考来源', '＋ Show sources')
                        ),
                        'style': 'primary' if state.show_sources else 'default',
                        'action': _preference_action(
                            chat_id,
                            'show_sources',
                            not state.show_sources,
                        ),
                    },
                    {
                        'label': (
                            _localized(state, '✓ 结果就绪标记', '✓ Mark ready results')
                            if state.ready_marker
                            else _localized(state, '＋ 结果就绪标记', '＋ Mark ready results')
                        ),
                        'style': 'primary' if state.ready_marker else 'default',
                        'action': _preference_action(
                            chat_id,
                            'ready_marker',
                            not state.ready_marker,
                        ),
                    },
                ]
            ),
            _button_row(
                [
                    {
                        'label': (
                            _localized(state, '✓ 返回时恢复上次分区', '✓ Restore last section')
                            if state.restore_last_view
                            else _localized(state, '＋ 返回时恢复上次分区', '＋ Restore last section')
                        ),
                        'style': (
                            'primary' if state.restore_last_view else 'default'
                        ),
                        'action': _preference_action(
                            chat_id,
                            'restore_last_view',
                            not state.restore_last_view,
                        ),
                    }
                ]
            ),
            {'tag': 'hr'},
            {
                'tag': 'markdown',
                'content': _localized(
                    state,
                    '**会话维护**',
                    '**Conversation maintenance**',
                ),
            },
            _button_row(
                [
                    {
                        'label': _localized(state, '清空本轮临时资源', 'Clear next-turn resources'),
                        'style': 'danger',
                        'action': _maintenance_action(
                            chat_id,
                            kind='maintenance.clear_turn',
                        ),
                        'confirm': {
                            'title': _localized(
                                state,
                                '清空本轮临时资源？',
                                'Clear next-turn resources?',
                            ),
                            'text': _localized(
                                state,
                                '将移除尚未发送的仅下一轮资源，不影响本会话常驻能力。',
                                (
                                    'Removes unsent next-turn resources without '
                                    'changing this conversation\'s persistent capabilities.'
                                ),
                            ),
                        },
                    },
                    {
                        'label': _localized(state, '恢复默认设置', 'Restore defaults'),
                        'style': 'danger',
                        'action': _maintenance_action(
                            chat_id,
                            kind='maintenance.reset_preferences',
                        ),
                        'confirm': {
                            'title': _localized(
                                state,
                                '恢复默认设置？',
                                'Restore default settings?',
                            ),
                            'text': _localized(
                                state,
                                '将覆盖当前体验设置并恢复系统默认值；能力选择不受影响。',
                                (
                                    'Resets experience settings without changing '
                                    'capability selections.'
                                ),
                            ),
                        },
                    },
                ]
            ),
            _button_row(
                [
                    {
                        'label': _localized(state, '清空当前会话上下文', 'Clear conversation context'),
                        'style': 'danger_filled',
                        'action': _maintenance_action(
                            chat_id,
                            kind='maintenance.clear_conversation',
                            create=True,
                        ),
                        'confirm': {
                            'title': _localized(
                                state,
                                '清空当前会话上下文？',
                                'Clear conversation context?',
                            ),
                            'text': _localized(
                                state,
                                (
                                    '将清除当前会话记忆与任务状态，后续回答不再引用'
                                    '当前会话内容。此操作不可撤销。'
                                ),
                                (
                                    'Clears current memory and task state. '
                                    'This cannot be undone.'
                                ),
                            ),
                        },
                    }
                ]
            ),
        ]
        return elements

    @staticmethod
    def _context(
        state: FeishuWorkspaceState,
        presentations: list[dict[str, Any]],
        chat_id: str,
    ) -> list[dict[str, Any]]:
        selected_names = [item.name for item in state.context_draft]
        selected_preview = (
            '、' if state.output_language == 'zh' else ', '
        ).join(selected_names[:4])
        if len(selected_names) > 4:
            selected_preview += _localized(
                state,
                f' 等 {len(selected_names)} 项',
                f' and {len(selected_names) - 4} more',
            )
        selected_summary = _localized(
            state,
            f'{len(selected_names)} 项已选',
            f'{len(selected_names)} selected',
        )
        selected_detail = (
            _localized(state, '**已选：** ', '**Selected:** ')
            + selected_preview
            if selected_preview
            else _localized(
                state,
                '<font color="grey">当前尚未选择资源。</font>',
                '<font color="grey">No resources selected.</font>',
            )
        )
        elements: list[dict[str, Any]] = [
            {
                'tag': 'markdown',
                'content': (
                    (
                        _localized(
                            state,
                            '**管理本会话能力**　',
                            '**Manage conversation capabilities**　',
                        )
                        + f'<font color="blue">{selected_summary}</font>\n'
                        + _localized(
                            state,
                            '<font color="grey">点击选项切换；带 ✓ 的项目将在保存后持续用于当前会话。</font>',
                            '<font color="grey">Toggle items below. Checked items remain active after saving.</font>',
                        )
                    )
                    if state.context_scope == 'conversation'
                    else (
                        _localized(
                            state,
                            '**添加仅下一轮资源**　',
                            '**Add resources for the next turn**　',
                        )
                        + f'<font color="blue">{selected_summary}</font>\n'
                        + _localized(
                            state,
                            '<font color="grey">点击选项切换；带 ✓ 的项目将在保存后用于下一条消息。</font>',
                            '<font color="grey">Toggle items below. Checked items apply to the next message.</font>',
                        )
                    )
                    + '\n'
                    + selected_detail
                ),
            },
            {'tag': 'hr'},
        ]
        categories = [
            (
                category,
                _localized(
                    state,
                    label,
                    {
                        'knowledge_base': 'Knowledge bases',
                        'skill': 'Skills',
                        'plugin': 'Workflows',
                        'tool': 'Tools',
                        'conversation': 'Conversations',
                        'prompt': 'Prompts',
                    }[category],
                ),
            )
            for category, label in _CONTEXT_LABELS.items()
        ]
        for start in range(0, len(categories), 3):
            elements.append(
                _button_row(
                    [
                        {
                            'label': label,
                            'style': (
                                'primary'
                                if state.context_category == category
                                else 'default'
                            ),
                            'action': _context_command_action(
                                chat_id,
                                kind='context.category',
                                scope=state.context_scope,
                                category=category,
                            ),
                        }
                        for category, label in categories[start:start + 3]
                    ]
                )
            )
        group = next(
            (
                group
                for group in _capability_groups(presentations)
                if group.get('resource_type') == state.context_category
            ),
            {},
        )
        items = group.get('items')
        values = items if isinstance(items, list) else []
        page_count = max(
            1,
            (len(values) + _CONTEXT_PAGE_SIZE - 1) // _CONTEXT_PAGE_SIZE,
        )
        page = min(state.context_page, page_count - 1)
        page_start = page * _CONTEXT_PAGE_SIZE
        page_values = values[
            page_start:page_start + _CONTEXT_PAGE_SIZE
        ]
        selected = {
            (item.type, item.id)
            for item in state.context_draft
        }
        elements.append({'tag': 'hr'})
        if state.context_category == 'prompt':
            elements.append(
                {
                    'tag': 'markdown',
                    'content': _localized(
                        state,
                        '<font color="grey">点击 Prompt 会直接作为一条新消息发送。</font>',
                        '<font color="grey">Selecting a prompt sends it as a new message.</font>',
                    ),
                }
            )
        if not values:
            elements.append(
                {
                    'tag': 'markdown',
                    'content': _localized(
                        state,
                        '当前分类暂无可用资源。',
                        'No resources are available in this category.',
                    ),
                }
            )
        else:
            for start in range(0, len(page_values), 2):
                elements.append(
                    _button_row(
                        [
                            {
                                'label': (
                                    f'▶ {str(item.get("name") or _localized(state, "未命名", "Untitled"))[:32]}'
                                    if state.context_category == 'prompt'
                                    else (
                                        f'{"✓" if _item_key(state, item) in selected else "＋"} '
                                        f'{str(item.get("name") or _localized(state, "未命名", "Untitled"))[:32]}'
                                    )
                                ),
                                'style': (
                                    'primary'
                                    if (
                                        state.context_category != 'prompt'

                                        and _item_key(state, item)
                                        in selected
                                    )
                                    else 'default'
                                ),
                                'action': (
                                    _prompt_action(chat_id, item)
                                    if state.context_category == 'prompt'
                                    else _context_toggle_action(
                                        chat_id,
                                        state,
                                        item,
                                    )
                                ),
                            }
                            for item in page_values[start:start + 2]
                            if isinstance(item, dict) and item.get('id')
                        ]
                    )
                )
        if page_count > 1:
            elements.append(
                _button_row(
                    [
                        {
                            'label': _localized(state, '上一页', 'Previous'),
                            'style': 'default',
                            'disabled': page == 0,
                            'action': _context_page_action(chat_id, page - 1),
                        },
                        {
                            'label': f'{page + 1} / {page_count}',
                            'style': 'default',
                            'disabled': True,
                            'action': _context_page_action(chat_id, page),
                        },
                        {
                            'label': _localized(state, '下一页', 'Next'),
                            'style': 'default',
                            'disabled': page + 1 == page_count,
                            'action': _context_page_action(chat_id, page + 1),
                        },
                    ]
                )
            )
        elements.extend(
            [
                {'tag': 'hr'},
                _button_row(
                    [
                        {
                            'label': _localized(state, '返回', 'Back'),
                            'style': 'default',
                            'action': _command_action(
                                chat_id=chat_id,
                                text='返回能力',
                                command=_TAB_COMMANDS['capabilities'],
                                workspace_action={
                                    'kind': 'navigate',
                                    'view': 'capabilities',
                                },
                            ),
                        },
                        {
                            'label': (
                                _localized(
                                    state,
                                    f'应用到本会话 · {len(selected_names)} 项',
                                    f'Apply to conversation · {len(selected_names)}',
                                )
                                if state.context_scope == 'conversation'
                                else _localized(
                                    state,
                                    f'应用到下一轮 · {len(selected_names)} 项',
                                    f'Apply to next turn · {len(selected_names)}',
                                )
                            ),
                            'style': 'primary',
                            'action': _command_action(
                                chat_id=chat_id,
                                text=(
                                    '应用资源到本会话'
                                    if state.context_scope == 'conversation'
                                    else '应用资源到下一轮'
                                ),
                                command=_TAB_COMMANDS[
                                    'capabilities'
                                    if state.context_scope == 'conversation'
                                    else 'chat'
                                ],
                                workspace_action={'kind': 'context.save'},
                            ),
                        },
                    ]
                ),
            ]
        )
        return elements

    @staticmethod
    def _selection(
        presentations: list[dict[str, Any]],
        chat_id: str,
        *,
        empty: str = '',
        workspace_action: dict[str, Any] | None = None,
        selected_value: str = '',
        loading: bool = False,
    ) -> list[dict[str, Any]]:
        selection = next(
            (
                item
                for item in presentations
                if item.get('kind') == 'selection'
            ),
            None,
        )
        if not isinstance(selection, dict):
            return [{'tag': 'markdown', 'content': empty}] if empty else []
        options = selection.get('options')
        values = options if isinstance(options, list) else []
        elements: list[dict[str, Any]] = []
        for start in range(0, len(values), 2):
            elements.append(
                _button_row(
                    [
                        {
                            'label': (
                                f'{"⏳" if loading else "✓"} {str(option.get("value") or "")}. '
                                f'{str(option.get("label") or "")}'
                                if str(option.get('value') or '') == selected_value
                                else (
                                    f'{str(option.get("value") or "")}. '
                                    f'{str(option.get("label") or "")}'
                                )
                            )[:40],
                            'style': (
                                'primary'
                                if str(option.get('value') or '') == selected_value
                                else 'default'
                            ),
                            'disabled': loading,
                            'action': {
                                'lazymind_action': 'select',
                                'selection_id': str(
                                    selection.get('selection_id') or ''
                                ),
                                'selection': str(option.get('value') or ''),
                                'text': str(option.get('value') or ''),
                                'intended_chat_id': chat_id,
                                'workspace_action': dict(
                                    workspace_action
                                    or {
                                        'kind': 'navigate',
                                        'view': 'chat',
                                    }
                                ),
                            },
                        }
                        for option in values[start:start + 2]
                        if isinstance(option, dict)
                    ]
                )
            )
        return elements


def _resources(value: Any) -> list[WorkspaceResource]:
    return _merge_resources(
        [
            resource
            for resource in (
                WorkspaceResource.from_dict(item)
                for item in (value if isinstance(value, list) else [])
            )
            if resource is not None
        ]
    )


def _presentations(value: Any) -> list[dict[str, Any]]:
    return [
        dict(item)
        for item in (
            value if isinstance(value, list) else []
        )[:_MAX_SNAPSHOT_PRESENTATIONS]
        if isinstance(item, dict) and item.get('kind')
    ]


def _view_snapshots(value: Any) -> dict[str, dict[str, Any]]:
    if not isinstance(value, dict):
        return {}
    snapshots: dict[str, dict[str, Any]] = {}
    for view in ('capabilities', 'history', 'settings', 'assistant'):
        snapshot = value.get(view)
        if not isinstance(snapshot, dict):
            continue
        snapshots[view] = {
            'text': str(snapshot.get('text') or '')[:_MAX_CARD_ANSWER_CHARS],
            'presentations': _presentations(snapshot.get('presentations')),
        }
        if view == 'assistant':
            snapshots[view]['threads'] = _assistant_threads(
                snapshot.get('threads')
            )
    return snapshots


def _assistant_threads(value: Any) -> list[dict[str, Any]]:
    return [
        {
            'id': str(item.get('id') or item.get('thread_id') or '')[:512],
            'title': str(
                item.get('title')
                or item.get('name')
                or item.get('preview')
                or '未命名 Codex 会话'
            )[:200],
            'preview': str(item.get('preview') or item.get('summary') or '')[:300],
            'source': str(item.get('source') or '')[:100],
            'cwd': str(item.get('cwd') or '')[:500],
            'updated_at': str(
                item.get('updated_at')
                or item.get('updatedAt')
                or ''
            )[:100],
            'available': bool(item.get('available', False)),
            'managed': bool(
                item.get('managed')
                or item.get('managed_by_lazymind')
                or False
            ),
            'status': (
                dict(item.get('status'))
                if isinstance(item.get('status'), dict)
                else {'type': str(item.get('status') or 'idle')}
            ),
        }
        for item in (value if isinstance(value, list) else [])[
            :_MAX_ASSISTANT_THREADS
        ]
        if isinstance(item, dict) and (item.get('id') or item.get('thread_id'))
    ]


def _assistant_turns(value: Any) -> list[dict[str, str]]:
    turns: list[dict[str, str]] = []
    for item in value if isinstance(value, list) else []:
        if not isinstance(item, dict):
            continue
        role = str(item.get('role') or '')
        text = str(item.get('text') or item.get('content') or '').strip()
        if role and text:
            turns.append({
                'role': role[:30],
                'text': text[:_MAX_ASSISTANT_MESSAGE_CHARS],
            })
    return turns[-4:]


def _assistant_request(value: Any) -> dict[str, Any] | None:
    if not isinstance(value, dict) or not value.get('request_id'):
        return None
    return {
        'request_id': str(value.get('request_id'))[:512],
        'kind': str(value.get('kind') or 'request')[:100],
        'summary': str(value.get('summary') or '')[:1000],
        'payload': (
            dict(value.get('payload'))
            if isinstance(value.get('payload'), dict)
            else {}
        ),
    }


def _workspace_images(value: Any) -> list[dict[str, str]]:
    images: list[dict[str, str]] = []
    for item in value if isinstance(value, list) else []:
        if not isinstance(item, dict):
            continue
        image_key = str(item.get('image_key') or '')
        if not is_feishu_image_key(image_key):
            continue
        images.append(
            {
                'image_key': image_key[:1024],
                'caption': str(item.get('caption') or '')[:300],
                'identity': str(
                    item.get('identity') or image_key
                )[:512],
            }
        )
    return images[-_MAX_WORKSPACE_IMAGES:]


def _integer(value: Any) -> int:
    try:
        return int(value)
    except (TypeError, ValueError):
        return 0


def _resources_from_mentions(value: Any) -> list[WorkspaceResource]:
    return _resources(
        [
            {
                'type': item.get('type'),
                'id': item.get('resource_id'),
                'name': item.get('display_name'),
            }
            for item in (value if isinstance(value, list) else [])
            if isinstance(item, dict)
        ]
    )


def _merge_resources(
    *groups: list[WorkspaceResource],
) -> list[WorkspaceResource]:
    merged: dict[tuple[str, str], WorkspaceResource] = {}
    for resource in (item for group in groups for item in group):
        if resource.type == 'plugin':
            merged = {
                key: item
                for key, item in merged.items()
                if item.type != 'plugin'
            }
        merged[(resource.type, resource.id)] = resource
    conversation_keys = [
        key
        for key, resource in merged.items()
        if resource.type == 'conversation'
    ]
    allowed_conversations = set(conversation_keys[-3:])
    return [
        resource
        for key, resource in merged.items()
        if (
            resource.type != 'conversation'
            or key in allowed_conversations
        )
    ]


def _button(
    item: dict[str, Any],
    *,
    width: str = 'fill',
    size: str = 'medium',
) -> dict[str, Any]:
    result = {
        'tag': 'button',
        'text': {
            'tag': 'plain_text',
            'content': str(item.get('label') or ''),
        },
        'type': str(item.get('style') or 'default'),
        'size': str(item.get('size') or size),
        'width': str(item.get('width') or width),
        'value': dict(item.get('action') or {}),
    }
    confirm = item.get('confirm')
    if isinstance(confirm, dict):
        result['confirm'] = {
            'title': {
                'tag': 'plain_text',
                'content': str(confirm.get('title') or '确认操作？'),
            },
            'text': {
                'tag': 'plain_text',
                'content': str(confirm.get('text') or ''),
            },
        }
    if bool(item.get('disabled')):
        result['disabled'] = True
    return result


def _button_row(items: list[dict[str, Any]]) -> dict[str, Any]:
    return {
        'tag': 'column_set',
        'flex_mode': 'none',
        'horizontal_spacing': '8px',
        'columns': [
            {
                'tag': 'column',
                'width': 'weighted',
                'weight': 1,
                'elements': [_button(item)],
            }
            for item in items
        ],
    }


def _heading_action(
    *,
    title: str,
    description: str,
    button: dict[str, Any],
) -> dict[str, Any]:
    return {
        'tag': 'column_set',
        'flex_mode': 'none',
        'vertical_align': 'top',
        'columns': [
            {
                'tag': 'column',
                'width': 'weighted',
                'weight': 4,
                'elements': [{
                    'tag': 'markdown',
                    'content': (
                        f'**{title}**\n'
                        f'<font color="grey">{description}</font>'
                    ),
                }],
            },
            {
                'tag': 'column',
                'width': 'auto',
                'elements': [
                    _button(button, width='default', size='small')
                ],
            },
        ],
    }


def render_assistant(state: Any, chat_id: str) -> list[dict[str, Any]]:
    if state.assistant_mode == 'detail':
        return _detail(state, chat_id)
    if state.assistant_mode == 'sessions':
        return _sessions(state, chat_id)
    return _projects(state, chat_id)


def _action(
    chat_id: str,
    kind: str,
    text: str,
    **values: Any,
) -> dict[str, Any]:
    return {
        'lazymind_action': 'local',
        'text': text,
        'intended_chat_id': chat_id,
        'workspace_action': {'kind': kind, **values},
    }


def _pagination(
    state: Any,
    chat_id: str,
    current_page: int,
    page_count: int,
    action_kind: str,
) -> dict[str, Any]:
    current_page = min(max(0, current_page), max(0, page_count - 1))
    is_turns = action_kind == 'assistant.turns_page'
    select = {
        'tag': 'select_static',
        'element_id': 'asst_turn_page' if is_turns else 'asst_session_page',
        'type': 'text',
        'width': 'fill',
        'initial_option': f'第 {current_page + 1} 页',
        'placeholder': {
            'tag': 'plain_text',
            'content': f'第 {current_page + 1} 页',
        },
        'behaviors': [{
            'type': 'callback',
            'value': _action(
                chat_id,
                action_kind,
                '跳转 Codex 分页',
            ),
        }],
        'options': [
            {
                'text': {
                    'tag': 'plain_text',
                    'content': f'第 {index + 1} 页',
                },
                'value': str(index),
            }
            for index in range(page_count)
        ],
    }
    previous = _button({
        'label': _localized(state, '上一页', 'Previous'),
        'disabled': current_page == 0,
        'action': _action(
            chat_id,
            action_kind,
            'Codex 上一页',
            direction='older' if is_turns else 'previous',
        ),
    })
    next_button = _button({
        'label': _localized(state, '下一页', 'Next'),
        'disabled': current_page >= page_count - 1,
        'action': _action(
            chat_id,
            action_kind,
            'Codex 下一页',
            direction='newer' if is_turns else 'next',
        ),
    })
    return {
        'tag': 'column_set',
        'flex_mode': 'none',
        'horizontal_spacing': '8px',
        'columns': [
            {
                'tag': 'column',
                'width': 'weighted',
                'weight': 1,
                'elements': [element],
            }
            for element in (previous, select, next_button)
        ],
    }


def _project_name(cwd: str) -> str:
    normalized = str(cwd or '').rstrip('/\\')
    if not normalized:
        return '未归属项目'
    return PurePath(normalized).name or normalized


def _project_groups(threads: list[dict[str, Any]]) -> list[dict[str, Any]]:
    grouped: dict[str, dict[str, Any]] = {}
    for thread in threads:
        cwd = str(thread.get('cwd') or '').strip()
        updated_at = str(thread.get('updated_at') or '')
        project = grouped.setdefault(cwd, {
            'cwd': cwd,
            'name': _project_name(cwd),
            'count': 0,
            'updated_at': updated_at,
        })
        project['count'] += 1
        if _integer(updated_at) > _integer(project['updated_at']):
            project['updated_at'] = updated_at
    return sorted(
        grouped.values(),
        key=lambda item: (
            _integer(item['updated_at']),
            str(item['name']).lower(),
        ),
        reverse=True,
    )


def _time_label(value: Any) -> str:
    raw = str(value or '').strip()
    if not raw:
        return '—'
    try:
        return datetime.fromtimestamp(int(float(raw))).strftime(
            '%Y-%m-%d %H:%M'
        )
    except (TypeError, ValueError, OverflowError, OSError):
        return raw


def _error_text(state: Any) -> str:
    error = str(state.assistant_error or '').strip()
    normalized = error.lower()
    if '2001600' in normalized:
        return _localized(
            state,
            'Codex 已连接，但当前没有可展示的项目会话。',
            'Codex is connected, but no project sessions are visible.',
        )
    if (
        'codex assistant is not configured' in normalized
        or 'codex executable not found' in normalized
    ):
        return _localized(
            state,
            (
                '当前设备未找到可用 Codex。请先在本机安装并登录 Codex，'
                '然后返回飞书重试；其他 LazyMind 功能不受影响。'
            ),
            (
                'Codex is unavailable on this device. Install and sign in to '
                'Codex locally, then retry in Feishu. Other LazyMind features '
                'are unaffected.'
            ),
        )
    return error or _localized(state, '请重试', 'Please retry')


def _projects(state: Any, chat_id: str) -> list[dict[str, Any]]:
    projects = _project_groups(state.assistant_threads)
    status = (
        '<font color="green">● 已连接</font>'
        if state.assistant_status == 'ready'
        else '<font color="grey">○ 正在检查连接</font>'
    )
    elements = [
        _heading_action(
            title=_localized(state, 'Codex 项目', 'Codex projects'),
            description=_localized(
                state,
                f'按工作目录整理 {len(state.assistant_threads)} 个原生会话。先选择项目，再选择会话。',
                f'{len(state.assistant_threads)} native sessions grouped by working directory.',
            ),
            button={
                'label': _localized(state, '刷新', 'Refresh'),
                'action': _action(
                    chat_id,
                    'assistant.refresh',
                    '刷新 Codex 项目',
                ),
            },
        ),
        {
            'tag': 'markdown',
            'content': (
                f'**Codex**　{status}\n<font color="grey">'
                + _localized(
                    state,
                    '项目按最近活动排序，项目内会话按更新时间排序。',
                    'Projects and sessions are ordered by recent activity.',
                )
                + '</font>'
            ),
        },
        _button_row([{
            'label': _localized(
                state,
                '＋ 新建 ChatGPT 对话',
                '＋ New ChatGPT chat',
            ),
            'style': 'primary',
            'action': _action(
                chat_id,
                'assistant.new',
                '新建 ChatGPT 对话',
                display_name='ChatGPT 对话',
            ),
        }]),
        {'tag': 'hr'},
    ]
    if state.assistant_status == 'loading':
        elements.append({
            'tag': 'markdown',
            'content': '<font color="grey">正在同步 Codex 会话…</font>',
        })
    elif state.assistant_status == 'error':
        elements.extend([
            {
                'tag': 'markdown',
                'content': f'<font color="red">同步失败：{_error_text(state)}</font>',
            },
            _button_row([{
                'label': _localized(state, '重试', 'Retry'),
                'action': _action(
                    chat_id,
                    'assistant.retry',
                    '重试同步 Codex 项目',
                ),
            }]),
        ])
    elif not projects:
        elements.append({
            'tag': 'markdown',
            'content': _localized(
                state,
                '<font color="grey">暂无可用 Codex 项目。</font>',
                '<font color="grey">No Codex projects are available.</font>',
            ),
        })
    for project in projects:
        elements.extend([
            {
                'tag': 'markdown',
                'content': (
                    f'📁 **{project["name"]}**　'
                    f'<font color="blue">{project["count"]} 个会话</font>\n'
                    f'<font color="grey">{project["cwd"]}</font>\n'
                    f'<font color="grey">最近活动：'
                    f'{_time_label(project["updated_at"])}</font>'
                ),
            },
            _button_row([{
                'label': _localized(state, '查看项目会话', 'View sessions'),
                'action': _action(
                    chat_id,
                    'assistant.project',
                    f'打开 Codex 项目 {project["name"]}',
                    project_cwd=project['cwd'],
                ),
            }]),
        ])
    return elements


def _sessions(state: Any, chat_id: str) -> list[dict[str, Any]]:
    sessions = sorted(
        (
            item for item in state.assistant_threads
            if str(item.get('cwd') or '') == state.assistant_project_cwd
        ),
        key=lambda item: _integer(item.get('updated_at')),
        reverse=True,
    )
    page_count = max(1, (len(sessions) + _ASSISTANT_SESSION_PAGE_SIZE - 1) // _ASSISTANT_SESSION_PAGE_SIZE)
    page = min(state.assistant_project_page, page_count - 1)
    visible = sessions[page * _ASSISTANT_SESSION_PAGE_SIZE:(page + 1) * _ASSISTANT_SESSION_PAGE_SIZE]
    project_name = _project_name(state.assistant_project_cwd)
    elements = [
        _heading_action(
            title=f'← {project_name}',
            description=_localized(
                state,
                f'{len(sessions)} 个 Codex 原生会话',
                f'{len(sessions)} native Codex sessions',
            ),
            button={
                'label': _localized(state, '返回项目', 'Projects'),
                'action': _action(
                    chat_id,
                    'assistant.projects',
                    '返回 Codex 项目',
                ),
            },
        ),
        _button_row([{
            'label': _localized(state, '＋ 新建项目会话', '＋ New session'),
            'style': 'primary',
            'action': _action(
                chat_id,
                'assistant.new',
                f'在 {project_name} 新建 Codex 会话',
                cwd=state.assistant_project_cwd,
                display_name=f'{project_name} 会话',
            ),
        }]),
        {'tag': 'hr'},
    ]
    if not visible:
        elements.append({
            'tag': 'markdown',
            'content': _localized(
                state,
                '<font color="grey">该项目暂无会话。</font>',
                '<font color="grey">No sessions in this project.</font>',
            ),
        })
    for thread in visible:
        available = bool(thread.get('available'))
        managed = bool(thread.get('managed'))
        thread_status = (
            _localized(state, '可继续', 'Ready')
            if available
            else _localized(state, '运行中', 'Running')
            if managed
            else _localized(state, '运行中·只读', 'Running · read-only')
        )
        elements.extend([
            {
                'tag': 'markdown',
                'content': (
                    f'**{thread.get("title") or "未命名 Codex 会话"}**　'
                    f'<font color="{"blue" if available else "orange"}">'
                    f'{thread_status}</font>\n'
                    f'<font color="grey">{thread.get("preview") or ""}</font>\n'
                    f'<font color="grey">{thread.get("source") or "Codex"} · '
                    f'{_time_label(thread.get("updated_at"))}</font>'
                ),
            },
            _button_row([{
                'label': _localized(state, '进入会话', 'Open session'),
                'action': _action(
                    chat_id,
                    'assistant.open',
                    '打开 Codex 会话',
                    thread_id=str(thread.get('id') or ''),
                ),
            }]),
        ])
    if sessions:
        elements.append(_pagination(
            state,
            chat_id,
            page,
            page_count,
            'assistant.sessions_page',
        ))
    return elements


def _answer_pages(text: str) -> list[str]:
    remaining = str(text or '').strip()
    pages: list[str] = []
    while len(remaining) > _ASSISTANT_ANSWER_PAGE_CHARS:
        cut = remaining.rfind('\n\n', 0, _ASSISTANT_ANSWER_PAGE_CHARS + 1)
        if cut < _ASSISTANT_ANSWER_PAGE_CHARS // 2:
            cut = remaining.rfind('\n', 0, _ASSISTANT_ANSWER_PAGE_CHARS + 1)
        if cut < _ASSISTANT_ANSWER_PAGE_CHARS // 2:
            cut = _ASSISTANT_ANSWER_PAGE_CHARS
        pages.append(remaining[:cut].strip())
        remaining = remaining[cut:].strip()
    if remaining:
        pages.append(remaining)
    return pages


def _detail(state: Any, chat_id: str) -> list[dict[str, Any]]:
    running = state.run_status == 'running'
    readonly = state.assistant_thread_readonly
    status = (
        _localized(state, '运行中', 'Running')
        if running
        else _localized(state, '运行中·只读', 'Running · read-only')
        if readonly
        else _localized(state, '已绑定，可从飞书继续', 'Bound · ready in Feishu')
        if state.assistant_conversation_id
        else _localized(state, '可继续', 'Ready')
    )
    users = [
        str(item.get('text') or '')
        for item in state.assistant_turns
        if str(item.get('role') or '').lower() in {'user', 'you'}
    ]
    answers = [
        str(item.get('text') or '')
        for item in state.assistant_turns
        if str(item.get('role') or '').lower() not in {'user', 'you'}
    ]
    pages = _answer_pages('\n\n'.join(answers))
    page = min(state.assistant_answer_page, max(0, len(pages) - 1))
    elements = [
        _heading_action(
            title=f'← {state.assistant_thread_title or "Codex 会话"}',
            description=(
                f'{_project_name(state.assistant_thread_cwd)} · '
                f'{state.assistant_thread_source or "Codex"} · '
                f'{_time_label(state.assistant_thread_updated_at)}'
            ),
            button={
                'label': _localized(state, '返回会话', 'Sessions'),
                'action': _action(
                    chat_id,
                    'assistant.back',
                    '返回 Codex 会话列表',
                ),
            },
        ),
        {
            'tag': 'markdown',
            'content': (
                f'<font color="{"orange" if readonly else "green"}">'
                f'● {status}</font>\n'
                f'<font color="grey">{state.assistant_thread_cwd or "—"}</font>'
            ),
        },
        {'tag': 'hr'},
    ]
    if state.assistant_status == 'loading':
        elements.append({
            'tag': 'markdown',
            'content': _localized(
                state,
                '<font color="blue">⏳ 正在读取 Codex 原生会话…</font>',
                '<font color="blue">⏳ Loading native Codex session…</font>',
            ),
        })
    elif state.assistant_status == 'error':
        elements.extend([
            {
                'tag': 'markdown',
                'content': f'<font color="red">打开失败：{_error_text(state)}</font>',
            },
            _button_row([{
                'label': _localized(state, '返回项目会话', 'Project sessions'),
                'action': _action(
                    chat_id,
                    'assistant.back',
                    '返回项目会话',
                ),
            }]),
        ])
    if users:
        elements.append({
            'tag': 'markdown',
            'content': f'**{_localized(state, "你", "You")}**\n\n' + '\n\n'.join(users),
        })
    if pages:
        page_label = (
            f'　<font color="grey">{page + 1} / {len(pages)}</font>'
            if len(pages) > 1 else ''
        )
        elements.append({
            'tag': 'markdown',
            'content': f'**Codex**{page_label}\n\n{pages[page]}',
        })
        if len(pages) > 1:
            elements.append(_button_row([
                {
                    'label': _localized(state, '上一段', 'Previous part'),
                    'disabled': page == 0,
                    'action': _action(
                        chat_id,
                        'assistant.answer_page',
                        '查看 Codex 回答上一段',
                        direction='previous',
                    ),
                },
                {
                    'label': _localized(state, '下一段', 'Next part'),
                    'disabled': page >= len(pages) - 1,
                    'action': _action(
                        chat_id,
                        'assistant.answer_page',
                        '查看 Codex 回答下一段',
                        direction='next',
                    ),
                },
            ]))
    if not users and not pages and not running and state.assistant_status == 'ready':
        elements.append({
            'tag': 'markdown',
            'content': _localized(
                state,
                '<font color="grey">该会话暂无消息，可直接从底部输入框开始。</font>',
                '<font color="grey">No messages yet. Start from the input below.</font>',
            ),
        })
    if state.assistant_turns_total:
        elements.append(_pagination(
            state,
            chat_id,
            state.assistant_turns_offset,
            state.assistant_turns_total,
            'assistant.turns_page',
        ))
    if running:
        progress = (
            state.chat_status
            or state.chat_thinking
            or state.chat_text
            or '正在等待流式事件…'
        )
        elements.extend([
            {
                'tag': 'markdown',
                'content': (
                    '<font color="blue">Codex 正在处理</font>\n'
                    f'<font color="grey">{progress}</font>'
                ),
            },
            {
                'tag': 'markdown',
                'content': _localized(
                    state,
                    (
                        '<font color="grey">LazyMind 正在控制此 Codex Thread；'
                        'Codex Desktop 会暂时显示“已在另一个应用中打开”。'
                        '完成或取消后将释放订阅。</font>'
                    ),
                    (
                        '<font color="grey">LazyMind currently controls this '
                        'Codex Thread. The subscription is released after '
                        'completion or cancellation.</font>'
                    ),
                ),
            },
            _button_row([{
                'label': _localized(state, '取消', 'Cancel'),
                'style': 'danger',
                'action': _action(
                    chat_id,
                    'operation.cancel',
                    '取消 Codex 任务',
                ),
            }]),
        ])
    elif state.user_text and state.chat_text:
        elements.extend([
            {'tag': 'hr'},
            {
                'tag': 'markdown',
                'content': (
                    f'**{_localized(state, "飞书本轮", "Latest from Feishu")}**'
                    f'\n\n{state.user_text}\n\n**Codex**\n\n{state.chat_text}'
                ),
            },
        ])
    request = state.assistant_pending_request
    if request:
        elements.extend([
            {
                'tag': 'markdown',
                'content': (
                    f'**需要你的操作**　'
                    f'{request.get("summary") or request.get("kind")}'
                ),
            },
            _button_row([
                {
                    'label': _localized(state, '允许', 'Allow'),
                    'style': 'primary',
                    'action': _action(
                        chat_id,
                        'assistant.respond',
                        '允许 Codex 请求',
                        request_id=request['request_id'],
                        decision='approve',
                    ),
                },
                {
                    'label': _localized(state, '拒绝', 'Deny'),
                    'style': 'danger',
                    'action': _action(
                        chat_id,
                        'assistant.respond',
                        '拒绝 Codex 请求',
                        request_id=request['request_id'],
                        decision='deny',
                    ),
                },
            ]),
        ])
    footer = _localized(
        state,
        (
            '<font color="grey">从飞书底部输入框继续；'
            '新消息、执行进度和回答都会更新在本卡片。</font>'
            if not readonly
            else '<font color="grey">此会话正在其他端运行，飞书暂时只读。</font>'
        ),
        (
            '<font color="grey">Continue from the Feishu input; new messages, '
            'progress, and answers update here.</font>'
            if not readonly
            else '<font color="grey">This session is running elsewhere and is read-only in Feishu.</font>'
        ),
    )
    elements.extend([
        {'tag': 'hr'},
        {'tag': 'markdown', 'content': footer},
        _button_row([{
            'label': _localized(state, '返回项目会话', 'Project sessions'),
            'action': _action(
                chat_id,
                'assistant.back',
                '返回项目会话',
            ),
        }]),
    ])
    return elements


def _command_action(
    *,
    chat_id: str,
    text: str,
    command: dict[str, Any],
    workspace_action: dict[str, Any],
) -> dict[str, Any]:
    return {
        'lazymind_action': 'command',
        'text': text,
        'intended_chat_id': chat_id,
        'command_action': command,
        'workspace_action': workspace_action,
    }


def _local_action(
    *,
    chat_id: str,
    text: str,
    workspace_action: dict[str, Any],
) -> dict[str, Any]:
    return {
        'lazymind_action': 'local',
        'text': text,
        'intended_chat_id': chat_id,
        'workspace_action': workspace_action,
    }


def _history_refresh_action(chat_id: str) -> dict[str, Any]:
    return _command_action(
        chat_id=chat_id,
        text='同步历史会话',
        command=_history_command('同步历史会话'),
        workspace_action={'kind': 'history.open'},
    )


def _context_command_action(
    chat_id: str,
    *,
    kind: str,
    scope: str,
    category: str,
) -> dict[str, Any]:
    return _command_action(
        chat_id=chat_id,
        text=f'查看{_CONTEXT_LABELS.get(category, "知识库")}',
        command={
            'schema_version': '1',
            'command': 'capability.list',
            'parameters': {
                'capabilities': (
                    list(_CONTEXT_CATEGORIES)
                    if kind in {'context.open', 'context.category'}
                    else [category]
                ),
                'evidence': [f'查看{_CONTEXT_LABELS.get(category, "知识库")}'],
            },
        },
        workspace_action={
            'kind': kind,
            'scope': scope,
            'category': category,
        },
    )


def _context_toggle_action(
    chat_id: str,
    state: FeishuWorkspaceState,
    item: dict[str, Any],
) -> dict[str, Any]:
    return _resource_toggle_action(
        chat_id=chat_id,
        kind='context.toggle',
        scope=state.context_scope,
        category=state.context_category,
        item=item,
    )


def _context_page_action(
    chat_id: str,
    page: int,
) -> dict[str, Any]:
    return _local_action(
        chat_id=chat_id,
        text='切换资源页',
        workspace_action={
            'kind': 'context.page',
            'page': max(0, page),
        },
    )


def _resource_toggle_action(
    *,
    chat_id: str,
    kind: str,
    scope: str,
    category: str,
    item: dict[str, Any],
) -> dict[str, Any]:
    return _local_action(
        chat_id=chat_id,
        text=f'切换{_CONTEXT_LABELS.get(category, "资源")}',
        workspace_action={
            'kind': kind,
            'scope': scope,
            'category': category,
            'resource': {
                'type': category,
                'id': str(item.get('id') or ''),
                'name': str(item.get('name') or '')[:100],
            },
        },
    )


def _new_session_action(
    chat_id: str,
    *,
    kind: str,
    mode: str,
    create: bool = False,
) -> dict[str, Any]:
    if not create:
        return _local_action(
            chat_id=chat_id,
            text='设置新会话起点',
            workspace_action={'kind': kind, 'mode': mode},
        )
    return _command_action(
        chat_id=chat_id,
        text='创建会话',
        command={
            'schema_version': '1',
            'command': 'conversation.new',
            'parameters': {
                'message': '',
                'resource_changes': [],
                'evidence': ['创建会话'],
            },
        },
        workspace_action={'kind': kind, 'mode': mode},
    )


def _maintenance_action(
    chat_id: str,
    *,
    kind: str,
    create: bool = False,
) -> dict[str, Any]:
    if not create:
        return _local_action(
            chat_id=chat_id,
            text='确认会话维护操作',
            workspace_action={'kind': kind},
        )
    return _command_action(
        chat_id=chat_id,
        text='确认会话维护操作',
        command={
            'schema_version': '1',
            'command': 'conversation.new',
            'parameters': {
                'message': '',
                'resource_changes': [],
                'evidence': ['清空当前会话上下文'],
            },
        },
        workspace_action={'kind': kind},
    )


def _item_key(
    state: FeishuWorkspaceState,
    item: dict[str, Any],
) -> tuple[str, str]:
    return state.context_category, str(item.get('id') or '')


def _prompt_action(
    chat_id: str,
    item: dict[str, Any],
) -> dict[str, Any]:
    content = (
        str(item.get('content') or '').strip()
        or str(item.get('name') or '').strip()
        or '使用 Prompt'
    )
    return _command_action(
        chat_id=chat_id,
        text=content,
        command={
            'schema_version': '1',
            'command': 'chat',
            'parameters': {
                'message': content,
                'resource_changes': [],
            },
        },
        workspace_action={
            'kind': 'prompt.run',
        },
    )


def _preference_action(
    chat_id: str,
    name: str,
    value: Any,
) -> dict[str, Any]:
    return _local_action(
        chat_id=chat_id,
        text='更新呈现设置',
        workspace_action={
            'kind': 'preference',
            'name': name,
            'value': value,
        },
    )


def _setting_action(
    chat_id: str,
    change: dict[str, Any],
    text: str,
    *,
    view: str = 'settings',
) -> dict[str, Any]:
    return _command_action(
        chat_id=chat_id,
        text=text,
        command={
            'schema_version': '1',
            'command': 'conversation.settings.update',
            'parameters': {
                'change': change,
                'evidence': [text],
            },
        },
        workspace_action={'kind': 'setting.update', 'view': view},
    )


def _capability_groups(
    presentations: list[dict[str, Any]],
) -> list[dict[str, Any]]:
    presentation = next(
        (
            item
            for item in presentations
            if item.get('kind') == 'capability'
        ),
        {},
    )
    groups = presentation.get('groups')
    return [
        dict(group)
        for group in (groups if isinstance(groups, list) else [])
        if isinstance(group, dict)
    ]
