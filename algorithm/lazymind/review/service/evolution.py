from __future__ import annotations

import threading
from typing import Any, Callable, Dict, List, Optional, Sequence

import httpx
import lazyllm
from lazyllm import LOG

from lazymind.config import config as _cfg
from lazymind.review.vocab import (
    LAZYLLM_CONTEXT_CREATE_USER_ATTR,
    VocabEvolutionRequest,
    get_ppl_vocab_evolution,
    json_dump_list,
    norm_text,
    summarize_action_for_log,
)
from lazymind.review.service.db import (
    fetch_chat_histories_for_user_id,
    fetch_vocab_groups_for_user_id,
    list_chat_users,
)

_WORD_GROUP_APPLY_PATH = '/api/core/inner/word_group:apply'
_WORD_GROUP_APPLY_INTERNAL_PATH = '/inner/word_group:apply'
_WORD_GROUP_APPLY_URL_ENV = 'LAZYMIND_WORD_GROUP_APPLY_URL'
_CORE_SERVICE_URL_ENV = 'LAZYMIND_CORE_SERVICE_URL'
_BACKEND_APPLY_TIMEOUT = 10.0


def _serialize_backend_action(action: Dict[str, Any]) -> Dict[str, Any]:
    payload = dict(action)
    payload['group_ids'] = json_dump_list(payload.get('group_ids') or [])
    payload['message_ids'] = json_dump_list(payload.get('message_ids') or [])
    return payload


def _wrap_backend_action_payload(actions: Sequence[Dict[str, Any]]) -> Dict[str, Any]:
    return {'action_list': list(actions)}


def resolve_word_group_apply_url(apply_url: Optional[str] = None) -> str:
    resolved_url = (norm_text(apply_url) or norm_text(_cfg['word_group_apply_url'])).rstrip('/')
    if resolved_url:
        return resolved_url

    core_service_url = norm_text(_cfg['core_service_url']).rstrip('/')
    if core_service_url:
        if (
            core_service_url.endswith(_WORD_GROUP_APPLY_PATH)
            or core_service_url.endswith(_WORD_GROUP_APPLY_INTERNAL_PATH)
        ):
            return core_service_url
        return core_service_url + _WORD_GROUP_APPLY_INTERNAL_PATH

    raise RuntimeError(
        'word group apply url is not configured; '
        f'set {_WORD_GROUP_APPLY_URL_ENV} or {_CORE_SERVICE_URL_ENV} '
        '(for example: http://core:8000 or http://kong:8000/api/core)'
    )


def apply_vocab_evolution_actions(
    actions: Sequence[Dict[str, Any]],
    *,
    apply_url: Optional[str] = None,
    post_fn: Optional[Callable[..., Any]] = None,
) -> Dict[str, Any]:
    payload = _wrap_backend_action_payload(actions)
    target_url = resolve_word_group_apply_url(apply_url)
    sender = post_fn or httpx.post
    try:
        response = sender(target_url, json=payload, timeout=_BACKEND_APPLY_TIMEOUT)
        raise_for_status = getattr(response, 'raise_for_status', None)
        if callable(raise_for_status):
            raise_for_status()
    except Exception as exc:
        LOG.error(f'[VocabEvolution] failed to apply {len(actions)} actions to {target_url}: {exc}')
        raise

    LOG.info(f'[VocabEvolution] applied {len(actions)} actions to {target_url}.')
    return payload


class VocabEvolutionService:
    def __init__(
        self,
        *,
        fetch_users_fn: Callable[..., List[str]] = list_chat_users,
        fetch_histories_fn: Callable[..., List[Dict[str, Any]]] = fetch_chat_histories_for_user_id,
        fetch_vocab_groups_fn: Callable[..., Dict[str, Dict[str, Any]]] = fetch_vocab_groups_for_user_id,
        extraction_llm: Optional[Any] = None,
        conflict_llm: Optional[Any] = None,
    ) -> None:
        self._fetch_users = fetch_users_fn
        self._pipeline = get_ppl_vocab_evolution(
            extraction_llm=extraction_llm,
            conflict_llm=conflict_llm,
            fetch_histories_fn=fetch_histories_fn,
            fetch_vocab_groups_fn=fetch_vocab_groups_fn,
        )

    def _resolve_users(self, request: VocabEvolutionRequest) -> List[str]:
        if request.user_id:
            return [request.user_id]
        start_time, end_time = request.resolve_time_range()
        return self._fetch_users(
            start_time=start_time,
            end_time=end_time,
            db_dsn=request.core_db_dsn,
            db_url=request.core_db_url,
        )

    def run(
        self,
        request: VocabEvolutionRequest | Dict[str, Any] | None = None,
    ) -> List[Dict[str, Any]]:
        req = VocabEvolutionRequest.from_value(request)
        actions: List[Dict[str, Any]] = []
        user_ids = self._resolve_users(req)
        target_label = req.user_id or '<all-users>'
        LOG.info(
            f'[VocabEvolution] start requested_user_id={target_label!r} '
            f'resolved_user_count={len(user_ids)}'
        )

        for user_id in user_ids:
            LOG.info(f'[VocabEvolution] processing user_id={user_id!r}')
            try:
                lazyllm.globals._init_sid(sid=user_id)
                lazyllm.locals._init_sid(sid=user_id)
                setattr(lazyllm.globals, LAZYLLM_CONTEXT_CREATE_USER_ATTR, user_id)
                result = self._pipeline({'request': req, 'user_id': user_id})
            except Exception as exc:
                LOG.error(f'[VocabEvolution] processing failed user_id={user_id!r} error={exc}')
                continue
            user_actions = result.get('actions', [])
            actions.extend(user_actions)
            LOG.info(
                f'[VocabEvolution] processed user_id={user_id!r} '
                f'action_count={len(user_actions)} skipped_count={len(result.get("skipped_reasons", []))}'
            )

        serialized_actions = [_serialize_backend_action(item) for item in actions]
        LOG.info(
            f'[VocabEvolution] finished requested_user_id={target_label!r} '
            f'action_count={len(serialized_actions)} '
            f'actions={[summarize_action_for_log(item) for item in actions]}'
        )
        return serialized_actions


_service_lock = threading.Lock()
_service: Optional[VocabEvolutionService] = None


def get_vocab_evolution_service(**kwargs: Any) -> VocabEvolutionService:
    global _service
    if _service is None:
        with _service_lock:
            if _service is None:
                _service = VocabEvolutionService(**kwargs)
    return _service


def run_vocab_evolution(
    request: VocabEvolutionRequest | Dict[str, Any] | None = None,
    *,
    service: Optional[VocabEvolutionService] = None,
    apply_url: Optional[str] = None,
    post_fn: Optional[Callable[..., Any]] = None,
) -> List[Dict[str, Any]]:
    svc = service or get_vocab_evolution_service()
    actions = svc.run(request)
    apply_vocab_evolution_actions(actions, apply_url=apply_url, post_fn=post_fn)
    return actions
