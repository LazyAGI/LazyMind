import logging
import os
import shutil
import subprocess
import tempfile
import threading
import json
import re
import statistics
from pathlib import Path
from typing import Iterable

from fastapi import FastAPI, HTTPException
from pydantic import BaseModel
import fitz
from fontTools.ttLib import TTCollection


logging.basicConfig(level=logging.INFO, format='%(message)s', force=True)
logger = logging.getLogger('office-convert-service')
logger.setLevel(logging.INFO)

app = FastAPI(
    title='Office Convert Service',
    description='A standalone service for converting Office documents to PDF',
    version='1.0.0',
    docs_url='/docs',
    redoc_url=None,
    openapi_url='/openapi.json',
)

OFFICE_EXTENSIONS = {'.doc', '.docx', '.xls', '.xlsx', '.ppt', '.pptx', '.pptm'}
DEFAULT_ALLOWED_ROOTS = '/var/lib/lazymind/uploads'
DEFAULT_TIMEOUT_SECONDS = 900
DEFAULT_CONCURRENCY = 4


def _extract_simplified_chinese_font(collection_path: Path) -> Path:
    output = Path('/tmp/lazymind-translation-font.ttf')
    collection = TTCollection(str(collection_path))
    try:
        selected = None
        for font in collection.fonts:
            family_names = {
                record.toUnicode()
                for record in font['name'].names
                if record.nameID in {1, 2, 4, 6}
            }
            if any(
                'CJK SC' in name
                or 'Sans SC' in name
                or 'Simplified Chinese' in name
                or 'WenQuanYi Zen Hei' in name
                for name in family_names
            ):
                selected = font
                break
        if selected is None:
            raise RuntimeError(f'no Simplified Chinese face in font collection: {collection_path}')
        selected.save(output)
    finally:
        collection.close()
    return output


def _translation_font_path() -> Path:
    configured = os.getenv('OFFICE_CONVERT_TRANSLATION_FONT_PATH', '').strip()
    candidates = [
        Path(configured) if configured else None,
        Path('/usr/share/fonts/truetype/wqy/wqy-zenhei.ttc'),
        Path('/usr/share/fonts/opentype/noto/NotoSansCJKsc-Regular.otf'),
        Path('/usr/share/fonts/opentype/noto/NotoSansCJK-Regular.ttc'),
    ]
    for candidate in candidates:
        if candidate and candidate.is_file():
            if candidate.suffix.lower() == '.ttc':
                return _extract_simplified_chinese_font(candidate)
            return candidate
    raise RuntimeError('no CJK translation font is installed')


TRANSLATION_FONT_PATH = _translation_font_path()


class ConvertRequest(BaseModel):
    source_path: str


class ConvertResponse(BaseModel):
    pdf_path: str
    reused: bool = False
    provider: str = 'libreoffice'


class PDFTranslationRenderRequest(BaseModel):
    source_path: str
    layout_path: str
    translations_path: str
    output_path: str
    target_language: str = 'zh'


class PDFTranslationLayoutRequest(BaseModel):
    source_path: str
    output_path: str


class PDFReferenceRegionsRequest(BaseModel):
    source_path: str
    references: list[str]


def _reference_match_text(value: str) -> str:
    value = re.sub(r'<[^>]+>', '', value)
    return ''.join(character.lower() for character in value if character.isalnum())


def _pdf_reference_lines(document: fitz.Document) -> list[dict[str, object]]:
    lines: list[dict[str, object]] = []
    for page_index, page in enumerate(document):
        page_lines: list[dict[str, object]] = []
        for block in page.get_text('dict', sort=False).get('blocks', []):
            if block.get('type') != 0:
                continue
            for line in block.get('lines', []):
                text = ''.join(str(span.get('text', '')) for span in line.get('spans', [])).strip()
                normalized = _reference_match_text(text)
                bbox = line.get('bbox')
                if normalized and bbox and len(bbox) == 4:
                    if float(bbox[1]) < 60:
                        continue
                    page_lines.append({
                        'page': page_index + 1,
                        'bbox': [float(value) for value in bbox],
                        'text': text,
                        'normalized': normalized,
                    })
        # Scientific PDFs are commonly two-column. Reading the complete left
        # column before the right column preserves bibliography entry order.
        midpoint = float(page.rect.width) / 2
        page_lines.sort(key=lambda item: (
            0 if (item['bbox'][0] + item['bbox'][2]) / 2 < midpoint else 1,
            item['bbox'][1], item['bbox'][0],
        ))
        lines.extend(page_lines)
    return lines


def _locate_reference_regions(document: fitz.Document, references: list[str]) -> list[dict[str, object]]:
    lines = _pdf_reference_lines(document)
    stream = ''
    offsets: list[tuple[int, int]] = []
    for line in lines:
        start = len(stream)
        stream += str(line['normalized'])
        offsets.append((start, len(stream)))

    starts: list[int] = []
    needles = [_reference_match_text(raw_reference) for raw_reference in references]
    cursor = 0
    for needle in needles:
        start = -1
        if len(needle) >= 12:
            # Reader output and native PDF text can disagree around ligatures,
            # soft hyphens and markup. Stable prefix/suffix anchors avoid a
            # runtime text match while retaining exact native PDF coordinates.
            for anchor_size in (48, 36, 24, 16, 12):
                anchor = needle[:min(anchor_size, len(needle))]
                start = stream.find(anchor, cursor)
                if start >= 0:
                    break
            if start >= 0:
                # Advance only beyond the start anchor. Advancing to an
                # estimated end can skip the next entry when reader text is
                # longer than native PDF text (markup, ligatures, hyphenation).
                cursor = start + 1
        starts.append(start)

    results: list[dict[str, object]] = []
    for index, (needle, start) in enumerate(zip(needles, starts)):
        next_start = next((value for value in starts[index + 1:] if value >= 0), -1)
        end = next_start
        if start >= 0 and end < 0:
            tail = needle[-min(28, max(12, len(needle) // 5)):]
            tail_at = stream.find(tail, start + min(12, len(needle)))
            end = tail_at + len(tail) if tail_at >= 0 else min(len(stream), start + len(needle))

        matched_lines = [
            line for line, (line_start, line_end) in zip(lines, offsets)
            if start >= 0 and line_end > start and line_start < end
        ]
        if matched_lines:
            first_page = int(matched_lines[0]['page'])
            first_top = float(matched_lines[0]['bbox'][1])
            page_height = float(document[first_page - 1].rect.height)
            if first_top < page_height * 0.75:
                matched_lines = [line for line in matched_lines if int(line['page']) == first_page]
            matched_lines = matched_lines[:12]
        regions: list[dict[str, object]] = []
        for line in matched_lines:
            page = int(line['page'])
            bbox = list(line['bbox'])
            if regions and regions[-1]['page'] == page:
                previous = regions[-1]['bbox']
                # Merge adjacent lines in the same column, but never bridge the
                # gutter between two columns.
                same_column = abs(previous[0] - bbox[0]) < float(document[page - 1].rect.width) * 0.12
                if same_column and bbox[1] - previous[3] < 18:
                    previous[0] = min(previous[0], bbox[0])
                    previous[1] = min(previous[1], bbox[1])
                    previous[2] = max(previous[2], bbox[2])
                    previous[3] = max(previous[3], bbox[3])
                    continue
            regions.append({'page': page, 'bbox': bbox})
        results.append({'index': index, 'regions': regions})
    return results


def _allowed_roots() -> list[Path]:
    raw = os.getenv('OFFICE_CONVERT_ALLOWED_ROOTS', DEFAULT_ALLOWED_ROOTS)
    roots: list[Path] = []
    for part in raw.split(','):
        part = part.strip()
        if not part:
            continue
        roots.append(Path(part).resolve())
    return roots


def _timeout_seconds() -> int:
    raw = (os.getenv('OFFICE_CONVERT_TIMEOUT_SECONDS') or '').strip()
    if not raw:
        return DEFAULT_TIMEOUT_SECONDS
    try:
        value = int(raw)
    except ValueError:
        return DEFAULT_TIMEOUT_SECONDS
    if value <= 0:
        return DEFAULT_TIMEOUT_SECONDS
    return value


def _concurrency() -> int:
    raw = (os.getenv('OFFICE_CONVERT_CONCURRENCY') or '').strip()
    if not raw:
        return DEFAULT_CONCURRENCY
    try:
        value = int(raw)
    except ValueError:
        return DEFAULT_CONCURRENCY
    if value <= 0:
        return DEFAULT_CONCURRENCY
    return value


_convert_semaphore = threading.BoundedSemaphore(_concurrency())


def _is_under_any_root(path: Path, roots: Iterable[Path]) -> bool:
    resolved = path.resolve()
    for root in roots:
        try:
            resolved.relative_to(root)
            return True
        except ValueError:
            continue
    return False


def _expected_pdf_path(source: Path) -> Path:
    return source.with_name(f'{source.stem}.pdf')


def _validate_source_path(source_path: str) -> Path:
    source = Path(source_path).expanduser().resolve()
    if not source.exists() or not source.is_file():
        raise HTTPException(status_code=404, detail='source file not found')
    if source.suffix.lower() not in OFFICE_EXTENSIONS:
        raise HTTPException(status_code=400, detail='source file is not a supported office document')
    if not _is_under_any_root(source, _allowed_roots()):
        raise HTTPException(status_code=400, detail='source path is outside allowed roots')
    return source


def _validate_shared_path(raw_path: str, *, must_exist: bool = True) -> Path:
    path = Path(raw_path).expanduser().resolve()
    if not _is_under_any_root(path, _allowed_roots()):
        raise HTTPException(status_code=400, detail='path is outside allowed roots')
    if must_exist and (not path.exists() or not path.is_file()):
        raise HTTPException(status_code=404, detail='input file not found')
    return path


def _reuse_if_fresh(source: Path, target: Path) -> bool:
    if not target.exists() or not target.is_file() or target.stat().st_size <= 0:
        return False
    return target.stat().st_mtime >= source.stat().st_mtime


def _normalize_pdf_block_text(value: str) -> str:
    # Native PDF text blocks contain visual line endings, not semantic paragraph
    # breaks. Remove those endings before translation so the provider receives a
    # complete paragraph and the renderer, not the source PDF, performs wrapping.
    value = re.sub(r'(?<=\w)-\s*\n\s*(?=[a-z])', '', value)
    value = re.sub(r'\s*\n\s*', ' ', value)
    value = re.sub(r'[ \t]+', ' ', value).strip()
    # PDF extraction reports visual line wraps, so they are flattened above.
    # Bullet markers are semantic, however: restore one item per line even when
    # a translation provider changes the common bullet from "•" to "·".
    value = re.sub(r'\s*[•●▪◦·]\s*', '\n· ', value)
    return value.lstrip()


def _looks_like_code_block(text: str, spans: list[dict[str, object]]) -> bool:
    visible_spans = [span for span in spans if str(span.get('text', '')).strip()]
    total_chars = sum(len(str(span.get('text', '')).strip()) for span in visible_spans)
    monospace_chars = sum(
        len(str(span.get('text', '')).strip())
        for span in visible_spans
        if (
            int(span.get('flags', 0)) & fitz.TEXT_FONT_MONOSPACED
            or re.search(r'(mono|courier|typewriter|newtxtt|cmtt|code)', str(span.get('font', '')), re.I)
        )
    )
    if total_chars and monospace_chars / total_chars >= 0.7:
        return True
    return bool(re.match(
        r'^(?:@\w+|async\s+def\b|def\b|class\b|import\b|from\b|if\b|elif\b|else\s*:|for\b|while\b|'
        r'try\s*:|except\b|finally\s*:|with\b|return\b|raise\b|yield\b|[A-Za-z_]\w*\s*=)',
        text,
    ))


def _protect_short_latin_terms(value: str) -> str:
    # PyMuPDF treats non-breaking spaces as legal line-break opportunities. Join
    # established technical names instead, which is also their conventional form.
    terms = {
        'Flash Attention': 'FlashAttention',
        'Flex Attention': 'FlexAttention',
        'Paged Attention': 'PagedAttention',
        'Document Masking': 'DocumentMasking',
        'Sliding Window Attention': 'SlidingWindowAttention',
        'Neighborhood Attention': 'NeighborhoodAttention',
        'Large Language Models': 'LargeLanguageModels',
    }
    for source, target in terms.items():
        value = value.replace(source, target)
    return value


def _balanced_heading_text(value: str, font: fitz.Font, font_size: float, max_width: float) -> str:
    """Wrap mixed CJK/Latin headings into the fewest, visually balanced lines."""
    value = re.sub(r'\s+', ' ', value).strip()
    if not value or max_width <= 0:
        return value
    # Keep Latin identifiers intact while allowing CJK headings to wrap at each
    # character. Leading whitespace remains attached to the following token and
    # is removed only when that token starts a new line.
    tokens = re.findall(r'\s*[A-Za-z0-9][A-Za-z0-9._/+:-]*|\s*[^\sA-Za-z0-9]', value)
    if not tokens:
        return value

    segment_cache: dict[tuple[int, int], tuple[str, float]] = {}

    def segment(start: int, end: int) -> tuple[str, float]:
        key = (start, end)
        if key not in segment_cache:
            text = ''.join(tokens[start:end]).strip()
            segment_cache[key] = (text, font.text_length(text, fontsize=font_size))
        return segment_cache[key]

    # First calculate the minimum number of lines required from every token.
    token_count = len(tokens)
    minimum_lines = [token_count + 1] * (token_count + 1)
    minimum_lines[token_count] = 0
    for start in range(token_count - 1, -1, -1):
        for end in range(start + 1, token_count + 1):
            _, width = segment(start, end)
            if width > max_width and end > start + 1:
                break
            if width <= max_width:
                minimum_lines[start] = min(minimum_lines[start], 1 + minimum_lines[end])

    if minimum_lines[0] > token_count:
        return value

    # Among layouts with the fewest lines, minimize raggedness and heavily
    # penalize an orphaned final character / very short final line.
    best_cost = [float('inf')] * (token_count + 1)
    best_break = [-1] * (token_count + 1)
    best_cost[token_count] = 0.0
    for start in range(token_count - 1, -1, -1):
        for end in range(start + 1, token_count + 1):
            text, width = segment(start, end)
            if width > max_width and end > start + 1:
                break
            if width > max_width or minimum_lines[start] != 1 + minimum_lines[end]:
                continue
            unused = max_width - width
            cost = unused * unused + best_cost[end]
            if end == token_count and minimum_lines[0] > 1:
                visible_chars = len(re.sub(r'\s+', '', text))
                if visible_chars <= 1 or width < max_width * 0.22:
                    cost += max_width * max_width * 100
            if cost < best_cost[start]:
                best_cost[start] = cost
                best_break[start] = end

    lines: list[str] = []
    start = 0
    while start < token_count and best_break[start] > start:
        end = best_break[start]
        lines.append(segment(start, end)[0])
        start = end
    return '\n'.join(lines) if start == token_count else value


def _split_list_items(value: str) -> list[str]:
    normalized = value.strip()
    if not normalized.startswith('· '):
        return []
    items = [item.strip() for item in re.split(r'\n·\s*', normalized[2:]) if item.strip()]
    return items


def _protect_latin_word_wrapping(value: str) -> str:
    """Give the PDF line breaker real word boundaries around Latin terms."""
    latin_term = r'[A-Za-z][A-Za-z0-9_./+:-]*'
    value = re.sub(rf'([\u3400-\u9fff])({latin_term})', r'\1 \2', value)
    value = re.sub(rf'({latin_term})([\u3400-\u9fff])', r'\1 \2', value)
    return value


def _restore_source_latin_terms(value: str, source_text: str) -> str:
    """Repair technical names split by a provider or an earlier PDF wrap."""
    terms = {
        match.group(0)
        for match in re.finditer(r'[A-Za-z][A-Za-z0-9_.+]*', source_text)
        if len(match.group(0)) >= 4 and (
            any(character.isupper() for character in match.group(0)[1:])
            or any(not character.isalpha() for character in match.group(0))
        )
    }
    for term in sorted(terms, key=len, reverse=True):
        broken = r'[\s-]*'.join(re.escape(character) for character in term)
        value = re.sub(broken, term, value, flags=re.IGNORECASE)
    return value


def _wrap_mixed_text(value: str, font: fitz.Font, fontsize: float, max_width: float) -> str:
    """Wrap CJK per character while keeping Latin technical terms atomic."""
    wrapped_paragraphs: list[str] = []
    for paragraph in value.splitlines() or [value]:
        tokens = re.findall(
            r'[A-Za-z][A-Za-z0-9_.+:/-]*(?:\s*\[[0-9,\s]+\])?|\[[0-9,\s]+\]|\s+|.',
            paragraph,
        )
        lines: list[str] = []
        current = ''
        for token in tokens:
            if token.isspace():
                if '\u3000' in token:
                    current += '\u3000' * token.count('\u3000')
                    continue
                if current and not current.endswith(' '):
                    current += ' '
                continue
            candidate = current + token
            if current and font.text_length(candidate, fontsize=fontsize) > max_width:
                lines.append(current.rstrip())
                current = token
            else:
                current = candidate
        if current.rstrip():
            lines.append(current.rstrip())
        wrapped_paragraphs.append('\n'.join(lines))
    return '\n'.join(wrapped_paragraphs)


def _insert_list_textbox(
    page: fitz.Page,
    rect: fitz.Rect,
    value: str,
    *,
    fontsize: float,
    lineheight: float,
    font: fitz.Font,
    color: tuple[float, float, float],
    overlay: bool = True,
) -> float:
    items = _split_list_items(value)
    if not items:
        return -1.0
    indent = max(fontsize * 1.35, 4.5)
    item_gap = max(fontsize * 0.3, 1.0)
    cursor_y = rect.y0
    for item in items:
        body_rect = fitz.Rect(rect.x0 + indent, cursor_y, rect.x1, rect.y1)
        item = _wrap_mixed_text(item, font, fontsize, body_rect.width)
        remaining = page.insert_textbox(
            body_rect,
            item,
            fontsize=fontsize,
            lineheight=lineheight,
            fontname='NotoSansCJK',
            color=color,
            align=fitz.TEXT_ALIGN_LEFT,
            overlay=overlay,
        )
        if remaining < 0:
            return remaining
        used_height = body_rect.height - remaining
        radius = max(fontsize * 0.16, 0.8)
        page.draw_circle(
            fitz.Point(rect.x0 + radius, cursor_y + fontsize * 0.55),
            radius,
            color=color,
            fill=color,
            overlay=overlay,
        )
        cursor_y += used_height + item_gap
        if cursor_y > rect.y1:
            return rect.y1 - cursor_y
    return rect.y1 - cursor_y


def _run_libreoffice_convert(source: Path, target: Path) -> None:
    output_dir = target.parent
    output_dir.mkdir(parents=True, exist_ok=True)

    with (
        tempfile.TemporaryDirectory(dir=str(output_dir)) as tmpdir,
        tempfile.TemporaryDirectory(prefix='lo-profile-') as profile_dir,
    ):
        tmp_output_dir = Path(tmpdir)
        profile_uri = Path(profile_dir).resolve().as_uri()
        command = [
            'libreoffice',
            f'-env:UserInstallation={profile_uri}',
            '--headless',
            '--nologo',
            '--nofirststartwizard',
            '--nolockcheck',
            '--nodefault',
            '--convert-to',
            'pdf',
            str(source),
            '--outdir',
            str(tmp_output_dir),
        ]
        logger.info('running libreoffice convert source=%s target=%s', source, target)
        try:
            completed = subprocess.run(
                command,
                check=True,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                text=True,
                timeout=_timeout_seconds(),
            )
        except subprocess.TimeoutExpired as exc:
            raise HTTPException(status_code=504, detail=f'libreoffice convert timeout: {exc}') from exc
        except subprocess.CalledProcessError as exc:
            stderr = (exc.stderr or '').strip()
            stdout = (exc.stdout or '').strip()
            detail = stderr or stdout or str(exc)
            raise HTTPException(status_code=500, detail=f'libreoffice convert failed: {detail}') from exc

        converted_tmp = tmp_output_dir / f'{source.stem}.pdf'
        if not converted_tmp.exists() or converted_tmp.stat().st_size <= 0:
            stdout = (completed.stdout or '').strip()
            stderr = (completed.stderr or '').strip()
            raise HTTPException(status_code=500, detail=f'converted pdf not found; stdout={stdout}; stderr={stderr}')

        shutil.move(str(converted_tmp), str(target))


@app.get('/health')
def health() -> dict[str, str]:
    return {'status': 'ok'}


@app.post('/v1/office/to-pdf', response_model=ConvertResponse)
def convert_office_to_pdf(req: ConvertRequest) -> ConvertResponse:
    source = _validate_source_path(req.source_path)
    target = _expected_pdf_path(source)

    if _reuse_if_fresh(source, target):
        logger.info('reuse converted pdf source=%s target=%s', source, target)
        return ConvertResponse(pdf_path=str(target), reused=True)

    logger.info('waiting for convert slot source=%s concurrency=%d', source, _concurrency())
    with _convert_semaphore:
        _run_libreoffice_convert(source, target)
    if not target.exists() or target.stat().st_size <= 0:
        raise HTTPException(status_code=500, detail='converted pdf not found after libreoffice run')

    logger.info('convert succeeded source=%s target=%s size=%d', source, target, target.stat().st_size)
    return ConvertResponse(pdf_path=str(target), reused=False)


@app.post('/v1/pdf/render-translation')
def render_pdf_translation(req: PDFTranslationRenderRequest) -> dict[str, object]:
    source = _validate_shared_path(req.source_path)
    layout = _validate_shared_path(req.layout_path)
    translations_file = _validate_shared_path(req.translations_path)
    output = _validate_shared_path(req.output_path, must_exist=False)
    if source.suffix.lower() != '.pdf' or output.suffix.lower() != '.pdf':
        raise HTTPException(status_code=400, detail='PDF source and output are required')
    try:
        blocks = json.loads(layout.read_text(encoding='utf-8')).get('blocks', [])
        translations = json.loads(translations_file.read_text(encoding='utf-8'))
    except (OSError, ValueError, TypeError) as exc:
        raise HTTPException(status_code=400, detail=f'invalid translation manifest: {exc}') from exc

    output.parent.mkdir(parents=True, exist_ok=True)
    document = fitz.open(source)
    warnings = 0
    rendered_blocks = 0
    requested_blocks = 0
    probe_document = fitz.open()
    for source_page in document:
        probe_page = probe_document.new_page(width=source_page.rect.width, height=source_page.rect.height)
        probe_page.insert_font(fontname='NotoSansCJK', fontfile=str(TRANSLATION_FONT_PATH))
    translation_font = fitz.Font(fontfile=str(TRANSLATION_FONT_PATH))
    prepared_blocks: list[dict[str, object]] = []
    try:
        candidates: list[dict[str, object]] = []
        for block in blocks:
            translated = _protect_short_latin_terms(
                _normalize_pdf_block_text(str(translations.get(str(block.get('id', '')), '')))
            )
            bbox = block.get('bbox')
            page_index = int(block.get('page', 1)) - 1
            if not translated or not isinstance(bbox, list) or len(bbox) != 4 or not (0 <= page_index < len(document)):
                continue
            requested_blocks += 1
            page = document[page_index]
            source_width = float(block.get('pageWidth') or page.rect.width)
            source_height = float(block.get('pageHeight') or page.rect.height)
            sx, sy = page.rect.width / source_width, page.rect.height / source_height
            rect = fitz.Rect(float(bbox[0]) * sx, float(bbox[1]) * sy, float(bbox[2]) * sx, float(bbox[3]) * sy)
            if rect.is_empty or rect.is_infinite:
                warnings += 1
                continue
            # Keep a small gutter inside the source block. This prevents glyph
            # ascenders / descenders from visually touching an adjacent block even
            # though both text boxes technically fit their source rectangles.
            redaction_rect = fitz.Rect(rect)
            is_single_line = rect.height < 18
            inset_rect = (
                fitz.Rect(rect.x0 + 0.5, rect.y0, rect.x1 - 0.5, rect.y1)
                if is_single_line
                else fitz.Rect(rect.x0 + 0.5, rect.y0 + 0.5, rect.x1 - 0.5, rect.y1 - 1.5)
            )
            if not inset_rect.is_empty:
                rect = inset_rect
            level = str(block.get('styleLevel') or 'body')
            if level not in {'title', 'heading', 'body', 'caption', 'footnote', 'reference'}:
                level = 'body'
            is_list = bool(_split_list_items(translated))
            source_text = str(block.get('text') or '').strip()
            source_indent = float(block.get('firstLineIndent') or 0)
            indent_first_line = (
                source_indent > float(block.get('fontSize') or 10.0) * 0.4
                or (
                    level == 'body'
                    and not is_list
                    and bool(re.match(r'^[A-Z“"(]', source_text))
                )
            )
            candidates.append({
                'page': page_index,
                'rect': rect,
                'redaction_rect': redaction_rect,
                'text': translated,
                'source_text': source_text,
                'level': level,
                'source_size': max(3.0, min(24.0, float(block.get('fontSize') or 10.0))),
                'bold': bool(block.get('bold')),
                'align': str(block.get('align') or 'justify'),
                'single_line': is_single_line,
                'is_list': is_list,
                'indent_first_line': indent_first_line,
            })

        # A translated body block needs a visible paragraph gap when it follows
        # a heading in the same column. Source PDFs often encode only a tiny
        # baseline gap that becomes visually lost with CJK glyph metrics.
        headings = [item for item in candidates if item['level'] in {'title', 'heading'}]
        for candidate in candidates:
            if candidate['level'] != 'body':
                continue
            preceding = [
                heading for heading in headings
                if heading['page'] == candidate['page']
                and 0 <= candidate['rect'].y0 - heading['rect'].y1 <= max(14, candidate['source_size'] * 1.5)
                and (candidate['rect'] & fitz.Rect(
                    heading['rect'].x0, candidate['rect'].y0,
                    heading['rect'].x1, candidate['rect'].y1,
                )).width > 0
            ]
            if preceding:
                top_gap = min(candidate['source_size'] * 0.6, candidate['rect'].height * 0.08)
                candidate['rect'].y0 += top_gap

        # Plan typography once for the whole document. The median source size keeps
        # each semantic level close to the original design. Every block is then
        # measured and the smallest fitting size becomes the shared size for that
        # level, guaranteeing consistency without overflowing any source bbox.
        level_candidates: dict[str, list[dict[str, object]]] = {}
        for candidate in candidates:
            level_candidates.setdefault(str(candidate['level']), []).append(candidate)
        planned_sizes: dict[str, float] = {}
        safety_factors = {
            'title': 0.98,
            'heading': 0.96,
            'body': 0.92,
            'caption': 0.92,
            'footnote': 0.92,
            'reference': 0.92,
        }
        minimum_sizes = {
            'title': 5.0,
            'heading': 4.5,
            'body': 3.5,
            'caption': 3.0,
            'footnote': 3.0,
            'reference': 3.0,
        }
        for level, level_blocks in level_candidates.items():
            source_sizes = [float(item['source_size']) for item in level_blocks]
            target_size = statistics.median(source_sizes) * safety_factors[level]
            fitting_sizes: list[float] = []
            for candidate in level_blocks:
                probe_page = probe_document[int(candidate['page'])]
                font_size = target_size
                remaining = -1.0
                while remaining < 0 and font_size >= minimum_sizes[level]:
                    is_bold = bool(candidate['bold'])
                    measured_text = _protect_latin_word_wrapping(_restore_source_latin_terms(
                        str(candidate['text']), str(candidate['source_text']),
                    ))
                    if candidate['indent_first_line']:
                        measured_text = '\u3000\u3000' + measured_text
                    if level in {'title', 'heading'} and not candidate['single_line']:
                        measured_text = _balanced_heading_text(
                            measured_text, translation_font, font_size, candidate['rect'].width,
                        )
                    elif not candidate['single_line'] and not candidate['is_list']:
                        measured_text = _wrap_mixed_text(
                            measured_text, translation_font, font_size, candidate['rect'].width,
                        )
                    if candidate['single_line']:
                        text_width = translation_font.text_length(measured_text, fontsize=font_size)
                        line_height_limit = candidate['rect'].height * 1.05
                        remaining = candidate['rect'].width - text_width if font_size <= line_height_limit else -1.0
                    elif candidate['is_list']:
                        remaining = _insert_list_textbox(
                            probe_page,
                            candidate['rect'],
                            measured_text,
                            fontsize=font_size,
                            lineheight=1.05,
                            font=translation_font,
                            color=(0, 0, 0),
                        )
                    else:
                        remaining = probe_page.insert_textbox(
                            candidate['rect'],
                            measured_text,
                            fontsize=font_size,
                            lineheight=1.05 if level in {'body', 'caption'} else 1.0,
                            fontname='NotoSansCJK',
                            color=(0, 0, 0),
                            align=(
                                fitz.TEXT_ALIGN_CENTER
                                if candidate['align'] == 'center'
                                else fitz.TEXT_ALIGN_JUSTIFY
                            ),
                            render_mode=2 if is_bold else 0,
                            border_width=0.06 if is_bold else 0.05,
                        )
                    if remaining < 0:
                        font_size -= 0.25
                if remaining < 0:
                    warnings += 1
                    break
                fitting_sizes.append(font_size)
            if len(fitting_sizes) == len(level_blocks):
                planned_sizes[level] = min(fitting_sizes)

        for candidate in candidates:
            level = str(candidate['level'])
            font_size = planned_sizes.get(level)
            if font_size is None:
                continue
            candidate['font_size'] = font_size
            prepared_text = _protect_latin_word_wrapping(_restore_source_latin_terms(
                str(candidate['text']), str(candidate['source_text']),
            ))
            if candidate['indent_first_line']:
                prepared_text = '\u3000\u3000' + prepared_text
            candidate['render_text'] = (
                _balanced_heading_text(
                    prepared_text, translation_font, font_size, candidate['rect'].width,
                )
                if level in {'title', 'heading'} and not candidate['single_line']
                else _wrap_mixed_text(
                    prepared_text, translation_font, font_size, candidate['rect'].width,
                ) if not candidate['single_line'] and not candidate['is_list'] else prepared_text
            )
            candidate['line_height'] = 1.0
            if not candidate['single_line'] and level in {'body', 'caption', 'footnote', 'reference'}:
                # Font sizes are shared by semantic level, while line spacing may
                # use the spare vertical room of each individual block. Probe in
                # small increments and never exceed 2x. Sparse blocks can use
                # their available height without changing the shared font size.
                probe_page = probe_document[int(candidate['page'])]
                for line_height_step in range(105, 201, 5):
                    line_height = line_height_step / 100
                    if candidate['is_list']:
                        remaining = _insert_list_textbox(
                            probe_page,
                            candidate['rect'],
                            str(candidate['render_text']),
                            fontsize=font_size,
                            lineheight=line_height,
                            font=translation_font,
                            color=(0, 0, 0),
                        )
                    else:
                        remaining = probe_page.insert_textbox(
                            candidate['rect'],
                            str(candidate['render_text']),
                            fontsize=font_size,
                            lineheight=line_height,
                            fontname='NotoSansCJK',
                            color=(0, 0, 0),
                            align=(
                                fitz.TEXT_ALIGN_CENTER
                                if candidate['align'] == 'center'
                                else fitz.TEXT_ALIGN_JUSTIFY
                            ),
                            render_mode=2 if candidate['bold'] else 0,
                            border_width=0.06 if candidate['bold'] else 0.05,
                        )
                    if remaining < 0:
                        break
                    candidate['line_height'] = line_height
            prepared_blocks.append(candidate)

        # Redact once per page, preserving both raster images and vector graphics.
        # Applying one redaction at a time can repeatedly damage overlapping line
        # art and can also erase text inserted for a previous block.
        page_indexes = sorted({int(item['page']) for item in prepared_blocks})
        for page_index in page_indexes:
            page = document[page_index]
            for item in prepared_blocks:
                if int(item['page']) == page_index:
                    page.add_redact_annot(item['redaction_rect'], fill=(1, 1, 1))
            page.apply_redactions(
                images=fitz.PDF_REDACT_IMAGE_NONE,
                graphics=fitz.PDF_REDACT_LINE_ART_NONE,
                text=fitz.PDF_REDACT_TEXT_REMOVE,
            )
            page.insert_font(fontname='NotoSansCJK', fontfile=str(TRANSLATION_FONT_PATH))

        for item in prepared_blocks:
            page_index = int(item['page'])
            page = document[page_index]
            if item['single_line']:
                text_width = translation_font.text_length(str(item['render_text']), fontsize=float(item['font_size']))
                start_x = item['rect'].x0
                if item['align'] == 'center':
                    start_x += max((item['rect'].width - text_width) / 2, 0)
                page.insert_text(
                    fitz.Point(start_x, item['rect'].y1 - 0.5),
                    str(item['render_text']),
                    fontsize=float(item['font_size']),
                    fontname='NotoSansCJK',
                    color=(0, 0, 0),
                    overlay=True,
                    render_mode=2 if item['bold'] else 0,
                    border_width=0.06 if item['bold'] else 0.05,
                )
                actual_remaining = item['rect'].width - text_width
            elif item['is_list']:
                actual_remaining = _insert_list_textbox(
                    page,
                    item['rect'],
                    str(item['render_text']),
                    fontsize=float(item['font_size']),
                    lineheight=float(item['line_height']),
                    font=translation_font,
                    color=(0, 0, 0),
                    overlay=True,
                )
            else:
                actual_remaining = page.insert_textbox(
                    item['rect'],
                    str(item['render_text']),
                    fontsize=float(item['font_size']),
                    lineheight=float(item['line_height']),
                    fontname='NotoSansCJK',
                    color=(0, 0, 0),
                    align=(fitz.TEXT_ALIGN_CENTER if item['align'] == 'center' else fitz.TEXT_ALIGN_JUSTIFY),
                    overlay=True,
                    render_mode=2 if item['bold'] else 0,
                    border_width=0.06 if item['bold'] else 0.05,
                )
            if actual_remaining < 0:
                warnings += 1
                continue
            rendered_blocks += 1
        if requested_blocks == 0:
            raise HTTPException(status_code=422, detail='translation manifest contains no renderable text blocks')
        if rendered_blocks != len(prepared_blocks):
            raise HTTPException(
                status_code=500,
                detail=f'translation renderer produced {rendered_blocks} of {len(prepared_blocks)} prepared text blocks',
            )
        target_language = req.target_language.strip().lower()
        is_chinese_translation = target_language.startswith('zh')
        attribution_text = (
            '由 LazyMind 免费翻译 · github.com/LazyAGI/LazyMind'
            if is_chinese_translation
            else 'Free translation by LazyMind · github.com/LazyAGI/LazyMind'
        )
        attribution_url = 'https://github.com/LazyAGI/LazyMind'
        for page in document:
            attribution_font = 'NotoSansCJK' if is_chinese_translation else 'helv'
            if is_chinese_translation:
                page.insert_font(fontname=attribution_font, fontfile=str(TRANSLATION_FONT_PATH))
            attribution_regions = (
                fitz.Rect(18, 3, page.rect.width - 18, 20),
                fitz.Rect(18, page.rect.height - 20, page.rect.width - 18, page.rect.height - 3),
            )
            for region in attribution_regions:
                page.insert_textbox(
                    region,
                    attribution_text,
                    fontsize=10.5,
                    fontname=attribution_font,
                    color=(0.18, 0.18, 0.18),
                    align=fitz.TEXT_ALIGN_CENTER,
                    overlay=True,
                    fill_opacity=0.96,
                )
                page.insert_link({'kind': fitz.LINK_URI, 'from': region, 'uri': attribution_url})
        document.save(output, garbage=4, deflate=True)
    finally:
        probe_document.close()
        document.close()
    return {
        'output_path': str(output),
        'warning_count': warnings,
        'rendered_block_count': rendered_blocks,
        'requested_block_count': requested_blocks,
    }


@app.post('/v1/pdf/extract-translation-layout')
def extract_pdf_translation_layout(req: PDFTranslationLayoutRequest) -> dict[str, object]:
    source = _validate_shared_path(req.source_path)
    output = _validate_shared_path(req.output_path, must_exist=False)
    if source.suffix.lower() != '.pdf' or output.suffix.lower() != '.json':
        raise HTTPException(status_code=400, detail='PDF source and JSON output are required')

    document = fitz.open(source)
    blocks: list[dict[str, object]] = []
    in_references = False
    try:
        for page_index, page in enumerate(document):
            rich_blocks = [
                block for block in page.get_text('dict', sort=True).get('blocks', [])
                if block.get('type') == 0
            ]
            figure_regions: list[fitz.Rect] = []
            for image in page.get_image_info():
                bbox = image.get('bbox')
                if bbox:
                    image_rect = fitz.Rect(bbox)
                    figure_regions.append(fitz.Rect(
                        max(page.rect.x0, image_rect.x0 - 20),
                        max(page.rect.y0, image_rect.y0 - 8),
                        min(page.rect.x1, image_rect.x1 + 20),
                        min(page.rect.y1, image_rect.y1 + 8),
                    ))
            for cluster in page.cluster_drawings():
                # Ignore isolated rules and tiny glyph-like paths. Dense vector
                # diagrams, charts and boxed figures must remain untouched,
                # including any text labels drawn over them.
                if cluster.width >= 25 and cluster.height >= 20 and cluster.width * cluster.height >= 1000:
                    figure_regions.append(fitz.Rect(
                        max(page.rect.x0, cluster.x0 - 60),
                        max(page.rect.y0, cluster.y0 - 8),
                        min(page.rect.x1, cluster.x1 + 60),
                        min(page.rect.y1, cluster.y1 + 8),
                    ))
            block_index = 0
            for raw_block in page.get_text('blocks', sort=True):
                if len(raw_block) < 7 or int(raw_block[6]) != 0:
                    continue
                text = _normalize_pdf_block_text(str(raw_block[4]))
                if not text:
                    continue
                if re.fullmatch(r'references?', text, re.IGNORECASE):
                    in_references = True
                elif re.match(r'^(appendix|[A-Z]\s+[A-Z][A-Z\s-]{4,})$', text):
                    in_references = False
                text_rect = fitz.Rect(raw_block[:4])
                rich_block = max(
                    rich_blocks,
                    key=lambda candidate: (
                        text_rect & fitz.Rect(candidate.get('bbox', (0, 0, 0, 0)))
                    ).get_area(),
                    default={},
                )
                lines = rich_block.get('lines', [])
                spans = [span for line in lines for span in line.get('spans', []) if span.get('text', '').strip()]
                # Equations and code are semantic, non-prose content. Translating
                # them both corrupts the expression and creates very narrow boxes
                # that would force the document-wide body font to an unreadable
                # size. Preserve these blocks verbatim in the source PDF.
                is_code = _looks_like_code_block(text, spans)
                is_formula = (
                    len(text) <= 100
                    and ('=' in text or '√' in text or '∈' in text)
                    and bool(re.search(r'[(){}\[\]\/]|[A-Z]{1,3}', text))
                )
                if is_code or is_formula:
                    continue
                text_area = max(text_rect.width * text_rect.height, 1.0)
                is_caption = re.match(r'^(figure|fig\.|table)\s*\d', text, re.IGNORECASE) is not None
                overlaps_figure = any(
                    (text_rect & region).get_area() / text_area >= 0.15
                    for region in figure_regions
                )
                if not is_caption and overlaps_figure:
                    continue
                # Preserve running headers, footers, narrow side metadata and the
                # author line. They are page furniture rather than translatable
                # content, and fitting translated prose into these narrow regions
                # destroys the original block geometry.
                is_page_furniture = (
                    text_rect.y1 < page.rect.height * 0.08
                    or text_rect.y0 > page.rect.height * 0.88
                    or text_rect.x0 < page.rect.width * 0.07
                    or text_rect.x1 > page.rect.width * 0.93
                )
                is_first_page_author_block = (
                    page_index == 0
                    and page.rect.height * 0.18 <= text_rect.y0
                    and text_rect.y1 < page.rect.height * 0.76
                    and text_rect.height < 30
                    and len(text) < 100
                )
                is_first_page_legal_notice = (
                    page_index == 0
                    and text_rect.y0 > page.rect.height * 0.72
                    and text_rect.x1 < page.rect.width * 0.52
                    and bool(re.search(r'copyright|permission|isbn|doi\.org|proceedings', text, re.IGNORECASE))
                )
                if is_page_furniture or is_first_page_author_block or is_first_page_legal_notice:
                    continue
                visible_rich_lines = [
                    line for line in lines
                    if ''.join(span.get('text', '') for span in line.get('spans', [])).strip()
                ]
                first_line_top = (
                    float(visible_rich_lines[0].get('bbox', (0, 0, 0, 0))[1])
                    if visible_rich_lines else 0.0
                )
                inline_heading_lines = [
                    line for line in visible_rich_lines
                    if abs(float(line.get('bbox', (0, 0, 0, 0))[1]) - first_line_top) < 2
                    and all(
                        int(span.get('flags', 0)) & fitz.TEXT_FONT_BOLD
                        for span in line.get('spans', []) if str(span.get('text', '')).strip()
                    )
                ]
                inline_heading_text = ' '.join(
                    ''.join(str(span.get('text', '')) for span in line.get('spans', [])).strip()
                    for line in inline_heading_lines
                ).strip()
                body_lines = visible_rich_lines[len(inline_heading_lines):]
                if re.match(r'^\d+(?:\.\d+)+\s+\S', inline_heading_text) and body_lines:
                    heading_rect = fitz.Rect(inline_heading_lines[0].get('bbox'))
                    for line in inline_heading_lines[1:]:
                        heading_rect |= fitz.Rect(line.get('bbox'))
                    body_rect = fitz.Rect(body_lines[0].get('bbox'))
                    for line in body_lines[1:]:
                        body_rect |= fitz.Rect(line.get('bbox'))
                    heading_spans = [span for line in inline_heading_lines for span in line.get('spans', [])]
                    body_spans = [span for line in body_lines for span in line.get('spans', [])]
                    heading_size = statistics.median(float(span.get('size', 10)) for span in heading_spans)
                    body_size = statistics.median(float(span.get('size', 10)) for span in body_spans)
                    body_text = _normalize_pdf_block_text('\n'.join(
                        ''.join(str(span.get('text', '')) for span in line.get('spans', []))
                        for line in body_lines
                    ))
                    blocks.append({
                        'id': f'native-block-{page_index + 1}-{block_index}',
                        'page': page_index + 1,
                        'pageWidth': page.rect.width,
                        'pageHeight': page.rect.height,
                        'bbox': [float(value) for value in heading_rect],
                        'type': 'native_text_block',
                        'text': inline_heading_text,
                        'fontSize': heading_size,
                        'bold': True,
                        'align': 'justify',
                        'styleLevel': 'heading',
                        'firstLineIndent': 0.0,
                    })
                    block_index += 1
                    blocks.append({
                        'id': f'native-block-{page_index + 1}-{block_index}',
                        'page': page_index + 1,
                        'pageWidth': page.rect.width,
                        'pageHeight': page.rect.height,
                        'bbox': [float(value) for value in body_rect],
                        'type': 'native_text_block',
                        'text': body_text,
                        'fontSize': body_size,
                        'bold': False,
                        'align': 'justify',
                        'styleLevel': 'body',
                        'firstLineIndent': 0.0,
                    })
                    block_index += 1
                    continue
                styled_chars = sum(max(len(span.get('text', '').strip()), 1) for span in spans)
                source_font_size = (
                    sum(float(span.get('size', 10)) * max(len(span.get('text', '').strip()), 1) for span in spans)
                    / styled_chars
                    if styled_chars else 10.0
                )
                bold_chars = sum(
                    max(len(span.get('text', '').strip()), 1)
                    for span in spans
                    if int(span.get('flags', 0)) & fitz.TEXT_FONT_BOLD
                )
                centered_lines = 0
                visible_lines = 0
                for line in lines:
                    if not ''.join(span.get('text', '') for span in line.get('spans', [])).strip():
                        continue
                    line_rect = fitz.Rect(line.get('bbox', (0, 0, 0, 0)))
                    visible_lines += 1
                    if abs((line_rect.x0 + line_rect.x1) / 2 - page.rect.width / 2) <= page.rect.width * 0.04:
                        centered_lines += 1
                is_bold = bool(styled_chars and bold_chars / styled_chars >= 0.55)
                is_centered = bool(visible_lines and centered_lines / visible_lines >= 0.6)
                is_first_page_title = (
                    page_index == 0
                    and text_rect.y1 < page.rect.height * 0.25
                    and is_bold
                    and is_centered
                    and len(text) > 25
                )
                if is_caption:
                    style_level = 'caption'
                elif source_font_size < 9.3:
                    style_level = 'footnote'
                elif is_first_page_title or (is_bold and is_centered and source_font_size >= 14):
                    style_level = 'title'
                elif is_bold or source_font_size >= 12:
                    style_level = 'heading'
                elif in_references:
                    style_level = 'reference'
                else:
                    style_level = 'body'
                blocks.append({
                    'id': f'native-block-{page_index + 1}-{block_index}',
                    'page': page_index + 1,
                    'pageWidth': page.rect.width,
                    'pageHeight': page.rect.height,
                    'bbox': [float(value) for value in text_rect],
                    'type': 'native_text_block',
                    'text': text,
                    'fontSize': source_font_size,
                    'bold': is_bold,
                    'align': 'center' if is_centered else 'justify',
                    'styleLevel': style_level,
                    'firstLineIndent': max(
                        0.0,
                        float(lines[0].get('bbox', (text_rect.x0,))[0])
                        - statistics.median([
                            float(line.get('bbox', (text_rect.x0,))[0]) for line in lines[1:]
                            if ''.join(span.get('text', '') for span in line.get('spans', [])).strip()
                        ]),
                    ) if len(lines) > 1 else 0.0,
                })
                block_index += 1
    finally:
        document.close()

    if not blocks:
        raise HTTPException(status_code=422, detail='PDF contains no extractable text blocks')
    output.parent.mkdir(parents=True, exist_ok=True)
    output.write_text(json.dumps({'version': 2, 'blocks': blocks}, ensure_ascii=False), encoding='utf-8')
    return {'output_path': str(output), 'block_count': len(blocks)}


@app.post('/v1/pdf/reference-regions')
def extract_pdf_reference_regions(req: PDFReferenceRegionsRequest) -> dict[str, object]:
    source = _validate_shared_path(req.source_path)
    if source.suffix.lower() != '.pdf':
        raise HTTPException(status_code=400, detail='PDF source is required')
    if not req.references:
        return {'references': []}
    document = fitz.open(source)
    try:
        return {'references': _locate_reference_regions(document, req.references)}
    finally:
        document.close()
