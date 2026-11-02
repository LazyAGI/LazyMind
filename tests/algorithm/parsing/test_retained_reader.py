import pytest

from lazyllm.module.module import ModuleExecutionError
from lazyllm.tools.rag.dataReader import SimpleDirectoryReader
from lazyllm.tools.rag.doc_node import DocNode
from lazyllm.tools.rag.readers.reader_config_inject import inject_reader_config
from lazymind.common import base_ingestion
from lazymind.parsing.engine.readers.retained import RetainedReader
from lazymind.parsing.service import build_document


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
