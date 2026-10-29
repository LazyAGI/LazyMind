from types import SimpleNamespace

import pytest

from lazyllm.module.module import ModuleExecutionError
from lazyllm.tools.rag.doc_node import DocNode
from lazyllm.tools.rag.readers.readerBase import LazyLLMReaderBase

from lazymind.common.base_ingestion import prepare_document
from lazymind.parsing.engine.readers.retained import RetainedReader
from lazymind.parsing.service.build_document import _read_native_text


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
