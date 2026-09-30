"""A package's name must not select platform behavior."""
from types import SimpleNamespace
import pytest

from lazymind.chat.workflow.execution_policy import policy_for
from lazymind.chat.host_extensions.registry import apply_host_extensions
from lazymind.chat.workflow.workflow_manager import _trigger_input_types

@pytest.mark.parametrize('identity', ['product_solution_delivery', 'renamed-report', 'writer-workflow'])
def test_budgets_and_host_extensions_depend_on_declarations(identity):
    runtime = {'execution_limits': {'renamed-step': {'rounds': 4, 'timeout': 90, 'tool_calls': {'save': 2}}},
               'host_extensions': ['installed-extension']}
    policy = policy_for({'workflow_id': identity, 'step_id': 'renamed-step', 'workflow_runtime': runtime})
    assert (policy.rounds, policy.timeout, policy.calls) == (4, 90, {'save': 2})
    assert policy_for({'workflow_id': identity, 'step_id': 'renamed-step'}) is None
    contribution = SimpleNamespace(runtime_policy=runtime, tools=[], stop_tools=[])
    apply_host_extensions(contribution, {'workflow_id': identity}, {},
                          providers={'installed-extension': lambda *_: (['domain-action'], ['domain-action'])})
    assert contribution.tools == contribution.stop_tools == ['domain-action']


def test_unknown_extension_is_not_silently_ignored():
    with pytest.raises(ValueError, match='not installed'):
        apply_host_extensions(SimpleNamespace(runtime_policy={'host_extensions': ['missing']}, tools=[], stop_tools=[]), {}, {}, providers={})


@pytest.mark.parametrize('identity', ['product_solution_delivery', 'copy'])
def test_input_visibility_uses_pinned_allowlist(identity):
    package = {'workflow_id': identity, 'runtime': {'trigger_inputs': ['goal']},
               'compiled_graph': {'material_types': {'goal': 'text', 'seed': 'json'},
                                  'material_producers': {'goal': {'kind': 'external'}, 'seed': {'kind': 'external'}}}}
    assert _trigger_input_types(package) == {'goal': 'text'}
    package['runtime']['trigger_inputs'] = []
    assert _trigger_input_types(package) == {}
    del package['runtime']['trigger_inputs']
    assert _trigger_input_types(package) == {'goal': 'text', 'seed': 'json'}


def test_verified_successor_updates_existing_turn_tools_without_domain_redirect(monkeypatch):
    import lazyllm
    from unittest.mock import MagicMock
    from lazymind.chat.workflow import workflow_manager as manager
    from lazymind.workflow_sdk import WorkflowClientError
    client = MagicMock()
    client.get_state.side_effect = lambda sid: {'session_id': sid, 'status': 'active', 'state_version': 1}
    monkeypatch.setattr(manager, '_client', lambda: client)
    previous = lazyllm.globals.get('agentic_config')
    lazyllm.globals['agentic_config'] = {'enable_workflow': True}
    try:
        contribution = manager.resolve_workflow_injection(
            {'session_id': 'source', 'workflow_id': 'copied-package', 'revision_id': 'pinned'}, conversation_id='conversation')
        state_tool = next(tool for tool in contribution.tools if tool.__name__ == 'get_workflow_state')
        result = {'source_session_id': 'source', 'session_id': 'successor', 'workflow_id': 'copied-package',
                  'workflow_revision_id': 'pinned', 'conversation_id': 'conversation'}
        with pytest.raises(WorkflowClientError):
            contribution.bind_successor({**result, 'workflow_revision_id': 'unexpected-upgrade'})
        state_tool()
        assert client.get_state.call_args.args == ('source',)
        contribution.bind_successor(result)
        state_tool()
        assert client.get_state.call_args.args == ('successor',)
        assert lazyllm.globals['agentic_config']['workflow_session_id'] == 'successor'
        with pytest.raises(WorkflowClientError):
            contribution.bind_successor({**result, 'session_id': 'unrelated'})
    finally:
        lazyllm.globals['agentic_config'] = previous
