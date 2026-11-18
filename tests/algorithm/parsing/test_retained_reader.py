from types import SimpleNamespace

import pytest

from lazyllm.module.module import ModuleExecutionError
from lazyllm.tools.rag.dataReader import SimpleDirectoryReader
from lazyllm.tools.rag.doc_node import DocNode
from lazyllm.tools.rag.readers.readerBase import LazyLLMReaderBase
from lazyllm.tools.rag.readers.reader_config_inject import inject_reader_config
from lazymind.common import base_ingestion
from lazymind.common.base_ingestion import prepare_document
from lazymind.parsing.engine.readers.retained import RetainedReader
from lazymind.parsing.service import build_document
from lazymind.parsing.service.build_document import _read_native_text


@pytest.fixture
def local_readers(monkeypatch):
    readers = {}

    class Registry:
        def add_reader(self, pattern, reader):
            readers[pattern] = reader

    monkeypatch.setitem(build_document._cfg._impl, 'runtime_mode', 'local')
    monkeypatch.setattr(build_document, '_build_pdf_reader', lambda: object())
    build_document._register_document_readers(Registry())
    return readers


def load_document(path, readers):
    errors = []
    nodes = SimpleDirectoryReader.load_file(
        path,
        metadata_genf=lambda _: {'source': 'generated', 'generated': 'kept'},
        file_extractor=readers,
        metadata={'source': 'upload', 'dataset_id': 'reader-regression'},
        load_errors=errors,
    )
    assert not errors, errors
    assert nodes
    assert all(isinstance(node, DocNode) for node in nodes)
    for node in nodes:
        assert node.global_metadata['source'] == 'upload'
        assert node.global_metadata['generated'] == 'kept'
        assert node.global_metadata['dataset_id'] == 'reader-regression'
    return '\n'.join(node.text for node in nodes)


@pytest.mark.parametrize('suffix,content', [
    ('.txt', 'GINKGO-7391 青瓷海豚七号 😀\n第二行'),
    ('.md', '# Test\n\nGINKGO-7391 青瓷海豚七号'),
    ('.csv', 'project,phrase\nGINKGO-7391,青瓷海豚七号\n'),
])
def test_local_registered_readers_parse_without_retained_artifact(tmp_path, local_readers, suffix, content):
    source = tmp_path / ('upload' + suffix)
    source.write_text(content, encoding='utf-8')
    assert base_ingestion.load_artifact(source) is None
    text = load_document(source, local_readers)
    assert 'GINKGO-7391' in text
    assert '青瓷海豚七号' in text


def test_local_txt_reader_reparses_changed_source(tmp_path, local_readers):
    source = tmp_path / 'changed.txt'
    source.write_text('original content', encoding='utf-8')
    base_ingestion.prepare_document(source, tmp_path)
    source.write_text('GINKGO-7391 changed source', encoding='utf-8')
    assert base_ingestion.load_artifact(source) is None
    assert load_document(source, local_readers) == 'GINKGO-7391 changed source'


def test_retained_roots_keep_metadata_without_reparsing(tmp_path):
    source = tmp_path / 'retained.txt'
    source.write_text('retained text', encoding='utf-8')
    artifact = base_ingestion.prepare_document(source, tmp_path)
    reader = RetainedReader(lambda *args, **kwargs: pytest.fail('must reuse retained roots'))
    nodes = reader(source, extra_info={'source': 'upload'}, use_cache=False)
    assert nodes[0].text == 'retained text'
    assert nodes[0].metadata['source'] == 'upload'
    assert base_ingestion.load_artifact(source) == artifact


def test_explicit_ocr_metadata_and_filesystem_reach_fallback(tmp_path):
    source = tmp_path / 'document.pdf'
    source.write_bytes(b'OCR route fixture; not parsed by a PDF decoder')
    info = {'source': 'upload', 'ocr_type': 'mineru'}
    filesystem = object()
    calls = []
    expected = [DocNode(text='OCR result')]

    def fallback(file, *, extra_info, fs):
        calls.append((file, extra_info, fs))
        return expected

    try:
        inject_reader_config(ocr_config={'ocr_type': 'mineru'})
        assert RetainedReader(fallback)(source, extra_info=info, fs=filesystem, use_cache=False) == expected
        assert calls == [(source, info, filesystem)]
    finally:
        inject_reader_config(ocr_config={})


def test_fallback_errors_are_not_suppressed_or_retried(tmp_path):
    calls = []

    def fallback(file, **kwargs):
        calls.append(file)
        raise TypeError('decoder failure')

    source = tmp_path / 'broken.txt'
    with pytest.raises(ModuleExecutionError, match='decoder failure'):
        RetainedReader(fallback)(source, use_cache=False)
    assert calls == [source]


@pytest.mark.parametrize('suffix,content', [
    ('.md', '# Heading\nMarkdown body'), ('.txt', 'Plain text body'),
    ('.xml', '<root>XML body</root>'), ('.csv', 'name,value\nentry,42\n'),
    ('.json', '{"name": "JSON body"}'),
])
@pytest.mark.parametrize('extra_info', [None, {'dataset': 'market', 'source': 'download'}])
def test_real_native_fallback_without_retained_artifact(tmp_path, suffix, content, extra_info):
    source = tmp_path / ('source' + suffix)
    source.write_text(content, encoding='utf-8')
    reader = RetainedReader(_read_native_text)
    nodes = reader(source, extra_info=extra_info)
    assert nodes and all(isinstance(node, DocNode) for node in nodes)
    assert any('body' in node.text or 'entry' in node.text for node in nodes)
    for node in nodes:
        for key, value in (extra_info or {}).items():
            assert node.metadata[key] == value


def test_direct_reader_without_extra_info_keeps_reader_metadata(tmp_path):
    class StrictReader(LazyLLMReaderBase):
        def _load_data(self, file, *, fs=None):
            assert fs == 'filesystem-marker'
            return [DocNode(text='body', metadata={'source': 'reader'})]

    source = tmp_path / 'source.txt'
    source.write_text('body', encoding='utf-8')
    metadata = {'source': 'caller', 'dataset': 'market'}
    nodes = RetainedReader(StrictReader())(source, extra_info=metadata, fs='filesystem-marker')
    assert nodes[0].metadata == {'source': 'reader', 'dataset': 'market'}
    assert metadata == {'source': 'caller', 'dataset': 'market'}


@pytest.mark.parametrize('cached', [False, True])
def test_metadata_precedence_matches_retained_artifact(tmp_path, cached):
    source = tmp_path / 'source.txt'
    source.write_text('body', encoding='utf-8')
    if cached:
        prepare_document(source, tmp_path)
    metadata = {'dataset': 'market'}
    nodes = RetainedReader(_read_native_text)(source, extra_info=metadata)
    assert nodes[0].text == 'body'
    assert nodes[0].metadata['dataset'] == 'market'
    assert metadata == {'dataset': 'market'}


def test_explicit_extra_info_is_forwarded_to_ocr_despite_retained_artifact(tmp_path, monkeypatch):
    source = tmp_path / 'source.pdf'
    source.write_bytes(b'fake PDF')
    monkeypatch.setattr('lazymind.parsing.engine.readers.retained.lazyllm',
                        SimpleNamespace(globals=SimpleNamespace(config={'dynamic_ocr_configs': {'ocr_type': 'mineru'}})))
    monkeypatch.setattr('lazymind.parsing.engine.readers.retained.load_artifact',
                        lambda _: pytest.fail('explicit OCR must bypass native retained text'))
    metadata = {'ocr_type': 'mineru', 'dataset': 'market'}

    class OcrReader(LazyLLMReaderBase):
        def _load_data(self, file, extra_info=None, **kwargs):
            assert extra_info == metadata
            return [DocNode(text='OCR body')]

    nodes = RetainedReader(OcrReader())(source, extra_info=metadata)
    assert nodes[0].text == 'OCR body'
    assert nodes[0].metadata['dataset'] == 'market'


def test_stale_retained_artifact_reads_updated_markdown(tmp_path):
    source = tmp_path / 'source.md'
    source.write_text('original body', encoding='utf-8')
    prepare_document(source, tmp_path)
    source.write_text('# Changed\nupdated body', encoding='utf-8')
    nodes = RetainedReader(_read_native_text)(source)
    assert 'updated body' in nodes[0].text
    assert 'original body' not in nodes[0].text


def test_fallback_internal_typeerror_is_not_retried(tmp_path):
    source = tmp_path / 'source.txt'
    source.write_text('body', encoding='utf-8')
    calls = []

    def broken(file, extra_info=None):
        calls.append(file)
        raise TypeError('reader implementation failed')

    with pytest.raises(ModuleExecutionError, match='reader implementation failed'):
        RetainedReader(broken)(source, extra_info={'dataset': 'market'})
    assert len(calls) == 1
