from __future__ import annotations

from pathlib import PurePath
from typing import Any


_ASSISTANT_UI_STATUSES = {'ready', 'error', 'dispatching', 'cancelling'}


def project_name(cwd: str) -> str:
    normalized = str(cwd or '').rstrip('/\\')
    if not normalized:
        return '未归属项目'
    return PurePath(normalized).name or normalized


def projects_view(response: dict[str, Any]) -> dict[str, Any]:
    return {
        'kind': 'projects',
        'projects': list(response.get('data') or []),
        'next_cursor': str(response.get('nextCursor') or '')[:512],
        'total': max(0, int(response.get('total') or 0)),
    }


def sessions_view(response: dict[str, Any]) -> dict[str, Any]:
    return {
        'kind': 'sessions',
        'threads': list(response.get('data') or []),
        'next_cursor': str(response.get('nextCursor') or '')[:512],
        'total': max(0, int(response.get('total') or 0)),
    }


def detail_view(page: dict[str, Any]) -> dict[str, Any]:
    return {
        **page,
        'kind': 'detail',
    }


def detail_snapshot(view: dict[str, Any]) -> dict[str, Any]:
    snapshot = view.get('snapshot')
    return dict(snapshot) if isinstance(snapshot, dict) else {}


def detail_with_prompt(
    view: dict[str, Any],
    prompt: str,
) -> dict[str, Any]:
    if view.get('kind') != 'detail':
        return view
    return {**view, 'prompt': str(prompt or '')[:4000]}


def detail_conversation_id(view: dict[str, Any]) -> str:
    return str(detail_snapshot(view).get('conversation_id') or '')


def detail_run_status(view: dict[str, Any]) -> str:
    snapshot = detail_snapshot(view)
    status = str(snapshot.get('status') or '')
    control_release = str(snapshot.get('control_release') or '')
    if snapshot.get('pending_request'):
        return 'waiting_for_input'
    if status == 'releasing' or control_release == 'pending':
        return 'releasing'
    if control_release == 'failed':
        return 'release_failed'
    if status in {'starting', 'running', 'waiting'}:
        return 'running'
    if status == 'failed':
        return 'failed'
    if status == 'interrupted':
        return 'cancelled'
    if status == 'completed':
        return 'completed'
    return 'idle'


def detail_readonly(view: dict[str, Any]) -> bool:
    if detail_run_status(view) in {'releasing', 'release_failed'}:
        return True
    thread = view.get('thread')
    thread = dict(thread) if isinstance(thread, dict) else {}
    return not bool(thread.get('available')) and not bool(
        thread.get('controlled_by_lazymind')
    )


def assistant_view_with_ui(
    view: dict[str, Any] | None,
    status: str,
    error: str = '',
) -> dict[str, Any]:
    result = dict(view or {})
    normalized = status if status in _ASSISTANT_UI_STATUSES else 'ready'
    result['_ui'] = {
        'status': normalized,
        'error': str(error or '')[:500] if normalized == 'error' else '',
    }
    return result


def assistant_ui_status(view: dict[str, Any]) -> str:
    ui = view.get('_ui')
    ui = ui if isinstance(ui, dict) else {}
    status = str(ui.get('status') or 'ready')
    return status if status in _ASSISTANT_UI_STATUSES else 'ready'


def assistant_ui_error(view: dict[str, Any]) -> str:
    ui = view.get('_ui')
    ui = ui if isinstance(ui, dict) else {}
    return str(ui.get('error') or '')[:500]


def user_input_answers(
    structured: dict[str, Any],
) -> dict[str, dict[str, list[str]]]:
    answers: dict[str, dict[str, list[str]]] = {}
    questions = structured.get('questions')
    for question in questions if isinstance(questions, list) else []:
        if not isinstance(question, dict):
            continue
        question_id = str(question.get('id') or '').strip()
        answer = question.get('answer')
        if not question_id or not isinstance(answer, dict):
            continue
        raw = answer.get('value')
        values = (
            [str(item) for item in raw if str(item)]
            if isinstance(raw, list)
            else [str(raw)] if str(raw or '') else []
        )
        other = str(answer.get('otherText') or '').strip()
        if other:
            values = [other if value == '其他' else value for value in values]
        answers[question_id] = {'answers': values}
    return answers
