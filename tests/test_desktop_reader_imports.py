"""The release-wheel backport must preserve exports without eager RAG imports."""

import importlib.util
from pathlib import Path
from types import SimpleNamespace

import pytest

ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location('reader_patch', ROOT / 'desktop/scripts/patch-reader-imports.py')
patcher = importlib.util.module_from_spec(spec)
spec.loader.exec_module(patcher)


def test_patch_defers_ingestion_but_preserves_reader_and_alias_exports():
    original = '''from lazyllm.thirdparty import check_dependency_by_group
check_dependency_by_group('rag')
from .document import Document
from .readers import PDFReader, inject_reader_config
from .global_metadata import GlobalMetadataDesc as DocField
from .dataReader import SimpleDirectoryReader
add_post_action_for_default_reader = SimpleDirectoryReader.add_post_action_for_default_reader
__all__ = ['Document', 'PDFReader', 'inject_reader_config', 'DocField', 'add_post_action_for_default_reader']
'''
    patched = patcher.patch_source(original)
    namespace = {'__name__': 'lazyllm.tools.rag'}
    exec(patched, namespace)
    calls = []

    def load(name, package):
        calls.append(name)
        assert package == 'lazyllm.tools.rag'
        return SimpleNamespace(PDFReader='reader', GlobalMetadataDesc='field', Document='document')

    namespace['import_module'] = load
    assert not calls
    assert namespace['__getattr__']('PDFReader') == 'reader'
    assert calls == ['.readers']
    assert namespace['__getattr__']('DocField') == 'field'
    assert namespace['__getattr__']('Document') == 'document'
    assert patcher.patch_source(patched) == patched


def test_patch_rejects_unknown_initializer():
    with pytest.raises(RuntimeError, match='Unrecognized'):
        patcher.patch_source('print("unknown version")')


def test_patch_parsing_service_preserves_exports_without_importing_workers():
    original = '''from .impl import _Processor
from .server import DocumentProcessor
from .worker import DocumentProcessorWorker
__all__ = ['_Processor', 'DocumentProcessor', 'DocumentProcessorWorker']
'''
    patched = patcher.patch_source(original, parsing_service=True)
    namespace = {'__name__': 'lazyllm.tools.rag.parsing_service'}
    exec(patched, namespace)
    assert 'DocumentProcessorWorker' not in namespace
    calls = []

    def load(name, package):
        calls.append((name, package))
        return SimpleNamespace(DocumentProcessorWorker='worker')

    namespace['import_module'] = load
    assert namespace['__getattr__']('DocumentProcessorWorker') == 'worker'
    assert calls == [('.worker', 'lazyllm.tools.rag.parsing_service')]
    assert patcher.patch_source(patched, parsing_service=True) == patched
