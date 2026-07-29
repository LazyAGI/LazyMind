"""Scheduling tools and lazy Toolkit for schedule management.

Provides create / list / cancel / update / trigger schedule tools,
packaged as a lazy Toolkit so the LLM only sees the gateway tool
until the user mentions scheduling topics.
"""
from __future__ import annotations

import lazyllm
from typing import Any, Dict, List, Optional


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

        def _fmt_month_days(dom: str) -> str:
            labels = []
            for token in dom.split(','):
                if token == '-1':
                    labels.append('last day')
                elif token in ('-2', '-3', '-4'):
                    labels.append(f'{abs(int(token))} days from month end')
                else:
                    labels.append(f'day {token}')
            return '/'.join(labels)

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
        elif not _is_any(day) and _is_any(month) and _is_any(weekday):
            date_str = f'on {_fmt_month_days(day)} of every month'
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


def _dependency_payload(schedule_ids: Optional[List[str]]) -> List[Dict[str, Any]]:
    """Build the dependency objects expected by the Core schedule API."""
    return [
        {
            'source_schedule_id': schedule_id,
            'window_type': 'between_target_fires',
            'content_types': ['final_answer', 'artifacts'],
            'incomplete_policy': 'wait_then_run_with_warning',
            'max_wait_seconds': 7200,
        }
        for schedule_id in (schedule_ids or [])
        if schedule_id
    ]


def _batch_task_payload(tasks: List[Dict[str, Any]]) -> List[Dict[str, Any]]:
    """Translate the model-friendly group task schema to the Core batch API."""
    payload = []
    for index, raw in enumerate(tasks):
        task = dict(raw)
        client_key = str(task.get('client_key') or f'task_{index + 1}')
        dependencies = _dependency_payload(task.pop('dependency_schedule_ids', None))
        dependencies.extend({
            'source_client_key': source_client_key,
            'window_type': 'between_target_fires',
            'content_types': ['final_answer', 'artifacts'],
            'incomplete_policy': 'wait_then_run_with_warning',
            'max_wait_seconds': 7200,
        } for source_client_key in (task.pop('dependency_client_keys', None) or []))
        task['client_key'] = client_key
        task['dependencies'] = dependencies
        payload.append(task)
    return payload


def _schedule_tools() -> List[Any]:
    """Build and return all schedule management tool functions."""

    def create_schedule(
        cron_expr: str,
        prompt_template: str,
        timezone: str = 'Asia/Shanghai',
        conversation_id: Optional[str] = None,
        name: Optional[str] = None,
        dependency_schedule_ids: Optional[List[str]] = None,
        group_id: Optional[str] = None,
    ) -> str:
        """Create a recurring scheduled task.

        Args:
            cron_expr: Standard 5-field cron expression: "<minute> <hour> <day> <month> <weekday>".
                Fields: minute(0-59), hour(0-23), day(1-31 or -1 through -4),
                month(1-12), weekday(0-6, 0=Sunday). In the day field, -1 means
                the last day of each month, -2 the second-to-last day, and so on.
                Examples:
                  '0 12 * * *'   — every day at noon
                  '30 8 * * 1-5' — 8:30am on weekdays
                  '0 9 1 * *'    — 9am on the 1st of every month
                  '0 10 -1 * *'  — 10am on the actual last day of every month
                Never approximate "last day of month" with 28-31: that runs on
                every matching day. Use -1 exactly.
                IMPORTANT: use exactly 5 fields. Do NOT use 6-field (seconds-prefixed) cron format.
            prompt_template: The query that will be sent to this conversation on each trigger.
                Supports placeholders: {{date}}, {{time}}, {{datetime}}.
            timezone: IANA timezone name. Defaults to 'Asia/Shanghai'.
            conversation_id: Bind to a specific conversation. Defaults to the current one.
            name: Short human-readable task name.
            dependency_schedule_ids: IDs of schedules whose completed outputs this task must
                collect. When the user says based on, collect, summarize, or aggregate another
                recurring task, list schedules first (or use an ID returned by an earlier
                create_schedule call) and pass its ID here. Create upstream schedules before
                downstream schedules. Sources must run at least as frequently as this task.
            group_id: Optional existing task-group ID from list_schedule_groups. This controls
                presentation only and does not replace dependency_schedule_ids.
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
            'dependencies': _dependency_payload(dependency_schedule_ids),
        }
        if name:
            payload['name'] = name
        if group_id:
            payload['group_id'] = group_id
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
            dependencies = s.get('dependencies') or []
            dependency_text = ','.join(
                f"{d.get('source_name') or d.get('source_schedule_id')}"
                for d in dependencies
            ) or 'none'
            lines.append(
                f"- [{status}] id={s.get('id')}{label} | schedule={_cron_to_human(s.get('cron_expr', ''))} "
                f"| dependencies={dependency_text} | next={s.get('next_run_at')} "
                f"| {s.get('prompt_template', '')[:60]}"
            )
        return '\n'.join(lines)

    def create_schedule_group(
        name: str,
        tasks: Optional[List[Dict[str, Any]]] = None,
        remark: str = '',
        timezone: str = 'Asia/Shanghai',
    ) -> str:
        """Create an empty task group or atomically create a group with related schedules.

        Prefer this tool when the user asks for a group/set/series of related recurring tasks.
        When tasks are supplied, the group, every schedule, and every dependency are committed
        together; any invalid item rolls back the whole request.

        Args:
            name: Human-readable task-group name.
            tasks: Optional ordered task definitions. Each item supports client_key, name,
                cron_expr, prompt_template, remark, dependency_client_keys, and
                dependency_schedule_ids. Use a prior task's client_key for dependencies inside
                this new group; only reference earlier items. Use dependency_schedule_ids for
                existing schedules, including schedules outside the group. Cron month-end values
                must use -1 through -4, never 28-31 as an approximation.
            remark: Optional group description.
            timezone: IANA timezone for every task in the group.
        """
        import httpx
        from lazymind.config import config as _cfg
        cfg = _agentic_config()
        user_id = cfg.get('user_id', '')
        core_url = str(_cfg['core_api_url']).rstrip('/')
        headers = {'X-User-Id': user_id} if user_id else {}
        if tasks:
            payload = {
                'group': {'name': name, 'remark': remark, 'timezone': timezone},
                'tasks': _batch_task_payload(tasks),
            }
            resp = httpx.post(
                f'{core_url}/automation-groups:batch-create',
                json=payload,
                headers=headers,
                timeout=20.0,
            )
        else:
            resp = httpx.post(
                f'{core_url}/automation-groups',
                json={'name': name, 'remark': remark, 'timezone': timezone},
                headers=headers,
                timeout=10.0,
            )
        if resp.status_code not in (200, 201):
            return f'Failed to create schedule group: {resp.text}'
        data = resp.json()
        group_id = data.get('group_id') or data.get('id')
        schedule_ids = data.get('schedule_ids') or {}
        return f'Schedule group created (id={group_id}). Schedule IDs: {schedule_ids}'

    def list_schedule_groups() -> str:
        """List task groups so schedules can be created in or moved to an existing group."""
        import httpx
        from lazymind.config import config as _cfg
        cfg = _agentic_config()
        user_id = cfg.get('user_id', '')
        core_url = str(_cfg['core_api_url']).rstrip('/')
        headers = {'X-User-Id': user_id} if user_id else {}
        resp = httpx.get(f'{core_url}/automation-groups', headers=headers, timeout=5.0)
        if resp.status_code != 200:
            return f'Could not fetch schedule groups: {resp.text}'
        items = resp.json().get('items', [])
        if not items:
            return 'No schedule groups found.'
        return '\n'.join(
            ['## Schedule groups'] + [
                f"- id={group.get('id')} | name={group.get('name')} | tasks={group.get('task_count', 0)}"
                for group in items
            ]
        )

    def move_schedule_to_group(
        schedule_id: str,
        group_id: Optional[str] = None,
        position: int = 0,
    ) -> str:
        """Move an existing schedule into a group, between groups, or out of all groups.

        Args:
            schedule_id: Existing schedule ID from list_schedules.
            group_id: Destination ID from list_schedule_groups. Omit to move into Other Tasks.
            position: Zero-based display position inside the destination group.
        """
        import httpx
        from lazymind.config import config as _cfg
        cfg = _agentic_config()
        user_id = cfg.get('user_id', '')
        core_url = str(_cfg['core_api_url']).rstrip('/')
        headers = {'X-User-Id': user_id} if user_id else {}
        resp = httpx.post(
            f'{core_url}/schedules/{schedule_id}:move',
            json={'group_id': group_id, 'position': max(0, position)},
            headers=headers,
            timeout=10.0,
        )
        if resp.status_code != 200:
            return f'Failed to move schedule {schedule_id!r}: {resp.text}'
        destination = group_id or 'Other Tasks'
        return f'Schedule {schedule_id!r} moved to {destination} at position {max(0, position)}.'

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

    def update_schedule(
        schedule_id: str,
        cron_expr: Optional[str] = None,
        prompt_template: Optional[str] = None,
        timezone: Optional[str] = None,
        name: Optional[str] = None,
        dependency_schedule_ids: Optional[List[str]] = None,
    ) -> str:
        """Update the cron expression, prompt, timezone, or name of an existing schedule.

        Only the fields you supply are changed; omitted fields keep their current values.

        Args:
            schedule_id: The ID of the schedule to update (from list_schedules).
            cron_expr: New 5-field cron expression, e.g. '0 9 * * *' for 9am daily.
            prompt_template: New prompt template for the scheduled query.
            timezone: New IANA timezone name, e.g. 'Asia/Shanghai'.
            name: New human-readable name for the schedule.
            dependency_schedule_ids: Replacement list of source schedule IDs to collect.
                Omit to preserve current dependencies; pass an empty list to clear them.
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
        if dependency_schedule_ids is not None:
            payload['dependencies'] = _dependency_payload(dependency_schedule_ids)
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

    return [
        create_schedule,
        list_schedules,
        create_schedule_group,
        list_schedule_groups,
        move_schedule_to_group,
        cancel_schedule,
        update_schedule,
        trigger_schedule,
    ]


def build_schedule_toolkit() -> dict:
    """Return a lazy Toolkit dict for all schedule management tools.

    The group activates when the user mentions scheduled tasks or timing topics.
    Provides schedule and task-group creation, listing, moving, updating, cancelling, and triggering.
    """
    return {
        'name': 'ScheduleToolkit',
        'tools': _schedule_tools(),
        'desc': (
            'Manage and query recurring scheduled tasks. '
            'Use this Toolkit to list existing schedules and task groups, create new ones, '
            'link dependent schedules that collect prior outputs, modify or cancel a schedule, '
            'and trigger a schedule immediately. For dependent chains, create or identify the '
            'upstream schedule first and pass its ID as dependency_schedule_ids on downstream tasks. '
            'When the user asks for a group of related tasks, use create_schedule_group with tasks '
            'so group creation, schedules, and dependencies are atomic.'
        ),
        'lazy': True,
    }
