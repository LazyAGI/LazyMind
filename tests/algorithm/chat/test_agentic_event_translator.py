from lazymind.chat.service.component import AgentEventFrameTranslator
from lazymind.chat.service.component.tool_rendering import (
    _tool_call_frame_text,
    _tool_result_status,
    _tool_result_frame_text,
)
from lazymind.chat.service.utils.citations import (
    CITATION_REFS_KEY,
    annotate_citations,
    register_external_search_result,
)


def test_business_failed_status_is_not_a_tool_execution_failure():
    assert _tool_result_status({'status': 'failed', 'message': 'Task did not finish.'}) == 'ok'


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
                'total': 1,
                'items': [item],
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


def test_translator_merges_searched_and_cited_sources_with_roles():
    translator = AgentEventFrameTranslator(query='q')
    first = register_external_search_result({
        'title': 'First',
        'url': 'https://example.test/first',
    }, translator.citation_state)
    register_external_search_result({
        'title': 'Second',
        'url': 'https://example.test/second',
    }, translator.citation_state)

    frames = translator.finish({
        'text': f'Use {first["ref"]}.',
        'sources': [{
            'index': '9.1',
            'source_type': 'external',
            'title': 'Unused existing source',
            'url': 'https://example.test/unused',
        }],
    })
    assert [(source['title'], source['source_roles']) for source in frames[-1]['sources']] == [
        ('First', ['cited', 'searched']),
        ('Second', ['searched']),
    ]
    assert 'searched_sources' not in frames[-1]


def test_final_sources_preserve_distinct_citation_indices_for_same_url():
    translator = AgentEventFrameTranslator(query='q')
    register_external_search_result({
        'title': 'Search result',
        'url': 'https://example.test/shared#search',
    }, translator.citation_state)

    frames = translator.finish({
        'text': 'Use [[9.1]].',
        'sources': [{
            'index': '9.1',
            'source_type': 'external',
            'title': 'Existing source',
            'url': 'https://example.test/shared',
            'content': 'Existing evidence',
        }],
    })

    assert [(source['index'], source['source_roles']) for source in frames[-1]['sources']] == [
        ('9.1', ['cited']),
        ('1.1', ['searched']),
    ]


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


def test_translator_forwards_tool_limit_pending_as_structured_frame():
    translator = AgentEventFrameTranslator(query='q')
    pending = {
        'decision_id': 'decision-1',
        'used_rounds': 21,
        'round_limit': 21,
        'expanded_max_rounds': 200,
        'timeout_seconds': 120,
    }

    frames = translator.feed({'tag': 'tool_limit_pending', **pending})

    assert frames == [{
        'think': None,
        'text': None,
        'sources': [],
        'tool_limit_pending': pending,
    }]


def test_translator_renders_every_parallel_tool_call_and_result():
    translator = AgentEventFrameTranslator(query='批量读取这些网页')
    calls = [
        {
            'id': f'call-{index}',
            'function': {
                'name': 'url_fetch',
                'arguments': {'url': f'https://example.test/{index}'},
            },
        }
        for index in range(5)
    ]

    call_text = ''.join(
        frame.get('text') or ''
        for frame in translator.feed({'tag': 'tool_calls', 'tool_calls': calls})
    )
    assert call_text.count('<tp id=') == 5
    assert call_text.count('<tool_call>') == 5
    for index in range(5):
        assert f'https://example.test/{index}' in call_text

    results = [
        {
            'id': f'call-{index}',
            'name': 'url_fetch',
            'result': {'final_url': f'https://example.test/{index}'},
        }
        for index in range(5)
    ]
    result_text = ''.join(
        frame.get('text') or ''
        for frame in translator.feed({'tag': 'tool_results', 'tool_results': results})
    )
    assert result_text.count('<trp id=') == 5
    assert result_text.count('<tool_result>') == 5


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


def test_skill_reference_rendering_does_not_treat_content_error_words_as_failure():
    result_text = _tool_result_frame_text({
        'id': 'call-reference',
        'name': 'read_reference',
        'result': 'Error handling for failed PDF operations is documented here.',
    }, language='zh', preview_value='reference.md')

    assert '已成功加载 **reference.md** 技能的参考资料。' in result_text


def test_tool_rendering_preserves_explicit_approval_signal():
    result_text = _tool_result_frame_text({
        'id': 'call-reference',
        'name': 'read_reference',
        'result': {
            'ok': False,
            'message': 'Reading reference.md requires approval.',
            'needs_approval': True,
        },
    }, language='zh', preview_value='reference.md')

    assert '此操作需要确认后才能继续。' in result_text


def test_skill_reference_rendering_preserves_explicit_tool_failure():
    result_text = _tool_result_frame_text({
        'id': 'call-reference',
        'name': 'read_reference',
        'result': {
            'ok': False,
            'message': 'reference.md not found',
        },
    }, language='zh', preview_value='reference.md')

    assert '未能读取 **reference.md** 技能参考资料。' in result_text


def test_workflow_rendering_normalizes_canonical_success_and_failure():
    success_text = _tool_result_frame_text({
        'id': 'call-workflow-success',
        'name': 'trigger_writer_workflow',
        'result': {
            'ok': True,
            'value': {'outcome': 'queued', 'reason': 'accepted'},
        },
    })
    failure_text = _tool_result_frame_text({
        'id': 'call-workflow-failure',
        'name': 'trigger_writer_workflow',
        'result': {
            'ok': False,
            'message': 'Workflow service is unavailable',
        },
    })

    assert 'Result: **queued**. Reason: **accepted**.' in success_text
    assert 'Result: **failed**. Reason: **Workflow service is unavailable**.' in failure_text
    assert '{result.' not in success_text
    assert '{result.' not in failure_text


def test_create_skill_rendering_uses_single_segment_name_and_preserves_failure():
    call_text, preview_value = _tool_call_frame_text({
        'id': 'call-create-skill',
        'function': {
            'name': 'SkillManagementToolkit_create_skill',
            'arguments': {
                'name': 'skill',
                'content': '---\nname: skill\ndescription: Test skill.\n---\nUse it.',
            },
        },
    }, language='zh')

    assert preview_value == 'skill'
    assert '正在创建 **skill** 技能。' in call_text

    result_text = _tool_result_frame_text({
        'id': 'call-create-skill',
        'name': 'SkillManagementToolkit_create_skill',
        'result': {
            'ok': False,
            'message': "Skill name 'internal2/skill' is invalid.",
        },
    }, language='zh', preview_value='internal2/skill')

    assert '未能创建 **internal2/skill** 技能。' in result_text
