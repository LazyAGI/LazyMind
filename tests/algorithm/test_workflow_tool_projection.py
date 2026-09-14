from lazymind.workflow_toolkit import AgentWorkflowToolProjection


def _named(name):
    def tool():
        return None
    tool.__name__ = name
    return tool


def test_projection_is_host_neutral_and_filters_model_unsafe_lifecycle_tools():
    tools = [_named(name) for name in (
        'trigger_demo_workflow', 'prepare_workflow', 'start_workflow',
        'stop_workflow', 'resume_workflow', 'get_workflow_state', 'advance_step',
    )]

    available = AgentWorkflowToolProjection().expose(tools)
    assert {tool.__name__ for tool in available} == {
        'trigger_demo_workflow',
        'get_workflow_state', 'advance_step',
    }

    stopped = AgentWorkflowToolProjection('session-1', 'stopped').expose(tools)
    assert {tool.__name__ for tool in stopped} == {
        'resume_workflow', 'get_workflow_state', 'advance_step',
    }
