from types import SimpleNamespace

import chat.pipelines.get_ppl_search as ppl_search_mod

retriever_mod = ppl_search_mod


class _DummyContext:
    def __enter__(self):
        return self

    def __exit__(self, exc_type, exc, tb):
        return False


class _DummyPipe:
    def __init__(self, value=None):
        self.value = value

    def __or__(self, other):
        return self

    def __ror__(self, other):
        return self


class _DummyBind:
    def __init__(self, kwargs):
        self.kwargs = kwargs

    def __ror__(self, other):
        if callable(other):
            return lambda *args, **kwargs: other(*args, **self.kwargs)
        return _DummyPipe(('bound-from-left', other, self.kwargs))


class _FakePipeline:
    def __init__(self, input_value=None, kwargs=None):
        object.__setattr__(self, 'assignments', [])
        object.__setattr__(self, 'input', input_value if input_value is not None else {})
        object.__setattr__(self, 'kwargs', kwargs if kwargs is not None else {})

    def __enter__(self):
        return self

    def __exit__(self, exc_type, exc, tb):
        return False

    def __setattr__(self, name, value):
        object.__setattr__(self, name, value)
        if name not in {'assignments', 'input', 'kwargs'}:
            self.assignments.append(name)


def test_get_remote_document_builds_default_name(monkeypatch):
    captured = {}

    class _FakeDocument:
        def __init__(self, url, name):
            captured['url'] = url
            captured['name'] = name

    monkeypatch.setattr(retriever_mod, 'Document', _FakeDocument)

    retriever_mod.get_remote_document('http://kb-service')

    assert captured == {'url': 'http://kb-service/_call', 'name': '__default__'}


def test_get_remote_document_parses_custom_name(monkeypatch):
    captured = {}

    class _FakeDocument:
        def __init__(self, url, name):
            captured['url'] = url
            captured['name'] = name

    monkeypatch.setattr(retriever_mod, 'Document', _FakeDocument)

    retriever_mod.get_remote_document('http://kb-service,my-kb')

    assert captured == {'url': 'http://kb-service/_call', 'name': 'my-kb'}


def test_get_retriever_builds_parts(monkeypatch):
    retriever_calls = []
    bind_calls = []
    automodel_calls = []
    temp_calls = {'init': None, 'sub': None}

    class _FakeRetriever:
        def __init__(self, document, **cfg):
            retriever_calls.append((document, cfg))

    class _FakeTempDocRetriever:
        def __init__(self, embed):
            temp_calls['init'] = embed

        def add_subretriever(self, name, topk):
            temp_calls['sub'] = (name, topk)

        def __or__(self, other):
            return _DummyPipe()

    class _FakePipeline(_DummyContext):
        def __init__(self):
            self.input = 'pipeline-input'

    fake_document = object()
    fake_pipeline = _FakePipeline()

    monkeypatch.setattr(retriever_mod, 'Retriever', _FakeRetriever)
    monkeypatch.setattr(retriever_mod, 'TempDocRetriever', _FakeTempDocRetriever)
    monkeypatch.setattr(retriever_mod, 'get_remote_document', lambda url: fake_document)
    monkeypatch.setattr(
        retriever_mod,
        'AutoModel',
        lambda model, config=False: automodel_calls.append(model) or 'embed-model',
    )
    monkeypatch.setattr(retriever_mod, 'pipeline', lambda: fake_pipeline)
    monkeypatch.setattr(retriever_mod, 'bind', lambda **kwargs: bind_calls.append(kwargs) or _DummyPipe())

    parts = retriever_mod.get_retriever(
        'http://kb-service',
        retriever_configs=[{'group_name': 'line', 'topk': 5}],
        tmp_block_topk=3,
    )

    assert len(parts.kb_retrievers) == 1
    assert parts.tmp_retriever_pipeline is fake_pipeline
    assert retriever_calls == [
        (fake_document, {'group_name': 'line', 'topk': 5}),
        (fake_document, {'group_name': 'image', 'topk': 3, 'embed_keys': ['embed_image']}),
    ]
    assert automodel_calls == [retriever_mod.EMBED_MAIN]
    assert temp_calls == {'init': 'embed-model', 'sub': ('block', 3)}
    assert bind_calls == [{'query': 'pipeline-input'}]


def test_adaptive_get_token_len_uses_text_length():
    assert ppl_search_mod._adaptive_get_token_len(SimpleNamespace(text='abcd' * 3)) == 3
    assert ppl_search_mod._adaptive_get_token_len(SimpleNamespace(text='')) == 1
    assert ppl_search_mod._adaptive_get_token_len(SimpleNamespace()) == 1


def test_get_ppl_search_keeps_expected_stage_order(monkeypatch):
    search_ppl = _FakePipeline(
        input_value={'query': 'q', 'files': [], 'filters': {'scope': 'all'}},
    )
    recorded = {}

    monkeypatch.setattr(ppl_search_mod.lazyllm, 'save_pipeline_result', lambda: _DummyContext())
    monkeypatch.setattr(ppl_search_mod, 'pipeline', lambda: search_ppl)
    monkeypatch.setattr(
        ppl_search_mod,
        'get_retriever',
        lambda url, retriever_configs: SimpleNamespace(
            kb_retrievers=[_DummyPipe('r1'), _DummyPipe('r2')],
            tmp_retriever_pipeline=_DummyPipe('tmp'),
            image_retriever=None,
        ),
    )
    monkeypatch.setattr(ppl_search_mod, 'get_remote_document', lambda url: 'document')
    monkeypatch.setattr(ppl_search_mod, 'AutoModel', lambda model, config=False: f'model:{model}')
    monkeypatch.setattr(ppl_search_mod, 'bind', lambda **kwargs: _DummyBind(kwargs))
    monkeypatch.setattr(ppl_search_mod, 'parallel', lambda *items: ('parallel', items))
    monkeypatch.setattr(
        ppl_search_mod,
        'ifs',
        lambda cond, tpath, fpath: (
            recorded.__setitem__('ifs', {'cond': cond, 'tpath': tpath, 'fpath': fpath}),
            _DummyPipe('ifs'),
        )[1],
    )
    monkeypatch.setattr(
        ppl_search_mod,
        'RRFFusion',
        lambda top_k: (
            recorded.__setitem__('join_top_k', top_k),
            _DummyPipe('rrf'),
        )[1],
    )

    class _FakeReranker:
        def __init__(self, name, model, topk):
            recorded['reranker'] = {'name': name, 'model': model, 'topk': topk}

        def __or__(self, other):
            recorded['reranker_bind'] = other
            return _DummyPipe('reranker')

    class _FakeAdaptiveKComponent:
        def __init__(self, **kwargs):
            recorded['adaptive_k'] = kwargs

    class _FakeContextExpansionComponent:
        def __init__(self, **kwargs):
            recorded['ctx_expand'] = kwargs

    monkeypatch.setattr(ppl_search_mod, 'Reranker', _FakeReranker)
    monkeypatch.setattr(ppl_search_mod, 'AdaptiveKComponent', _FakeAdaptiveKComponent)
    monkeypatch.setattr(ppl_search_mod, 'ContextExpansionComponent', _FakeContextExpansionComponent)

    result = ppl_search_mod.get_ppl_search(
        'http://kb-service',
        retriever_configs=[{'group_name': 'line'}],
        topk=7,
        k_max=4,
    )

    assert result is search_ppl
    assert search_ppl.assignments == [
        'parse_input',
        'divert',
        'merge_results',
        'join',
        'reranker',
        'adaptive_k',
        'ctx_expand',
        'search',
    ]
    assert recorded['join_top_k'] == 50
    assert recorded['ifs']['cond']() is False
    assert recorded['reranker'] == {'name': 'ModuleReranker', 'model': 'model:reranker', 'topk': 7}
    assert recorded['adaptive_k']['k_max'] == 4
    assert recorded['adaptive_k']['max_tokens'] == 2048
    assert recorded['adaptive_k']['get_token_len'] is ppl_search_mod._adaptive_get_token_len
    assert recorded['ctx_expand'] == {
        'document': 'document',
        'token_budget': 1500,
        'score_decay': 0.97,
        'max_seeds': 1,
    }


def test_get_ppl_search_diverts_to_temp_retriever_when_files_present(monkeypatch):
    search_ppl = _FakePipeline(
        input_value={'query': 'q', 'files': ['tmp.doc'], 'filters': {}},
    )
    recorded = {}

    monkeypatch.setattr(ppl_search_mod.lazyllm, 'save_pipeline_result', lambda: _DummyContext())
    monkeypatch.setattr(ppl_search_mod, 'pipeline', lambda: search_ppl)
    monkeypatch.setattr(
        ppl_search_mod,
        'get_retriever',
        lambda url, retriever_configs: SimpleNamespace(
            kb_retrievers=[_DummyPipe('r1')],
            tmp_retriever_pipeline=_DummyPipe('tmp'),
            image_retriever=None,
        ),
    )
    monkeypatch.setattr(ppl_search_mod, 'get_remote_document', lambda url: 'document')
    monkeypatch.setattr(ppl_search_mod, 'AutoModel', lambda model, config=False: f'model:{model}')
    monkeypatch.setattr(ppl_search_mod, 'bind', lambda **kwargs: _DummyBind(kwargs))
    monkeypatch.setattr(ppl_search_mod, 'parallel', lambda *items: ('parallel', items))
    monkeypatch.setattr(
        ppl_search_mod,
        'ifs',
        lambda cond, tpath, fpath: (
            recorded.__setitem__('ifs', {'cond': cond, 'tpath': tpath, 'fpath': fpath}),
            _DummyPipe('ifs'),
        )[1],
    )
    monkeypatch.setattr(ppl_search_mod, 'RRFFusion', lambda top_k: _DummyPipe('rrf'))
    monkeypatch.setattr(ppl_search_mod, 'Reranker', lambda *args, **kwargs: _DummyPipe('reranker'))
    monkeypatch.setattr(ppl_search_mod, 'AdaptiveKComponent', lambda **kwargs: _DummyPipe('adaptive'))
    monkeypatch.setattr(ppl_search_mod, 'ContextExpansionComponent', lambda **kwargs: _DummyPipe('expand'))

    ppl_search_mod.get_ppl_search('http://kb-service', retriever_configs=[{'group_name': 'line'}])

    assert recorded['ifs']['cond']('ignored') is True
