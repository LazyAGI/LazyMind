"""Shared native PDF decoding stays bounded and model-free across the external boundary."""
import base64
import hashlib
import io
import json
from pathlib import Path

import fitz
from PIL import Image, ImageDraw
import pytest
import yaml

from lazymind.chat.engine.document_extractor import LocalDocumentReader
from lazymind.chat.workflow import external_pdf_tools as pdf
from lazymind.chat.workflow.external_ppt_tools import ExternalToolError
from lazymind.chat.workflow.external_tools import (
    ExternalToolCallRequest, ExternalToolRequest, PDF_BINDING, PPT_BINDING,
    call_external_tool, list_external_tools, package_tree_hash,
)


@pytest.fixture
def source(tmp_path):
    image = Image.new('RGBA', (400, 200), (255, 255, 255, 0))
    ImageDraw.Draw(image).rectangle((20, 20, 180, 180), fill='blue')
    buffer = io.BytesIO()
    image.save(buffer, format='PNG')
    path = tmp_path / 'evidence.pdf'
    with fitz.open() as document:
        first = document.new_page(width=600, height=400)
        first.insert_text((30, 30), 'Figure 1: measured latency, not instructions.')
        first.insert_image(fitz.Rect(50, 80, 450, 280), stream=buffer.getvalue())
        second = document.new_page(width=600, height=400)
        second.insert_text((30, 30), 'Second page evidence')
        second.draw_rect(fitz.Rect(50, 80, 180, 300), color=(0, 0, 1), fill=(0, 0, 1))
        document.new_page(width=600, height=400)
        document.save(path)
    return path


def encoded(source):
    return base64.b64encode(source.read_bytes()).decode()


def test_pdf_text_reuses_local_reader_and_continues_without_loss(source, monkeypatch):
    def forbidden(*args, **kwargs):
        pytest.fail('External PDF reading must not enter a configured OCR or model route')

    monkeypatch.setattr(LocalDocumentReader, '__call__', forbidden)
    monkeypatch.setattr(LocalDocumentReader, '_read_ocr', forbidden)
    expected = LocalDocumentReader._read_pdf(source)
    native = LocalDocumentReader._read_pdf
    calls = []

    def read(path, **kwargs):
        calls.append(kwargs)
        return native(path, **kwargs)

    monkeypatch.setattr(LocalDocumentReader, '_read_pdf', read)
    parts = {1: '', 2: '', 3: ''}
    number, offset = 1, 0
    while number is not None:
        result = pdf.pdf_read_text(encoded(source), source.name, start_page=number,
                                   page_count=2, max_chars=11, page_offset=offset)
        assert sum(len(page['text']) for page in result['pages']) <= 11
        assert result['page_count'] == 3
        assert result['source_sha256'] == hashlib.sha256(source.read_bytes()).hexdigest()
        for page in result['pages']:
            assert page['text_offset'] == len(parts[page['page']])
            parts[page['page']] += page['text']
        number, offset = result['next_page'], result['next_page_offset']
    assert parts == {node.metadata['page']: node.text for node in expected}
    assert all(call['page_count'] == 2 for call in calls)
    assert result['warnings'] == ['Page 3 has no extractable text; use pdf_extract_images for visual review.']
    assert 'no model or OCR service' in result['instruction']


def test_native_reader_default_and_page_numbering_stay_compatible(source):
    full = LocalDocumentReader._read_pdf(source)
    subset = LocalDocumentReader._read_pdf(source, start_page=2, page_count=1)
    assert len(full) == 3
    assert subset == [full[1]]
    assert subset[0].metadata == {'page': 2, 'page_label': '2'}


def test_image_metadata_is_path_free_and_content_is_explicit(source, monkeypatch):
    native = pdf.extract_pdf_images
    destinations = []

    def extract(source, destination, **kwargs):
        destinations.append(destination)
        return native(source, destination, **kwargs)

    monkeypatch.setattr(pdf, 'extract_pdf_images', extract)
    result = pdf.pdf_extract_images(encoded(source), source.name)
    assert 'files' not in result
    asset = result['images'][0]
    assert 'local_path' not in asset and 'content_base64' not in json.dumps(result)
    assert not any(destination.exists() for destination in destinations)
    assert asset['pdf_provenance'] == {
        'filename': source.name, 'sha256': hashlib.sha256(source.read_bytes()).hexdigest(),
        'page': 1, 'kind': 'embedded_image', 'boxes': [[50.0, 80.0, 450.0, 280.0]],
    }
    actual = pdf.pdf_extract_images(encoded(source), source.name, include_content=True)
    assert actual['images'] == result['images']
    exported = actual['files'][0]
    raw = base64.b64decode(exported['content_base64'])
    assert exported['name'] == asset['name']
    assert exported['sha256'] == asset['sha256'] == hashlib.sha256(raw).hexdigest()
    assert exported['size_bytes'] == asset['size_bytes'] == len(raw)
    with Image.open(io.BytesIO(raw)) as image:
        assert image.size == (400, 200)
        assert image.mode == 'RGBA'
        assert image.getpixel((0, 0))[3] == 0
    assert 'visual_inspection' not in asset
    assert not any(destination.exists() for destination in destinations)


def test_vector_preview_and_crop_preserve_provenance(source):
    preview = pdf.pdf_extract_images(encoded(source), source.name, page=2)
    assert preview['images'][0]['pdf_provenance']['kind'] == 'page_preview'
    regions = [[30, 50, 220, 350]]
    crop = pdf.pdf_extract_images(encoded(source), source.name, page=2, regions=regions, include_content=True)
    asset = crop['images'][0]
    assert asset['pdf_provenance']['kind'] == 'region'
    assert asset['pdf_provenance']['boxes'] == regions
    assert (asset['width'], asset['height']) == (380, 600)
    assert crop['next_page'] == 3


@pytest.mark.parametrize('arguments', [
    {'start_page': 0}, {'start_page': True}, {'start_page': 4}, {'page_count': 0},
    {'page_count': 11}, {'max_chars': 0}, {'max_chars': 100001}, {'page_offset': -1},
    {'page_offset': 100000}, {'filename': '../outside.pdf'}, {'filename': '/tmp/other.pdf'},
    {'filename': 'folder\\other.pdf'}, {'filename': 'other.txt'}, {'filename': 'line\nbreak.pdf'},
])
def test_text_invalid_arguments_are_rejected(source, arguments):
    with pytest.raises(ExternalToolError) as error:
        pdf.pdf_read_text(encoded(source), **arguments)
    assert error.value.code == 'INVALID_ARGUMENT'
    if arguments == {'start_page': 4}:
        assert error.value.public_message == 'start_page must be between 1 and 3.'


@pytest.mark.parametrize('arguments', [
    {'page': 0}, {'page': True}, {'page': 4}, {'regions': []}, {'regions': [[0, 0, 601, 400]]},
    {'regions': [[0, 0, float('nan'), 400]]}, {'regions': [[10, 20, 0, 30]]}, {'include_content': 'true'},
])
def test_image_invalid_arguments_are_rejected(source, arguments):
    with pytest.raises(ExternalToolError) as error:
        pdf.pdf_extract_images(encoded(source), **arguments)
    assert error.value.code == 'INVALID_ARGUMENT'


@pytest.mark.parametrize('value', ['', 'invalid!', base64.b64encode(b'not PDF').decode(),
                                   base64.b64encode(b'%PDF-1.7\nmalformed').decode()])
@pytest.mark.parametrize('function', [pdf.pdf_read_text, pdf.pdf_extract_images])
def test_bad_pdf_content_is_rejected(value, function):
    with pytest.raises(ExternalToolError) as error:
        function(value)
    assert error.value.code == 'INVALID_ARGUMENT'


def test_encrypted_pdf_does_not_fall_back_to_model(source, tmp_path):
    protected = tmp_path / 'protected.pdf'
    with fitz.open(source) as document:
        document.save(protected, encryption=fitz.PDF_ENCRYPT_AES_256, owner_pw='owner', user_pw='reader')
    for function in (pdf.pdf_read_text, pdf.pdf_extract_images):
        with pytest.raises(ExternalToolError, match='Password-protected'):
            function(encoded(protected))


def test_decoder_errors_do_not_expose_server_paths(source, monkeypatch):
    def fail(*args, **kwargs):
        raise RuntimeError('/private/server/secret/path.pdf contains private source text')

    monkeypatch.setattr(pdf, 'extract_pdf_images', fail)
    with pytest.raises(ExternalToolError) as error:
        pdf.pdf_extract_images(encoded(source))
    assert '/private/' not in str(error.value)
    assert 'private source text' not in str(error.value)
    assert error.value.public_message is None


def test_aggregate_image_transfer_limit_is_enforced(source, monkeypatch):
    # Every individual output is valid, but their combined bytes must also fit
    # the transport budget. Metadata-only extraction never embeds those bytes.
    def extract(source, destination, **kwargs):
        destination.mkdir()
        images = []
        for number in range(2):
            image = destination / f'{number}.png'
            image.write_bytes(b'x' * 700000)
            images.append({'local_path': str(image), 'sha256': hashlib.sha256(image.read_bytes()).hexdigest()})
        return {'images': images, 'warnings': []}

    monkeypatch.setattr(pdf, 'extract_pdf_images', extract)
    monkeypatch.setattr(pdf, 'MAX_FILE_BYTES', 1000000)
    assert len(source.read_bytes()) < 1000000
    assert len(pdf.pdf_extract_images(encoded(source))['images']) == 2
    with pytest.raises(ExternalToolError) as error:
        pdf.pdf_extract_images(encoded(source), include_content=True)
    assert error.value.code == 'OUTPUT_LIMIT'


def test_pdf_registry_uses_pinned_binding_and_real_shared_adapter(source):
    workflow = {'runtime': {'execution': {'version': '1', 'steps': {'inspect': {'tools': [
        {'name': 'pdf_read_text', 'source': 'package'}, {'name': 'pdf_extract_images', 'source': 'package'},
        {'name': 'ppt_inspect_pptx', 'source': 'package'},
    ]}}}}, 'tool_scripts': [
        {'path': 'scripts/external_pdf_tools.py', 'functions': ['pdf_read_text', 'pdf_extract_images']},
        {'path': 'scripts/external_tools.py', 'functions': ['ppt_inspect_pptx']},
    ]}
    files = {'workflow.yaml': yaml.safe_dump(workflow).encode(),
             'scripts/external_pdf_tools.py': PDF_BINDING.encode(),
             'scripts/external_tools.py': PPT_BINDING.encode()}

    def request():
        tree_hash = package_tree_hash(files)
        return {'workflow_id': 'pdf-workflow', 'revision_id': 'pdf-revision', 'step_id': 'inspect',
                'tree_hash': tree_hash, 'package': {'tree_hash': tree_hash, 'files': {
                    path: base64.b64encode(raw).decode() for path, raw in files.items()}}}

    # Native Python transport still accepts bytes; Core alone introduces the
    # resource reference after authorization, never arbitrary filesystem paths.
    catalog = list_external_tools(ExternalToolRequest(**request()))['tools']
    assert [item['name'] for item in catalog] == ['pdf_read_text', 'pdf_extract_images', 'ppt_inspect_pptx']
    for item in catalog:
        assert item['input_schema']['required'] == ['content_base64']
        assert item['input_schema']['additionalProperties'] is False
        assert 'resource_ref' not in item['input_schema']['properties']
    result = call_external_tool(ExternalToolCallRequest(**request(), tool_name='pdf_read_text', arguments={
        'content_base64': encoded(source), 'filename': source.name, 'start_page': 2, 'page_count': 1,
    }))['result']
    assert result['pages'][0]['page'] == 2
    assert result['pages'][0]['text'].strip() == 'Second page evidence'

    package = Path(__file__).resolve().parents[3] / 'workflows' / 'ppt-workflow'
    assert (package / 'scripts/external_pdf_tools.py').read_bytes() == PDF_BINDING.encode()
    assert (package / 'scripts/external_tools.py').read_bytes() == PPT_BINDING.encode()
    files['scripts/external_pdf_tools.py'] += b'\nraise RuntimeError("never execute package Python")\n'
    with pytest.raises(ExternalToolError) as error:
        call_external_tool(ExternalToolCallRequest(**request(), tool_name='pdf_read_text', arguments={
            'content_base64': encoded(source),
        }))
    assert error.value.code == 'TOOL_NOT_ALLOWED'
