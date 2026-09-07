import pytest
from types import SimpleNamespace
import lazyllm
from lazyllm import init_session, locals as lazyllm_locals
from lazyllm.tools.agent import ToolExecutionError
from lazyllm.tools.agent.toolsManager import ToolManager
from lazyllm.tools.tools.search import SearchBase
from lazymind.chat.engine.tools.kb import KBToolkit
from lazymind.chat.engine.tools.infra import CitationResultMiddleware
from lazymind.chat.engine.tools.lazy_kb import KBToolkit as LazyKBToolkit
from lazymind.chat.service.utils.citations import (
    CITATION_REFS_KEY,
    materialize_source_views,
    reset_citation_state,
    rewrite_citations,
)


def test_kb_toolkit_is_available_without_selected_kb():
    lazyllm.globals['agentic_config'] = {'filters': {}}
    toolkit = KBToolkit()
    assert 'list_knowledge_bases' in toolkit.__public_apis__
    with pytest.raises(ToolExecutionError, match='kb_ids is required'):
        toolkit._kb_ids()


def _kb_tool_names(manager):
    return {item['function']['name'] for item in manager.tools_description}


def test_kb_citations_are_added_by_tool_result_middleware():
    state = {}
    reset_citation_state(state)
    lazyllm.globals['agentic_config'] = {'citation_state': state}

    class FakeKnowledgeSearch(LazyKBToolkit):
        def kb_search(self, query: str):
            """Search knowledge-base fixtures."""
            return {'items': [{
                'uid': 'node-1', 'docid': 'doc-1', 'content': query,
            }]}

    manager = CitationResultMiddleware(ToolManager([FakeKnowledgeSearch()]))
    results = manager([
        {'function': {'name': 'FakeKnowledgeSearch_kb_search', 'arguments': {'query': 'knowledge'}}},
    ])

    assert results[0]['value']['items'][0]['ref'] == '[[1.1]]'
    assert len(state[CITATION_REFS_KEY]) == 1
    assert [source['source_roles'] for source in materialize_source_views(state)] == [
        ['searched'],
    ]


def test_mixed_web_and_kb_sources_share_searched_and_cited_role_semantics():
    state = {}
    reset_citation_state(state)
    lazyllm.globals['agentic_config'] = {'citation_state': state}

    class FakeWebSearch(SearchBase):
        __tool_public_apis__ = ['search']

        def __init__(self):
            super().__init__(source_name='fake', skip_auth=True)

        def search(self, query: str):
            """Search web fixtures."""
            return [{
                'title': 'Web result',
                'url': 'https://example.test/web',
                'snippet': query,
                'source': 'fake',
            }]

    class FakeKnowledgeSearch(LazyKBToolkit):
        def kb_search(self, query: str):
            """Search knowledge-base fixtures."""
            return {'items': [{
                'uid': 'node-1',
                'docid': 'doc-1',
                'kb_id': 'kb-1',
                'content': query,
            }]}

    manager = CitationResultMiddleware(ToolManager([FakeWebSearch(), FakeKnowledgeSearch()]))
    results = manager([
        {'function': {'name': 'FakeWebSearch_search', 'arguments': {'query': 'web evidence'}}},
        {'function': {'name': 'FakeKnowledgeSearch_kb_search', 'arguments': {'query': 'kb evidence'}}},
    ])
    web_item = results[0]['value'][0]
    kb_item = results[1]['value']['items'][0]

    assert [source['source_roles'] for source in materialize_source_views(state)] == [
        ['searched'], ['searched'],
    ]

    rewrite_citations(f'{web_item["ref"]} {kb_item["ref"]}', state)

    sources = materialize_source_views(state)
    assert [source['source_type'] for source in sources] == ['external', 'knowledge_base']
    assert [source['source_roles'] for source in sources] == [
        ['cited', 'searched'], ['cited', 'searched'],
    ]


def test_selected_knowledge_base_exposes_concrete_tools_directly():
    init_session()
    lazyllm_locals['_lazyllm_agent'] = {'workspace': {}}
    lazyllm.globals['agentic_config'] = {'filters': {'kb_id': 'selected-kb'}}

    names = _kb_tool_names(ToolManager([KBToolkit()]))

    assert 'KBToolkit_kb_search' in names
    assert 'get_KBToolkit_methods' not in names


@pytest.mark.parametrize('query', [
    '请查询知识库里的发布计划',
    'search the knowledge base for the release plan',
    'search our KNOWLEDGE-BASES',
])
def test_knowledge_base_words_auto_expand_toolkit(query):
    init_session()
    lazyllm_locals['_lazyllm_agent'] = {'workspace': {}}
    lazyllm.globals['agentic_config'] = {'filters': {}}
    manager = ToolManager([KBToolkit()])

    assert _kb_tool_names(manager) == {'get_KBToolkit_methods'}
    manager.sync_active_groups(query)

    assert 'KBToolkit_kb_search' in _kb_tool_names(manager)


def test_knowledge_base_rule_does_not_match_longer_word_fragment():
    init_session()
    lazyllm_locals['_lazyllm_agent'] = {'workspace': {}}
    lazyllm.globals['agentic_config'] = {'filters': {}}
    manager = ToolManager([KBToolkit()])

    manager.sync_active_groups('the knowledge baseline changed')

    assert _kb_tool_names(manager) == {'get_KBToolkit_methods'}


def test_explicit_kb_ids_override_request_selection(monkeypatch):
    calls = []

    def fake_get_core_api(path, params=None):
        calls.append((path, params))
        return {
            'datasets': [
                {'dataset_id': 'explicit-kb'},
                {'dataset_id': 'request-kb'},
            ],
            'next_page_token': '',
        }

    monkeypatch.setattr('lazymind.chat.engine.tools.kb.get_core_api', fake_get_core_api)
    lazyllm.globals['agentic_config'] = {'filters': {'kb_id': 'request-kb'}}
    assert KBToolkit()._kb_ids(['explicit-kb']) == ['explicit-kb']
    assert KBToolkit()._kb_ids() == ['request-kb']
    assert len(calls) == 1


def test_request_selected_kb_ids_skip_catalog_validation(monkeypatch):
    def unexpected_get_core_api(path, params=None):
        raise AssertionError('request-selected knowledge bases should not reload the catalog')

    monkeypatch.setattr('lazymind.chat.engine.tools.kb.get_core_api', unexpected_get_core_api)
    lazyllm.globals['agentic_config'] = {'filters': {'kb_id': 'request-kb'}}

    assert KBToolkit()._kb_ids() == ['request-kb']


def test_kb_ids_load_all_catalog_pages_and_cache_result(monkeypatch):
    calls = []

    def fake_get_core_api(path, params=None):
        calls.append((path, params))
        if not params.get('page_token'):
            return {
                'datasets': [{'dataset_id': 'kb-first'}],
                'next_page_token': 'page-2',
            }
        return {
            'datasets': [{'dataset_id': 'kb-second'}],
            'next_page_token': '',
        }

    monkeypatch.setattr('lazymind.chat.engine.tools.kb.get_core_api', fake_get_core_api)
    lazyllm.globals['agentic_config'] = {'filters': {}}

    assert KBToolkit()._kb_ids(['kb-second']) == ['kb-second']
    assert KBToolkit()._kb_ids(['kb-first']) == ['kb-first']
    assert len(calls) == 2


def test_kb_ids_reject_unavailable_id(monkeypatch):
    monkeypatch.setattr(
        'lazymind.chat.engine.tools.kb.get_core_api',
        lambda path, params=None: {
            'datasets': [{'dataset_id': 'readable-kb'}],
            'next_page_token': '',
        },
    )
    lazyllm.globals['agentic_config'] = {'filters': {}}

    with pytest.raises(ToolExecutionError, match='requested knowledge bases are unavailable'):
        KBToolkit()._kb_ids(['unreadable-kb'])


def _node(uid, *, parent=None, number=1, kb_id='kb-one', docid='doc-one'):
    return SimpleNamespace(
        uid=uid,
        text=f'text-{uid}',
        metadata={},
        global_metadata={'kb_id': kb_id, 'docid': docid},
        group='block',
        number=number,
        _parent=parent,
    )


def test_parent_node_derives_kb_id_from_target_node(monkeypatch):
    current = _node('node-one', parent='parent-one')
    parent = _node('parent-one')
    calls = []

    class FakeDocument:
        def get_nodes(self, **kwargs):
            calls.append(kwargs)
            return [current] if kwargs.get('uids') == ['node-one'] else [parent]

    monkeypatch.setattr('lazymind.chat.engine.tools.kb.DOCUMENT', FakeDocument())

    result = KBToolkit().kb_get_parent_node('node-one')

    assert result['items'][0]['uid'] == 'parent-one'
    assert calls == [
        {'uids': ['node-one']},
        {'uids': ['parent-one'], 'kb_id': 'kb-one'},
    ]


def test_window_nodes_derive_scope_and_position_from_target_node(monkeypatch):
    seed = _node('node-one', number=3)
    previous = _node('node-zero', number=2)
    following = _node('node-two', number=4)
    calls = []

    class FakeDocument:
        def get_nodes(self, **kwargs):
            calls.append(('get_nodes', kwargs))
            return [seed]

        def get_window_nodes(self, node, span, merge):
            calls.append(('get_window_nodes', node.uid, span, merge))
            return [previous, seed, following]

    monkeypatch.setattr('lazymind.chat.engine.tools.kb.DOCUMENT', FakeDocument())

    result = KBToolkit().kb_get_window_nodes('node-one', before=1, after=1)

    assert [item['uid'] for item in result['items']] == [
        'node-zero', 'node-one', 'node-two',
    ]
    assert calls == [
        ('get_nodes', {'uids': ['node-one']}),
        ('get_window_nodes', 'node-one', (-1, 1), False),
    ]


@pytest.mark.parametrize('method, kwargs', [
    ('kb_search', {'query': 'evidence', 'kb_ids': ['outside-kb']}),
    ('kb_keyword_search', {'keyword': 'evidence', 'target': 'doc', 'kb_ids': ['outside-kb']}),
    ('list_knowledge_base_documents', {'knowledge_base_ids': ['inherited-kb', 'outside-kb']}),
    ('aggregate_knowledge_base_documents', {'knowledge_base_ids': ['outside-kb']}),
])
def test_scoped_kb_rejects_explicit_ids_outside_inheritance(monkeypatch, method, kwargs):
    def forbidden(*args, **kwargs):
        raise AssertionError('out-of-scope KB must be rejected before retrieval or API calls')

    lazyllm.globals['agentic_config'] = {'filters': {'kb_id': ['outside-kb']}}
    monkeypatch.setattr('lazymind.chat.engine.tools.kb.get_core_api', forbidden)
    monkeypatch.setattr('lazymind.chat.engine.tools.kb.post_core_api', forbidden)
    monkeypatch.setattr('lazymind.chat.engine.tools.kb._ensure_kb_search_runtime', forbidden)
    monkeypatch.setattr('lazymind.chat.engine.tools.kb.resolve_index', lambda group: group)
    with pytest.raises(ToolExecutionError, match='inherited knowledge-base scope'):
        getattr(KBToolkit(kb_scope=['inherited-kb']), method)(**kwargs)


def test_scoped_kb_discovery_and_default_filters_stay_inherited(monkeypatch):
    calls = []
    lazyllm.globals['agentic_config'] = {'filters': {'kb_id': ['outside-kb']}}

    def get_dataset(path, params=None):
        calls.append(path)
        assert path == '/datasets/inherited-kb'
        return {'dataset_id': 'inherited-kb', 'display_name': 'Research', 'tags': ['papers']}

    def post_documents(path, payload):
        calls.append((path, payload))
        return {'response': {'documents': []}}

    monkeypatch.setattr('lazymind.chat.engine.tools.kb.get_core_api', get_dataset)
    monkeypatch.setattr('lazymind.chat.engine.tools.kb.post_core_api', post_documents)
    monkeypatch.setattr('lazymind.chat.engine.tools.kb._ensure_kb_search_runtime', lambda: ([], None, None))
    monkeypatch.setattr('lazymind.chat.engine.tools.kb.search_kb', lambda payload, **kwargs: payload)
    toolkit = KBToolkit(kb_scope=['inherited-kb'])
    assert toolkit.list_knowledge_bases(keyword='search', tags=['papers'])['datasets'][0]['dataset_id'] == 'inherited-kb'
    assert toolkit.list_knowledge_bases(tags=['missing'])['datasets'] == []
    toolkit.aggregate_knowledge_base_documents()
    assert calls[-1][1]['dataset_ids'] == ['inherited-kb']
    toolkit.list_knowledge_base_documents([])
    assert calls[-1][1]['dataset_ids'] == ['inherited-kb']
    result = toolkit.kb_search('evidence', filters={'kb_id': ['outside-kb']})
    assert result['filters']['kb_id'] == ['inherited-kb']


@pytest.mark.parametrize('method', ['kb_get_parent_node', 'kb_get_window_nodes'])
@pytest.mark.parametrize('kb_id', ['outside-kb', None])
def test_scoped_kb_node_navigation_rejects_outside_or_unknown_scope(monkeypatch, method, kb_id):
    document = SimpleNamespace(get_nodes=lambda **kwargs: [_node('outside-node', kb_id=kb_id)])
    monkeypatch.setattr('lazymind.chat.engine.tools.kb.DOCUMENT', document)
    with pytest.raises(ToolExecutionError, match='inherited knowledge-base scope'):
        getattr(KBToolkit(kb_scope=['kb-one']), method)('outside-node')


@pytest.mark.parametrize('outside', [False, True])
def test_scoped_kb_checks_parent_and_window_results(monkeypatch, outside):
    seed = _node('seed', parent='parent')
    related = _node('parent', kb_id='outside-kb' if outside else 'kb-one')
    document = SimpleNamespace(
        get_nodes=lambda **kwargs: [seed] if kwargs['uids'] == ['seed'] else [related],
        get_window_nodes=lambda *args, **kwargs: [seed, related],
    )
    monkeypatch.setattr('lazymind.chat.engine.tools.kb.DOCUMENT', document)
    toolkit = KBToolkit(kb_scope=['kb-one'])
    for method in (toolkit.kb_get_parent_node, toolkit.kb_get_window_nodes):
        if outside:
            with pytest.raises(ToolExecutionError, match='inherited knowledge-base scope'):
                method('seed')
        else:
            assert all(item['kb_id'] == 'kb-one' for item in method('seed')['items'])


def test_lazy_kb_keeps_bound_scope_when_runtime_loads(monkeypatch):
    from lazymind.chat import runtime_loader

    monkeypatch.setattr(runtime_loader, 'ensure_rag_runtime', lambda: SimpleNamespace(KBToolkit=KBToolkit))
    with pytest.raises(ToolExecutionError, match='inherited knowledge-base scope'):
        LazyKBToolkit(kb_scope=['inherited-kb']).aggregate_knowledge_base_documents(['outside-kb'])
