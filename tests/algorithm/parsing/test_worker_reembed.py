import importlib

import pytest
from PIL import Image

from lazyllm.tools.rag.doc_node import DocNode, ImageDocNode
from lazyllm.tools.rag.global_metadata import RAG_DOC_ID
from lazyllm.tools.rag.parsing_service.impl import _Processor
from lazyllm.tools.rag.store import LAZY_IMAGE_GROUP, LAZY_ROOT_NAME
from lazyllm.tools.rag.utils import parallel_do_embedding


TEXT_GROUPS = ('block', 'line', 'code', 'doc-summary')


class _Store:
    def __init__(self):
        self.nodes = {
            'block': [DocNode(text='paragraph', group='block', embedding={'embed_main': [1.0]},
                              global_metadata={RAG_DOC_ID: 'doc'})],
            'line': [DocNode(text='sentence', group='line', embedding={'embed_main': [1.0]},
                             global_metadata={RAG_DOC_ID: 'doc'})],
            'code': [],
            'doc-summary': [],
            'image': [DocNode(text='image', group='image', embedding={'embed_image': [9.0]},
                              global_metadata={RAG_DOC_ID: 'doc'})],
        }
        self._group_embed_keys = {name: {'embed_main'} for name in TEXT_GROUPS}
        self._group_embed_keys['image'] = {'embed_image'}
        self.embedding_inputs = []
        self.updated = []

    def get_nodes(self, *, group, doc_ids=None, kb_id=None):
        nodes = self.nodes.get(group, [])
        return [node for node in nodes if not doc_ids or node.global_metadata[RAG_DOC_ID] in doc_ids]

    def update_nodes(self, nodes, skip_embed_groups=None):
        self.updated.extend(node.group for node in nodes)
        if skip_embed_groups:
            for node in nodes:
                self.nodes.setdefault(node.group, []).append(node)
            return

        def embed(value):
            self.embedding_inputs.append(value)
            return [2.0]

        parallel_do_embedding({'embed_main': embed}, [], nodes, self._group_embed_keys)

    def activated_groups(self):
        return list(self.nodes)


def _node_groups():
    return {
        'block': {'parent': LAZY_ROOT_NAME},
        'line': {'parent': 'block'},
        'code': {'parent': LAZY_ROOT_NAME},
        'doc-summary': {'parent': LAZY_ROOT_NAME, 'lazy_mode': 'all'},
        'image': {'parent': LAZY_IMAGE_GROUP, 'lazy_mode': 'embed'},
    }


def _worker_with_processor(monkeypatch, processor):
    from lazymind.config import config

    monkeypatch.setitem(config._impl, 'database_url', 'sqlite:///:memory:')
    from lazymind.parsing.service import embedding_reset
    monkeypatch.setattr(embedding_reset, 'index_state', lambda: {'generation': 0, 'phase': 'ready'})
    worker = importlib.import_module('lazymind.processor.service.worker')
    base_impl = worker.DocumentProcessorWorker._Impl.__mro__[1]
    monkeypatch.setattr(base_impl, '_get_processor', lambda _self: processor)
    return object.__new__(worker.DocumentProcessorWorker._Impl)


def test_worker_reembed_replaces_old_text_vectors_skips_empty_groups_and_preserves_image(monkeypatch):
    store = _Store()
    processor = _Processor(store)
    worker = _worker_with_processor(monkeypatch, processor)
    try:
        processor = worker._get_processor()
        for group in ('block', 'code', 'doc-summary'):
            processor.reparse(group, _node_groups(), doc_ids=['doc'], kb_id='kb', strategy='reembed')
        assert store.updated == ['block', 'line']
        assert len(store.embedding_inputs) == 2
        assert store.nodes['block'][0].embedding['embed_main'] == [2.0]
        assert store.nodes['line'][0].embedding['embed_main'] == [2.0]
        assert store.nodes['image'][0].embedding == {'embed_image': [9.0]}
    finally:
        processor._thread_pool.shutdown(wait=False)


def test_worker_reembed_visits_retained_child_when_parent_group_is_empty(monkeypatch):
    store = _Store()
    store.nodes['block'] = []
    processor = _Processor(store)
    worker = _worker_with_processor(monkeypatch, processor)
    try:
        worker._get_processor().reparse('block', _node_groups(), doc_ids=['doc'], kb_id='kb', strategy='reembed')
        assert store.updated == ['line']
        assert store.nodes['line'][0].embedding['embed_main'] == [2.0]
    finally:
        processor._thread_pool.shutdown(wait=False)


def test_worker_reembed_without_text_requires_original_or_manual_rebuild(monkeypatch, tmp_path):
    store = _Store()
    store.nodes['block'] = []
    store.nodes['line'] = []
    processor = _Processor(store)
    worker = _worker_with_processor(monkeypatch, processor)
    try:
        base_impl = type(worker).__mro__[1]
        ordinary_calls = []
        monkeypatch.setattr(base_impl, '_exec_reparse_task',
                            lambda *_args, **_kwargs: ordinary_calls.append(True))
        payload = {'strategy': 'reembed', 'processing_level': 'indexed', 'kb_id': 'kb',
                   'file_infos': [{'doc_id': 'doc', 'file_path': str(tmp_path / 'missing.txt'), 'metadata': {}}],
                   'ng_names': list(TEXT_GROUPS)}
        with pytest.raises(RuntimeError, match='rebuild'):
            worker._exec_reparse_task(processor, 'task', payload, _node_groups(), {}, reader=object())

        original = tmp_path / 'original.txt'
        original.write_text('source')
        rebuilt = []

        class Reader:
            text_root = True
            pure_image = True

            def load_data(self, _paths, _metadatas, *, split_nodes_by_type):
                assert split_nodes_by_type
                text_roots = ([DocNode(text='source', group=LAZY_ROOT_NAME,
                                       global_metadata={RAG_DOC_ID: 'doc'})] if self.text_root else [])
                return {
                    LAZY_ROOT_NAME: text_roots,
                    LAZY_IMAGE_GROUP: [
                        ImageDocNode(
                            image_path=str(original),
                            group=LAZY_IMAGE_GROUP,
                            metadata={'is_pure_image': self.pure_image},
                        ),
                    ],
                }

        reader = Reader()

        def rebuild(*args, **kwargs):
            rebuilt.append((args, kwargs))
            if kwargs['group_name'] == 'block':
                store.nodes['block'] = [
                    DocNode(text='rebuilt', group='block', global_metadata={RAG_DOC_ID: 'doc'}),
                ]

        monkeypatch.setattr(processor, 'reparse', rebuild)
        payload['file_infos'][0]['file_path'] = str(original)
        worker._exec_reparse_task(processor, 'task', payload, _node_groups(), {}, reader=reader)
        assert [item[1]['group_name'] for item in rebuilt] == ['block', 'code']
        assert all(item[1]['strategy'] == 'slice_missing' for item in rebuilt)
        assert all(item[1]['processing_level'] == 'chunked' for item in rebuilt)
        assert len(store.nodes[LAZY_ROOT_NAME]) == 1
        assert store.nodes['image'][0].embedding == {'embed_image': [9.0]}
        assert ordinary_calls == [True]

        payload['strategy'] = 'rebuild'
        payload['file_infos'][0]['file_path'] = str(tmp_path / 'missing.txt')
        worker._exec_reparse_task(processor, 'task', payload, _node_groups(), {}, reader=object())
        assert ordinary_calls == [True, True]
    finally:
        processor._thread_pool.shutdown(wait=False)


def test_worker_image_only_reembed_succeeds_without_rewriting_image(monkeypatch, tmp_path):
    store = _Store()
    store.nodes['block'] = []
    store.nodes['line'] = []
    processor = _Processor(store)
    worker = _worker_with_processor(monkeypatch, processor)
    original = tmp_path / 'image.png'
    Image.new('RGB', (1, 1)).save(original)

    class Reader:
        pure_image = True

        def load_data(self, _paths, _metadatas, *, split_nodes_by_type):
            assert split_nodes_by_type
            return {LAZY_IMAGE_GROUP: [ImageDocNode(
                image_path=str(original), group=LAZY_IMAGE_GROUP,
                metadata={'is_pure_image': self.pure_image},
            )]}

    reader = Reader()
    payload = {'strategy': 'reembed', 'processing_level': 'indexed', 'kb_id': 'kb',
               'file_infos': [{'doc_id': 'doc', 'file_path': str(original), 'metadata': {}}],
               'ng_names': list(TEXT_GROUPS)}
    try:
        ordinary_calls = []
        base_impl = type(worker).__mro__[1]
        monkeypatch.setattr(base_impl, '_exec_reparse_task',
                            lambda *_args, **_kwargs: ordinary_calls.append(True))

        worker._exec_reparse_task(processor, 'task', payload, _node_groups(), {}, reader=reader)
        assert ordinary_calls == [True]
        assert store.updated == []
        assert store.nodes['image'][0].embedding == {'embed_image': [9.0]}

        reader.pure_image = False
        with pytest.raises(RuntimeError, match='manual rebuild'):
            worker._exec_reparse_task(processor, 'task', payload, _node_groups(), {}, reader=reader)
        reader.pure_image = True
        store.nodes['image'][0].embedding = {}
        with pytest.raises(RuntimeError, match='manual rebuild'):
            worker._exec_reparse_task(processor, 'task', payload, _node_groups(), {}, reader=reader)
        payload['file_infos'][0]['file_path'] = str(tmp_path / 'missing.png')
        with pytest.raises(RuntimeError, match='manual rebuild'):
            worker._exec_reparse_task(processor, 'task', payload, _node_groups(), {}, reader=reader)
        assert ordinary_calls == [True]
    finally:
        processor._thread_pool.shutdown(wait=False)
