from types import SimpleNamespace
from unittest.mock import MagicMock

import cloudpickle
import pytest
from fastapi import HTTPException
from lazyllm.tools.rag.parsing_service.server import DocumentProcessor
from lazyllm.tools.rag.store import HybridStore, MapStore
from lazyllm.tools.rag.store.document_store import _DocumentStore
from lazyllm.tools.rag.store.segment.sqlite_store import SQLiteStore

from lazymind.processor.service.chunks import install_chunk_preview, list_doc_chunks_data


@pytest.mark.parametrize('hybrid', [False, True])
def test_chunk_preview_reads_text_without_initialized_vectors(tmp_path, hybrid):
    segments = SQLiteStore(str(tmp_path / 'segments.db'))
    vectors = MapStore()
    vectors.connect = MagicMock(side_effect=AssertionError('preview must not initialize vectors'))
    vectors.get = MagicMock(side_effect=AssertionError('preview must not read vectors'))
    backend = HybridStore(segments, vectors) if hybrid else segments
    store = _DocumentStore(backend)
    store.activate_group('chunks')
    store.seg_impl
    collection = store._gen_collection_name('chunks')
    segments.upsert(collection, [
        {'uid': f'n{i}', 'doc_id': 'd1', 'kb_id': 'kb1', 'group': 'chunks',
         'content': f'text {i}', 'number': i} for i in [3, 1, 2]
    ] + [{'uid': 'other', 'doc_id': 'd2', 'kb_id': 'kb1', 'content': 'other document', 'number': 0}])
    processor = object.__new__(DocumentProcessor._Impl)
    processor._get_algo = lambda _: {'node_groups': {'chunks': {}}}
    processor._get_or_init_store = lambda *args, **kwargs: store
    install_chunk_preview(SimpleNamespace(_raw_impl=processor))
    try:
        result = processor._list_doc_chunks_data('algo', 'kb1', 'd1', 'chunks', offset=1, limit=1)
        assert [item['content'] for item in result['items']] == ['text 2']
        assert (result['total'], result['offset'], result['page_size']) == (3, 1, 1)
        assert store._impl is backend
        vectors.connect.assert_not_called()
        vectors.get.assert_not_called()
        with pytest.raises(HTTPException) as error:
            processor._list_doc_chunks_data('algo', 'kb1', 'd1', 'missing')
        assert error.value.status_code == 400
    finally:
        segments._open_conn().close()


def test_chunk_preview_binding_survives_server_serialization():
    processor = SimpleNamespace(_raw_impl=object.__new__(DocumentProcessor._Impl))
    install_chunk_preview(processor)
    restored = cloudpickle.loads(cloudpickle.dumps(processor._raw_impl))
    assert restored._list_doc_chunks_data.__self__ is restored
    assert restored._list_doc_chunks_data.__func__ is list_doc_chunks_data
