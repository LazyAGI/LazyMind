from __future__ import annotations

from lazymind.chat.engine.subagent.context import SubAgentContext
from lazymind.chat.engine.subagent.runner import (
    _build_subagent_plan,
    _build_subagent_tools,
    _publisher_owns_outputs,
)


def _ctx(*, workflow_id: str, output_slots: list[str]) -> SubAgentContext:
    return SubAgentContext(
        task_id='task-1',
        conversation_id='conversation-1',
        agent_type='workflow_step',
        objective='生成 PPT 页面',
        params={
            'workflow_id': workflow_id,
            'workflow_runtime': {
                'publisher_owned_slots': ['slide_outline', 'preview_html', 'preview_notes'],
            },
        },
        workspace_path='',
        input_slots=[],
        output_slots=output_slots,
        db=None,
        emit=lambda _event: None,
    )


def _tool_names(tools: list[object]) -> set[str]:
    return {str(getattr(tool, '__name__', '')) for tool in tools}


def test_ppt_publisher_outputs_disable_generic_artifact_writes() -> None:
    ctx = _ctx(workflow_id='builtin:ppt-workflow', output_slots=['preview_html', 'preview_notes'])

    assert _publisher_owns_outputs(ctx)
    tools = _build_subagent_tools([], include_artifact_writes=False)
    names = _tool_names(tools)
    assert 'save_artifacts' not in names
    assert 'patch_artifact' not in names
    assert 'discard_draft' not in names
    assert 'get_artifact' in names


def test_ppt_publisher_prompt_has_no_generic_save_contract() -> None:
    ctx = _ctx(workflow_id='ppt-workflow', output_slots=['preview_html', 'preview_notes'])
    plan = _build_subagent_plan(
        ctx,
        None,
        tools=[],
        tool_prompt_appendices={},
    )

    prompt = f'{plan.prompt.system_prompt}\n{plan.prompt.current_input}'
    assert 'publisher-owned' in prompt
    assert 'After the package publisher tool succeeds, stop' in prompt
    assert '## Exact save_artifacts call shape' not in prompt


def test_non_ppt_step_keeps_generic_artifact_write_contract() -> None:
    ctx = _ctx(workflow_id='report-workflow', output_slots=['report'])
    ctx.params['workflow_runtime'] = {}

    assert not _publisher_owns_outputs(ctx)
    tools = _build_subagent_tools([], include_artifact_writes=True)
    assert 'save_artifacts' in _tool_names(tools)
    plan = _build_subagent_plan(
        ctx,
        None,
        tools=tools,
        tool_prompt_appendices={},
    )
    prompt = f'{plan.prompt.system_prompt}\n{plan.prompt.current_input}'
    assert '## Exact save_artifacts call shape' in prompt
