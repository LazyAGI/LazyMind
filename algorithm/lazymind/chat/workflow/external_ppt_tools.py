"""Audited, model-free PPT primitives for external Workflow executors.

Inputs and outputs cross the boundary as values, never server filesystem paths.
The renderer/exporter is a configured deployment executable, not package code.
"""
from __future__ import annotations

import base64
import hashlib
from html.parser import HTMLParser
import io
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import tempfile
from typing import Any
import xml.etree.ElementTree as ET
import zipfile


class ExternalToolError(ValueError):
    def __init__(self, code: str, message: str):
        super().__init__(code, message)
        self.code = code

    def __str__(self):
        return self.args[1]


MAX_FILE_BYTES = 24 * 1024 * 1024
MAX_HTML_BYTES = 16 * 1024 * 1024


class _OfflineHTML(HTMLParser):
    def handle_starttag(self, tag, attrs):
        if tag.lower() in {'script', 'iframe', 'object', 'embed', 'link', 'base'}:
            raise ExternalToolError('INVALID_ARGUMENT', f'Offline rendering does not accept {tag} elements.')
        if tag.lower() == 'meta' and set(dict(attrs)) != {'charset'}:
            raise ExternalToolError('INVALID_ARGUMENT', 'Only charset metadata is accepted.')
        for name, value in attrs:
            key, value = name.lower(), (value or '').strip()
            if key.startswith('on') or key in {'srcdoc', 'srcset'}:
                raise ExternalToolError('INVALID_ARGUMENT', 'Active HTML is not accepted by the renderer.')
            if key in {'src', 'href', 'xlink:href', 'action', 'formaction', 'poster'}:
                if value and not value.startswith(('data:', '#')):
                    raise ExternalToolError('INVALID_ARGUMENT',
                                            'Embed assets inline; server paths and URLs are not accepted.')
        if tag.lower() == 'form':
            raise ExternalToolError('INVALID_ARGUMENT', 'Forms are not supported in slide rendering.')

    handle_startendtag = handle_starttag


def _html(value: str) -> str:
    if not isinstance(value, str) or not value.strip() or len(value.encode()) > MAX_HTML_BYTES:
        raise ExternalToolError('INVALID_ARGUMENT', 'Provide a nonempty HTML slide within the 16 MiB limit.')
    if not re.search(r'<html\b', value, re.I):
        raise ExternalToolError('INVALID_ARGUMENT', 'A complete HTML document is required.')
    # CSS escapes can disguise URL schemes; this conservative offline profile
    # accepts plain CSS and inline assets, not arbitrary browser applications.
    if re.search(r'@import|expression\s*\(|javascript\s*:|\\[0-9a-f]{1,6}', value, re.I):
        raise ExternalToolError('INVALID_ARGUMENT', 'Active or escaped external CSS is not supported.')
    for url in re.findall(r'url\s*\(\s*[\'\"]?([^\)\'\"]+)', value, re.I):
        if not url.strip().startswith(('data:', '#')):
            raise ExternalToolError('INVALID_ARGUMENT', 'CSS assets must be embedded inline.')
    parser = _OfflineHTML(convert_charrefs=True)
    parser.feed(value)
    parser.close()
    return value


def _file(raw: bytes, name: str, mime: str) -> dict[str, Any]:
    if not raw or len(raw) > MAX_FILE_BYTES:
        raise ExternalToolError('OUTPUT_LIMIT', 'The generated file is empty or exceeds the 24 MiB transfer limit.')
    return {'name': name, 'mime_type': mime, 'content_base64': base64.b64encode(raw).decode(),
            'sha256': hashlib.sha256(raw).hexdigest(), 'size_bytes': len(raw)}


def _cli(render: bool = False) -> tuple[str, Path]:
    configured = os.environ.get('LAZYMIND_PPT_EXPORT_CLI', '').strip()
    node = os.environ.get('LAZYMIND_PPT_EXPORT_NODE', '').strip() or shutil.which('node')
    cli = Path(configured) if configured else None
    if render and cli is not None:
        cli = cli.with_name('screenshot.mjs')
    if not node or cli is None or not cli.is_file():
        raise ExternalToolError('DEPENDENCY_MISSING',
                                'Configure the PPT export dependency in LazyMind, then retry this tool.')
    deps = Path(os.environ.get('LAZYMIND_PPT_EXPORT_DEPS', '').strip() or str(cli.parent))
    if not any((root / 'node_modules' / 'playwright').is_dir() for root in (deps, cli.parent)):
        raise ExternalToolError('DEPENDENCY_MISSING',
                                'Install the PPT renderer dependency in LazyMind, then retry; tools never install it.')
    return node, cli


def _run(command: list[str], timeout: int) -> dict[str, Any]:
    try:
        result = subprocess.run(command, capture_output=True, text=True, timeout=timeout, check=False,
                                env={**os.environ, 'LAZYMIND_PPT_EXTERNAL_OFFLINE': '1'})
    except (OSError, subprocess.TimeoutExpired) as exc:
        raise ExternalToolError('TOOL_EXECUTION_FAILED', 'The configured PPT renderer could not finish.') from exc
    if result.returncode:
        # Never return deployment paths or arbitrary process output to the host.
        raise ExternalToolError('TOOL_EXECUTION_FAILED',
                                'The configured PPT renderer failed; inspect LazyMind service diagnostics.')
    payload = {}
    for line in result.stdout.splitlines():
        try:
            candidate = json.loads(line)
            if isinstance(candidate, dict):
                payload = candidate
        except ValueError:
            continue
    if payload.get('status') in {'skipped', 'failed'} or payload.get('success') is False:
        raise ExternalToolError('TOOL_EXECUTION_FAILED',
                                'PPT rendering was skipped or failed; configure its dependency and retry.')
    return payload


def ppt_export_html(pages: list[dict[str, Any]], filename: str = 'presentation.pptx') -> dict[str, Any]:
    """Convert 1–100 supplied offline HTML slides and notes into an editable PPTX file."""
    if not isinstance(pages, list) or not 1 <= len(pages) <= 100:
        raise ExternalToolError('INVALID_ARGUMENT', 'Provide between 1 and 100 slides, including one-slide summaries.')
    prepared = []
    total = 0
    for page in pages:
        if not isinstance(page, dict) or set(page) - {'html', 'notes'}:
            raise ExternalToolError('INVALID_ARGUMENT', 'Each page accepts only html and notes.')
        html = _html(page.get('html'))
        notes = page.get('notes', '')
        if not isinstance(notes, str) or len(notes.encode()) > 1024 * 1024:
            raise ExternalToolError('INVALID_ARGUMENT', 'Speaker notes must be text within the 1 MiB limit.')
        total += len(html.encode()) + len(notes.encode())
        prepared.append((html, notes))
    if total > MAX_FILE_BYTES:
        raise ExternalToolError('INVALID_ARGUMENT', 'The deck exceeds the 24 MiB input limit.')
    name = Path(filename).name
    if (name != filename or not name.lower().endswith('.pptx') or not Path(name).stem
            or len(name) > 200 or any(char in name for char in '\\\0\r\n')):
        raise ExternalToolError('INVALID_ARGUMENT', 'filename must be a plain .pptx filename.')
    node, cli = _cli()
    with tempfile.TemporaryDirectory(prefix='external-ppt-') as directory:
        root = Path(directory)
        (root / 'pages').mkdir()
        for number, (html, notes) in enumerate(prepared, 1):
            (root / 'pages' / f'page_{number:03d}.html').write_text(html)
            (root / 'pages' / f'page_{number:03d}.notes.txt').write_text(notes)
        (root / 'task_pack.json').write_text(json.dumps({'deck_id': root.name, 'deck_dir': str(root)}))
        (root / 'review.md').write_text('External agent owns validation.\n')
        result = _run([node, str(cli), '--deck-dir', str(root), '--output', 'result.pptx', '--force'], 300)
        path = root / 'result.pptx'
        if not path.is_file() or result.get('failed', 0):
            raise ExternalToolError('TOOL_EXECUTION_FAILED', 'The exporter did not produce every requested slide.')
        raw = path.read_bytes()
        inspection = ppt_inspect_pptx(base64.b64encode(raw).decode())
        if inspection['page_count'] != len(pages):
            raise ExternalToolError('TOOL_EXECUTION_FAILED', 'The exported slide count differs from the input.')
        return {'files': [_file(raw, name, 'application/vnd.openxmlformats-officedocument.presentationml.presentation')],
                'page_count': len(pages), 'validation_owner': 'external-agent'}


def ppt_render_html(html: str) -> dict[str, Any]:
    """Render one supplied offline HTML slide to PNG for the external agent to inspect."""
    html = _html(html)
    node, cli = _cli(render=True)
    with tempfile.TemporaryDirectory(prefix='external-ppt-preview-') as directory:
        source, output = Path(directory) / 'slide.html', Path(directory) / 'slide.png'
        source.write_text(html)
        _run([node, str(cli), '--html', str(source), '--out', str(output), '--viewport', '1600x900'], 60)
        if not output.is_file():
            raise ExternalToolError('TOOL_EXECUTION_FAILED', 'The renderer did not produce a preview.')
        return {'files': [_file(output.read_bytes(), 'slide.png', 'image/png')],
                'validation_owner': 'external-agent'}


def ppt_inspect_pptx(content_base64: str) -> dict[str, Any]:
    """Read slide text/count from an inline PPTX without rendering or calling any model."""
    if not isinstance(content_base64, str) or len(content_base64) > MAX_FILE_BYTES * 4 // 3 + 4:
        raise ExternalToolError('INVALID_ARGUMENT', 'PPTX input exceeds the transfer limit.')
    try:
        raw = base64.b64decode(content_base64, validate=True)
        with zipfile.ZipFile(io.BytesIO(raw)) as archive:
            infos = archive.infolist()
            if len(infos) > 10000 or sum(item.file_size for item in infos) > 128 * 1024 * 1024:
                raise ExternalToolError('INVALID_ARGUMENT', 'PPTX expanded contents exceed the inspection limit.')
            names = [item.filename for item in infos]
            if 'ppt/presentation.xml' not in names or '[Content_Types].xml' not in names:
                raise ValueError('missing presentation parts')
            presentation = ET.fromstring(archive.read('ppt/presentation.xml'))
            relationships = ET.fromstring(archive.read('ppt/_rels/presentation.xml.rels'))
            slide_targets = {}
            for relationship in relationships:
                target = relationship.attrib.get('Target', '')
                if (relationship.attrib.get('Type', '').endswith('/slide')
                        and relationship.attrib.get('TargetMode') != 'External'
                        and re.fullmatch(r'slides/slide\d+\.xml', target)):
                    slide_targets[relationship.attrib['Id']] = 'ppt/' + target
            slides = [slide_targets[element.attrib[
                '{http://schemas.openxmlformats.org/officeDocument/2006/relationships}id']]
                for element in presentation.iter()
                if element.tag == '{http://schemas.openxmlformats.org/presentationml/2006/main}sldId']
            if len(slides) != len(set(slides)):
                raise ValueError('duplicate slide identities')
            pages = []
            for number, name in enumerate(slides, 1):
                document = ET.fromstring(archive.read(name))
                texts = [element.text or '' for element in document.iter()
                         if element.tag == '{http://schemas.openxmlformats.org/drawingml/2006/main}t']
                pages.append({'page': number, 'texts': texts})
            if not pages:
                raise ValueError('no slides')
            return {'page_count': len(pages), 'pages': pages,
                    'has_notes': any(re.fullmatch(r'ppt/notesSlides/notesSlide\d+\.xml', name) for name in names)}
    except (ValueError, KeyError, OSError, zipfile.BadZipFile, ET.ParseError) as exc:
        if isinstance(exc, ExternalToolError):
            raise
        raise ExternalToolError('INVALID_ARGUMENT', 'Provide a valid base64 PPTX file.') from exc
