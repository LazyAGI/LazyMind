import json

from lazymind.chat.engine.subagent.context import SubAgentContext


def _context(workspace_path: str) -> SubAgentContext:
    return SubAgentContext(
        task_id='task-1',
        conversation_id='conversation-1',
        agent_type='test',
        objective='test drafts',
        params={},
        workspace_path=workspace_path,
        input_slots=[],
        output_slots=['result'],
        db=None,  # type: ignore[arg-type]
        emit=lambda _event: None,
    )


def test_only_dirty_drafts_are_pending(tmp_path):
    ctx = _context(str(tmp_path))

    ctx.write_draft('result', 'text', 'saved', pending_commit=False)
    assert ctx.list_pending_drafts() == []

    ctx.write_draft('result', 'text', 'patched')
    assert ctx.list_pending_drafts() == [('result', None, 'text', 'patched')]


def test_legacy_draft_metadata_defaults_to_pending(tmp_path):
    ctx = _context(str(tmp_path))
    ctx.write_draft('result', 'json', '{"ok": true}', pending_commit=False)
    with open(ctx._meta_path('result'), 'w', encoding='utf-8') as fh:
        json.dump({'original_type': 'json'}, fh)

    assert ctx.list_pending_drafts() == [('result', None, 'json', '{"ok": true}')]


def test_key_ending_in_number_is_not_mistaken_for_list_index(tmp_path):
    ctx = _context(str(tmp_path))

    ctx.write_draft('section_1', 'text', 'patched')

    assert ctx.list_pending_drafts() == [('section_1', None, 'text', 'patched')]


def test_draft_key_rejects_path_components(tmp_path):
    ctx = _context(str(tmp_path))

    for key in ('../escape', 'dir/file', r'dir\file'):
        try:
            ctx.write_draft(key, 'text', 'bad')
        except ValueError:
            pass
        else:
            raise AssertionError(f'unsafe draft key accepted: {key}')


def test_artifact_sequence_continues_from_persisted_revisions(tmp_path):
    ctx = _context(str(tmp_path))

    class FakeDB:
        @staticmethod
        def next_artifact_seq(_task_id, _key):
            return 4

    ctx.db = FakeDB()  # type: ignore[assignment]
    assert ctx.next_artifact_seq('result') == 4
    assert ctx.next_artifact_seq('result') == 5
