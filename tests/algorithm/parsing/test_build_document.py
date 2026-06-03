import pytest

import lazymind.parsing.service.build_document as build_document


def test_parse_bool_env_accepts_common_values(monkeypatch):
    # _parse_bool_config takes a value string directly (not an env var name)
    assert build_document._parse_bool_config(None) is None
    assert build_document._parse_bool_config('  ') is None
    for value in ['1', 'true', 'yes', 'on']:
        assert build_document._parse_bool_config(value) is True
    for value in ['0', 'false', 'no', 'off']:
        assert build_document._parse_bool_config(value) is False


def test_parse_bool_env_rejects_invalid_values(monkeypatch):
    with pytest.raises(ValueError, match='mineru_upload_mode must be a boolean string'):
        build_document._parse_bool_config('maybe')


def test_default_mineru_upload_mode_only_disables_container_hostname(monkeypatch):
    assert build_document._default_mineru_upload_mode('http://mineru:8000') is False
    assert build_document._default_mineru_upload_mode('http://localhost:8000') is True
    assert build_document._default_mineru_upload_mode('https://ocr.example.test') is True


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
    monkeypatch.setenv('LAZYMIND_OPENSEARCH_URI', 'https://opensearch.test')
    monkeypatch.setenv('LAZYMIND_OPENSEARCH_USER', 'user')
    monkeypatch.setenv('LAZYMIND_OPENSEARCH_PASSWORD', 'pass')

    config = build_document._build_store_config({'index': 'flat'})

    assert config['vector_store']['kwargs']['uri'] == 'http://milvus.test'
    assert config['vector_store']['kwargs']['index_kwargs'] == {'index': 'flat'}
    assert config['segment_store']['kwargs']['uris'] == 'https://opensearch.test'
    assert config['segment_store']['kwargs']['client_kwargs']['user'] == 'user'
    assert config['segment_store']['kwargs']['client_kwargs']['password'] == 'pass'


def test_build_store_config_raises_for_missing_milvus_uri(monkeypatch):
    # _require_env was removed; build_document now raises ValueError directly
    # when required config values are missing.
    monkeypatch.setitem(build_document._cfg._impl, 'milvus_uri', '')

    with pytest.raises(ValueError, match='LAZYMIND_MILVUS_URI is required'):
        build_document._build_store_config({})


def test_build_pdf_reader_selects_plain_pdf_reader(monkeypatch):
    class FakePDFReader:
        pass

    monkeypatch.setattr(build_document, 'PDFReader', FakePDFReader)
    monkeypatch.setitem(build_document._cfg._impl, 'ocr_server_type', 'none')

    assert isinstance(build_document._build_pdf_reader(), FakePDFReader)


def test_build_pdf_reader_selects_mineru_with_upload_mode(monkeypatch):
    seen = {}

    class FakeMineruPDFReader:
        def __init__(self, **kwargs):
            seen.update(kwargs)

    monkeypatch.setattr(build_document, 'MineruPDFReader', FakeMineruPDFReader)
    monkeypatch.setitem(build_document._cfg._impl, 'ocr_server_type', 'mineru')
    monkeypatch.setitem(build_document._cfg._impl, 'ocr_server_url', 'http://mineru:8000/')
    monkeypatch.setitem(build_document._cfg._impl, 'mineru_upload_mode', '')
    monkeypatch.setitem(build_document._cfg._impl, 'mineru_backend', 'pipeline')

    reader = build_document._build_pdf_reader()

    assert isinstance(reader, FakeMineruPDFReader)
    assert seen['url'] == 'http://mineru:8000'
    assert seen['backend'] == 'pipeline'
    assert seen['upload_mode'] is False
    assert seen['timeout'] == 3600
    assert seen['patch_applied'] == build_document._cfg['ocr_patch_applied']
    assert seen['service_variant'] == build_document._cfg['ocr_service_variant']
    assert seen['image_cache_dir'] == build_document._cfg['image_cache_dir']


def test_build_pdf_reader_selects_paddleocr(monkeypatch):
    seen = {}

    class FakePaddleOCRPDFReader:
        def __init__(self, **kwargs):
            seen.update(kwargs)

    monkeypatch.setattr(build_document, 'PaddleOCRPDFReader', FakePaddleOCRPDFReader)
    monkeypatch.setitem(build_document._cfg._impl, 'ocr_server_type', 'paddleocr')
    monkeypatch.setitem(build_document._cfg._impl, 'ocr_server_url', 'http://paddle.test/')

    assert isinstance(build_document._build_pdf_reader(), FakePaddleOCRPDFReader)
    assert seen == {
        'url': 'http://paddle.test',
        'service_variant': build_document._cfg['ocr_service_variant'],
        'images_dir': build_document._cfg['image_cache_dir'],
    }


def test_build_pdf_reader_rejects_unknown_ocr_type(monkeypatch):
    monkeypatch.setitem(build_document._cfg._impl, 'ocr_server_type', 'unknown')

    with pytest.raises(ValueError, match='Unsupported OCR server type'):
        build_document._build_pdf_reader()


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
    assert [group['name'] for group in docs.node_groups] == ['block', 'line']
    assert 'parent' not in docs.node_groups[0]
    assert docs.node_groups[1]['parent'] == 'block'
    assert docs.activated == [
        ('image', build_document.EMBED_IMAGE),
        ('block', [build_document.EMBED_MAIN]),
        ('line', [build_document.EMBED_MAIN]),
    ]


# ---------------------------------------------------------------------------
# _build_pdf_reader — explicit LAZYMIND_MINERU_UPLOAD_MODE override
# ---------------------------------------------------------------------------

def test_build_pdf_reader_mineru_upload_mode_explicit_true(monkeypatch):
    seen = {}

    class FakeMineruPDFReader:
        def __init__(self, **kwargs):
            seen.update(kwargs)

    monkeypatch.setattr(build_document, 'MineruPDFReader', FakeMineruPDFReader)
    monkeypatch.setitem(build_document._cfg._impl, 'ocr_server_type', 'mineru')
    monkeypatch.setitem(build_document._cfg._impl, 'ocr_server_url', 'http://mineru:8000/')
    monkeypatch.setitem(build_document._cfg._impl, 'mineru_upload_mode', 'true')
    monkeypatch.setitem(build_document._cfg._impl, 'mineru_backend', 'pipeline')

    build_document._build_pdf_reader()

    assert seen['upload_mode'] is True


def test_build_pdf_reader_mineru_upload_mode_explicit_false(monkeypatch):
    seen = {}

    class FakeMineruPDFReader:
        def __init__(self, **kwargs):
            seen.update(kwargs)

    monkeypatch.setattr(build_document, 'MineruPDFReader', FakeMineruPDFReader)
    monkeypatch.setitem(build_document._cfg._impl, 'ocr_server_type', 'mineru')
    monkeypatch.setitem(build_document._cfg._impl, 'ocr_server_url', 'http://localhost:8000/')
    monkeypatch.setitem(build_document._cfg._impl, 'mineru_upload_mode', 'false')
    monkeypatch.setitem(build_document._cfg._impl, 'mineru_backend', 'pipeline')

    build_document._build_pdf_reader()

    # explicit 'false' overrides the default (which would be True for localhost)
    assert seen['upload_mode'] is False


def test_build_pdf_reader_mineru_upload_mode_invalid_raises(monkeypatch):
    import pytest

    monkeypatch.setitem(build_document._cfg._impl, 'ocr_server_type', 'mineru')
    monkeypatch.setitem(build_document._cfg._impl, 'ocr_server_url', 'http://mineru:8000/')
    monkeypatch.setitem(build_document._cfg._impl, 'mineru_upload_mode', 'maybe')

    with pytest.raises(ValueError, match='mineru_upload_mode must be a boolean string'):
        build_document._build_pdf_reader()
