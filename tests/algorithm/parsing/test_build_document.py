import pytest

import lazymind.parsing.service.build_document as build_document


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
    assert docs.kwargs['embed'] == {
        key: f'emb-{key}'
        for key in build_document.EMBED_KEYS
    }
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
