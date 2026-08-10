from lazymind.chat.engine.subagent.db import MemorySubAgentStore


def test_memory_store_restores_steps_and_allocates_sequences():
    store = MemorySubAgentStore(
        {'id': 'task-1', 'objective': 'continue'},
        [{'seq': 1, 'role': 'text', 'content': {'content': 'old'}}],
    )
    assert store.load_task('task-1')['objective'] == 'continue'
    assert store.load_task('other') is None
    assert store.max_step_seq('task-1') == 1
    store.append_step('task-1', 2, 'tool', {'tool_results': []})
    assert [step['seq'] for step in store.load_steps('task-1')] == [1, 2]
    assert store.next_artifact_seq('task-1', 'report') == 1
    assert store.load_artifacts('task-1') == []
