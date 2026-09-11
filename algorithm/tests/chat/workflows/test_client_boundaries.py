from pathlib import Path


def test_chat_never_queries_workflow_runtime_tables():
    chat_root = Path(__file__).parents[3] / 'lazymind' / 'chat'
    violations = []
    table_tokens = ('workflow_sessions', 'workflow_session_steps', 'workflow_slot_revisions',
                    'workflow_attempt_input_bindings', 'workflow_input_bindings')
    for path in chat_root.rglob('*.py'):
        text = path.read_text()
        if any(token in text for token in table_tokens) and any(
                sql in text.upper() for sql in ('SELECT ', 'INSERT ', 'UPDATE ', 'DELETE ')):
            violations.append(str(path.relative_to(chat_root)))
    assert violations == []


def test_workflow_http_payloads_live_in_public_client_adapter_only():
    chat_root = Path(__file__).parents[3] / 'lazymind' / 'chat'
    violations = []
    for path in chat_root.rglob('*.py'):
        if path.name in {'client.py', 'file_adapter.py'}:
            continue
        text = path.read_text()
        if 'httpx.' in text and '/workflow-' in text:
            violations.append(str(path.relative_to(chat_root)))
    assert violations == []


def test_chat_has_no_private_workflow_runtime_modules_or_imports():
    chat_root = Path(__file__).parents[3] / 'lazymind' / 'chat'
    removed = {
        'workflow_loader.py', 'persistence_compat.py', 'decision_policy.py',
        'driver_agent.py', 'compat.py',
    }
    assert not any((chat_root / 'workflow' / name).exists() for name in removed)
    forbidden = ('workflow_loader', 'persistence_compat', 'decision_policy', 'driver_agent')
    violations = [
        str(path.relative_to(chat_root)) for path in chat_root.rglob('*.py')
        if any(token in path.read_text() for token in forbidden)
    ]
    assert violations == []


def test_workflow_manager_is_a_public_sdk_and_handoff_adapter():
    manager = Path(__file__).parents[3] / 'lazymind' / 'chat' / 'workflow' / 'workflow_manager.py'
    text = manager.read_text()
    assert 'from lazymind.workflow_sdk import' in text
    assert 'HostWorkflowToolkit(' in text
    assert 'origin_ref=conversation_id' in text
    assert "['advance_step_and_hand_off']" in text
    assert 'WorkflowSession' not in text
    assert 'WorkflowSlotRevision' not in text


def test_chat_contains_no_parallel_workflow_operating_prompt():
    chat_root = Path(__file__).parents[3] / 'lazymind' / 'chat'
    manager = (chat_root / 'workflow/workflow_manager.py').read_text()
    service = (chat_root / 'service/chat_service.py').read_text()
    assert 'Available public Workflows' not in manager
    assert 'prepare and start' not in manager
    assert 'chat_workflow_policy' not in service
    assert 'workflow.scenario' not in service


def test_frontend_refreshes_panel_only_for_runtime_state_invalidation():
    repository = Path(__file__).parents[4]
    task_center = (repository / 'frontend/src/modules/chat/store/taskCenter.ts').read_text()
    workflow_stream = (repository / 'frontend/src/modules/chat/utils/workflowEventStream.ts').read_text()
    assert "type === 'workflow_runtime_updated'" in task_center
    assert 'get().loadConversationTasks(conversationId)' in task_center
    assert 'PLUGIN_GRAPH_REFRESH_EVENT' in task_center
    assert "'artifact.upsert'" in workflow_stream
    assert "'attempt.progress'" in workflow_stream
