from __future__ import annotations

import datetime as dt
import json
import re
import uuid
from dataclasses import dataclass, field
from typing import Any
from urllib.parse import quote, urlsplit

import httpx


class LazyMindError(RuntimeError):
    pass


class LazyMindHTTPError(LazyMindError):
    def __init__(self, status_code: int, message: str):
        super().__init__(status_code, message)
        self.status_code = status_code
        self.message = message

    def __str__(self) -> str:
        return self.message


@dataclass
class ChatOptions:
    search_config: dict[str, Any] | None = None
    mentions: list[dict[str, str]] = field(default_factory=list)
    use_memory: bool | None = None
    disabled_tools: list[str] = field(default_factory=list)
    filters: dict[str, Any] | None = None


_CHANNEL_HIDDEN_TAGS = re.compile(
    r'(?s)<(?:think|tool_call|tool_result|tp|trp)\b[^>]*>.*?</(?:think|tool_call|tool_result|tp|trp)>'
)
_BLOCKED_TOOL_NAMES = {
    'ask_user',
    'schedule',
    'skill',
    'plugin',
    'subagent',
    'task',
    'task_center',
}
_MAX_CHANNEL_IMAGE_BYTES = 20 * 1024 * 1024


class LazyMindClient:
    def __init__(self, base_url: str, chat_timeout_seconds: int):
        self._base_url = base_url.rstrip('/')
        self._timeout = httpx.Timeout(
            connect=20.0,
            read=float(chat_timeout_seconds),
            write=30.0,
            pool=20.0,
        )

    @staticmethod
    def _headers(
        owner_user_id: str,
        request_id: str,
        *,
        accept: str = 'application/json',
    ) -> dict[str, str]:
        return {
            'Accept': accept,
            'Content-Type': 'application/json',
            'X-User-Id': owner_user_id,
            'X-User-Name': owner_user_id,
            'X-Request-Id': request_id,
        }

    def chat(
        self,
        *,
        owner_user_id: str,
        text: str,
        conversation_id: str,
        request_id: str,
        options: ChatOptions | None = None,
    ) -> tuple[str, str]:
        options = options or ChatOptions()
        conversation: dict[str, Any] = {}
        if options.search_config is not None:
            conversation['search_config'] = options.search_config
        payload: dict[str, Any] = {
            'conversation': conversation,
            'stream': True,
            'input': [{'text': text, 'input_type': 'text'}],
            'mode': 'auto',
            'basic_chat_only': True,
            'enable_plugin': False,
            'enable_subagent': False,
            'disabled_tools': self._unique(['ask_user', *options.disabled_tools]),
            'create_time': dt.datetime.now(dt.timezone.utc).isoformat(),
        }
        if options.mentions:
            payload['mentions'] = options.mentions
        if options.use_memory is not None:
            payload['use_memory'] = options.use_memory
        if options.filters is not None:
            payload['filters'] = options.filters
        if conversation_id:
            payload['conversation_id'] = conversation_id

        resolved_conversation_id = conversation_id
        history_id = ''
        deltas: list[str] = []
        last_message = ''
        saw_done = False
        terminal_finish_reason = ''
        endpoint = f'{self._base_url}/conversations:chat'
        try:
            with httpx.stream(
                'POST',
                endpoint,
                json=payload,
                headers=self._headers(
                    owner_user_id,
                    request_id,
                    accept='text/event-stream',
                ),
                timeout=self._timeout,
            ) as response:
                self._raise_for_status(response, 'chat')
                for line in response.iter_lines():
                    normalized = line.strip()
                    if not normalized.startswith('data:'):
                        continue
                    data = normalized[5:].strip()
                    if not data:
                        continue
                    if data == '[DONE]':
                        saw_done = True
                        continue
                    try:
                        frame = json.loads(data)
                    except json.JSONDecodeError as exc:
                        raise LazyMindError('LazyMind chat returned invalid SSE JSON') from exc
                    result = frame.get('result', frame)
                    if not isinstance(result, dict):
                        continue
                    current_id = result.get('conversation_id')
                    if current_id:
                        resolved_conversation_id = str(current_id)
                    current_history_id = result.get('history_id')
                    if current_history_id:
                        history_id = str(current_history_id)
                    if result.get('task_created'):
                        raise LazyMindError(
                            'LazyMind created a task in basic channel chat'
                        )
                    if result.get('ask_pending'):
                        raise LazyMindError(
                            'LazyMind requested structured input in basic channel chat'
                        )
                    finish_reason = str(result.get('finish_reason') or '')
                    if finish_reason == 'FINISH_REASON_UNKNOWN':
                        raise LazyMindError('LazyMind chat generation failed')
                    if finish_reason and finish_reason != 'FINISH_REASON_UNSPECIFIED':
                        terminal_finish_reason = finish_reason
                    delta = result.get('delta')
                    if isinstance(delta, str) and delta:
                        deltas.append(delta)
                    message = result.get('message')
                    if isinstance(message, str) and message:
                        last_message = message
        except LazyMindError:
            raise
        except httpx.HTTPError as exc:
            raise LazyMindError(
                f'Cannot reach LazyMind Core: {exc.__class__.__name__}'
            ) from exc

        answer = ''.join(deltas).strip() or last_message.strip()
        if not resolved_conversation_id:
            raise LazyMindError('LazyMind did not return a conversation id')
        if not saw_done and not terminal_finish_reason:
            raise LazyMindError('LazyMind chat stream ended before completion')
        try:
            latest_history_id, latest_answer = self._latest_answer(
                owner_user_id=owner_user_id,
                conversation_id=resolved_conversation_id,
                request_id=request_id,
            )
            if latest_history_id == history_id and latest_answer:
                answer = latest_answer
        except LazyMindError:
            if not answer:
                raise
        answer = self._channel_answer(answer)
        if not answer:
            raise LazyMindError('LazyMind returned no answer')
        return resolved_conversation_id, answer

    def classify_intent(
        self,
        *,
        owner_user_id: str,
        request_id: str,
        provider: str,
        message: str,
        state: dict[str, Any],
        command_registry: dict[str, Any],
    ) -> dict[str, Any]:
        payload = self._request_json(
            'POST',
            f'{self._base_url}/channel-intents:classify',
            owner_user_id=owner_user_id,
            request_id=request_id,
            json_body={
                'provider': provider,
                'message': message,
                'state': state,
                'command_registry': command_registry,
            },
            error_label='channel intent classifier',
        )
        data = payload.get('data')
        if not isinstance(data, dict):
            raise LazyMindError('LazyMind channel intent response is invalid')
        return data

    def download_static_image(
        self,
        *,
        source: str,
    ) -> bytes:
        """Download an already signed LazyMind image without granting new access."""
        static_path = self._static_file_path(source)
        status_code, content = self._download_static_image(static_path)
        if status_code == 403:
            raise LazyMindHTTPError(403, 'LazyMind static image access was denied')
        return content

    def list_conversations(
        self,
        *,
        owner_user_id: str,
        request_id: str,
        page_size: int = 100,
        page_token: str = '',
    ) -> dict[str, Any]:
        params: dict[str, Any] = {'page_size': page_size}
        if page_token:
            params['page_token'] = page_token
        return self._request_json(
            'GET',
            f'{self._base_url}/conversations',
            owner_user_id=owner_user_id,
            request_id=request_id,
            params=params,
            error_label='conversation list',
        )

    def get_conversation_detail(
        self,
        *,
        owner_user_id: str,
        conversation_id: str,
        request_id: str,
    ) -> dict[str, Any]:
        payload = self._request_json(
            'GET',
            f'{self._base_url}/conversations/{quote(conversation_id, safe="")}:detail',
            owner_user_id=owner_user_id,
            request_id=request_id,
            error_label='conversation detail',
        )
        conversation = payload.get('conversation')
        if not isinstance(conversation, dict):
            raise LazyMindError('LazyMind conversation detail is invalid')
        return conversation

    def get_conversation_history(
        self,
        *,
        owner_user_id: str,
        conversation_id: str,
        request_id: str,
        page_size: int = 3,
        page_token: str = '',
    ) -> dict[str, Any]:
        params: dict[str, Any] = {'page_size': page_size}
        if page_token:
            params['page_token'] = page_token
        return self._request_json(
            'GET',
            f'{self._base_url}/conversations/{quote(conversation_id, safe="")}:history',
            owner_user_id=owner_user_id,
            request_id=request_id,
            params=params,
            error_label='conversation history',
        )

    def update_conversation_search_config(
        self,
        *,
        owner_user_id: str,
        conversation_id: str,
        request_id: str,
        dataset_ids: list[str],
    ) -> dict[str, Any]:
        payload = self._request_json(
            'PATCH',
            f'{self._base_url}/conversations/{quote(conversation_id, safe="")}:search-config',
            owner_user_id=owner_user_id,
            request_id=request_id,
            json_body={'dataset_ids': dataset_ids},
            error_label='conversation knowledge-base configuration',
        )
        data = payload.get('data')
        if not isinstance(data, dict):
            raise LazyMindError('LazyMind conversation configuration response is invalid')
        return data

    def get_capability_catalog(
        self,
        *,
        owner_user_id: str,
        request_id: str,
        kinds: set[str],
    ) -> dict[str, Any]:
        datasets_payload = self._request_json(
            'GET',
            f'{self._base_url}/datasets',
            owner_user_id=owner_user_id,
            request_id=f'{request_id}_kb',
            params={'page_size': 100},
            error_label='knowledge bases',
        ) if 'knowledge_base' in kinds else {}
        skills_payload = self._request_json(
            'GET',
            f'{self._base_url}/skills',
            owner_user_id=owner_user_id,
            request_id=f'{request_id}_skills',
            params={'page_size': 100},
            error_label='skills',
        ) if 'skill' in kinds else {}
        tools_payload = self._request_json(
            'GET',
            f'{self._base_url}/tools',
            owner_user_id=owner_user_id,
            request_id=f'{request_id}_tools',
            error_label='tools',
        ) if 'tool' in kinds else {}
        personalization_payload = self._request_json(
            'GET',
            f'{self._base_url}/personalization-setting',
            owner_user_id=owner_user_id,
            request_id=f'{request_id}_personalization',
            error_label='personalization setting',
        ) if 'personalization' in kinds else {}

        datasets = datasets_payload.get('datasets')
        skill_data = skills_payload.get('data')
        skills = skill_data.get('items') if isinstance(skill_data, dict) else None
        tool_data = tools_payload.get('data')
        tools = tool_data.get('tool_groups') if isinstance(tool_data, dict) else None
        personalization_data = personalization_payload.get('data')
        personalization_enabled = (
            bool(personalization_data.get('enabled', True))
            if isinstance(personalization_data, dict)
            else True
        )
        return {
            'knowledge_base': [
                {
                    'id': str(item.get('dataset_id') or ''),
                    'name': str(item.get('display_name') or '').strip(),
                    'enabled': bool(item.get('default_dataset', False)),
                    'default': bool(item.get('default_dataset', False)),
                }
                for item in (datasets if isinstance(datasets, list) else [])
                if isinstance(item, dict)
                and item.get('dataset_id')
                and str(item.get('display_name') or '').strip()
            ],
            'skill': [
                {
                    'id': str(item.get('id') or item.get('skill_id') or ''),
                    'name': str(item.get('name') or item.get('skill_name') or '').strip(),
                    'enabled': bool(item.get('is_enabled', True)),
                    'category': str(item.get('category') or ''),
                }
                for item in (skills if isinstance(skills, list) else [])
                if isinstance(item, dict)
                and item.get('head_revision_id')
                and (item.get('id') or item.get('skill_id'))
            ],
            'tool': [
                {
                    'id': str(item.get('name') or ''),
                    'name': str(item.get('label') or item.get('name') or '').strip(),
                    'enabled': not bool(item.get('disabled', False)),
                    'can_disable': bool(item.get('can_disable', False)),
                }
                for item in (tools if isinstance(tools, list) else [])
                if isinstance(item, dict)
                and bool(item.get('active', False))
                and str(item.get('name') or '') not in _BLOCKED_TOOL_NAMES
            ],
            'personalization': [
                {
                    'id': 'personalization',
                    'name': '个人习惯',
                    'enabled': personalization_enabled,
                }
            ],
        }

    def set_default_dataset(
        self,
        *,
        owner_user_id: str,
        request_id: str,
        dataset_id: str,
        name: str,
        enabled: bool,
    ) -> None:
        action = 'setDefault' if enabled else 'unsetDefault'
        self._request_json(
            'POST',
            f'{self._base_url}/datasets/{quote(dataset_id, safe="")}:{action}',
            owner_user_id=owner_user_id,
            request_id=request_id,
            json_body={'name': name},
            error_label='default knowledge base',
        )

    def set_tool_enabled(
        self,
        *,
        owner_user_id: str,
        request_id: str,
        tool_name: str,
        enabled: bool,
    ) -> None:
        action = 'enable' if enabled else 'disable'
        self._request_json(
            'POST',
            f'{self._base_url}/tools/{quote(tool_name, safe="")}:{action}',
            owner_user_id=owner_user_id,
            request_id=request_id,
            error_label='tool setting',
        )

    def set_personalization_enabled(
        self,
        *,
        owner_user_id: str,
        request_id: str,
        enabled: bool,
    ) -> None:
        self._request_json(
            'PUT',
            f'{self._base_url}/personalization-setting',
            owner_user_id=owner_user_id,
            request_id=request_id,
            json_body={'enabled': enabled},
            error_label='personalization setting',
        )

    def _request_json(
        self,
        method: str,
        endpoint: str,
        *,
        owner_user_id: str,
        request_id: str,
        error_label: str,
        params: dict[str, Any] | None = None,
        json_body: dict[str, Any] | None = None,
    ) -> dict[str, Any]:
        try:
            response = httpx.request(
                method,
                endpoint,
                params=params,
                json=json_body,
                headers=self._headers(owner_user_id, request_id),
                timeout=self._timeout,
            )
        except httpx.HTTPError as exc:
            raise LazyMindError(f'Cannot load LazyMind {error_label}') from exc
        self._raise_for_status(response, error_label)
        try:
            payload = response.json()
        except ValueError as exc:
            raise LazyMindError(
                f'LazyMind {error_label} returned invalid JSON'
            ) from exc
        if not isinstance(payload, dict):
            raise LazyMindError(f'LazyMind {error_label} returned an invalid payload')
        return payload

    @staticmethod
    def _raise_for_status(response: httpx.Response, error_label: str) -> None:
        if 200 <= response.status_code < 300:
            return
        body = response.read().decode('utf-8', errors='replace')
        raise LazyMindHTTPError(
            response.status_code,
            f'LazyMind {error_label} returned HTTP {response.status_code}: {body[:300]}',
        )

    def _latest_answer(
        self,
        *,
        owner_user_id: str,
        conversation_id: str,
        request_id: str,
    ) -> tuple[str, str]:
        payload = self.get_conversation_history(
            owner_user_id=owner_user_id,
            conversation_id=conversation_id,
            request_id=request_id,
            page_size=1,
        )
        history = payload.get('history')
        if not isinstance(history, list) or not history:
            return '', ''
        item = history[0]
        if not isinstance(item, dict):
            return '', ''
        return str(item.get('id') or ''), str(item.get('result') or '').strip()

    def _download_static_image(self, static_path: str) -> tuple[int, bytes]:
        try:
            with httpx.stream(
                'GET',
                f'{self._base_url}{static_path}',
                timeout=60.0,
            ) as response:
                if response.status_code == 403:
                    return response.status_code, b''
                self._raise_for_status(response, 'static image')
                content_type = str(
                    response.headers.get('content-type') or ''
                ).lower()
                if not content_type.startswith('image/'):
                    raise LazyMindError('LazyMind static file is not an image')
                raw_length = str(
                    response.headers.get('content-length') or ''
                ).strip()
                if raw_length.isdigit() and int(raw_length) > _MAX_CHANNEL_IMAGE_BYTES:
                    raise LazyMindError('LazyMind image is too large for the channel')
                content = bytearray()
                for chunk in response.iter_bytes():
                    content.extend(chunk)
                    if len(content) > _MAX_CHANNEL_IMAGE_BYTES:
                        raise LazyMindError('LazyMind image is too large for the channel')
                if not content:
                    raise LazyMindError('LazyMind image is empty')
                return response.status_code, bytes(content)
        except httpx.HTTPError as exc:
            raise LazyMindError('Cannot download LazyMind static image') from exc

    @staticmethod
    def _static_file_path(source: str) -> str:
        parsed = urlsplit(str(source or '').strip())
        if not parsed.path.startswith('/static-files/'):
            raise LazyMindError('Only LazyMind static images can be sent to a channel')
        suffix = f'?{parsed.query}' if parsed.query else ''
        return f'{parsed.path}{suffix}'

    @staticmethod
    def mention(resource_type: str, item: dict[str, Any]) -> dict[str, str]:
        return {
            'mention_id': f'channel_{uuid.uuid4().hex}',
            'type': resource_type,
            'resource_id': str(item.get('id') or ''),
            'display_name': str(item.get('name') or ''),
        }

    @staticmethod
    def _unique(values: list[str]) -> list[str]:
        return list(dict.fromkeys(value for value in values if value))

    @staticmethod
    def _channel_answer(answer: str) -> str:
        cleaned = _CHANNEL_HIDDEN_TAGS.sub('', answer)
        cleaned = re.sub(r'\n{3,}', '\n\n', cleaned)
        return cleaned.strip()
