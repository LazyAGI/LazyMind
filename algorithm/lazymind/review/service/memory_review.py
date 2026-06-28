from __future__ import annotations

from typing import Any, Dict, List, Literal, Optional
from uuid import uuid4

import lazyllm
from lazyllm import AutoModel, LOG
from lazyllm.tools.fs.client import FS
from pydantic import BaseModel, ConfigDict

from lazymind.chat.engine.tools import memory_editor
from lazymind.chat.service.component.history import normalize_history_for_agent
from lazymind.config import config as _cfg
from lazymind.model_config import inject_model_config
from lazymind.review.memory_review.prompts import build_memory_review_prompt


class MemoryReviewResult(BaseModel):
    model_config = ConfigDict(extra='forbid')

    status: Literal['success', 'failed']


def _truncate_log_text(value: Any, limit: int = 4000) -> str:
    text = str(value)
    if len(text) <= limit:
        return text
    return f'{text[:limit]}...<truncated {len(text) - limit} chars>'


def review_memory(
    *,
    user_id: str,
    history: List[Dict[str, Any]],
    memory: str,
    user: str,
    llm_config: Optional[Dict[str, Any]] = None,
) -> MemoryReviewResult:
    sid = f'memory_review_{user_id.strip() or uuid4().hex}'
    lazyllm.globals._init_sid(sid=sid)
    lazyllm.locals._init_sid(sid=sid)
    inject_model_config(llm_config)
    LOG.info(
        f'[MemoryReview] review started: user_id={user_id} '
        f'history_len={len(history)} memory_len={len(memory or "")} '
        f'user_len={len(user or "")} has_llm_config={bool(llm_config)}'
    )

    prompt = build_memory_review_prompt(
        memory=memory,
        user=user,
    )

    config = {
        'user_id': user_id,
        'core_api_url': _cfg['core_api_url'],
        'memory': memory,
        'user_preference': user,
    }
    lazyllm.globals['agentic_config'] = config

    llm = AutoModel(model='llm')
    review_agent = lazyllm.tools.agent.ReactAgent(
        llm=llm,
        tools=[memory_editor],
        max_retries=_cfg['review_max_retries'],
        return_trace=False,
        prompt=' ',
        keep_full_turns=3,
        fs=FS,
        enable_builtin_tools=False,
        force_summarize=True,
    )
    lazyllm.locals['_lazyllm_agent'] = {}
    res = review_agent(
        prompt,
        llm_chat_history=normalize_history_for_agent(history),
    )
    LOG.info(
        f'[MemoryReview] review finished: user_id={user_id} '
        f'history_len={len(history)} memory_len={len(memory or "")} '
        f'user_len={len(user or "")} has_llm_config={bool(llm_config)} '
        f'res={_truncate_log_text(res)!r}'
    )
    return MemoryReviewResult(status='success')
