"""Scheduling tools and lazy ToolGroup for schedule management.

Provides create / list / cancel / update / trigger schedule tools,
packaged as a lazy ToolGroup so the LLM only sees the gateway tool
until the user mentions scheduling topics.
"""
from __future__ import annotations

import lazyllm
from typing import Any, Dict, List, Optional

from lazymind.chat.engine.tools.infra import handle_tool_errors


def _cron_to_human(cron_expr: str) -> str:
    """Convert a 5-field cron expression to a human-readable Chinese description.

    Handles common patterns; falls back to the raw expression for edge cases.
    """
    try:
        parts = cron_expr.strip().split()
        if len(parts) != 5:
            return cron_expr
        minute, hour, day, month, weekday = parts

        WEEKDAY_NAMES = {
            '0': 'Sun', '7': 'Sun',
            '1': 'Mon', '2': 'Tue', '3': 'Wed',
            '4': 'Thu', '5': 'Fri', '6': 'Sat',
        }

        def _fmt_weekdays(wd: str) -> str:
            names = []
            for token in wd.split(','):
                if '-' in token:
                    a, b = token.split('-', 1)
                    names.append(f'{WEEKDAY_NAMES.get(a, a)}-{WEEKDAY_NAMES.get(b, b)}')
                else:
                    names.append(WEEKDAY_NAMES.get(token, token))
            return '/'.join(names)

        def _is_any(f: str) -> bool:
            return f in ('*', '?')

        # Build time part
        if minute.isdigit() and hour.isdigit():
            time_str = f'{int(hour):02d}:{int(minute):02d}'
        elif _is_any(minute) and _is_any(hour):
            time_str = 'every minute'
        elif minute.isdigit() and _is_any(hour):
            time_str = f'at minute {minute} of every hour'
        else:
            time_str = f'at {hour}h{minute}m'

        # Build date/repeat part
        if _is_any(day) and _is_any(month) and _is_any(weekday):
            date_str = 'every day'
        elif not _is_any(weekday):
            date_str = f'every {_fmt_weekdays(weekday)}'
        elif day.isdigit() and _is_any(month):
            date_str = f'on day {day} of every month'
        elif day.isdigit() and month.isdigit():
            date_str = f'on {month}/{day} every year'
        else:
            date_str = f'({cron_expr})'

        if time_str == 'every minute':
            return time_str
        if time_str.startswith('at minute'):
            return f'{date_str}, {time_str}'
        return f'{date_str} at {time_str}'
    except Exception:
        return cron_expr


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
            f"Next run: {data.get('next_run_at')} | Schedule: {_cron_to_human(cron_expr)}"
        )

    @handle_tool_errors
    def list_schedules(include_disabled: bool = True) -> str:
        """List recurring schedules for this user.

        Default (include_disabled=True): returns ALL schedules (enabled and disabled).
        Pass include_disabled=False only when the user explicitly asks for active/enabled schedules only.

        Args:
            include_disabled: When True (default), return all schedules regardless of enabled state.
                Pass False only when user explicitly wants only active/enabled schedules.
        """
        import httpx
        from lazymind.config import config as _cfg
        cfg = _agentic_config()
        user_id = cfg.get('user_id', '')
        core_url = str(_cfg['core_api_url']).rstrip('/')
        headers = {'X-User-Id': user_id} if user_id else {}
        params = {'include_disabled': 'true'} if include_disabled else {}
        resp = httpx.get(f'{core_url}/schedules', headers=headers, params=params, timeout=5.0)
        if resp.status_code != 200:
            return f'Could not fetch schedules: {resp.text}'
        items = resp.json().get('items', [])
        if not items:
            return 'No schedules found.'
        header = '## All schedules' if include_disabled else '## Active schedules'
        lines = [header]
        for s in items:
            status = 'enabled' if s.get('enabled', True) else 'disabled'
            name = s.get('name') or ''
            label = f' ({name})' if name else ''
            lines.append(
                f"- [{status}] id={s.get('id')}{label} | schedule={_cron_to_human(s.get('cron_expr', ''))} "
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
            f'Schedule {schedule_id!r} updated.\n'
            f"Next run: {data.get('next_run_at')} | Schedule: {_cron_to_human(data.get('cron_expr', ''))}"
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
            f'Schedule {schedule_id!r} triggered immediately.\n'
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
            'Manage and query recurring scheduled tasks. '
            'Use this tool group to list existing schedules, create new ones, '
            'modify or cancel a schedule, and trigger a schedule immediately.'
        ),
        'lazy': True,
    }
