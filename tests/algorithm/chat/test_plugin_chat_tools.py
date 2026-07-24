from lazymind.chat.engine.tools import plugin_chat_tools


def test_create_plugin_draft_emits_flat_agent_event(monkeypatch) -> None:
    events = []

    def post_core_api(path, _payload):
        if path == '/plugin-drafts':
            return {'response': {'data': {'id': 'draft-1'}}}
        return {'response': {}}

    monkeypatch.setattr(plugin_chat_tools, 'post_core_api', post_core_api)
    monkeypatch.setattr(
        plugin_chat_tools,
        '_write_agent_data',
        lambda tag, **payload: events.append((tag, payload)),
    )

    result = plugin_chat_tools.create_plugin_draft('Review', 'Review documents')

    assert result['success'] is True
    assert events == [(
        'plugin_draft_created',
        {
            'draft_id': 'draft-1',
            'name': 'Review',
            'editor_url': '/plugin/draft-1',
            'status': 'generating',
        },
    )]
