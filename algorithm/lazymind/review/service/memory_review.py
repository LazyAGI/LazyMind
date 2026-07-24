from __future__ import annotations

from typing import Any, Dict, List, Literal, Optional

import lazyllm
from lazyllm import AutoModel, LOG
from lazyllm.tools.fs.client import FS
from pydantic import BaseModel, ConfigDict

from lazymind.chat.engine.tools.memory_editor import memory_editor
from lazymind.chat.engine.tools.memory_reader import read_memory
from lazymind.chat.engine.tools.infra import MemoryRemoteStore
from lazymind.chat.service.component.history import normalize_history_for_agent
from lazymind.config import config as _cfg
from lazymind.model_config import inject_model_config
from lazymind.review.memory_review.prompts import build_memory_review_prompt


class MemoryReviewResult(BaseModel):
    model_config = ConfigDict(extra='forbid')

    status: Literal['success', 'failed']
    task_id: str


def _truncate_log_text(value: Any, limit: int = 4000) -> str:
    text = str(value)
    if len(text) <= limit:
        return text
    return f'{text[:limit]}...<truncated {len(text) - limit} chars>'


def review_memory(
    task_id: str,
    user_id: str,
    history: List[Dict[str, Any]],
    llm_config: Optional[Dict[str, Any]] = None,
) -> MemoryReviewResult:
    lazyllm.globals._init_sid(sid=task_id)
    lazyllm.locals._init_sid(sid=task_id)
    inject_model_config(llm_config)
    LOG.info(
        f'[MemoryReview] review started: user_id={user_id} '
        f'task_id={task_id} history_len={len(history)} '
        f'has_llm_config={bool(llm_config)}'
    )

    config = {
        'user_id': user_id,
        'task_id': task_id,
    }
    lazyllm.globals['agentic_config'] = config

    store = MemoryRemoteStore()
    remote_memory = store.read('memory')
    remote_user = store.read('user_preference')

    prompt = build_memory_review_prompt(
        memory=remote_memory,
        user=remote_user,
    )

    llm = AutoModel(model='llm')
    review_agent = lazyllm.tools.agent.ReactAgent(
        llm=llm,
        tools=[read_memory, memory_editor],
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
        f'task_id={task_id} history_len={len(history)} '
        f'memory_len={len(remote_memory or "")} '
        f'user_len={len(remote_user or "")} has_llm_config={bool(llm_config)} '
        f'res={_truncate_log_text(res)!r}'
    )
    return MemoryReviewResult(status='success', task_id=task_id)
