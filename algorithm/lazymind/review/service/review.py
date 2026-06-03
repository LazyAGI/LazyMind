from __future__ import annotations

import threading
import traceback
from typing import Any

import lazyllm
from lazyllm.tools.fs.client import FS

from lazymind.chat.engine.tools import (
    memory_editor,
    skill_editor,
    vocab_learn,
)
from lazymind.chat.engine.tools.infra import list_all_skills_with_category
from lazymind.config import config as _cfg
from lazymind.review.config import REVIEW_PROMPTS, REVIEW_TOOLS
from lazymind.review.prompts import COMBINED_REVIEW_PROMPT


_REVIEW_TOOLS: dict[str, Any] = {
    'memory_editor': memory_editor,
    'skill_editor': skill_editor,
    'vocab_learn': vocab_learn,
}


def _resolve_review_runtime_tools(review_tools: list[str]) -> list[Any]:
    runtime_tools: list[Any] = []
    for tool_name in review_tools:
        tool = _REVIEW_TOOLS.get(tool_name)
        if tool is None:
            runtime_tools.append(tool_name)
        else:
            runtime_tools.append(tool)
    return runtime_tools


def _build_review_decision(
    available_tools: list[str],
    tool_turns: int,
    user_turns: int,
    memory_review_interval: int,
    skill_review_interval: int,
) -> dict[str, Any]:
    memory_due = (
        'memory_editor' in available_tools
        and user_turns > memory_review_interval
    )
    skill_due_by_tool_turns = (
        'skill_editor' in available_tools
        and tool_turns >= skill_review_interval
        and user_turns > 1
    )
    skill_due_by_user_turns = (
        'skill_editor' in available_tools
        and user_turns > skill_review_interval
    )
    skill_due = skill_due_by_tool_turns or skill_due_by_user_turns

    debug_force_combined = bool(_cfg['skill_review_debug'])

    if debug_force_combined:
        mode = 'combined'
    elif memory_due and skill_due:
        mode = 'combined'
    elif memory_due:
        mode = 'memory'
    elif skill_due:
        mode = 'skill'
    else:
        mode = None

    return {
        'mode': mode,
        'memory_due': memory_due,
        'skill_due': skill_due,
        'skill_due_by_tool_turns': skill_due_by_tool_turns,
        'skill_due_by_user_turns': skill_due_by_user_turns,
        'debug_force_combined': debug_force_combined,
        'tool_turns': tool_turns,
        'user_turns': user_turns,
        'memory_review_interval': memory_review_interval,
        'skill_review_interval': skill_review_interval,
        'available_tools': list(available_tools or []),
    }


def _spawn_background_review(
    config: dict,
    llm: Any,
    keep_full_turns: int,
    history_snapshot: list,
    review_mode: str,
    request_global_sid: str,
) -> None:
    review_tools = REVIEW_TOOLS.get(review_mode, [])
    runtime_review_tools = _resolve_review_runtime_tools(review_tools)
    review_prompt = REVIEW_PROMPTS.get(review_mode, COMBINED_REVIEW_PROMPT)
    if not review_tools:
        print(f'[bg-review:{review_mode}] SKIP no review tools')
        return

    snapshot = list(history_snapshot)
    skills_dir = _cfg['skill_fs_url']
    skills_with_cat = (
        list_all_skills_with_category(skills_dir)
        if review_mode in ('skill', 'combined') and skills_dir
        else {}
    )
    review_skills = list(skills_with_cat.keys())
    print(
        f'[bg-review:{review_mode}] PREP sid={request_global_sid} '
        f'tools={review_tools} runtime_tools={runtime_review_tools!r} keep_full_turns={keep_full_turns} '
        f'history_messages={len(snapshot)} review_skills={len(review_skills)} '
        f'skills_dir={skills_dir or "(empty)"}'
    )
    if skills_with_cat:
        print(
            f'[bg-review:{review_mode}] SKILLS_WITH_CAT '
            f'skills={skills_with_cat!r}'
        )

    def _worker() -> None:
        tname = threading.current_thread().name
        print(f'[bg-review:{review_mode}] START thread={tname} sid={request_global_sid}')
        try:
            lazyllm.globals._init_sid(request_global_sid)
            lazyllm.locals._init_sid()
            lazyllm.globals['agentic_config'] = config

            review_agent = lazyllm.tools.agent.ReactAgent(
                llm=llm,
                tools=runtime_review_tools,
                max_retries=_cfg['review_max_retries'],
                return_trace=False,
                prompt=' ',
                skills=review_skills,
                keep_full_turns=keep_full_turns,
                fs=FS,
                skills_dir=skills_dir,
                enable_builtin_tools=False,
                force_summarize=True,
            )
            print(
                f'[bg-review:{review_mode}] AGENT_READY thread={tname} '
                f'max_retries={_cfg["review_max_retries"]} '
                f'review_tools={review_tools} '
                f'runtime_review_tools={runtime_review_tools!r} '
                f'review_skills={len(review_skills)}'
            )
            res = review_agent(review_prompt, llm_chat_history=snapshot)
            res_text = res if isinstance(res, str) else str(res)
            preview = res_text[:500].replace('\n', '\\n')
            print(
                f'[bg-review:{review_mode}] DONE thread={tname} '
                f'result_chars={len(res_text)} result_preview="{preview}"'
            )
        except Exception:
            print(f'[bg-review:{review_mode}] FAILED thread={tname}')
            traceback.print_exc()
        finally:
            lazyllm.locals.clear()
            print(f'[bg-review:{review_mode}] EXIT thread={tname}')

    review_debug = _cfg['review_debug']
    if review_debug is True or str(review_debug).strip().lower() in {'1', 'true', 'yes'}:
        _worker()
        return

    thread = threading.Thread(target=_worker, daemon=True)
    print(
        f'[bg-review:{review_mode}] SPAWN_ASYNC sid={request_global_sid} '
        f'thread={thread.name}'
    )
    thread.start()
