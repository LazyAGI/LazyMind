import inspect

from lazymind.chat.engine.tools.schedule import (
    _batch_task_payload,
    _cron_to_human,
    _dependency_payload,
    _schedule_tools,
)


def test_schedule_tools_expose_dependencies_for_create_and_update():
    tools = {tool.__name__: tool for tool in _schedule_tools()}

    create_params = inspect.signature(tools['create_schedule']).parameters
    update_params = inspect.signature(tools['update_schedule']).parameters

    assert 'dependency_schedule_ids' in create_params
    assert 'dependency_schedule_ids' in update_params
    assert 'name' in create_params
    assert 'group_id' in create_params

    assert 'create_schedule_group' in tools
    assert 'list_schedule_groups' in tools
    assert 'move_schedule_to_group' in tools


def test_dependency_payload_uses_collection_defaults():
    assert _dependency_payload(['sched_daily']) == [{
        'source_schedule_id': 'sched_daily',
        'window_type': 'between_target_fires',
        'content_types': ['final_answer', 'artifacts'],
        'incomplete_policy': 'wait_then_run_with_warning',
        'max_wait_seconds': 7200,
    }]


def test_month_end_cron_is_documented_as_last_day():
    assert _cron_to_human('0 10 -1 * *') == 'on last day of every month at 10:00'


def test_batch_group_dependencies_can_reference_prior_and_existing_tasks():
    tasks = _batch_task_payload([
        {
            'client_key': 'weekly',
            'name': 'Weekly report',
            'cron_expr': '0 9 * * 5',
            'prompt_template': 'Summarize daily reports',
            'dependency_schedule_ids': ['sched_daily'],
        },
        {
            'client_key': 'monthly',
            'name': 'Monthly report',
            'cron_expr': '0 10 -1 * *',
            'prompt_template': 'Summarize weekly reports',
            'dependency_client_keys': ['weekly'],
        },
    ])

    assert tasks[0]['dependencies'][0]['source_schedule_id'] == 'sched_daily'
    assert tasks[1]['dependencies'][0]['source_client_key'] == 'weekly'
