import asyncio
import importlib
import json
import sys
from types import ModuleType, SimpleNamespace


def _import_chat_service_module(monkeypatch, *, chat_server=None):
    fake_lazyllm = ModuleType('lazyllm')
    fake_lazyllm.LOG = SimpleNamespace(
        info=lambda *args, **kwargs: None,
        warning=lambda *args, **kwargs: None,
        exception=lambda *args, **kwargs: None,
    )
    fake_lazyllm.globals = SimpleNamespace(
        _init_sid=lambda sid: None,
        get=lambda key, default=None: None,
    )
    fake_lazyllm.locals = SimpleNamespace(_init_sid=lambda sid: None)

    # Inject lazyllm.tracing submodule hierarchy so chat_service.py can import it
    fake_tracing = ModuleType('lazyllm.tracing')
    fake_tracing.current_trace = lambda: None
    fake_tracing.enable_trace = lambda *a, **kw: None
    fake_tracing_collect = ModuleType('lazyllm.tracing.collect')
    fake_tracing_collect.runtime = SimpleNamespace(
        get_trace_id=lambda: None,
        start_span=lambda *a, **kw: None,
        end_span=lambda *a, **kw: None,
    )
    fake_tracing_collect_configs = ModuleType('lazyllm.tracing.collect.configs')
    fake_lazyllm.tracing = fake_tracing
    fake_tracing.collect = fake_tracing_collect

    fake_config = ModuleType('chat.config')
    fake_config.URL_MAP = {'algo': 'http://kb-service,algo'}
    fake_config.RAG_MODE = True
    fake_config.MULTIMODAL_MODE = True
    fake_config.MAX_CONCURRENCY = 2
    fake_config.LAZYMIND_LLM_PRIORITY = 5
    fake_config.SENSITIVE_FILTER_RESPONSE_TEXT = 'blocked'
    fake_config.resolve_dataset_url = lambda dataset: f'http://kb-service/{dataset}'

    fake_helpers = ModuleType('chat.utils.helpers')
    fake_helpers.tool_schema_to_string = lambda schema: '{}'
    fake_helpers.validate_and_resolve_files = lambda files: (['/tmp/a.txt'], ['/tmp/b.png'])

    fake_schema = ModuleType('chat.utils.schema')
    fake_schema.BaseMessage = type('BaseMessage', (), {})
    fake_schema.SessionMemory = type('SessionMemory', (), {})
    fake_schema.MiddleResults = type('MiddleResults', (), {})
    fake_schema.ToolMemory = type('ToolMemory', (), {})
    fake_schema.ToolCall = type('ToolCall', (), {})
    fake_schema.PlanStep = type('PlanStep', (), {})
    fake_schema.TaskContext = type('TaskContext', (), {})

    fake_markdown_images = ModuleType('chat.utils.markdown_images')
    fake_markdown_images.rewrite_markdown_image_urls = lambda markdown, **kw: markdown

    fake_load_config = ModuleType('chat.utils.load_config')
    fake_load_config.get_config_path = lambda: '/tmp/dummy_model_config.yaml'
    fake_load_config.inject_model_config = lambda *a, **kw: None
    fake_load_config.summarize_model_config_for_log = lambda model_config: 'roles=[]'

    if chat_server is None:
        chat_server = SimpleNamespace(
            sensitive_filter=SimpleNamespace(loaded=False, check=lambda query: (False, None)),
            has_dataset=lambda dataset: dataset == 'algo',
            get_query_pipeline=lambda dataset, stream=False: f'pipeline:{dataset}:{stream}',
            query_ppl_reasoning='reasoning-pipeline',
        )

    fake_server = ModuleType('chat.app.core.chat_server')
    fake_server.chat_server = chat_server

    fake_lazyllm_configs = ModuleType('lazyllm.configs')
    fake_lazyllm_configs.Config = type('Config', (), {
        '__init__': lambda self, *a, **kw: None,
        '__getitem__': lambda self, k: '',
        'add': lambda self, *a, **kw: None,
    })
    fake_lazyllm.configs = fake_lazyllm_configs

    # Pop stale cached modules BEFORE setting up fakes
    sys.modules.pop('chat.app.core.chat_service', None)

    monkeypatch.setitem(sys.modules, 'lazyllm', fake_lazyllm)
    monkeypatch.setitem(sys.modules, 'lazyllm.configs', fake_lazyllm_configs)
    monkeypatch.setitem(sys.modules, 'lazyllm.tracing', fake_tracing)
    monkeypatch.setitem(sys.modules, 'lazyllm.tracing.collect', fake_tracing_collect)
    monkeypatch.setitem(sys.modules, 'lazyllm.tracing.collect.configs', fake_tracing_collect_configs)
    fake_tracing_datamodel_raw = ModuleType('lazyllm.tracing.datamodel.raw')
    fake_tracing_datamodel_raw.RawSpanRecord = type('RawSpanRecord', (), {})
    fake_tracing_datamodel_raw.RawTracePayload = type('RawTracePayload', (), {})
    fake_tracing_datamodel_raw.RawTraceRecord = type('RawTraceRecord', (), {})
    monkeypatch.setitem(sys.modules, 'lazyllm.tracing.datamodel', ModuleType('lazyllm.tracing.datamodel'))
    monkeypatch.setitem(sys.modules, 'lazyllm.tracing.datamodel.raw', fake_tracing_datamodel_raw)
    monkeypatch.setitem(sys.modules, 'chat.config', fake_config)
    monkeypatch.setitem(sys.modules, 'chat.utils.helpers', fake_helpers)
    monkeypatch.setitem(sys.modules, 'chat.utils.schema', fake_schema)
    monkeypatch.setitem(sys.modules, 'chat.utils.markdown_images', fake_markdown_images)
    monkeypatch.setitem(sys.modules, 'chat.utils.load_config', fake_load_config)
    monkeypatch.setitem(sys.modules, 'chat.app.core.chat_server', fake_server)

    return importlib.import_module('chat.app.core.chat_service')


def test_sse_line_and_resp_helpers(monkeypatch):
    module = _import_chat_service_module(monkeypatch)

    payload = module._resp(200, 'success', {'x': 1}, 0.25)

    assert payload == {'code': 200, 'msg': 'success', 'data': {'x': 1}, 'cost': 0.25}
    assert json.loads(module._sse_line(payload).strip()) == payload


def test_check_sensitive_content_returns_block_response(monkeypatch):
    chat_server = SimpleNamespace(
        sensitive_filter=SimpleNamespace(loaded=True, check=lambda query: (True, 'secret')),
    )
    module = _import_chat_service_module(monkeypatch, chat_server=chat_server)

    result = module.check_sensitive_content('secret question', 'sid-1', 0.0)

    assert result['code'] == 200
    assert result['msg'] == 'success'
    assert result['data']['text'] == 'blocked'
    assert result['data']['sources'] == []


def test_build_query_params_filters_history_and_modes(monkeypatch):
    module = _import_chat_service_module(monkeypatch)

    params = module.build_query_params(
        query='hello',
        history=[{'role': 'user', 'content': 123}, 'ignore-me', {'content': 'answer'}],
        filters={'scope': 'all'},
        other_files=['doc.txt'],
        databases=[{'name': 'db'}],
        debug=True,
        image_files=['img.png'],
        priority=8,
        dataset='algo',
        session_id='sid-1',
        available_tools=None,
        available_skills=None,
        memory=None,
        user_preference=None,
        use_memory=None,
    )

    assert params['query'] == 'hello'
    assert params['history'] == [
        {'role': 'user', 'content': '123'},
        {'role': 'assistant', 'content': 'answer'},
    ]
    assert params['filters'] == {'scope': 'all'}
    assert params['files'] == ['doc.txt']
    assert params['image_files'] == ['img.png']
    assert params['debug'] is True
    assert params['databases'] == [{'name': 'db'}]
    assert params['priority'] == 8
    assert params['dataset'] == 'algo'
    assert params['session_id'] == 'sid-1'
    assert params['user_id'] == ''
    assert 'document_url' in params


def test_build_query_params_sets_user_id(monkeypatch):
    module = _import_chat_service_module(monkeypatch)

    params = module.build_query_params(
        query='hello',
        history=None,
        filters=None,
        other_files=[],
        databases=None,
        debug=False,
        image_files=[],
        priority=8,
        dataset='algo',
        session_id='sid-1',
        available_tools=None,
        available_skills=None,
        memory=None,
        user_preference=None,
        use_memory=None,
        user_id='user-1',
    )

    assert params['user_id'] == 'user-1'

def test_handle_chat_rejects_unknown_dataset(monkeypatch):
    chat_server = SimpleNamespace(
        sensitive_filter=SimpleNamespace(loaded=False, check=lambda query: (False, None)),
        has_dataset=lambda dataset: False,
    )
    module = _import_chat_service_module(monkeypatch, chat_server=chat_server)

    result = asyncio.run(
        module.handle_chat(
            query='hello',
            history=None,
            session_id='sid-1',
            filters=None,
            files=None,
            debug=False,
            reasoning=False,
            databases=None,
            dataset='missing',
            priority=None,
            available_tools=None,
            available_skills=None,
            memory=None,
            user_preference=None,
            use_memory=None,
            is_stream=False,
        )
    )

    assert result == {'code': 400, 'msg': 'dataset missing not found', 'data': None, 'cost': 0.0}


def test_handle_chat_non_stream_returns_pipeline_result(monkeypatch):
    module = _import_chat_service_module(monkeypatch)

    def fake_run_ppl_with_trace(ppl, ppl_args, *, session_id, dataset, mode_tag, trace_enabled):
        return {'text': 'answer'}, None, None

    monkeypatch.setattr(module, '_run_ppl_with_trace', fake_run_ppl_with_trace)

    result = asyncio.run(
        module.handle_chat(
            query='hello',
            history=[],
            session_id='sid-1',
            filters={'scope': 'all'},
            files=['input.txt'],
            debug=False,
            reasoning=False,
            databases=[],
            dataset='algo',
            priority=None,
            available_tools=None,
            available_skills=None,
            memory=None,
            user_preference=None,
            use_memory=None,
            is_stream=False,
        )
    )

    assert result['code'] == 200
    assert result['msg'] == 'success'
    assert result['data'] == {'text': 'answer'}


def test_run_sync_ppl_uses_reasoning_pipeline(monkeypatch):
    """_build_ppl_call with reasoning=True should use query_ppl_reasoning."""
    def fake_reasoning(params):
        return {'text': 'reasoned'}

    chat_server = SimpleNamespace(
        sensitive_filter=SimpleNamespace(loaded=False, check=lambda query: (False, None)),
        has_dataset=lambda dataset: True,
        query_ppl_reasoning=fake_reasoning,
        get_query_pipeline=lambda dataset, stream=False: 'unused',
    )
    module = _import_chat_service_module(monkeypatch, chat_server=chat_server)

    query_params = {
        'query': 'hello',
        'filters': {'scope': 'all'},
        'files': [],
        'stream': True,
        'priority': 7,
        'document_url': 'stale-url',
    }
    ppl_call = module._build_ppl_call(
        True,
        'algo',
        query_params,
        stream=False,
    )

    # ppl_call is (ppl_fn, params)
    assert ppl_call[0] is fake_reasoning
    assert ppl_call[1] == {
        'query': 'hello',
        'filters': {'scope': 'all'},
        'files': [],
        'stream': False,
        'priority': 7,
        'document_url': 'http://kb-service/algo',
    }


def _decode_sse_payloads(raw_chunks):
    payloads = []
    for raw in raw_chunks:
        for line in raw.splitlines():
            if line.strip():
                payloads.append(json.loads(line))
    return payloads


def test_handle_chat_stream_returns_sse_chunks_and_final_status(monkeypatch):
    first_frame_logs = []
    init_calls = []
    captured = {}

    async def _stream():
        yield {'text': 'chunk-1'}
        yield {'text': 'chunk-2'}

    chat_server = SimpleNamespace(
        sensitive_filter=SimpleNamespace(loaded=False, check=lambda query: (False, None)),
        has_dataset=lambda dataset: dataset == 'algo',
        get_query_pipeline=lambda dataset, stream=False: None,
        query_ppl_reasoning='unused',
    )
    module = _import_chat_service_module(monkeypatch, chat_server=chat_server)

    class _FakeStreamingResponse:
        def __init__(self, body_iterator, media_type):
            self.body_iterator = body_iterator
            self.media_type = media_type

    def fake_run_ppl_with_trace(ppl, ppl_args, *, session_id, dataset, mode_tag, trace_enabled):
        # ppl_args is a tuple; first element is the query_params dict
        captured['query_params'] = ppl_args[0] if ppl_args else {}
        return _stream(), None, None

    monkeypatch.setattr(module, '_run_ppl_with_trace', fake_run_ppl_with_trace)
    monkeypatch.setattr(module, 'validate_and_resolve_files', lambda files: (['/tmp/a.txt'], ['/tmp/b.png']))
    monkeypatch.setattr(module.LOG, 'info', lambda message: first_frame_logs.append(message))
    monkeypatch.setattr(module.lazyllm.globals, '_init_sid', lambda sid: init_calls.append(('global', sid)))
    monkeypatch.setattr(module.lazyllm.locals, '_init_sid', lambda sid: init_calls.append(('local', sid)))
    monkeypatch.setattr(module, 'StreamingResponse', _FakeStreamingResponse)

    response = asyncio.run(
        module.handle_chat(
            query='hello',
            history=[{'role': 'user', 'content': 'hi'}],
            session_id='sid-1',
            filters={'scope': 'all'},
            files=['doc.txt', 'img.png'],
            debug=True,
            reasoning=False,
            databases=[{'name': 'db'}],
            dataset='algo',
            priority=9,
            available_tools=None,
            available_skills=None,
            memory=None,
            user_preference=None,
            use_memory=None,
            is_stream=True,
        )
    )

    async def _collect():
        return [chunk async for chunk in response.body_iterator]

    payloads = _decode_sse_payloads(asyncio.run(_collect()))

    assert captured['query_params']['query'] == 'hello'
    assert captured['query_params']['filters'] == {'scope': 'all'}
    assert captured['query_params']['priority'] == 9
    assert [payload['data'] for payload in payloads] == [
        {'text': 'chunk-1'},
        {'text': 'chunk-2'},
        {'status': 'FINISHED'},
    ]
    assert any('KB_CHAT_STREAM_FIRST_FRAME' in message for message in first_frame_logs)
    assert init_calls == [('global', 'sid-1'), ('local', 'sid-1')]


def test_handle_chat_stream_preserves_separate_think_and_text_frames(monkeypatch):
    async def _stream():
        yield {'think': '思考片段', 'text': None}
        yield {'think': None, 'text': '正文片段'}

    def _pipeline(query_params):
        return _stream()

    chat_server = SimpleNamespace(
        sensitive_filter=SimpleNamespace(loaded=False, check=lambda query: (False, None)),
        has_dataset=lambda dataset: dataset == 'algo',
        get_query_pipeline=lambda dataset, stream=False: _pipeline,
        query_ppl_reasoning='unused',
    )
    module = _import_chat_service_module(monkeypatch, chat_server=chat_server)

    class _FakeStreamingResponse:
        def __init__(self, body_iterator, media_type):
            self.body_iterator = body_iterator
            self.media_type = media_type

    async def fake_to_thread(fn, *args, **kwargs):
        return fn(*args, **kwargs)

    monkeypatch.setattr(module, 'validate_and_resolve_files', lambda files: ([], []))
    monkeypatch.setattr(module, 'StreamingResponse', _FakeStreamingResponse)
    monkeypatch.setattr(module.asyncio, 'to_thread', fake_to_thread)

    response = asyncio.run(
        module.handle_chat(
            query='hello',
            history=[],
            session_id='sid-1',
            filters=None,
            files=None,
            debug=False,
            reasoning=False,
            databases=[],
            dataset='algo',
            priority=1,
            available_tools=None,
            available_skills=None,
            memory=None,
            user_preference=None,
            use_memory=None,
            is_stream=True,
        )
    )

    async def _collect():
        return [chunk async for chunk in response.body_iterator]

    payloads = _decode_sse_payloads(asyncio.run(_collect()))

    assert [payload['data'] for payload in payloads] == [
        {'think': '思考片段', 'text': None},
        {'think': None, 'text': '正文片段'},
        {'status': 'FINISHED'},
    ]


def test_handle_chat_concurrency_respects_semaphore_and_session_isolation(monkeypatch):
    init_calls = []
    start_order = []
    release_first = asyncio.Event()

    module = _import_chat_service_module(monkeypatch)
    monkeypatch.setattr(module, 'rag_sem', asyncio.Semaphore(1))
    monkeypatch.setattr(module, 'validate_and_resolve_files', lambda files: ([], []))
    monkeypatch.setattr(module.lazyllm.globals, '_init_sid', lambda sid: init_calls.append(('global', sid)))
    monkeypatch.setattr(module.lazyllm.locals, '_init_sid', lambda sid: init_calls.append(('local', sid)))

    def fake_run_ppl_with_trace(ppl, ppl_args, *, session_id, dataset, mode_tag, trace_enabled):
        # ppl_args is a tuple; for non-reasoning it's (query_params_dict,)
        query_params_dict = ppl_args[0] if ppl_args else {}
        start_order.append(query_params_dict.get('query', ''))
        return {'text': query_params_dict.get('query', '')}, None, None

    monkeypatch.setattr(module, '_run_ppl_with_trace', fake_run_ppl_with_trace)

    async def _run_pair():
        task1 = asyncio.create_task(
            module.handle_chat(
                query='q1',
                history=[],
                session_id='sid-1',
                filters=None,
                files=None,
                debug=False,
                reasoning=False,
                databases=None,
                dataset='algo',
                priority=None,
                available_tools=None,
                available_skills=None,
                memory=None,
                user_preference=None,
                use_memory=None,
                is_stream=False,
            )
        )
        await asyncio.sleep(0)
        task2 = asyncio.create_task(
            module.handle_chat(
                query='q2',
                history=[],
                session_id='sid-2',
                filters=None,
                files=None,
                debug=False,
                reasoning=False,
                databases=None,
                dataset='algo',
                priority=None,
                available_tools=None,
                available_skills=None,
                memory=None,
                user_preference=None,
                use_memory=None,
                is_stream=False,
            )
        )
        await asyncio.sleep(0)
        assert start_order == ['q1']
        release_first.set()
        return await asyncio.gather(task1, task2)

    results = asyncio.run(_run_pair())

    assert [item['data'] for item in results] == [{'text': 'q1'}, {'text': 'q2'}]
    assert start_order == ['q1', 'q2']
    assert init_calls == [
        ('global', 'sid-1'),
        ('local', 'sid-1'),
        ('global', 'sid-2'),
        ('local', 'sid-2'),
    ]


def test_log_chat_request_includes_observability_fields(monkeypatch):
    messages = []
    module = _import_chat_service_module(monkeypatch)
    monkeypatch.setattr(module.LOG, 'info', lambda message: messages.append(message))

    module.log_chat_request(
        'hello',
        'sid-1',
        {'scope': 'all'},
        ['/tmp/a.txt'],
        [{'name': 'db'}],
        ['/tmp/b.png'],
        0.123,
        {'text': 'answer'},
        'KB_CHAT_STREAM_FINISH',
    )

    assert len(messages) == 1
    message = messages[0]
    assert '[KB_CHAT_STREAM_FINISH]' in message
    assert '[session_id=sid-1]' in message
    assert "[files=['/tmp/a.txt']]" in message
    assert "[image_files=['/tmp/b.png']]" in message
    assert '[cost=0.123]' in message
