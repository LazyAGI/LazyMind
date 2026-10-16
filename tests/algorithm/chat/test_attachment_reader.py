from importlib import import_module
from types import SimpleNamespace

import pytest

ar = import_module('lazymind.chat.engine.attachment_reader')


def test_filter_chat_document_files():
    files = [
        '/data/a.png',
        '/data/report.pdf',
        '/data/note.docx',
        '/data/slide.pptx',
    ]
    assert ar.filter_chat_document_files(files) == [
        '/data/report.pdf',
        '/data/note.docx',
        '/data/slide.pptx',
    ]


def test_text_files_are_supported_chat_attachments():
    files = ['/data/notes.txt', '/data/draft.lmd', '/data/config.YAML', '/data/main.py']

    assert all(ar.is_chat_text_file(path) for path in files)
    assert all(ar.is_chat_attachment_file(path) for path in files)


def test_filter_chat_image_files():
    files = ['/data/a.png', '/data/report.pdf', '/data/b.JPEG']
    assert ar.filter_chat_image_files(files) == ['/data/a.png', '/data/b.JPEG']


def test_parse_attachment_content_routes_by_suffix(monkeypatch, tmp_path):
    pdf_path = tmp_path / 'demo.pdf'
    image_path = tmp_path / 'photo.png'
    pdf_path.write_text('dummy', encoding='utf-8')
    image_path.write_bytes(b'png')

    monkeypatch.setattr(ar, 'select_vision_model_role', lambda: 'vlm')
    monkeypatch.setattr(ar, 'read_chat_document_text', lambda path: f'parsed:{path}')
    monkeypatch.setattr(ar, 'extract_image_description', lambda path, **kwargs: 'blue sky photo')

    assert ar.parse_attachment_content(str(pdf_path)) == f'parsed:{pdf_path.resolve()}'
    assert ar.parse_attachment_content(str(image_path)) == 'blue sky photo'


def test_parse_attachment_content_reads_text_without_ocr(monkeypatch, tmp_path):
    text_path = tmp_path / 'config.yaml'
    text_path.write_text('name: 测试\nenabled: true\n', encoding='utf-8')
    monkeypatch.setattr(
        ar,
        'read_chat_document_text',
        lambda path: (_ for _ in ()).throw(AssertionError('text file must not use OCR')),
    )

    assert ar.parse_attachment_content(str(text_path)) == 'name: 测试\nenabled: true\n'


def test_read_chat_text_file_truncates_and_rejects_nul(tmp_path):
    long_text = tmp_path / 'long.log'
    long_text.write_text('abcdefgh', encoding='utf-8')
    binary_text = tmp_path / 'fake.txt'
    binary_text.write_bytes(b'plain\x00binary')

    truncated = ar.read_chat_text_file(str(long_text), max_chars=4)
    assert truncated.startswith('abcd')
    assert 'truncated after 4 characters' in truncated

    with pytest.raises(ValueError) as error:
        ar.read_chat_text_file(str(binary_text))
    assert 'NUL bytes' in str(error.value)


def test_build_attachment_reference_prompt(monkeypatch, tmp_path):
    pdf_path = tmp_path / 'demo.pdf'
    image_path = tmp_path / 'photo.png'
    pdf_path.write_text('dummy', encoding='utf-8')
    image_path.write_bytes(b'png')

    monkeypatch.setattr(ar, 'select_vision_model_role', lambda: 'vlm')
    monkeypatch.setattr(
        ar,
        'read_chat_document_text',
        lambda path: f'parsed:{path}',
    )
    monkeypatch.setattr(
        ar,
        'extract_image_description',
        lambda path, **kwargs: 'blue sky photo',
    )

    prompt = ar.build_attachment_reference_prompt([str(pdf_path), str(image_path)])

    assert 'Attached File References' in prompt
    assert 'demo.pdf' in prompt
    assert f'parsed:{pdf_path.resolve()}' in prompt
    assert 'photo.png' in prompt
    assert 'blue sky photo' in prompt


def test_sanitize_for_prompt_template_escapes_placeholders():
    raw = '<|im_start|>user\n{query} /think\n{response}<|im_end|>'
    sanitized = ar._sanitize_for_prompt_template(raw)
    assert sanitized == '<|im_start|>user\n{ query } /think\n{ response }<|im_end|>'
    assert '{query}' not in sanitized
    assert '{response}' not in sanitized


def test_build_attachment_reference_prompt_sanitizes_document_body(monkeypatch, tmp_path):
    pdf_path = tmp_path / 'demo.pdf'
    pdf_path.write_text('dummy', encoding='utf-8')
    monkeypatch.setattr(
        ar,
        'read_chat_document_text',
        lambda path: 'template {query} then {response}',
    )

    prompt = ar.build_attachment_reference_prompt([str(pdf_path)])

    assert 'template { query } then { response }' in prompt
    assert '{query}' not in prompt
    assert '{response}' not in prompt


def test_parse_attachment_content_rejects_unsupported_suffix(tmp_path):
    bad = tmp_path / 'archive.zip'
    bad.write_bytes(b'zip')
    try:
        ar.parse_attachment_content(str(bad))
        raise AssertionError('expected ValueError')
    except ValueError as exc:
        assert 'Unsupported attachment type' in str(exc)


def test_read_chat_document_text_joins_nodes(monkeypatch):
    def reader(_path):
        return [SimpleNamespace(text='line one'), SimpleNamespace(text='line two')]

    monkeypatch.setattr(ar, '_get_document_reader', lambda: reader)
    assert ar.read_chat_document_text('/tmp/demo.pdf') == 'line one\n\nline two'


def test_read_chat_document_text_reads_docx_locally_without_ocr(tmp_path):
    from docx import Document

    path = tmp_path / 'requirements.docx'
    document = Document()
    document.add_paragraph('第一段要求')
    table = document.add_table(rows=1, cols=2)
    table.cell(0, 0).text = '指标'
    table.cell(0, 1).text = '性能'
    document.add_paragraph('最后一段')
    document.save(path)
    ar._get_document_reader.cache_clear()

    body = ar.read_chat_document_text(str(path))

    assert body.splitlines() == ['第一段要求', '指标\t性能', '最后一段']


def test_read_chat_document_text_rejects_invalid_docx_without_loading_rag(tmp_path):
    from docx.opc.exceptions import PackageNotFoundError
    from zipfile import BadZipFile

    path = tmp_path / 'broken.docx'
    path.write_bytes(b'not-a-docx')

    with pytest.raises((PackageNotFoundError, BadZipFile)):
        ar.read_chat_document_text(str(path))


def test_read_chat_document_text_returns_empty_for_image_only_docx(tmp_path):
    from docx import Document

    path = tmp_path / 'image-only.docx'
    Document().save(path)
    assert ar.read_chat_document_text(str(path)) == ''


def test_pdf_reader_is_available_without_rag_runtime(monkeypatch, tmp_path):
    import fitz
    from lazymind.chat import runtime_loader

    path = tmp_path / 'report.pdf'
    document = fitz.open()
    page = document.new_page()
    page.insert_text((72, 72), 'standalone reader')
    document.save(path)
    document.close()
    monkeypatch.setattr(
        runtime_loader,
        'ensure_rag_runtime',
        lambda: (_ for _ in ()).throw(AssertionError('document extraction must not load RAG')),
    )
    ar._get_document_reader.cache_clear()

    assert 'standalone reader' in ar.read_chat_document_text(str(path))


def test_pptx_reader_is_available_without_rag_runtime(tmp_path):
    from pptx import Presentation
    from pptx.util import Inches

    path = tmp_path / 'slides.pptx'
    presentation = Presentation()
    slide = presentation.slides.add_slide(presentation.slide_layouts[6])
    box = slide.shapes.add_textbox(Inches(1), Inches(1), Inches(5), Inches(1))
    box.text = 'presentation content'
    presentation.save(path)
    ar._get_document_reader.cache_clear()

    assert ar.read_chat_document_text(str(path)) == 'presentation content'


def test_ocr_route_and_credentials_work_without_rag(monkeypatch):
    import lazyllm
    from lazyllm.tools.rag.readers.reader_config_inject import inject_reader_config
    from lazymind.chat.engine.document_extractor import LocalDocumentReader

    monkeypatch.setenv('LAZYMIND_RAG_ENABLED', 'false')
    calls = []

    def read_ocr(path):
        calls.append((path, lazyllm.globals.config['dynamic_ocr_auth']))
        return [SimpleNamespace(text='scanned page', metadata={'page': 1})]

    monkeypatch.setattr(LocalDocumentReader, '_read_ocr', staticmethod(read_ocr))
    try:
        inject_reader_config(ocr_config={'ocr_type': 'mineru', 'ocr_auth': {'mineru': 'test-token'}})
        assert ar.read_chat_document_text('scan.pdf') == 'scanned page'
        assert calls == [('scan.pdf', {'mineru': 'test-token'})]
        inject_reader_config(ocr_config={})
        assert lazyllm.globals.config['dynamic_ocr_configs'] is None
        assert lazyllm.globals.config['dynamic_ocr_auth'] is None
    finally:
        inject_reader_config(ocr_config={})


def test_reader_imports_and_pdf_parsing_do_not_load_optional_dependencies(tmp_path):
    import os
    import subprocess
    import sys
    import textwrap

    code = textwrap.dedent('''
        import importlib.abc
        import sys
        import fitz

        class NoRag(importlib.abc.MetaPathFinder):
            def find_spec(self, fullname, path=None, target=None):
                if fullname.split('.')[0] in {'spacy', 'pymilvus', 'milvus_lite', 'bm25s', 'Stemmer', 'nltk'}:
                    raise AssertionError('Optional dependency loaded: ' + fullname)
                if fullname in {'lazyllm.tools.rag.document', 'lazyllm.tools.rag.store'}:
                    raise AssertionError('Knowledge-base runtime loaded: ' + fullname)

        sys.meta_path.insert(0, NoRag())
        from lazymind.chat.engine.attachment_reader import read_chat_document_text
        from lazyllm.tools.rag.readers.ocrReader import DynamicPDFReader
        from lazymind.chat.engine.tools.file_resources.ingest import parse_pdf_pages
        pdf = fitz.open()
        pdf.new_page().insert_text((72, 72), 'base reader works')
        pdf.save(sys.argv[1])
        pdf.close()
        assert 'base reader works' in read_chat_document_text(sys.argv[1])
        assert parse_pdf_pages(sys.argv[1]) == [(1, 'base reader works')]
    ''')
    result = subprocess.run(
        [sys.executable, '-c', code, str(tmp_path / 'base.pdf')],
        env={**os.environ, 'LAZYMIND_RAG_ENABLED': 'false', 'LAZYLLM_INIT_DOC': '0'},
        capture_output=True, text=True, timeout=30,
    )
    assert result.returncode == 0, result.stdout + result.stderr


def test_image_reader_reuses_llm_with_image_format(monkeypatch, tmp_path):
    from unittest.mock import Mock
    path = tmp_path / 'screen.png'
    path.write_bytes(b'png')
    monkeypatch.setattr(ar, 'select_vision_model_role', lambda: 'llm')
    model = Mock(return_value='visible screen text')
    factory = Mock(return_value=model)
    monkeypatch.setattr(ar, 'AutoModel', factory)
    assert ar.extract_image_description(str(path)) == 'visible screen text'
    factory.assert_called_once_with(model='llm', type='vlm')
    assert 'screen.png' in model.call_args.args[0]


def test_unavailable_image_is_explained_instead_of_silently_skipped(monkeypatch, tmp_path):
    from lazymind.vision_model import VisionModelUnavailable
    path = tmp_path / 'image.png'
    path.write_bytes(b'png')

    def unavailable():
        raise VisionModelUnavailable('请配置视觉模型后重试。')
    monkeypatch.setattr(ar, 'select_vision_model_role', unavailable)
    prompt = ar.build_attachment_reference_prompt([str(path)])
    assert 'Image was NOT read' in prompt
    assert '请配置视觉模型后重试' in prompt
    assert 'Do not infer its contents' in prompt
