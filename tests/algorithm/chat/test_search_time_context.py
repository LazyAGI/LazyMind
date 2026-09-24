from copy import deepcopy

import pytest

from lazymind.chat.engine.prompts.system_prompt import build_standard_prompt_bundle
from lazymind.chat.engine.subagent import SUBAGENT_ENVIRONMENT_CONTEXT_KEY
from lazymind.chat.engine.subagent import runner
from lazymind.chat.engine.subagent.context import SubAgentContext
from lazymind.chat.engine.tools import subagent_chat_tools
from lazymind.chat.service.component.tool_registry import (
    WEB_SEARCH_TOOL_POLICY_APPENDIX,
    collect_system_prompt_appendices,
)


ENVIRONMENT = {
    'locale': 'zh-CN',
    'time': {'now': '2026-09-15T18:30:00Z', 'timezone': 'Asia/Shanghai'},
}


@pytest.mark.parametrize('environment', [ENVIRONMENT, {}, None])
def test_subagent_creation_snapshots_host_environment(monkeypatch, environment):
    cfg = {'mode': 'manual', 'environment_context': deepcopy(environment)}
    captured = []
    monkeypatch.setattr(subagent_chat_tools, '_agentic_config', lambda: cfg)
    monkeypatch.setattr(subagent_chat_tools, '_write_agent_data', lambda tag, **kw: captured.append(kw))
    subagent_chat_tools.create_subagent(
        agent_type='research', title='search', objective='find current information',
        params={SUBAGENT_ENVIRONMENT_CONTEXT_KEY: {'time': {'now': '1999-01-01'}}},
    )
    params = captured[0]['params']
    if environment:
        assert params[SUBAGENT_ENVIRONMENT_CONTEXT_KEY] == ENVIRONMENT
        cfg['environment_context']['time']['now'] = 'changed'
        assert params[SUBAGENT_ENVIRONMENT_CONTEXT_KEY] == ENVIRONMENT
    else:
        assert SUBAGENT_ENVIRONMENT_CONTEXT_KEY not in params


@pytest.mark.parametrize('resume', [False, True])
@pytest.mark.parametrize('source', ['snapshot', 'legacy', 'missing', 'empty_snapshot'])
def test_subagent_time_layout_and_tool_context(tmp_path, resume, source):
    params = {}
    if source in ('legacy', 'empty_snapshot'):
        params['parent_agentic_config'] = {'environment_context': deepcopy(ENVIRONMENT)}
    if source in ('snapshot', 'empty_snapshot'):
        params[SUBAGENT_ENVIRONMENT_CONTEXT_KEY] = deepcopy(ENVIRONMENT) if source == 'snapshot' else {}
    ctx = SubAgentContext(
        task_id='time-test', conversation_id='conversation', agent_type='research',
        objective='find current information', params=params, workspace_path=str(tmp_path),
        input_slots=[], output_slots=[], db=None, emit=lambda event: None,
    )
    plan = runner._build_subagent_plan(ctx, None, tools=[], tool_prompt_appendices={}, resume=resume)
    config = runner._build_agentic_config({'objective': ctx.objective}, params, 'research')
    assert ctx.objective in plan.prompt.current_input
    assert '### Task Objective' in plan.prompt.current_input
    assert '02:30:00' not in plan.prompt.system_prompt
    assert '2026-09-16' not in plan.prompt.current_input
    assert SUBAGENT_ENVIRONMENT_CONTEXT_KEY not in plan.prompt.current_input
    if source in ('snapshot', 'legacy'):
        assert config['environment_context'] == ENVIRONMENT
        assert 'Current user date: 2026-09-16 (Asia/Shanghai)' in plan.prompt.system_prompt
        assert 'Current user time: 02:30:00 (Asia/Shanghai)' in plan.prompt.current_input
        current_input = plan.prompt.current_input
        assert current_input.index('Current user time:') < current_input.index('### Task Objective')
    else:
        assert config['environment_context'] == {}
        assert 'Current user date:' not in plan.prompt.system_prompt
        assert 'Current user time:' not in plan.prompt.current_input


@pytest.mark.parametrize('role', ['chat', 'subagent'])
@pytest.mark.parametrize('web_enabled', [False, True])
def test_search_time_policy_follows_tools_without_task_routing(tmp_path, role, web_enabled):
    appendices = collect_system_prompt_appendices(
        [], extra_appendices=(WEB_SEARCH_TOOL_POLICY_APPENDIX,) if web_enabled else (),
    )
    if role == 'chat':
        bundle = build_standard_prompt_bundle(
            True, environment_context=ENVIRONMENT, tool_prompt_appendices=appendices,
            dynamic_prompt_modules=False,
        )
    else:
        ctx = SubAgentContext(
            task_id='policy-test', conversation_id='conversation', agent_type='research',
            objective='search', params={SUBAGENT_ENVIRONMENT_CONTEXT_KEY: ENVIRONMENT},
            workspace_path=str(tmp_path), input_slots=[], output_slots=[], db=None, emit=lambda event: None,
        )
        bundle = runner._build_subagent_plan(ctx, None, tools=[lambda: None], tool_prompt_appendices=appendices).prompt
    assert ('prefer the latest information' in bundle.system_prompt) is web_enabled
    assert 'Current user date: 2026-09-16 (Asia/Shanghai)' in bundle.system_prompt
    assert '02:30:00' not in bundle.system_prompt
    assert 'Current user time: 02:30:00 (Asia/Shanghai)' in bundle.current_input
