from lazymind.chat.service.component import normalize_history_for_agent


def test_normalize_history_restores_plain_assistant_text_without_name_errors():
    history = [
        {'role': 'user', 'content': 'nihao'},
        {'role': 'assistant', 'content': '\n\n你好！我吃牛肉。有什么我可以帮你的吗？'},
    ]

    normalized = normalize_history_for_agent(history)

    assert normalized == [
        {'role': 'user', 'content': 'nihao'},
        {'role': 'assistant', 'content': '你好！我吃牛肉。有什么我可以帮你的吗？'},
    ]


def test_normalize_history_strips_source_links_from_assistant_text():
    history = [
        {
            'role': 'assistant',
            'content': '答案见 [1](#source-1.2 "doc") 和 [2](#source-2.3)，另见 [官网](https://example.com)。',
        },
    ]

    normalized = normalize_history_for_agent(history)

    assert normalized == [
        {'role': 'assistant', 'content': '答案见 和，另见 [官网](https://example.com)。'},
    ]


def test_normalize_history_strips_bracket_refs_from_assistant_text():
    history = [
        {'role': 'assistant', 'content': '答案来自 [[4.2]]。'},
    ]

    normalized = normalize_history_for_agent(history)

    assert normalized == [
        {'role': 'assistant', 'content': '答案来自。'},
    ]


def test_normalize_history_keeps_kb_tool_calls_and_sanitizes_results():
    history = [{
        'role': 'assistant',
        'content': (
            '<tool_call>{"id":"call-1","name":"kb_search","arguments":{"query":"q"}}</tool_call>'
            '<tool_result>{"id":"call-1","name":"kb_search","result":{"items":[{"text":"old [[9.1]]","ref":"[[9.1]]","citation_index":"9.1"}]}}</tool_result>'
            '最终答案 [9](#source-9.1 "old.pdf")。'
        ),
    }]

    normalized = normalize_history_for_agent(history)

    assert normalized == [
        {
            'role': 'assistant',
            'content': '',
            'reasoning_content': '',
            'tool_calls': [{
                'id': 'call-1',
                'type': 'function',
                'function': {
                    'name': 'kb_search',
                    'arguments': '{"query": "q"}',
                },
            }],
        },
        {
            'role': 'tool',
            'tool_call_id': 'call-1',
            'name': 'kb_search',
            'content': '{"items":[{"text":"old "}]}',
        },
        {'role': 'assistant', 'content': '最终答案。', 'reasoning_content': ''},
    ]
