import json
import os
from pathlib import Path
import subprocess
import sys

import pytest

from lazymind.common import base_ingestion as base


def test_notebook_extracts_cells_without_execution(tmp_path):
    source = tmp_path / 'sample.ipynb'
    source.write_text(json.dumps({'nbformat': 4, 'cells': [
        {'cell_type': 'markdown', 'source': ['# Heading\n', 'Introduction']},
        {'cell_type': 'code', 'source': 'raise RuntimeError("must not execute")',
         'outputs': [{'text': 'ignored output'}]},
        {'cell_type': 'raw', 'source': '文' * 5000},
        {'cell_type': 'code', 'source': []},
    ]}), encoding='utf-8')
    artifact = base.prepare_document(source, tmp_path)
    assert len(artifact['roots']) == 3
    assert artifact['roots'][0]['text'] == '# Heading\nIntroduction'
    assert artifact['roots'][1]['metadata']['cell_index'] == 1
    assert len(artifact['chunks']) == 5
    assert all(len(chunk['text']) <= 2048 for chunk in artifact['chunks'])


@pytest.mark.parametrize('cells', [None, [None], [{'source': [42]}]])
def test_notebook_rejects_invalid_cells(tmp_path, cells):
    source = tmp_path / 'bad.ipynb'
    source.write_text(json.dumps({'nbformat': 4, 'cells': cells}), encoding='utf-8')
    with pytest.raises(ValueError, match='Invalid notebook'):
        base.prepare_document(source, tmp_path)
    assert not Path(str(source) + base.ARTIFACT_SUFFIX).exists()


@pytest.mark.parametrize('suffix', ['.tiff', '.tif', '.png'])
def test_images_report_ocr_requirement_without_fake_chunks(tmp_path, suffix):
    source = tmp_path / ('image' + suffix)
    source.write_bytes(b'image fixture')
    with pytest.raises(ValueError, match='OCR'):
        base.prepare_document(source, tmp_path)
    assert source.exists()
    assert not Path(str(source) + base.ARTIFACT_SUFFIX).exists()


def test_native_artifacts_are_durable_and_idempotent(tmp_path, monkeypatch):
    source = tmp_path / 'source.md'
    text = ('第一段内容。\n' * 900) + '最后一段'
    source.write_text(text, encoding='utf-8')
    first = base.prepare_document(source, tmp_path)
    assert ''.join(chunk['text'] for chunk in first['chunks']) == text
    assert all(len(chunk['text']) <= 2048 for chunk in first['chunks'])
    assert first['roots'][0]['text'] == text
    artifact = Path(str(source) + base.ARTIFACT_SUFFIX)
    before = artifact.stat().st_mtime_ns
    monkeypatch.setattr(base, '_read_nodes', lambda _: pytest.fail('must reuse native parse'))
    assert base.prepare_document(source, tmp_path) == first
    assert artifact.stat().st_mtime_ns == before
    source.write_text('changed', encoding='utf-8')
    assert base.load_artifact(source) is None


def test_reader_failure_preserves_previous_artifact(tmp_path, monkeypatch):
    source = tmp_path / 'source.txt'
    source.write_text('original', encoding='utf-8')
    base.prepare_document(source, tmp_path)
    artifact = Path(str(source) + base.ARTIFACT_SUFFIX)
    previous = artifact.read_bytes()
    source.write_text('changed', encoding='utf-8')

    def fail(_):
        raise ValueError('reader failed')
    monkeypatch.setattr(base, '_read_nodes', fail)
    with pytest.raises(ValueError, match='reader failed'):
        base.prepare_document(source, tmp_path)
    assert artifact.read_bytes() == previous


def test_rejects_outside_upload_root_and_empty_documents(tmp_path):
    root = tmp_path / 'uploads'
    root.mkdir()
    outside = tmp_path / 'outside.txt'
    outside.write_text('private', encoding='utf-8')
    with pytest.raises(ValueError, match='upload directory'):
        base.prepare_document(outside, root)
    (root / 'link.txt').symlink_to(outside)
    with pytest.raises(ValueError, match='upload directory'):
        base.prepare_document(root / 'link.txt', root)
    empty = root / 'empty.txt'
    empty.write_text('  ', encoding='utf-8')
    with pytest.raises(ValueError, match='No readable text'):
        base.prepare_document(empty, root)


def test_base_reader_without_optional_rag_imports(tmp_path):
    source = tmp_path / 'native.docx'
    code = '''
import importlib.abc
import sys
class NoRag(importlib.abc.MetaPathFinder):
    def find_spec(self, fullname, path=None, target=None):
        if fullname.split('.')[0] in {'spacy','pymilvus','milvus_lite','bm25s','Stemmer','nltk'}:
            raise AssertionError(fullname)
        if fullname in {'lazyllm.tools.rag.document', 'lazyllm.tools.rag.store'}:
            raise AssertionError(fullname)
sys.meta_path.insert(0, NoRag())
from lazymind.common.base_ingestion import prepare_document
from lazymind.chat.api.base_ingestion_routes import router
assert router.routes
from docx import Document
doc = Document()
doc.add_paragraph('native text without rag')
doc.save(sys.argv[1])
data = prepare_document(sys.argv[1], sys.argv[2])
assert data['chunks'][0]['text'] == 'native text without rag'
from pathlib import Path
import json
notebook = Path(sys.argv[2]) / 'native.ipynb'
notebook.write_text(json.dumps({'nbformat': 4, 'cells': [{'cell_type': 'code', 'source': ['print(1)']}]}),
                    encoding='utf-8')
assert prepare_document(notebook, sys.argv[2])['chunks'][0]['text'] == 'print(1)'
'''
    # Match the runtime's no-doc-initialization mode rather than inheriting CI's
    # LAZYLLM_INIT_DOC=1, which eagerly imports every documented tool.
    result = subprocess.run(
        [sys.executable, '-c', code, str(source), str(tmp_path)],
        env={**os.environ, 'LAZYMIND_RAG_ENABLED': 'false', 'LAZYLLM_INIT_DOC': 'False'},
        capture_output=True, text=True,
    )
    assert result.returncode == 0, result.stderr


def test_retained_reader_rechunks_from_roots_for_new_embedding(tmp_path, monkeypatch):
    from lazyllm.tools.rag.doc_node import DocNode
    from lazymind.parsing.engine.readers.retained import RetainedReader
    from lazymind.parsing.engine.transform import general_parser
    source = tmp_path / 'text.txt'
    text = '中😀文' * 1000
    source.write_text(text, encoding='utf-8')
    artifact = base.prepare_document(source, tmp_path)
    snapshot = json.dumps(artifact, ensure_ascii=False)
    reader = RetainedReader(lambda *args, **kwargs: pytest.fail('must not parse source again'))
    roots = reader(source, use_cache=False)
    assert isinstance(roots[0], DocNode)
    monkeypatch.setattr(general_parser, '_runtime_embed_max_input_tokens', lambda: 256)
    chunks = general_parser.GeneralParser().forward(roots[0])
    assert ''.join(chunk.text for chunk in chunks) == text
    assert all(len(chunk.text.encode('utf-8')) <= 192 for chunk in chunks)
    assert len(chunks) > len(artifact['chunks'])
    assert json.dumps(base.load_artifact(source), ensure_ascii=False) == snapshot


def test_prepare_endpoint_requires_internal_auth_and_upload_scope(tmp_path, monkeypatch):
    from fastapi import FastAPI
    from fastapi.testclient import TestClient
    from lazymind.chat.api.base_ingestion_routes import router
    from lazymind.config import config
    root = tmp_path / 'uploads'
    root.mkdir()
    source = root / 'sample.txt'
    source.write_text('base artifact', encoding='utf-8')
    outside = tmp_path / 'private.txt'
    outside.write_text('private', encoding='utf-8')
    monkeypatch.setitem(config._impl, 'core_internal_token', 'internal-test-token')
    monkeypatch.setitem(config._impl, 'shared_upload_dir', str(root))
    app = FastAPI()
    app.include_router(router)
    with TestClient(app) as client:
        assert client.post('/internal/documents:prepare', json={'source_path': str(source)}).status_code == 401
        headers = {'X-LazyMind-Internal-Token': 'internal-test-token'}
        assert client.post('/internal/documents:prepare', headers=headers,
                           json={'source_path': str(outside)}).status_code == 422
        response = client.post('/internal/documents:prepare', headers=headers, json={'source_path': str(source)})
        assert response.status_code == 200
        assert response.json()['artifact_ref'] == str(source) + base.ARTIFACT_SUFFIX
