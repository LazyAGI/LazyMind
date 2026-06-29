from lazymind.chat.service.component import AgentEventFrameTranslator
from lazymind.chat.service.component.tool_rendering import (
    _tool_call_frame_text,
    _tool_result_frame_text,
)
from lazymind.chat.service.utils.citations import (
    CITATION_REFS_KEY,
    annotate_citations,
)


def test_translator_rewrites_citations_registered_by_tools():
    translator = AgentEventFrameTranslator(query='q')
    item = {
        'uid': 'node-1',
        'text': 'source text',
        'docid': 'doc-1',
        'kb_id': 'kb-1',
        'group': 'block',
        'number': 3,
        'metadata': {'file_name': 'doc.md'},
        'global_metadata': {'docid': 'doc-1', 'kb_id': 'kb-1', 'file_name': 'doc.md'},
    }
    annotate_citations(item, translator.citation_state)

    translator.feed({
        'tag': 'tool_results',
        'tool_results': [{
            'id': 'call-1',
            'name': 'kb_search',
            'result': {
                'success': True,
                'tool': 'kb_search',
                'result': {
                    'total': 1,
                    'items': [item],
                },
            },
        }],
    })

    assert item['citation_index'] == '1.1'
    assert item['ref'] == '[[1.1]]'
    assert translator.citation_state[CITATION_REFS_KEY]['1.1']['content'] == 'source text'

    frames = translator.feed({'tag': 'text', 'delta': 'Use [[1.1]].'})
    assert ''.join(frame['text'] for frame in frames) == 'Use [1](#source-1.1 "doc.md").'

    final_frames = translator.finish('')
    assert final_frames[-1]['sources'][0]['index'] == '1.1'


def test_final_answer_citation_display_starts_from_first_cited_document():
    translator = AgentEventFrameTranslator(query='q')
    for idx in range(1, 4):
        annotate_citations({
            'uid': f'node-{idx}',
            'text': f'source text {idx}',
            'docid': f'doc-{idx}',
            'kb_id': 'kb-1',
            'group': 'block',
            'number': idx,
            'metadata': {'file_name': f'doc-{idx}.md'},
            'global_metadata': {
                'docid': f'doc-{idx}',
                'kb_id': 'kb-1',
                'file_name': f'doc-{idx}.md',
            },
        }, translator.citation_state)

    frames = translator.finish('Use [[3.1]] and [3](#source-3.1 "doc-3.md").')
    text = ''.join(frame.get('text') or '' for frame in frames)
    sources = frames[-1]['sources']

    assert '[1](#source-3.1 "doc-3.md")' in text
    assert '[3](#source-3.1 "doc-3.md")' not in text
    assert sources[0]['index'] == '3.1'
    assert sources[0]['display_index'] == 1


def test_translator_counts_tool_call_turns_not_individual_calls():
    translator = AgentEventFrameTranslator(query='q')

    translator.feed({'tag': 'tool_calls', 'tool_calls': []})
    assert translator.tool_call_turns == 0

    translator.feed({
        'tag': 'tool_calls',
        'tool_calls': [
            {'id': 'call-1', 'function': {'name': 'kb_search', 'arguments': {'query': 'q'}}},
            {'id': 'call-2', 'function': {'name': 'calculator', 'arguments': {'exp': '1+1'}}},
        ],
    })
    assert translator.tool_call_turns == 1

    translator.feed({
        'tag': 'tool_calls',
        'tool_calls': [
            {'id': 'call-3', 'function': {'name': 'web_search', 'arguments': {'query': 'q'}}},
        ],
    })
    assert translator.tool_call_turns == 2


def test_searchbase_tool_rendering_extracts_provider_brand():
    text, preview_value = _tool_call_frame_text({
        'id': 'call-tavily',
        'function': {
            'name': 'TavilySearch_search',
            'arguments': {'query': 'agent news', 'max_results': 5},
        },
    })

    assert preview_value == 'agent news'
    assert 'Searching **Tavily** for **agent news**.' in text
    assert '"name":"TavilySearch_search"' in text

    result_text = _tool_result_frame_text({
        'id': 'call-tavily',
        'name': 'TavilySearch_search',
        'result': [{'title': 'Agent news item', 'url': 'https://example.test'}],
    }, preview_value=preview_value)

    assert '**Tavily** search results for **agent news** are ready now.' in result_text
    assert '"name":"TavilySearch_search"' in result_text


def test_searchbase_tool_rendering_handles_multiword_and_special_brands():
    google_books_text, _ = _tool_call_frame_text({
        'id': 'call-books',
        'function': {
            'name': 'GoogleBooksSearch_search',
            'arguments': {'query': 'database internals'},
        },
    })
    semantic_text, _ = _tool_call_frame_text({
        'id': 'call-semantic',
        'function': {
            'name': 'SemanticScholarSearch_search',
            'arguments': {'query': 'retrieval augmented generation'},
        },
    })
    arxiv_text, _ = _tool_call_frame_text({
        'id': 'call-arxiv',
        'function': {
            'name': 'ArxivSearch_search',
            'arguments': {'query': 'tool use agents'},
        },
    })

    assert 'Searching **Google Books** for **database internals**.' in google_books_text
    assert 'Searching **Semantic Scholar** for **retrieval augmented generation**.' in semantic_text
    assert 'Searching **Arxiv** for **tool use agents**.' in arxiv_text


def test_searchbase_tool_rendering_supports_zh_and_content_methods():
    call_text, preview_value = _tool_call_frame_text({
        'id': 'call-content',
        'function': {
            'name': 'SciverseSearch_get_content',
            'arguments': {'item': {'title': '论文标题', 'url': 'https://example.test/paper'}},
        },
    }, language='zh')

    assert preview_value == '论文标题/https://example.test/paper'
    assert '正在读取 **Sciverse** 搜索结果 **论文标题/https://example.test/paper**。' in call_text

    result_text = _tool_result_frame_text({
        'id': 'call-content',
        'name': 'SciverseSearch_get_content',
        'result': {'text': '论文正文'},
    }, language='zh', preview_value=preview_value)

    assert '已成功读取 **Sciverse** 搜索结果 **论文标题/https://example.test/paper** 的内容。' in result_text
