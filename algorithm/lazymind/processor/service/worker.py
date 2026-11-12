import signal
import threading
import inspect
import os
import time
from contextvars import ContextVar

import lazyllm
from lazymind.parsing.service.opensearch_compat import install as install_opensearch_compat
from lazymind.model_config import inject_model_config
install_opensearch_compat()
from lazymind.parsing.service.embedding_input_compat import install as install_embedding_input_compat
install_embedding_input_compat()
lazyllm.inject_model_config = inject_model_config

from lazyllm.tools.rag.parsing_service import DocumentProcessorWorker  # noqa: E402
from lazyllm.tools.rag.doc_node import ImageDocNode, MetadataMode  # noqa: E402
from lazyllm.tools.rag.store import LAZY_IMAGE_GROUP, LAZY_ROOT_NAME  # noqa: E402
from lazymind.config import config as _cfg  # noqa: E402
from lazymind.processor.service.db import require_shared_db_config  # noqa: E402


_TEXT_REEMBED_GROUPS = frozenset(('block', 'line', 'code', 'doc-summary'))


def _has_verified_image_only_index(loaded, processor, doc_id, kb_id):
    if not isinstance(loaded, dict):
        return False
    image_roots = loaded.get(LAZY_IMAGE_GROUP) or []
    if not image_roots or any(nodes for group, nodes in loaded.items() if group != LAZY_IMAGE_GROUP):
        return False
    if not all(isinstance(node, ImageDocNode) and node.metadata.get('is_pure_image') is True
               for node in image_roots):
        return False
    try:
        retained = processor._store.get_nodes(group='image', doc_ids=[doc_id], kb_id=kb_id)
    except Exception:
        return False
    return bool(retained) and all((node.embedding or {}).get('embed_image') for node in retained)


def _install_deferred_group_guard(processor):
    if getattr(processor, '_lazymind_deferred_group_guard', False):
        return processor
    original = processor._reparse_docs
    full_rebuild = ContextVar('lazymind_full_rebuild', default=False)

    def reparse_docs(group_name, node_groups, *args, **kwargs):
        # Full source rebuild follows initial ingestion: summaries and other
        # lazy groups remain deferred. Explicitly selecting one still creates it.
        if (full_rebuild.get() and group_name is not None
                and node_groups.get(group_name, {}).get('lazy_mode') == 'all'):
            return
        token = full_rebuild.set(full_rebuild.get() or group_name is None)
        try:
            return original(group_name, node_groups, *args, **kwargs)
        finally:
            full_rebuild.reset(token)

    processor._reparse_docs = reparse_docs
    processor._lazymind_deferred_group_guard = True
    return processor


def _install_text_reembed_guard(processor):
    if getattr(processor, '_lazymind_text_reembed_guard', False):
        return processor
    original = processor._reembed_group

    def reembed_group(group_name, node_groups, doc_ids=None, kb_id=None):
        if group_name not in _TEXT_REEMBED_GROUPS:
            return original(group_name, node_groups, doc_ids=doc_ids, kb_id=kb_id)
        embed_keys = processor._store._group_embed_keys.get(group_name) or set()
        if 'embed_main' not in embed_keys:
            raise ValueError(f'Node group {group_name!r} has no embed_main key for text reembedding')
        nodes = processor._store.get_nodes(group=group_name, doc_ids=doc_ids, kb_id=kb_id)
        for node in nodes:
            if node.embedding:
                node.embedding = {key: value for key, value in node.embedding.items() if key != 'embed_main'}
            node.embedding_state.discard('embed_main')
        if nodes:
            processor._store.update_nodes(nodes)
        # A parent may have no segments while its child still has retained
        # segments; continue into children even when this group was empty.
        for child in processor._store.activated_groups():
            cfg = node_groups.get(child, {})
            if cfg.get('parent') == group_name and cfg.get('lazy_mode') != 'all':
                processor._reembed_group(child, node_groups, doc_ids=doc_ids, kb_id=kb_id)

    processor._reembed_group = reembed_group
    processor._lazymind_text_reembed_guard = True
    return processor


def _rebuild_empty_text_documents(processor, payload, node_groups, reader):
    if payload.get('strategy') != 'reembed' or payload.get('processing_level', 'indexed') != 'indexed':
        return
    kb_id = payload.get('kb_id')
    for info in payload.get('file_infos') or []:
        doc_id = info.get('doc_id')
        if not doc_id:
            raise RuntimeError('text vector rebuild requires a document ID')
        if any(node.get_text(MetadataMode.EMBED).strip()
               for group in _TEXT_REEMBED_GROUPS if group in node_groups
               for node in processor._store.get_nodes(group=group, doc_ids=[doc_id], kb_id=kb_id)
               if not node.is_null_node):
            continue
        roots = processor._store.get_nodes(group=LAZY_ROOT_NAME, doc_ids=[doc_id], kb_id=kb_id)
        if not roots:
            path = info.get('file_path')
            if not path or not os.path.isfile(path) or reader is None:
                raise RuntimeError(f'document {doc_id!r} has no retained text nodes or available original; '
                                   'manual rebuild is required')
            _, metadatas, _ = processor._prepare_doc_inputs([path], [doc_id], [info.get('metadata') or {}], kb_id)
            try:
                loaded = reader.load_data([path], metadatas, split_nodes_by_type=True)
            except Exception as exc:
                raise RuntimeError(f'document {doc_id!r} could not be checked for text roots; '
                                   'manual rebuild is required') from exc
            if not isinstance(loaded, dict):
                raise RuntimeError(f'document {doc_id!r} has an unrecognized reader result; manual rebuild is required')
            if _has_verified_image_only_index(loaded, processor, doc_id, kb_id):
                continue
            roots = loaded.get(LAZY_ROOT_NAME) or []
            if roots:
                # Restore only text roots. A full reparse would remove and
                # regenerate image nodes, which this text-vector rebuild must
                # leave untouched.
                processor._store.update_nodes(
                    processor._set_nodes_number(roots), skip_embed_groups=set(node_groups) | set(loaded))
        if not roots:
            raise RuntimeError(f'document {doc_id!r} has no text roots; manual rebuild is required')
        for group in ('block', 'code'):
            if group in node_groups:
                processor.reparse(
                    group_name=group, node_groups=node_groups, doc_ids=[doc_id], kb_id=kb_id,
                    processing_level='chunked', strategy='slice_missing',
                )
        if not any(node.get_text(MetadataMode.EMBED).strip()
                   for group in _TEXT_REEMBED_GROUPS if group in node_groups
                   for node in processor._store.get_nodes(group=group, doc_ids=[doc_id], kb_id=kb_id)
                   if not node.is_null_node):
            raise RuntimeError(f'document {doc_id!r} produced no text nodes; manual rebuild is required')


# The underlying worker runs in a serialized ServerModule subprocess. Keep
# this override on its class so request isolation survives that process hop.
_BaseDocumentWorkerImpl = DocumentProcessorWorker._Impl
if getattr(_BaseDocumentWorkerImpl, '_lazymind_isolated', False):
    _BaseDocumentWorkerImpl = _BaseDocumentWorkerImpl.__mro__[1]
if not getattr(DocumentProcessorWorker._Impl, '_lazymind_isolated', False):
    class _IsolatedDocumentWorkerImpl(_BaseDocumentWorkerImpl):
        _lazymind_isolated = True

        def _get_processor(self):
            # ServerModule can reconstruct this class in a fresh subprocess.
            # Install there as well, before any retained segment is written.
            install_opensearch_compat()
            from lazymind.parsing.service.embedding_input_compat import install
            install()
            from lazymind.parsing.service.embedding_reset import index_state, IndexBusy
            state = index_state()
            expected = getattr(self, '_lazymind_task_generation', None)
            # Drop stale work before constructing a processor: chunk-only reparses
            # also delete vectors. Missing generations from legacy reparses are
            # unsafe after the first index transition.
            stale = expected is not None and expected != state['generation']
            legacy_reparse = (getattr(self, '_lazymind_task_reparse', False)
                              and expected is None and state['generation'] > 0)
            if stale or legacy_reparse:
                raise IndexBusy('obsolete document task discarded after index generation changed')
            if (getattr(self, '_lazymind_task_indexes', True)
                    and state['phase'] in ('clearing', 'cleanup_failed', 'cleared', 'detached')):
                raise IndexBusy('document task belongs to an unavailable index generation')
            if getattr(self, '_lazymind_store_generation', None) != state['generation']:
                self._processor = None
                self._lazymind_store_generation = state['generation']
            processor = _BaseDocumentWorkerImpl._get_processor(self)
            if state['generation'] > 0 and not getattr(processor, '_lazymind_generation_initialized', False):
                # Registered algorithms can contain a pickled store from the old
                # generation. Recreate connections and infer the new dimensions.
                from lazymind.parsing.service.build_document import _build_store_config
                from lazymind.config import EMBED_INDEX_KWARGS
                from lazyllm.tools.rag.store.document_store import _DocumentStore
                old = processor._store
                fresh = _DocumentStore(_build_store_config(EMBED_INDEX_KWARGS),
                                       group_embed_keys=old._group_embed_keys, embed=old._embed,
                                       embed_dims={}, embed_datatypes={},
                                       global_metadata_desc=old._global_metadata_desc)
                fresh.activate_group(old.activated_groups())
                processor._store = fresh
                processor._lazymind_generation_initialized = True
            # Reembedding reads retained vectors before its first write. Initialize
            # the vector connection now rather than letting HybridStore.get hit a
            # freshly deserialized Milvus store without a client pool.
            if getattr(self, '_lazymind_task_indexes', True) and hasattr(processor._store, '_seg_init'):
                processor._store._seg_init()
                processor._store._vec_init()
            return _install_deferred_group_guard(_install_text_reembed_guard(processor))

        def _exec_reparse_task(self, processor, task_id, payload, node_groups, name_to_id, reader):
            # The document service expands an all-groups selection into names,
            # including the source root. Preserve full rebuild semantics: otherwise
            # the base worker drops that root and only reslices stale source nodes.
            from lazyllm.tools.rag.store import LAZY_ROOT_NAME
            if (payload.get('strategy', 'rebuild') == 'rebuild'
                    and LAZY_ROOT_NAME in (payload.get('ng_names') or [])):
                payload = {**payload, 'ng_names': None}
            if payload.get('strategy', 'rebuild') == 'rebuild' and payload.get('processing_level') != 'parsed':
                # A freshly deserialized store has no local initialization flag,
                # even when remote segments/vectors exist. Full rebuild must
                # connect before deletion; otherwise remove_nodes silently skips
                # old content and the subsequent empty-store check never passes.
                store = processor._store
                if hasattr(store, '_seg_init'):
                    store._seg_init()
                    store._vec_init()
            _rebuild_empty_text_documents(processor, payload, node_groups, reader)
            return _BaseDocumentWorkerImpl._exec_reparse_task(
                self, processor, task_id, payload, node_groups, name_to_id, reader)

        def _worker_impl(self):
            # Queue acknowledgements and the index fence also use PostgreSQL.
            # An outage there can escape the base task handler. Keep consuming
            # after recovery; the queue lease owns retrying interrupted tasks.
            while not self._shutdown:
                try:
                    return _BaseDocumentWorkerImpl._worker_impl(self)
                except Exception as exc:
                    lazyllm.LOG.error(f'Document worker loop interrupted ({type(exc).__name__}); retrying')
                    if not self._shutdown:
                        time.sleep(5)

        def _run_task(self, task_id, task_type, payload, from_queue):
            lazyllm.globals['config'].clear()
            lazyllm.inject_model_config = inject_model_config
            from lazymind.parsing.service.embedding_reset import index_lock
            self._lazymind_task_reparse = task_type == 'DOC_REPARSE'
            self._lazymind_task_indexes = payload.get('processing_level', 'indexed') == 'indexed'
            self._lazymind_task_generation = (payload.get('llm_config') or {}).get('_embedding_generation')
            with index_lock():
                return _BaseDocumentWorkerImpl._run_task(self, task_id, task_type, payload, from_queue)

    DocumentProcessorWorker._Impl = _IsolatedDocumentWorkerImpl


def _parse_high_priority_task_types(raw: str | None) -> list[str] | None:
    if raw is None or not raw.strip():
        return None
    return [item.strip() for item in raw.split(',') if item.strip()]


db_config = require_shared_db_config('DocumentProcessorWorker')
worker_kwargs = {
    'port': _cfg['document_worker_port'],
    'db_config': db_config,
    'num_workers': _cfg['document_worker_num_workers'],
    'lease_duration': float(_cfg['document_worker_lease_duration']),
    'lease_renew_interval': float(_cfg['document_worker_lease_renew_interval']),
    'high_priority_task_types': _parse_high_priority_task_types(_cfg['document_worker_high_priority_task_types']),
    'high_priority_only': _cfg['document_worker_high_priority_only'],
    'poll_mode': _cfg['document_worker_poll_mode'],
}
supported_params = set(inspect.signature(DocumentProcessorWorker).parameters)
doc_processor_worker = DocumentProcessorWorker(
    **{key: value for key, value in worker_kwargs.items() if key in supported_params}
)

_shutdown_event = threading.Event()


def _on_signal(signum, frame):
    _shutdown_event.set()
    try:
        doc_processor_worker.stop()
    except Exception:
        pass


if __name__ == '__main__':
    signal.signal(signal.SIGTERM, _on_signal)
    signal.signal(signal.SIGINT, _on_signal)
    doc_processor_worker.start()
    try:
        doc_processor_worker.wait()
    except KeyboardInterrupt:
        pass
    # Keep process alive; wait() may return immediately with some launcher configs
    _shutdown_event.wait()
