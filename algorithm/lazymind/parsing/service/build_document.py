import lazyllm
from lazyllm.tracing import set_trace_context
from lazyllm import AutoModel
from lazyllm.tools.rag import AdaptiveTransform, CodeSplitter, Document, LLMParser, TransformArgs
from lazyllm.tools.rag.doc_impl import NodeGroupType
from lazyllm.tools.rag.parsing_service import DocumentProcessor
from lazyllm.tools.rag.readers import (
    EpubReader,
    HWPReader,
    IPYNBReader,
    MboxReader,
    PandasCSVReader,
    PandasExcelReader
)
from lazyllm.tools.rag.readers.ocrReader import DynamicPDFReader

from lazymind.model_config import get_dynamic_role_slot_map
from lazymind.config import EMBED_IMAGE, EMBED_INDEX_KWARGS, EMBED_KEYS, EMBED_MAIN, config as _cfg
from lazymind.parsing.engine.readers import ImageEmbReader, VideoReader
from lazymind.parsing.engine.transform import GeneralParser, LineSplitter, NodeParser

ALGO_ID = 'general_algo'
_CODE_CHUNK_SIZE = 512
_CODE_OVERLAP = 0
_CODE_FILE_PATTERNS = (
    ('*.json', 'json'),
    ('*.jsonl', 'jsonl'),
    ('*.yaml', 'yaml'),
    ('*.yml', 'yml'),
    ('*.xml', 'xml'),
    ('*.html', 'html'),
    ('*.htm', 'htm'),
    ('*.py', 'python'),
    ('*.ipynb', 'python'),
)


def _quiet_trace(kbs):
    def call(kb_group, *args, **kwargs):
        set_trace_context({'enabled': False})
        return kbs[kb_group](*args, **kwargs)
    return call


def get_algo_server_port() -> int:
    port = _cfg['algo_server_port']
    if port:
        return port
    return _cfg['document_server_port']


def _build_store_config(index_kwargs):
    milvus_uri = _cfg['milvus_uri']
    if not milvus_uri:
        raise ValueError('LAZYMIND_MILVUS_URI is required')

    store_type = _cfg['segment_store_type']
    uri_or_path = _cfg['segment_store_uri_or_path']
    if store_type == 'SQLiteStore':
        if not uri_or_path:
            raise ValueError('LAZYMIND_SEGMENT_STORE_URI_OR_PATH is required for SQLite segment store')
        segment_store = {'type': 'SQLiteStore', 'kwargs': {'db_path': uri_or_path}}
    elif store_type == 'opensearch':
        if not uri_or_path:
            raise ValueError('LAZYMIND_SEGMENT_STORE_URI_OR_PATH is required for OpenSearch segment store')
        segment_store = {
            'type': store_type,
            'kwargs': {
                'uris': uri_or_path,
                'client_kwargs': {
                    'http_compress': True,
                    'use_ssl': True,
                    'verify_certs': False,
                    'user': _cfg['segment_store_user'],
                    'password': _cfg['segment_store_password'],
                },
            },
        }
    else:
        raise ValueError(f'Unsupported segment store type: {store_type!r}')

    return {
        'vector_store': {
            'type': 'milvus',
            'kwargs': {
                'uri': milvus_uri,
                'index_kwargs': index_kwargs,
            },
        },
        'segment_store': segment_store,
    }


def _build_code_transform():
    code_kwargs = dict(chunk_size=_CODE_CHUNK_SIZE, overlap=_CODE_OVERLAP)
    return AdaptiveTransform([
        TransformArgs(f=CodeSplitter, pattern=pattern, kwargs={**code_kwargs, 'filetype': filetype})
        for pattern, filetype in _CODE_FILE_PATTERNS
    ] + [TransformArgs(f=lambda _: [], name='skip_non_code_files')])


def _build_block_transform():
    return AdaptiveTransform(TransformArgs(f=GeneralParser, kwargs={'max_length': 2048, 'split_by': '\n'}))


def _build_line_transform():
    return AdaptiveTransform([
        TransformArgs(f=lambda _: [], pattern=pattern, name='skip_line_for_code_files')
        for pattern, _ in _CODE_FILE_PATTERNS
    ] + [TransformArgs(f=LineSplitter)])


def _build_pdf_reader():
    return DynamicPDFReader(
        image_cache_dir=_cfg['ocr_cache_dir'],
        post_func=NodeParser(),
        timeout=3600,
    )


def _register_document_readers(docs: Document) -> None:
    pdf_reader = _build_pdf_reader()
    docs.add_reader('*.pdf', pdf_reader)
    docs.add_reader('*.hwp', HWPReader())

    # mineru ppt reader.
    docs.add_reader('*.pptx', pdf_reader)
    docs.add_reader('*.ppt', pdf_reader)
    docs.add_reader('*.pptm', pdf_reader)

    docs.add_reader('*.ipynb', IPYNBReader())
    docs.add_reader('*.epub', EpubReader())
    docs.add_reader('*.mbox', MboxReader())
    docs.add_reader('*.csv', PandasCSVReader())

    excel_reader = PandasExcelReader()
    docs.add_reader('*.xls', excel_reader)
    docs.add_reader('*.xlsx', excel_reader)


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
    from lazyllm.tools.rag.store import MilvusStore, OpenSearchStore, SQLiteStore

    LOG.warning(f'[build_document] Clearing vector/segment stores for algo "{ALGO_ID}"')

    _pat = re.compile(r'[^a-z0-9_]+')

    def _col(group: str) -> str:
        return _pat.sub('_', f'col_{group}'.lower()).strip('_')

    activated_groups = ['block', 'line', 'code', 'doc-summary', 'image', '__lazyllm_root__', '__lazyllm_image__']
    store_conf = _build_store_config(EMBED_INDEX_KWARGS)

    milvus_cfg = (store_conf.get('vector_store') or {}).get('kwargs', {})
    seg_cfg = (store_conf.get('segment_store') or {}).get('kwargs', {})
    seg_type = (store_conf.get('segment_store') or {}).get('type', '')

    if milvus_cfg.get('uri'):
        milvus = MilvusStore(**{k: v for k, v in milvus_cfg.items() if k != 'index_kwargs'})
        for group in activated_groups:
            milvus.delete(_col(group))
        LOG.warning(f'[build_document] Milvus collections dropped for algo "{ALGO_ID}"')

    if seg_type == 'SQLiteStore' and seg_cfg.get('db_path'):
        sqlite = SQLiteStore(**seg_cfg)
        for group in activated_groups:
            sqlite.delete(_col(group))
        LOG.warning(f'[build_document] SQLite collections dropped for algo "{ALGO_ID}"')
    elif seg_cfg.get('uris'):
        opensearch = OpenSearchStore(**seg_cfg)
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


def build_document(algo_id: str = ALGO_ID, *, serve: bool = True) -> Document:
    processor_url = _cfg['document_processor_url']
    server_port = get_algo_server_port()
    embed = {k: AutoModel(model=k) for k in EMBED_KEYS}

    # Current LazyLLM expects store_conf on DocumentProcessor when using DocumentProcessor,
    # while Document receives only the remote processor manager.
    # Document validates this manager/store_conf combination before wiring DocImpl.
    processor = DocumentProcessor(url=processor_url, store_conf=_build_store_config(EMBED_INDEX_KWARGS))

    docs = Document(
        dataset_path=None,
        name=algo_id,
        embed=embed,
        manager=processor,
        doc_fields=[],
    )

    _register_document_readers(docs)

    image_extensions = ('.jpg', '.jpeg', '.png', '.gif', '.bmp', '.webp', '.tiff', '.tif')
    image_reader = ImageEmbReader()

    # Pass an STT model, otherwise openai-whisper will be used by default.
    # For optimal performance, openai-whisper is recommended to run on a GPU server.
    media_reader = VideoReader(model_role='speech_to_text')
    for ext in image_extensions:
        docs.add_reader(f'*{ext}', image_reader)
    docs.add_reader('*.mp3', media_reader)
    docs.add_reader('*.mp4', media_reader)

    docs.create_node_group(name='block', display_name='paragraph slice',
                           group_type=NodeGroupType.CHUNK, transform=_build_block_transform())
    docs.create_node_group(name='line', display_name='sentence slice',
                           group_type=NodeGroupType.CHUNK, transform=_build_line_transform(), parent='block')
    docs.create_node_group(name='code', display_name='code slice',
                           group_type=NodeGroupType.CODE, transform=_build_code_transform())
    docs.create_node_group(
        name='doc-summary',
        display_name='document summary',
        group_type=NodeGroupType.SUMMARY,
        transform=LLMParser(AutoModel(model='llm'), language='zh', task_type='summary'),
        lazy_mode='all',
    )

    # Only source=dynamic embed_image needs lazy mode; static configs are always ready.
    if EMBED_IMAGE in get_dynamic_role_slot_map():
        from lazyllm.tools.rag.store import LAZY_IMAGE_GROUP
        docs._impl.node_groups[LAZY_IMAGE_GROUP]['lazy_mode'] = 'embed'
    docs.activate_group('image', embed_keys=EMBED_IMAGE)
    docs.activate_group('block', embed_keys=[EMBED_MAIN])
    docs.activate_group('line', embed_keys=[EMBED_MAIN])
    docs.activate_group('code', embed_keys=[EMBED_MAIN])
    docs.activate_group('doc-summary', embed_keys=[EMBED_MAIN])
    if serve:
        docs._manager._kbs = lazyllm.ServerModule(_quiet_trace(docs._manager._kbs), port=server_port)
    return docs


def register_parser_algorithm(algo_id: str) -> None:
    build_document(algo_id, serve=False).start()


def drop_parser_algorithm(algo_id: str) -> None:
    DocumentProcessor(url=_cfg['document_processor_url']).drop_algorithm(algo_id)
