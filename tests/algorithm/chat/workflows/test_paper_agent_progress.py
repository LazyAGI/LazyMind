"""Scientific phase projection from native/external durable Agent records."""
import asyncio
import copy
import json

import httpx
import pytest
from fastapi import HTTPException

from lazymind.document_tools.paper.analysis import agent_progress, progress_routes


def record(task, name, args, *, ok=None, value=None):
    identifier = str(len(task['analysis_steps']))
    task['analysis_steps'].append(dict(
        seq=len(task['analysis_steps']) + 1, role='assistant',
        content=dict(tool_calls=[dict(id=identifier, name=name, args=args)])))
    if ok is not None:
        task['analysis_steps'].append(dict(seq=len(task['analysis_steps']) + 1, role='tool', content=dict(
            tool_results=[dict(tool_call_id=identifier, name=name, result=json.dumps(dict(ok=ok, value=value)))])))


@pytest.mark.parametrize('serialized', [False, True])
def test_real_analysis_content_replaces_tool_rows_and_retains_failed_attempt(serialized):
    task = dict(status='running', execution_id='current', analysis_steps=[])
    args = dict(plan=dict(questions=['What is the observed mean?'], outline='Describe the supplied scores.'),
                code='def analyze(tables, plan, rng): return missing')
    record(task, 'paper_analyze', json.dumps(args) if serialized else args,
           ok=False, value='NameError: missing')
    first = agent_progress.project(task)
    assert first['process_steps'][-1]['title'] == '修正分析方案与代码'
    record(task, 'paper_analyze', dict(args, code='def analyze(tables, plan, rng): return 3'), ok=True,
           value=dict(analysis=dict(summary='The observed mean is 3.', findings=[dict(value=3.)])))
    task['status'] = 'succeeded'
    before = copy.deepcopy(task)
    projected = agent_progress.project(task)
    steps = projected['process_steps']
    assert projected['revision'] > first['revision']
    assert [step['status'] for step in steps if step['title'].startswith('统计执行')] == ['failed', 'succeeded']
    assert 'What is the observed mean?' in steps[0]['output']['text']
    assert steps[1]['output']['text'] == args['code']
    assert 'The observed mean is 3.' in steps[-1]['output']['text']
    assert not any('paper_analyze' in step['title'] for step in steps)
    assert 'progress_pct' not in projected
    assert task == before


def test_plot_details_exclude_receipts_svg_and_private_reasoning():
    task = dict(status='failed', execution_id='current', analysis_steps=[
        dict(seq=1, role='think', content=dict(content='PRIVATE_REASONING'))])
    args = dict(code='def draw(data, statistics, spec): pass',
                specification=dict(title='Observed scores', caption='Actual data', svg='PRIVATE_SVG'))
    record(task, 'paper_plot', args,
           ok=False, value='invalid figure')
    projected = agent_progress.project(task)
    text = json.dumps(projected)
    assert 'Observed scores' in text and 'invalid figure' in text
    assert 'PRIVATE_SVG' not in text and 'PRIVATE_REASONING' not in text
    assert projected['error']['code'] == 'ANALYSIS_EXECUTION_FAILED'


def test_missing_or_obsolete_history_does_not_invent_analysis():
    assert agent_progress.project(dict(status='running', execution_id='new', analysis_steps=[
        dict(seq=1, role='assistant', execution_id='old', content=dict(tool_calls=[dict(
            id='old', name='paper_analyze', args=dict(code='old', plan={}))]))])) is None


@pytest.mark.parametrize('owned', [False, True])
def test_history_read_keeps_owner_check_and_selects_only_the_bound_task(monkeypatch, owned):
    paths = []
    task = dict(task_id='task', conversation_id='conversation', execution_id='current',
                agent_type='workflow_step', title='paper-writing:data_analysis', status='failed')

    class Client:
        async def __aenter__(self):
            return self

        async def __aexit__(self, *args):
            pass

        async def get(self, url, headers):
            paths.append(url)
            if url.endswith('/tasks/task'):
                return httpx.Response(200 if owned else 404, json=dict(data=dict(task=task)))
            return httpx.Response(200, json=dict(data=dict(tasks=[
                dict(task_id='other', execution_id='current', steps=['PRIVATE_OTHER_TASK']),
                dict(task_id='task', execution_id='current', steps=[dict(seq=1, role='text', content={})]),
            ])))
    monkeypatch.setattr(progress_routes.httpx, 'AsyncClient', lambda **kwargs: Client())
    if not owned:
        with pytest.raises(HTTPException) as error:
            asyncio.run(progress_routes.authorized_task('task', 'Bearer local'))
        assert error.value.status_code == 404 and len(paths) == 1
    else:
        result = asyncio.run(progress_routes.authorized_task('task', 'Bearer local'))
        assert result['analysis_steps'] == [dict(seq=1, role='text', content={})]
        assert 'PRIVATE_OTHER_TASK' not in json.dumps(result)
