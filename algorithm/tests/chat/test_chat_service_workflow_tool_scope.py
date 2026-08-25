from lazymind.chat.service.chat_service import (
    _normalize_document_filter,
    _should_register_subagent_tools,
    _workflow_startup_clarification_available,
    _workflow_collects_knowledge_internally,
    _workflow_turn_is_bound,
)


def test_public_document_filter_is_translated_to_rag_metadata_key():
    filters = {'kb_id': ['kb-1'], 'doc_id': ['doc-1']}

    _normalize_document_filter(filters)

    assert filters == {'kb_id': ['kb-1'], 'docid': 'doc-1'}


def test_selected_ppt_workflow_owns_knowledge_collection():
    assert _workflow_collects_knowledge_internally(
        None, ['builtin:deck-workflow'], [{
            'workflow_ref': 'builtin:deck-workflow',
            'runtime': {'collects_knowledge': True},
        }],
    )


def test_active_ppt_workflow_owns_knowledge_collection():
    assert _workflow_collects_knowledge_internally(
        {'workflow_id': 'deck-workflow', 'runtime': {'collects_knowledge': True}}, [],
    )


def test_unrelated_workflow_keeps_global_knowledge_tools():
    assert not _workflow_collects_knowledge_internally(
        {'workflow_ref': 'builtin:image-workflow'},
        ['builtin:image-workflow'],
    )


def test_completed_workflow_session_owns_mutation_tools():
    context = {'session_id': 'session-1', 'workflow_ref': 'builtin:ppt-workflow'}

    assert _workflow_turn_is_bound(context, [])
    assert not _should_register_subagent_tools(True, [], context)


def test_explicit_workflow_selection_owns_mutation_tools_before_session_exists():
    refs = ['builtin:ppt-workflow']

    assert _workflow_turn_is_bound(None, refs)
    assert not _should_register_subagent_tools(True, refs, None)


def test_plain_chat_keeps_generic_subagent_tools():
    assert not _workflow_turn_is_bound(None, [])
    assert _should_register_subagent_tools(True, [], None)


def test_selected_workflow_can_clarify_before_session_creation():
    runtime = {'clarification_fields': [{
        'id': 'topic', 'question': 'PPT 主题是什么？', 'type': 'text',
    }]}

    assert _workflow_startup_clarification_available(runtime, None)
    assert not _workflow_startup_clarification_available(
        runtime, {'session_id': 'session-1'},
    )


def test_discovery_mode_exposes_ask_for_declaratively_interactive_workflow():
    assert _workflow_startup_clarification_available(
        None,
        None,
        [{'runtime': {'clarification_fields': [{
            'id': 'topic', 'question': 'PPT 主题是什么？',
        }]}}],
        discovery_mode=True,
    )


def test_bound_unrelated_workflow_does_not_inherit_catalog_clarification():
    assert not _workflow_startup_clarification_available(
        None,
        None,
        [{'runtime': {'clarification_fields': [{
            'id': 'topic', 'question': 'PPT 主题是什么？',
        }]}}],
        discovery_mode=False,
    )
