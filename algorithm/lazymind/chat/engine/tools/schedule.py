"""Scheduling tools and lazy ToolGroup for schedule management.

Provides create / list / cancel / update / trigger schedule tools,
packaged as a lazy ToolGroup so the LLM only sees the gateway tool
until the user mentions scheduling topics.
"""
from __future__ import annotations

import lazyllm
from typing import Any, Dict, List, Optional

from lazymind.chat.engine.tools.infra import handle_tool_errors


def _agentic_config() -> Dict[str, Any]:
    try:
        return lazyllm.globals['agentic_config'] or {}
    except Exception:
        return {}


def _schedule_tools() -> List[Any]:
    """Build and return all schedule management tool functions."""

    @handle_tool_errors
    def create_schedule(
        cron_expr: str,
        prompt_template: str,
        timezone: str = 'Asia/Shanghai',
        conversation_id: Optional[str] = None,
    ) -> str:
        """Create a recurring scheduled task.

        Args:
            cron_expr: Standard 5-field cron expression: "<minute> <hour> <day> <month> <weekday>".
                Fields: minute(0-59), hour(0-23), day(1-31), month(1-12), weekday(0-6, 0=Sunday).
                Examples:
                  '0 12 * * *'   — every day at noon
                  '30 8 * * 1-5' — 8:30am on weekdays
                  '0 9 1 * *'    — 9am on the 1st of every month
                IMPORTANT: use exactly 5 fields. Do NOT use 6-field (seconds-prefixed) cron format.
            prompt_template: The query that will be sent to this conversation on each trigger.
                Supports placeholders: {{date}}, {{time}}, {{datetime}}.
            timezone: IANA timezone name. Defaults to 'Asia/Shanghai'.
            conversation_id: Bind to a specific conversation. Defaults to the current one.
        """
        import httpx
        from lazymind.config import config as _cfg
        cfg = _agentic_config()
        conv_id = conversation_id or cfg.get('conversation_id', '')
        user_id = cfg.get('user_id', '')
        core_url = str(_cfg['core_api_url']).rstrip('/')
        headers = {'X-User-Id': user_id} if user_id else {}
        payload: Dict[str, Any] = {
            'cron_expr': cron_expr,
            'prompt_template': prompt_template,
            'timezone': timezone,
        }
        if conv_id:
            payload['conversation_id'] = conv_id
        resp = httpx.post(f'{core_url}/schedules', json=payload, headers=headers, timeout=10.0)
        if resp.status_code not in (200, 201):
            return f'Failed to create schedule: {resp.text}'
        data = resp.json()
        return (
            f"Schedule created (id={data.get('id')}).\n"
            f"Next run: {data.get('next_run_at')} | Cron: {cron_expr}"
        )

    @handle_tool_errors
    def list_schedules() -> str:
        """List all active recurring schedules for this user."""
        import httpx
        from lazymind.config import config as _cfg
        cfg = _agentic_config()
        user_id = cfg.get('user_id', '')
        core_url = str(_cfg['core_api_url']).rstrip('/')
        headers = {'X-User-Id': user_id} if user_id else {}
        resp = httpx.get(f'{core_url}/schedules', headers=headers, timeout=5.0)
        if resp.status_code != 200:
            return f'Could not fetch schedules: {resp.text}'
        items = resp.json().get('items', [])
        if not items:
            return 'No active schedules.'
        lines = ['## Active schedules']
        for s in items:
            lines.append(
                f"- id={s.get('id')} | cron={s.get('cron_expr')} "
                f"| next={s.get('next_run_at')} | {s.get('prompt_template', '')[:60]}"
            )
        return '\n'.join(lines)

    @handle_tool_errors
    def cancel_schedule(schedule_id: str) -> str:
        """Cancel (disable) a recurring schedule by its ID."""
        import httpx
        from lazymind.config import config as _cfg
        cfg = _agentic_config()
        user_id = cfg.get('user_id', '')
        core_url = str(_cfg['core_api_url']).rstrip('/')
        headers = {'X-User-Id': user_id} if user_id else {}
        resp = httpx.post(f'{core_url}/schedules/{schedule_id}:cancel', headers=headers, timeout=5.0)
        if resp.status_code != 200:
            return f'Failed to cancel schedule {schedule_id!r}: {resp.text}'
        return f'Schedule {schedule_id!r} has been cancelled.'

    @handle_tool_errors
    def update_schedule(
        schedule_id: str,
        cron_expr: Optional[str] = None,
        prompt_template: Optional[str] = None,
        timezone: Optional[str] = None,
        name: Optional[str] = None,
    ) -> str:
        """Update the cron expression, prompt, timezone, or name of an existing schedule.

        Only the fields you supply are changed; omitted fields keep their current values.

        Args:
            schedule_id: The ID of the schedule to update (from list_schedules).
            cron_expr: New 5-field cron expression, e.g. '0 9 * * *' for 9am daily.
            prompt_template: New prompt template for the scheduled query.
            timezone: New IANA timezone name, e.g. 'Asia/Shanghai'.
            name: New human-readable name for the schedule.
        """
        import httpx
        from lazymind.config import config as _cfg
        cfg = _agentic_config()
        user_id = cfg.get('user_id', '')
        core_url = str(_cfg['core_api_url']).rstrip('/')
        headers = {'X-User-Id': user_id} if user_id else {}
        payload: Dict[str, Any] = {}
        if cron_expr is not None:
            payload['cron_expr'] = cron_expr
        if prompt_template is not None:
            payload['prompt_template'] = prompt_template
        if timezone is not None:
            payload['timezone'] = timezone
        if name is not None:
            payload['name'] = name
        if not payload:
            return 'Nothing to update — please provide at least one field to change.'
        resp = httpx.put(f'{core_url}/schedules/{schedule_id}', json=payload, headers=headers, timeout=10.0)
        if resp.status_code != 200:
            return f'Failed to update schedule {schedule_id!r}: {resp.text}'
        data = resp.json()
        return (
            f"Schedule {schedule_id!r} updated.\n"
            f"Next run: {data.get('next_run_at')} | Cron: {data.get('cron_expr')}"
        )

    @handle_tool_errors
    def trigger_schedule(schedule_id: str) -> str:
        """Immediately run a scheduled task once, without waiting for its next scheduled time.

        This fires the schedule right now — it does NOT change the next_run_at, so the
        regular recurring execution continues on its original schedule.

        Args:
            schedule_id: The ID of the schedule to trigger (from list_schedules).
        """
        import httpx
        from lazymind.config import config as _cfg
        cfg = _agentic_config()
        user_id = cfg.get('user_id', '')
        core_url = str(_cfg['core_api_url']).rstrip('/')
        headers = {'X-User-Id': user_id} if user_id else {}
        resp = httpx.post(
            f'{core_url}/schedules/{schedule_id}:run-now', headers=headers, timeout=10.0,
        )
        if resp.status_code != 200:
            return f'Failed to trigger schedule {schedule_id!r}: {resp.text}'
        data = resp.json()
        return (
            f"Schedule {schedule_id!r} triggered immediately.\n"
            f"Task ID: {data.get('task_id')} | Conversation: {data.get('conversation_id')}"
        )

    return [create_schedule, list_schedules, cancel_schedule, update_schedule, trigger_schedule]


def build_schedule_tool_group() -> dict:
    """Return a lazy ToolGroup dict for all schedule management tools.

    The group activates when the user mentions scheduled tasks or timing topics.
    Provides: create_schedule, list_schedules, cancel_schedule, update_schedule, trigger_schedule.
    """
    return {
        'name': 'schedule',
        'tools': _schedule_tools(),
        'desc': (
            'Activate this group when the user mentions scheduled tasks, recurring jobs, '
            'timed reminders, or asks to create / view / modify / cancel / trigger a schedule. '
            'Provides: create_schedule, list_schedules, cancel_schedule, update_schedule, trigger_schedule.'
        ),
        'lazy': True,
    }
