from urllib.parse import urlparse

import lazyllm
from lazyllm.tracing import set_trace_context
from lazyllm import AutoModel
from lazyllm.tools.rag import Document, MineruPDFReader, PDFReader
from lazyllm.tools.rag.doc_impl import NodeGroupType
from lazyllm.tools.rag.parsing_service import DocumentProcessor
from lazyllm.tools.rag.readers import PaddleOCRPDFReader

from lazymind.model_config import get_dynamic_role_slot_map
from lazymind.config import EMBED_IMAGE, EMBED_INDEX_KWARGS, EMBED_KEYS, EMBED_MAIN, config as _cfg
from lazymind.parsing.engine.readers import ImageEmbReader, VideoReader
from lazymind.parsing.engine.transform import GeneralParser, LineSplitter, NodeParser

ALGO_ID = 'general_algo'


def _quiet_trace(kbs):
    def call(kb_group, *args, **kwargs):
        set_trace_context({'enabled': False})
        return kbs[kb_group](*args, **kwargs)
    return call


def _parse_bool_config(value: str | None) -> bool | None:
    if value is None:
        return None
    value = value.strip().lower()
    if value == '':
        return None
    if value in ('1', 'true', 'yes', 'on'):
        return True
    if value in ('0', 'false', 'no', 'off'):
        return False
    raise ValueError(f'mineru_upload_mode must be a boolean string, got: {value!r}')


def _default_mineru_upload_mode(ocr_url: str) -> bool:
    hostname = (urlparse(ocr_url).hostname or '').lower()
    # Only the in-network MinerU service can resolve the same container path.
    return hostname != 'mineru'


def get_algo_server_port() -> int:
    port = _cfg['algo_server_port']
    if port:
        return port
    return _cfg['document_server_port']


def _build_store_config(index_kwargs):
    milvus_uri = _cfg['milvus_uri']
    if not milvus_uri:
        raise ValueError('LAZYMIND_MILVUS_URI is required')
    opensearch_uri = _cfg['opensearch_uri']
    if not opensearch_uri:
        raise ValueError('LAZYMIND_OPENSEARCH_URI is required')
    return {
        'vector_store': {
            'type': 'milvus',
            'kwargs': {
                'uri': milvus_uri,
                'index_kwargs': index_kwargs,
            },
        },
        'segment_store': {
            'type': 'opensearch',
            'kwargs': {
                'uris': opensearch_uri,
                'client_kwargs': {
                    'http_compress': True,
                    'use_ssl': True,
                    'verify_certs': False,
                    'user': _cfg['opensearch_user'],
                    'password': _cfg['opensearch_password'] or 'LazyRAG_OpenSearch123!',
                },
            },
        },
    }


def _build_pdf_reader():
    ocr_type = _cfg['ocr_server_type']
    ocr_url = _cfg['ocr_server_url'].rstrip('/')
    patch_applied = _cfg['ocr_patch_applied']
    service_variant = _cfg['ocr_service_variant']
    if ocr_type in ('none', None, ''):
        return PDFReader()
    if ocr_type == 'mineru':
        upload_mode = _parse_bool_config(_cfg['mineru_upload_mode'])
        if upload_mode is None:
            upload_mode = _default_mineru_upload_mode(ocr_url)
        return MineruPDFReader(
            url=ocr_url,
            backend=_cfg['mineru_backend'],
            upload_mode=upload_mode,
            post_func=NodeParser(),
            timeout=3600,
            patch_applied=patch_applied,
            service_variant=service_variant,
            image_cache_dir=_cfg['ocr_cache_dir'],
        )
    if ocr_type == 'paddleocr':
        return PaddleOCRPDFReader(
            url=ocr_url,
            service_variant=service_variant,
            images_dir=_cfg['ocr_cache_dir'],
        )
    raise ValueError(f'Unsupported OCR server type: {ocr_type!r}')


def reset_stores() -> None:
    '''Drop all Milvus collections and OpenSearch indices for this algo.

    Called when LAZYMIND_RESET_ALGO_ON_STARTUP=true, after drop_lazyllm_tables()
    and before build_document().  Clears the vector/segment data so the next
    document parse starts from a clean state.

    Note: when using `make reset-kb`, Milvus/OpenSearch volumes are already
    wiped externally, so this function is a no-op in that flow.  It is useful
    when resetting algo state without removing the underlying volumes (e.g.
    changing embed model or node group config in-place).

    TODO(wangzhihong): move it to lazyllm.Document
    '''
    import re
    from lazyllm import LOG
    from lazyllm.tools.rag.store import MilvusStore, OpenSearchStore

    LOG.warning(f'[build_document] Clearing vector/segment stores for algo "{ALGO_ID}"')

    _pat = re.compile(r'[^a-z0-9_]+')

    def _col(group: str) -> str:
        return _pat.sub('_', f'col_{group}'.lower()).strip('_')

    activated_groups = ['block', 'line', 'image', '__lazyllm_root__', '__lazyllm_image__']
    store_conf = _build_store_config(EMBED_INDEX_KWARGS)

    milvus_cfg = (store_conf.get('vector_store') or {}).get('kwargs', {})
    opensearch_cfg = (store_conf.get('segment_store') or {}).get('kwargs', {})

    if milvus_cfg.get('uri'):
        milvus = MilvusStore(**{k: v for k, v in milvus_cfg.items() if k != 'index_kwargs'})
        for group in activated_groups:
            milvus.delete(_col(group))
        LOG.warning(f'[build_document] Milvus collections dropped for algo "{ALGO_ID}"')

    if opensearch_cfg.get('uris'):
        opensearch = OpenSearchStore(**opensearch_cfg)
        for group in activated_groups:
            opensearch.delete(_col(group))
        LOG.warning(f'[build_document] OpenSearch indices dropped for algo "{ALGO_ID}"')


# Backward-compat alias — callers that imported reset_document() still work.
reset_document = reset_stores


# All tables created and owned by lazyllm's SqlManager / doc-service.
# Order matters: tables with FK dependencies on others should come first.
_LAZYLLM_TABLES = [
    'lazyllm_doc_node_group_status',
    'lazyllm_doc_parse_state',
    'lazyllm_kb_algorithm',
    'lazyllm_kb_documents',
    'lazyllm_knowledge_bases',
    'lazyllm_doc_path_locks',
    'lazyllm_documents',
    'lazyllm_doc_service_tasks',
    'lazyllm_callback_records',
    'lazyllm_idempotency_records',
    'lazyllm_node_group',
    'lazyllm_algorithm',
    'lazyllm_waiting_task_queue',
    'lazyllm_finished_task_queue',
]


def drop_lazyllm_tables() -> None:
    '''Drop all lazyllm-managed tables using the configured database URL.

    Uses DROP TABLE IF EXISTS … CASCADE so the operation is idempotent and
    handles FK dependencies automatically.  SqlManager will recreate the tables
    with the current schema on next startup.
    '''
    from lazyllm import LOG
    db_url = _cfg.get('database_url') if hasattr(_cfg, 'get') else _cfg['database_url']
    if not db_url:
        LOG.warning('[build_document] database_url not set — skipping lazyllm table drop')
        return
    # Normalise psycopg3 URL to psycopg2 for SQLAlchemy (lazyllm uses psycopg2 internally)
    sa_url = db_url.replace('postgresql+psycopg://', 'postgresql+psycopg2://', 1)
    try:
        import sqlalchemy
        engine = sqlalchemy.create_engine(sa_url)
        table_list = ', '.join(f'"{t}"' for t in _LAZYLLM_TABLES)
        with engine.connect() as conn:
            conn.execute(sqlalchemy.text(f'DROP TABLE IF EXISTS {table_list} CASCADE'))
            conn.commit()
        engine.dispose()
        LOG.warning(f'[build_document] Dropped {len(_LAZYLLM_TABLES)} lazyllm tables — will be recreated on startup')
    except Exception as e:
        LOG.error(f'[build_document] Failed to drop lazyllm tables: {e}')


def build_document() -> Document:
    processor_url = _cfg['document_processor_url']
    server_port = get_algo_server_port()
    embed = {k: AutoModel(model=k) for k in EMBED_KEYS}

    # Current LazyLLM expects store_conf on DocumentProcessor when using DocumentProcessor,
    # while Document receives only the remote processor manager.
    # Document validates this manager/store_conf combination before wiring DocImpl.
    processor = DocumentProcessor(url=processor_url, store_conf=_build_store_config(EMBED_INDEX_KWARGS))

    docs = Document(
        dataset_path=None,
        name=ALGO_ID,
        embed=embed,
        manager=processor,
        doc_fields=[],
    )

    docs.add_reader('*.pdf', _build_pdf_reader())

    image_extensions = ('.jpg', '.jpeg', '.png', '.gif', '.bmp', '.webp', '.tiff', '.tif')
    image_reader = ImageEmbReader()
    media_reader = VideoReader()
    for ext in image_extensions:
        docs.add_reader(f'*{ext}', image_reader)
    docs.add_reader('*.mp3', media_reader)
    docs.add_reader('*.mp4', media_reader)

    docs.create_node_group(name='block', display_name='paragraph slice',
                           group_type=NodeGroupType.CHUNK, transform=GeneralParser(max_length=2048, split_by='\n'))
    docs.create_node_group(name='line', display_name='sentence slice',
                           group_type=NodeGroupType.CHUNK, transform=LineSplitter, parent='block')

    # Only source=dynamic embed_image needs lazy mode; static configs are always ready.
    if EMBED_IMAGE in get_dynamic_role_slot_map():
        from lazyllm.tools.rag.store import LAZY_IMAGE_GROUP
        docs._impl.node_groups[LAZY_IMAGE_GROUP]['lazy_mode'] = 'embed'
    docs.activate_group('image', embed_keys=EMBED_IMAGE)
    docs.activate_group('block', embed_keys=[EMBED_MAIN])
    docs.activate_group('line', embed_keys=[EMBED_MAIN])
    docs._manager._kbs = lazyllm.ServerModule(
        _quiet_trace(docs._manager._kbs),
        port=server_port,
    )
    return docs
