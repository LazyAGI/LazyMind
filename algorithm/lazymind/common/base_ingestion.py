"""Durable, model-independent document artifacts; no RAG runtime required."""
import hashlib
import json
import os
from pathlib import Path
import tempfile

from lazymind.chat.config import CHAT_TEXT_EXTENSIONS
from lazymind.chat.engine.document_extractor import LocalDocumentReader

ARTIFACT_SUFFIX = '.lazymind-base.json'
PARSER_VERSION = 'native-reader-v1'
CHUNKER_VERSION = 'paragraph-chars-2048-v1'
MAX_SOURCE_BYTES = 64 * 1024 * 1024
MAX_TEXT_CHARS = 16 * 1024 * 1024


def fingerprint(path):
    digest = hashlib.sha256()
    with open(path, 'rb') as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b''):
            digest.update(block)
    return digest.hexdigest()


def load_artifact(path):
    """Return only a complete artifact matching the current original file."""
    artifact = Path(str(path) + ARTIFACT_SUFFIX)
    if not artifact.is_file() or artifact.is_symlink() or artifact.stat().st_size > 128 * 1024 * 1024:
        return None
    try:
        data = json.loads(artifact.read_text(encoding='utf-8'))
        if not isinstance(data, dict):
            return None
        for key in ('roots', 'chunks'):
            nodes = data.get(key)
            if not isinstance(nodes, list) or not nodes or not all(
                isinstance(node, dict) and isinstance(node.get('text'), str)
                and isinstance(node.get('metadata'), dict) for node in nodes
            ):
                return None
        if (data.get('parser_version') == PARSER_VERSION
                and data.get('chunker_version') == CHUNKER_VERSION
                and data.get('source_sha256') == fingerprint(path)):
            return data
    except (OSError, ValueError):
        pass
    return None


def split_baseline(text, size=2048):
    offset = 0
    while offset < len(text):
        end = min(offset + size, len(text))
        if end < len(text):
            boundary = text.rfind('\n', offset + size // 2, end)
            if boundary >= 0:
                end = boundary + 1
        yield offset, end, text[offset:end]
        offset = end


def _read_nodes(path):
    suffix = path.suffix.lower()
    if suffix in ('.tif', '.tiff', '.png', '.jpg', '.jpeg', '.bmp', '.webp', '.gif'):
        raise ValueError('图片文件需要 OCR 才能提取文字；原文件已保留，请配置 OCR 后重试。')
    if suffix == '.ipynb':
        notebook = json.loads(path.read_text(encoding='utf-8'))
        if not isinstance(notebook, dict) or notebook.get('nbformat') != 4:
            raise ValueError('Invalid notebook: expected nbformat 4')
        cells = notebook.get('cells')
        if not isinstance(cells, list):
            raise ValueError('Invalid notebook cells')
        nodes = []
        for index, cell in enumerate(cells):
            if not isinstance(cell, dict):
                raise ValueError('Invalid notebook cell')
            source = cell.get('source', '')
            if isinstance(source, list) and all(isinstance(line, str) for line in source):
                source = ''.join(source)
            if not isinstance(source, str):
                raise ValueError('Invalid notebook cell source')
            if source.strip():
                nodes.append({
                    'text': source,
                    'metadata': {'cell_index': index, 'cell_type': cell.get('cell_type', '')},
                })
        return nodes
    readers = {'.pdf': LocalDocumentReader._read_pdf, '.docx': LocalDocumentReader._read_docx,
               '.pptx': LocalDocumentReader._read_pptx}
    if suffix in readers:
        return [{'text': node.text, 'metadata': node.metadata} for node in readers[suffix](path)]
    if suffix in ('.xls', '.xlsx', '.csv', '.tsv'):
        import pandas as pd
        if suffix in ('.xls', '.xlsx'):
            sheets = pd.read_excel(path, sheet_name=None)
        else:
            sheets = {'data': pd.read_csv(path, sep='\t' if suffix == '.tsv' else ',')}
        return [{'text': frame.to_csv(index=False), 'metadata': {'sheet': name}}
                for name, frame in sheets.items()]
    if suffix in CHAT_TEXT_EXTENSIONS:
        text = path.read_text(encoding='utf-8')
        if '\x00' in text:
            raise ValueError('file is not plain text')
        return [{'text': text, 'metadata': {}}]
    raise ValueError('Unsupported native document type; convert to DOCX, PPTX, PDF or text first')


def prepare_document(source_path, upload_root):
    root = Path(upload_root).resolve(strict=True)
    path = Path(source_path).resolve(strict=True)
    if not path.is_relative_to(root) or not path.is_file():
        raise ValueError('document must be inside the upload directory')
    if path.stat().st_size > MAX_SOURCE_BYTES:
        raise ValueError('document exceeds native reader size limit')
    cached = load_artifact(path)
    if cached is not None:
        return cached
    source_hash = fingerprint(path)
    nodes = _read_nodes(path)
    if sum(len(node['text']) for node in nodes) > MAX_TEXT_CHARS:
        raise ValueError('extracted document exceeds native reader text limit')
    if not any(node['text'].strip() for node in nodes):
        raise ValueError('No readable text; scanned or image-only documents require OCR')
    chunks = []
    for index, node in enumerate(nodes):
        for start, end, text in split_baseline(node['text']):
            chunks.append({'id': f'{source_hash}:{index}:{start}:{end}', 'text': text,
                           'root': index, 'start': start, 'end': end, 'metadata': node['metadata']})
    if fingerprint(path) != source_hash:
        raise ValueError('document changed during parsing; retry')
    artifact = Path(str(path) + ARTIFACT_SUFFIX)
    data = {'source_sha256': source_hash, 'parser_version': PARSER_VERSION,
            'chunker_version': CHUNKER_VERSION, 'roots': nodes, 'chunks': chunks}
    # Atomic replacement leaves the previous complete artifact available on failure.
    fd, temporary = tempfile.mkstemp(dir=path.parent, prefix='.base-ingest-')
    try:
        with os.fdopen(fd, 'w', encoding='utf-8') as stream:
            json.dump(data, stream, ensure_ascii=False)
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(temporary, artifact)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)
    return data
