from lazymind.chat.service.component import normalize_history_for_agent
from lazymind.chat.service.utils.citations import annotate_citations


def test_normalize_history_restores_plain_assistant_text_without_name_errors():
    history = [
        {'role': 'user', 'content': 'nihao'},
        {'role': 'assistant', 'content': '\n\n你好！我吃牛肉。有什么我可以帮你的吗？'},
    ]

    normalized, _ = normalize_history_for_agent(history)

    assert normalized == [
        {'role': 'user', 'content': 'nihao'},
        {'role': 'assistant', 'content': '你好！我吃牛肉。有什么我可以帮你的吗？'},
    ]


def test_normalize_history_restores_source_links_to_bracket_refs():
    history = [
        {'role': 'assistant', 'content': '答案见 [1](#source-1.2 "doc") 和 [2](#source-2.3)。'},
    ]

    normalized, _ = normalize_history_for_agent(history)

    assert normalized == [
        {'role': 'assistant', 'content': '答案见 [[1.2]] 和 [[2.3]]。'},
    ]


def test_normalize_history_restores_next_document_index_from_source_links():
    config = {}
    history = [
        {'role': 'assistant', 'content': '答案见 [1](#source-1.2 "doc-a")。'},
    ]

    _, config = normalize_history_for_agent(history)

    item = {
        'text': 'new chunk',
        'docid': 'doc-2',
        'kb_id': 'kb-1',
        'uid': 'u-2',
        'group': 'block',
        'number': 1,
        'metadata': {},
        'global_metadata': {'docid': 'doc-2', 'kb_id': 'kb-1', 'file_name': 'doc-b.pdf'},
    }
    annotate_citations(item, config)

    assert item['citation_index'] == '2.1'
