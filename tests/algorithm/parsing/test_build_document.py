import importlib
import os
from pathlib import Path
import subprocess
import sys

import pytest
import lazyllm

from lazymind.parsing.service import build_document
from lazymind.parsing.engine.readers.native_office import NativeOfficeReader
from lazymind.parsing.engine.readers.retained import RetainedReader


def test_quiet_trace_clears_previous_request_model_configuration():
    seen = []

    def backend(*args, **kwargs):
        seen.append(dict(lazyllm.globals['config']))
        return 'ok'

    with lazyllm.new_session():
        lazyllm.globals['config']['dynamic_model_configs'] = {
            'embed_main': {'embed': {'source': 'old-supplier', 'model': 'old-model'}},
        }
        lazyllm.globals['config']['old-supplier_api_key'] = {'embed_main': 'old-secret'}
        assert build_document._quiet_trace({'kb': backend})('kb', 'retrieve') == 'ok'

    assert seen == [{}]


def test_processor_entrypoint_clears_previous_task_model_configuration(monkeypatch, tmp_path):
    from lazymind.config import config

    monkeypatch.setitem(config._impl, 'database_url', 'sqlite:///:memory:')
    original_injector = lazyllm.inject_model_config
    try:
        worker = importlib.import_module('lazymind.processor.service.worker')
        importlib.reload(worker)
        assert worker.DocumentProcessorWorker._Impl.__name__ == '_IsolatedDocumentWorkerImpl'
        base_impl = worker.DocumentProcessorWorker._Impl.__mro__[1]
        seen = []
        static_models = tmp_path / 'static-models.yaml'
        static_models.write_text('embed_main:\n  type: embed\n  source: openai\n  model: static-embed\n')

        def run_task(_self, *_args, **_kwargs):
            seen.append(dict(lazyllm.globals['config']))
            assert lazyllm.inject_model_config is worker.inject_model_config
            lazyllm.inject_model_config(None)
            from lazymind.model_config import is_model_role_available
            assert is_model_role_available('embed_main', config_path=str(static_models))

        monkeypatch.setattr(base_impl, '_run_task', run_task)
        impl = object.__new__(worker.DocumentProcessorWorker._Impl)
        with lazyllm.new_session():
            lazyllm.globals['config']['dynamic_model_configs'] = {
                'embed_main': {'embed': {'source': 'old-supplier', 'model': 'old-model'}},
            }
            lazyllm.globals['config']['old-supplier_api_key'] = {'embed_main': 'old-secret'}
            impl._run_task('task', 'DOC_ADD', {}, False)
        assert seen == [{}]
    finally:
        lazyllm.inject_model_config = original_injector


@pytest.mark.parametrize('serialize_as_main', [False, True])
def test_serialized_processor_worker_resets_config_in_child_process(monkeypatch, tmp_path, serialize_as_main):
    from lazymind.config import config

    monkeypatch.setitem(config._impl, 'database_url', 'sqlite:///:memory:')
    original_injector = lazyllm.inject_model_config
    try:
        worker = importlib.import_module('lazymind.processor.service.worker')
        impl = object.__new__(worker.DocumentProcessorWorker._Impl)
        serialized = tmp_path / 'worker.txt'
        impl_class = type(impl)
        original_module = impl_class.__module__
        try:
            if serialize_as_main:
                impl_class.__module__ = '__main__'
            serialized.write_text(lazyllm.dump_obj(impl))
        finally:
            impl_class.__module__ = original_module
    finally:
        lazyllm.inject_model_config = original_injector

    static_models = tmp_path / 'static-models.yaml'
    static_models.write_text('embed_main:\n  type: embed\n  source: openai\n  model: static-embed\n')
    root = Path(__file__).resolve().parents[3]
    env = os.environ.copy()
    env['DATABASE_URL'] = 'sqlite:///:memory:'
    env['LAZYMIND_DATABASE_URL'] = 'sqlite:///:memory:'
    env['PYTHONPATH'] = os.pathsep.join((str(root / 'algorithm'), str(root / 'algorithm/lazyllm')))
    code = '''
import sys
import lazyllm
from lazymind.model_config import is_model_role_available

with open(sys.argv[1]) as stream:
    impl = lazyllm.load_obj(stream.read())
assert type(impl).__name__ == '_IsolatedDocumentWorkerImpl'

def run_task(_self, *_args, **_kwargs):
    assert lazyllm.globals['config'] == {}
    assert lazyllm.inject_model_config.__module__ == 'lazymind.model_config'
    lazyllm.inject_model_config(None)
    assert is_model_role_available('embed_main', config_path=sys.argv[2])

impl._run_task.__func__.__globals__['_BaseDocumentWorkerImpl']._run_task = run_task
with lazyllm.new_session():
    lazyllm.globals['config']['dynamic_model_configs'] = {'embed_main': {'embed': {'model': 'old-model'}}}
    lazyllm.globals['config']['old_api_key'] = {'embed_main': 'old-secret'}
    impl._run_task('task', 'DOC_ADD', {}, False)

class Store:
    _group_embed_keys = {'code': {'embed_main'}}

    def get_nodes(self, **_kwargs):
        return []

    def activated_groups(self):
        return ['code']

class Processor:
    def __init__(self):
        self._store = Store()
        self._reparse_docs = lambda *_args, **_kwargs: None
        self._reembed_group = lambda *_args, **_kwargs: (_ for _ in ()).throw(AssertionError('old fallback'))

processor = Processor()
impl._get_processor.__func__.__globals__['_BaseDocumentWorkerImpl']._get_processor = lambda _self: processor
assert impl._get_processor() is processor
assert processor._lazymind_deferred_group_guard
processor._reembed_group('code', {'code': {'parent': 'lazyllm_root'}}, doc_ids=['doc'], kb_id='kb')
'''
    result = subprocess.run(
        [sys.executable, '-c', code, str(serialized), str(static_models)], cwd=root, env=env,
        capture_output=True, text=True, timeout=30,
    )
    assert result.returncode == 0, result.stdout + result.stderr


@pytest.mark.parametrize('force,previous,expected', [
    (True, 'none', 'force'), (True, 'update', 'force'),
    (False, 'none', 'none'), (False, 'update', 'update'),
])
def test_effective_registration_policy_without_reset(monkeypatch, force, previous, expected):
    monkeypatch.setenv('LAZYLLM_ALGO_REGISTER_POLICY', previous)
    monkeypatch.setitem(build_document._cfg._impl, 'force_algo_register', force)
    monkeypatch.setitem(build_document.lazyllm.config._impl, 'algo_register_policy', previous)
    monkeypatch.setattr(build_document, 'drop_lazyllm_tables', lambda: pytest.fail('must not drop data'))
    monkeypatch.setattr(build_document, 'reset_stores', lambda: pytest.fail('must not reset vectors'))
    assert build_document.configure_algorithm_registration() == expected
    assert build_document.lazyllm.config['algo_register_policy'] == expected
    assert build_document.os.environ['LAZYLLM_ALGO_REGISTER_POLICY'] == expected


@pytest.mark.parametrize('mode,profile,native', [
    ('local', 'cloud', True), ('cloud', 'local', True), ('cloud', 'cloud', False),
])
def test_office_reader_registration_by_runtime(monkeypatch, mode, profile, native):
    class Readers:
        def __init__(self):
            self.readers = {}

        def add_reader(self, pattern, reader):
            self.readers[pattern] = reader

    monkeypatch.setitem(build_document._cfg._impl, 'runtime_mode', mode)
    monkeypatch.setitem(build_document._cfg._impl, 'document_parse_profile', profile)
    pdf_reader = object()
    monkeypatch.setattr(build_document, '_build_pdf_reader', lambda: pdf_reader)
    docs = Readers()
    build_document._register_document_readers(docs)
    assert (isinstance(docs.readers['*.pdf'], RetainedReader) if mode == 'local'
            else docs.readers['*.pdf'] is pdf_reader)
    if native:
        reader_type = RetainedReader if mode == 'local' else NativeOfficeReader
        assert isinstance(docs.readers['*.docx'], reader_type)
        assert isinstance(docs.readers['*.pptx'], reader_type)
    else:
        assert '*.docx' not in docs.readers
        assert docs.readers['*.pptx'] is pdf_reader


def test_get_algo_server_port_prefers_algo_port(monkeypatch):
    # _cfg is read at call time, so we patch _impl directly
    monkeypatch.setitem(build_document._cfg._impl, 'algo_server_port', 0)
    monkeypatch.setitem(build_document._cfg._impl, 'document_server_port', 0)
    assert build_document.get_algo_server_port() == 0
    monkeypatch.setitem(build_document._cfg._impl, 'document_server_port', 18001)
    assert build_document.get_algo_server_port() == 18001
    monkeypatch.setitem(build_document._cfg._impl, 'algo_server_port', 18002)
    assert build_document.get_algo_server_port() == 18002


def test_build_store_config_reads_required_and_optional_env(monkeypatch):
    monkeypatch.setenv('LAZYMIND_MILVUS_URI', 'http://milvus.test')
    monkeypatch.setenv('LAZYMIND_SEGMENT_STORE_URI_OR_PATH', 'https://opensearch.test')
    monkeypatch.setenv('LAZYMIND_SEGMENT_STORE_USER', 'user')
    monkeypatch.setenv('LAZYMIND_SEGMENT_STORE_PASSWORD', 'pass')

    config = build_document._build_store_config({'index': 'flat'})

    assert config['vector_store']['kwargs']['uri'] == 'http://milvus.test'
    assert config['vector_store']['kwargs']['index_kwargs'] == {'index': 'flat'}
    assert config['segment_store']['kwargs']['uris'] == 'https://opensearch.test'
    assert config['segment_store']['kwargs']['client_kwargs']['user'] == 'user'
    assert config['segment_store']['kwargs']['client_kwargs']['password'] == 'pass'


def test_build_store_config_supports_sqlite_segment_store(monkeypatch, tmp_path):
    db_path = tmp_path / 'segment-store.db'
    monkeypatch.setitem(build_document._cfg._impl, 'milvus_uri', 'http://milvus.test')
    monkeypatch.setitem(build_document._cfg._impl, 'segment_store_type', 'SQLiteStore')
    monkeypatch.setitem(build_document._cfg._impl, 'segment_store_uri_or_path', str(db_path))

    config = build_document._build_store_config({'index': 'flat'})

    assert config['vector_store']['kwargs']['uri'] == 'http://milvus.test'
    assert config['segment_store'] == {
        'type': 'SQLiteStore',
        'kwargs': {'db_path': str(db_path)},
    }


def test_build_store_config_raises_for_missing_milvus_uri(monkeypatch):
    # _require_env was removed; build_document now raises ValueError directly
    # when required config values are missing.
    monkeypatch.setitem(build_document._cfg._impl, 'milvus_uri', '')

    with pytest.raises(ValueError, match='LAZYMIND_MILVUS_URI is required'):
        build_document._build_store_config({})


def test_build_pdf_reader_uses_dynamic_reader_without_static_ocr_route(monkeypatch):
    seen = {}

    class FakeDynamicPDFReader:
        def __init__(self, **kwargs):
            seen.update(kwargs)

    monkeypatch.setattr(build_document, 'DynamicPDFReader', FakeDynamicPDFReader)
    monkeypatch.setitem(build_document._cfg._impl, 'ocr_cache_dir', '/app/uploads/.image_cache')

    reader = build_document._build_pdf_reader()

    assert isinstance(reader, FakeDynamicPDFReader)
    assert 'ocr_type' not in seen
    assert 'ocr_url' not in seen
    assert seen['timeout'] == 3600
    assert seen['image_cache_dir'] == '/app/uploads/.image_cache'


def test_build_document_wires_readers_groups_and_embeddings(monkeypatch):
    monkeypatch.setitem(build_document._cfg._impl, 'runtime_mode', 'cloud')
    monkeypatch.setitem(build_document._cfg._impl, 'document_parse_profile', 'cloud')

    class FakeDocumentProcessor:
        def __init__(self, **kwargs):
            self.kwargs = kwargs

    class FakeDocument:
        def __init__(self, **kwargs):
            self.kwargs = kwargs
            self._manager = kwargs['manager']
            self._manager._kbs = {'default': lambda *args, **kwargs: None}
            self.readers = []
            self.node_groups = []
            self.activated = []

        def add_reader(self, pattern, reader):
            self.readers.append((pattern, reader))

        def create_node_group(self, **kwargs):
            self.node_groups.append(kwargs)

        def activate_group(self, name, embed_keys):
            self.activated.append((name, embed_keys))

    monkeypatch.setattr(build_document, 'Document', FakeDocument)
    monkeypatch.setattr(build_document, 'DocumentProcessor', FakeDocumentProcessor)
    monkeypatch.setattr(build_document, 'get_dynamic_role_slot_map', lambda: {})
    monkeypatch.setattr(build_document, 'AutoModel', lambda model, config=False: f'emb-{model}')
    monkeypatch.setattr(build_document, 'LLMParser', lambda *args, **kwargs: ('llm-parser', args, kwargs))
    monkeypatch.setattr(build_document, '_build_store_config', lambda index_kwargs: {'index_kwargs': index_kwargs})
    monkeypatch.setattr(build_document, '_build_pdf_reader', lambda: 'pdf-reader')
    monkeypatch.setitem(build_document._cfg._impl, 'document_processor_url', 'http://processor.test')
    monkeypatch.setitem(build_document._cfg._impl, 'algo_server_port', 18003)
    monkeypatch.setitem(build_document._cfg._impl, 'document_server_port', 0)

    docs = build_document.build_document()

    assert docs.kwargs['name'] == build_document.ALGO_ID
    from lazymind.embedding_options import EmbeddingModel
    assert isinstance(docs.kwargs['embed']['embed_main'], EmbeddingModel)
    assert docs.kwargs['embed']['embed_main'].model == 'emb-embed_main'
    assert docs.kwargs['embed']['embed_image'] == 'emb-embed_image'
    assert docs.kwargs['manager'].kwargs['store_conf'] == {'index_kwargs': build_document.EMBED_INDEX_KWARGS}
    assert docs.kwargs['manager'].kwargs['url'] == 'http://processor.test'
    assert ('*.pdf', 'pdf-reader') in docs.readers
    assert [group['name'] for group in docs.node_groups] == ['block', 'line', 'code', 'doc-summary']
    assert 'parent' not in docs.node_groups[0]
    assert docs.node_groups[1]['parent'] == 'block'
    assert docs.activated == [
        ('image', build_document.EMBED_IMAGE),
        ('block', [build_document.EMBED_MAIN]),
        ('line', [build_document.EMBED_MAIN]),
        ('code', [build_document.EMBED_MAIN]),
        ('doc-summary', [build_document.EMBED_MAIN]),
    ]
