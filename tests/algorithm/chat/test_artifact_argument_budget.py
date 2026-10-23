import pytest

from lazyllm.tools.agent import ToolExecutionError, ToolManager
from lazymind.chat.engine.agent_runtime.tool_call_guard import (
    ToolArgumentRetryExhausted, ToolExecutionMiddleware, FailureRetryPolicy,
)
from lazymind.chat.engine.tools.workspace_context import WorkspaceContext


def publish(entries: list[dict]):
    """Publish typed entries."""
    if not entries or any('paper_id' not in entry for entry in entries):
        raise ToolExecutionError('ARTIFACT_ARGUMENT_INVALID: entries must cover the selected papers')
    return {'saved': len(entries)}


def _middleware():
    return ToolExecutionMiddleware(
        ToolManager([publish]), argument_limits={'publish': 3}, failure_policy=FailureRetryPolicy({'publish': 2}),
        workspace_permission=WorkspaceContext(local_runtime=False))


def _call(value):
    return {'id': 'call', 'function': {'name': 'publish', 'arguments': value}}


@pytest.mark.parametrize('invalid', ['{"entries":', {'entries': 'not-an-array'}, {'entries': []}])
def test_three_invalid_submissions_return_feedback_and_block_further_execution(invalid):
    middleware = _middleware()
    for i in range(3):
        batch = middleware.execute_with_records(_call(invalid))
        assert batch.results[0]['ok'] is False
        assert bool(middleware.argument_failure) == (i == 2)
    with pytest.raises(ToolArgumentRetryExhausted, match='TOOL_ARGUMENT_RETRY_EXHAUSTED'):
        middleware.execute_with_records(_call({'entries': [{'paper_id': 'a'}]}))


def test_success_resets_only_that_tools_consecutive_argument_failures():
    middleware = _middleware()
    for _ in range(2):
        middleware.execute_with_records(_call({'entries': 'bad'}))
    assert middleware.execute_with_records(_call({'entries': [{'paper_id': 'a'}]})).results[0]['ok'] is True
    for _ in range(2):
        middleware.execute_with_records(_call({'entries': 'bad'}))
    assert middleware.argument_failure == ''


def test_pre_execution_configuration_failure_does_not_consume_argument_repairs(monkeypatch):
    middleware = _middleware()

    def unavailable(*args, **kwargs):
        raise ToolExecutionError('Service configuration is missing')

    monkeypatch.setattr(middleware._manager, 'prepare_capabilities', unavailable)
    for _ in range(3):
        middleware.execute_with_records(_call({'entries': [{'paper_id': 'a'}]}))
    assert middleware.argument_failure == ''


def test_same_batch_cannot_execute_a_fourth_call_after_three_invalid_submissions():
    middleware = _middleware()
    calls = [_call({'entries': [{'attempt': i}]}) for i in range(3)]
    calls.append(_call({'entries': [{'paper_id': 'would-save'}]}))
    batch = middleware.execute_with_records(calls)
    assert all(result['ok'] is False for result in batch.results)
    assert 'TOOL_ARGUMENT_RETRY_EXHAUSTED' in batch.results[-1]['value']


def test_recreated_agent_restores_repairs_even_when_model_history_is_corrupt():
    import json
    first = _middleware()
    history = []
    for _ in range(2):
        result = first.execute_with_records(_call('{"entries":')).results[0]
        history.append({'name': 'publish', 'result': json.dumps(result)})
    resumed = _middleware()
    resumed.restore_argument_results(history)
    resumed.execute_with_records(_call({'entries': []}))
    with pytest.raises(ToolArgumentRetryExhausted):
        resumed.execute_with_records(_call({'entries': [{'paper_id': 'a'}]}))


def test_actual_agent_stops_before_a_fourth_model_request(tmp_path):
    import uuid
    import lazyllm
    from lazymind.chat.engine.agent_runtime import (
        AgentExecutor, AgentRunPlan, AgentRole, AgentExecutionOptions, PromptBuilder,
    )

    class Model:
        def __init__(self):
            self._module_id = uuid.uuid4().hex
            self.requests = 0

        def share(self, **kwargs):
            return self

        def used_by(self, _):
            return self

        def __call__(self, *args, **kwargs):
            self.requests += 1
            return {'content': '', 'tool_calls': [{
                'id': str(self.requests), 'type': 'function',
                'function': {'name': 'publish', 'arguments': '{"entries":[]}'}}]}

    model = Model()
    with lazyllm.locals._scope():
        plan = AgentRunPlan(
            role=AgentRole.SUBAGENT,
            prompt=PromptBuilder.for_role(AgentRole.SUBAGENT).input('publish', source='user').build(),
            tools=[publish], stop_tools=['publish'], execution_options=AgentExecutionOptions(
                workspace=str(tmp_path), max_retries=20,
                workspace_permission=WorkspaceContext(local_runtime=False)))
        agent = AgentExecutor().create_agent(model, plan)
        with pytest.raises(Exception, match='TOOL_ARGUMENT_RETRY_EXHAUSTED'):
            agent('Publish the selected papers')
    assert model.requests == 3
