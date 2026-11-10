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
import posixpath
import re
import shutil
import subprocess
import sys
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


def ppt_compute_timing(total_minutes: float, pages: list[dict[str, Any]]) -> dict[str, Any]:
    """Use native weighted timing for ordered {title, weight} pages; return exact seconds and notes prefixes."""
    from .ppt_planning import plan_timing
    try:
        return plan_timing(total_minutes, json.dumps(pages, ensure_ascii=False))
    except (TypeError, ValueError) as exc:
        raise ExternalToolError('INVALID_ARGUMENT', str(exc)) from exc


def ppt_order_papers(
    papers: list[dict[str, Any]],
    dependencies: list[dict[str, Any]] | None = None,
) -> dict[str, Any]:
    """Use native prerequisite ordering for {source_id,title} papers and {prerequisite,dependent,evidence} edges."""
    from .ppt_planning import plan_paper_order
    if not isinstance(papers, list) or not 1 <= len(papers) <= 200:
        raise ExternalToolError('INVALID_ARGUMENT', 'Provide between 1 and 200 selected papers.')
    if dependencies is not None and (not isinstance(dependencies, list) or len(dependencies) > 1000):
        raise ExternalToolError('INVALID_ARGUMENT', 'Provide at most 1000 paper dependencies.')
    try:
        return plan_paper_order(json.dumps(papers, ensure_ascii=False),
                                json.dumps(dependencies if dependencies is not None else [], ensure_ascii=False))
    except (TypeError, ValueError) as exc:
        raise ExternalToolError('INVALID_ARGUMENT', str(exc)) from exc


def _package_page_prompt() -> str:
    """Read the native prompt from the verified snapshot, never live package code."""
    from .external_tool_context import get_external_package_files
    files = get_external_package_files()
    remaining = 1024 * 1024

    def expand(name: str, stack: tuple[str, ...] = ()) -> str:
        nonlocal remaining
        if name in stack or len(stack) >= 16:
            raise ExternalToolError('INVALID_PACKAGE', 'Page prompt includes are recursive or too deep.')
        raw = files.get(name)
        if not isinstance(raw, bytes) or len(raw) > 1024 * 1024:
            raise ExternalToolError('INVALID_PACKAGE', 'The pinned package is missing its native page prompt.')
        try:
            prompt = raw.decode('utf-8')
        except UnicodeDecodeError as exc:
            raise ExternalToolError('INVALID_PACKAGE', 'The native page prompt must be UTF-8 text.') from exc
        pattern = r'<<<INLINE:\s*([^>]+)>>>'
        # Account for each repeated include before re.sub accumulates its
        # replacement strings. Checking only the final joined string permits
        # an otherwise small package to allocate an unbounded expanded prompt.
        remaining -= len(prompt.encode()) - sum(
            len(match.group(0).encode()) for match in re.finditer(pattern, prompt)
        )
        if remaining < 0:
            raise ExternalToolError('INVALID_PACKAGE', 'The expanded page prompt exceeds the 1 MiB limit.')

        def include(match: re.Match) -> str:
            reference = match.group(1).strip()
            name = posixpath.normpath('runtime/' + reference)
            if (reference.startswith('/') or '\\' in reference or '\0' in reference
                    or name == '..' or name.startswith('../')):
                raise ExternalToolError('INVALID_PACKAGE', 'Page prompt includes must stay inside the package.')
            return expand(name, (*stack, name_of_prompt))

        name_of_prompt = name
        prompt = re.sub(pattern, include, prompt)
        if len(prompt.encode()) > 1024 * 1024:
            raise ExternalToolError('INVALID_PACKAGE', 'The expanded page prompt exceeds the 1 MiB limit.')
        return prompt

    return expand('runtime/prompts/page_html.md')


def ppt_prepare_page(
    page_no: int,
    brief: str,
    generation_responses: list[dict[str, Any]] | None = None,
) -> dict[str, Any]:
    """Prepare or accept one page using the native PPT generation contract.

    Pass the approved complete page brief, including its language, content,
    shared style and inline material assets. The first call returns a
    generation_request for the external agent's model. Call again with the
    same inputs and generation_responses=[{request_hash, text}] to accept its
    HTML; the result contains one HTML file for rendering and deck export.
    This adapter never calls a model. The external agent owns content/visual
    validation; LazyMind owns prompt assembly, HTML normalization and export.
    """
    from .action_generation import GenerationRequired, generate, generation_scope
    from .ppt_generation import (
        HTML_DOCUMENT, page_generation_query, page_generation_system, sanitize_page_html,
    )
    if type(page_no) is not int or not 1 <= page_no <= 100:
        raise ExternalToolError('INVALID_ARGUMENT', 'page_no must be between 1 and 100.')
    if not isinstance(brief, str) or not brief.strip() or len(brief.encode()) > MAX_HTML_BYTES:
        raise ExternalToolError('INVALID_ARGUMENT', 'Provide a complete page brief within the 16 MiB limit.')
    responses = generation_responses if generation_responses is not None else []
    if (not isinstance(responses, list) or len(responses) > 1
            or any(not isinstance(item, dict) or set(item) != {'request_hash', 'text'}
                   or not isinstance(item['request_hash'], str) or not isinstance(item['text'], str)
                   or len(item['text'].encode()) > MAX_HTML_BYTES for item in responses)):
        raise ExternalToolError('INVALID_ARGUMENT', 'Provide at most one {request_hash, text} generation response.')
    system_prompt = page_generation_system(_package_page_prompt()) + (
        '\n\nOFFLINE TRANSFER CONTRACT: Return a standalone HTML page with all assets inline. '
        'Use static HTML/CSS or self-contained SVG for charts. Scripts, ECharts initialization, '
        'external URLs, local file paths, named asset placeholders and active elements are unavailable '
        'in this transfer. If an approved image is supplied as a data URI, preserve it inline. '
        'Do not invent or fetch missing assets. This transport rule takes priority over optional '
        'script-based chart or named-asset examples in the package prompt.'
    )
    query = page_generation_query(brief, prompt_mode='complete-page-prompt', language='')
    try:
        with generation_scope(responses):
            html = generate(system_prompt, query, request_name=f'page-html:{page_no}')
    except GenerationRequired as pending:
        return {'status': 'generation_required', 'page_no': page_no,
                'generation_request': pending.request, 'validation_owner': 'external-agent'}
    except ValueError as exc:
        raise ExternalToolError('INVALID_ARGUMENT',
                                'Generation response does not match this pinned page request.') from exc
    html = sanitize_page_html(html)
    if not HTML_DOCUMENT.fullmatch(html.strip()):
        raise ExternalToolError('INVALID_ARGUMENT',
                                'Regenerate a complete HTML document; the page is truncated or invalid.')
    if re.search(r'\bdata-ppt-asset\s*=', html, re.I):
        raise ExternalToolError('INVALID_ARGUMENT',
                                'Inline actual assets instead of unresolved named asset placeholders.')
    html = _html(html)
    return {'status': 'ready', 'page_no': page_no,
            'files': [_file(html.encode(), f'page_{page_no:03d}.html', 'text/html')],
            'validation_owner': 'external-agent'}


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
                                env={**os.environ, 'LAZYMIND_PPT_EXTERNAL_OFFLINE': '1',
                                     'LAZYMIND_FONT_SUBSET_PYTHON':
                                         os.environ.get('LAZYMIND_FONT_SUBSET_PYTHON') or sys.executable})
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
                # OPC permits both presentation-relative and package-absolute
                # targets. Artifact Tool uses /ppt/slides/slideN.xml.
                if target.startswith('/ppt/slides/'):
                    target = target[len('/ppt/'):]
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
