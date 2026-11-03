import pytest

from lazyllm.tools.agent import ToolManager, ToolExecutionError, fc_register
from lazymind.chat.engine.agent_runtime.tool_call_guard import ToolCallQuota, ToolExecutionMiddleware
from lazymind.chat.engine.subagent.runner import WORKFLOW_TOOL_CALL_LIMITS
from lazymind.chat.engine.tools.workspace_context import WorkspaceContext


@pytest.mark.parametrize('fails', [False, True])
def test_background_generation_executes_once_per_step_even_with_changed_arguments(fails):
    calls = []

    @fc_register(host_file='NONE')
    def ppt_generate_background_images(prompt: str):
        '''Generate test backgrounds.

        Args:
            prompt: Approved background prompt.
        '''
        calls.append(prompt)
        if fails:
            raise ToolExecutionError('provider HTTP 502')
        return {'local_path': '/uploads/background.png'}

    manager = ToolManager([ppt_generate_background_images])

    def step():
        return ToolExecutionMiddleware(
            manager, workspace_permission=WorkspaceContext(local_runtime=False),
            call_quota=ToolCallQuota(WORKFLOW_TOOL_CALL_LIMITS),
        )

    middleware = step()
    for number in range(3):
        result = middleware.execute_with_records({
            'id': str(number),
            'function': {'name': 'ppt_generate_background_images', 'arguments': {'prompt': str(number)}},
        })
        if number:
            assert result.results[0]['ok'] is False
    assert calls == ['0']
    step().execute_with_records({
        'id': 'user-retry',
        'function': {'name': 'ppt_generate_background_images', 'arguments': {'prompt': '0'}},
    })
    assert calls == ['0', '0']
