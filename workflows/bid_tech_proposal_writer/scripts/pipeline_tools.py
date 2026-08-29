"""Deterministic business tools for the bid technical-proposal workflow.

The business algorithms are self-contained and never rely on the source skill directory.
Only the final input-wrapper reads immutable material paths from the Workflow runtime.
"""

from __future__ import annotations

import hashlib
import json
import re
import shutil
import subprocess
import tempfile
import zipfile
from collections import Counter
from datetime import datetime, timezone
from pathlib import Path
from typing import Any, Iterable


SUPPORTED_SUFFIXES = {'.docx', '.pdf', '.txt', '.md', '.markdown', '.html', '.htm', '.rtf', '.doc'}
ILLEGAL_TITLE = re.compile(r'[\\/:*?"<>|\r\n\t]')
HAN = re.compile(r'[\u3400-\u4dbf\u4e00-\u9fff]')
REQ_ID = re.compile(r'\b(?:BG|FUNC|PERF|SEC|SVC|IMPL)-\d{3}\b')
DISQ_ID = re.compile(r'\bD-\d{3}\b')
MARKDOWN_HEADING = re.compile(r'^(#{1,6})\s+(.+?)\s*$')
SOURCE_ITEM_ID = re.compile(
    r'^(?P<code>REQ|BG|FUNC|PERF|SEC|SVC|IMPL|DISQ|D)[-_]?\d+(?:\.\d+)*\b', re.I,
)
LIST_ITEM = re.compile(
    r'^(?:[-*+•\uf0b7]\s+|\d+(?:\.\d+)*[、.．)]\s*|[（(]?[一二三四五六七八九十]+[）)、.．]\s*)',
)
BULLET_ONLY = re.compile(r'^[•●▪◦‣·\uf0b7]+$')
NUMBER_PATTERN = re.compile(
    r'(?:[<>≤≥]=?\s*)?\d+(?:\.\d+)?\s*(?:%|秒|毫秒|分钟|小时|天|个|人|台|套|年|月|'
    r'MB|GB|TB|Mbps|Gbps|TPS|QPS|万元|元|级|次|并发|工作日)?', re.I,
)
STANDARD_PATTERN = re.compile(
    r'\b(?:GB/?T|GB|GM/?T|ISO/?IEC|ISO|IEC|JR/?T|YD/?T|DB\d*)[ -]?\d+(?:\.\d+)*(?:-\d{4})?\b',
    re.I,
)
POLICY_PATTERN = re.compile(r'[《「][^》」]{3,50}[》」](?:\s*[（(〔]\d{4}[）)〕]\s*第?\s*\d+\s*号)?')

TOPICS: tuple[tuple[str, str, tuple[str, ...]], ...] = (
    ('BG', '建设背景', ('背景', '现状', '目标', '范围', '政策', '规划', '痛点', '必要性')),
    ('FUNC', '功能要求', ('功能', '模块', '业务', '流程', '查询', '管理', '审批', '展示', '接口', '数据')),
    ('PERF', '性能要求', ('性能', '并发', '响应时间', '吞吐', '可用性', '可靠性', '容量', '延迟', '扩展')),
    ('SEC', '安全要求', ('安全', '等保', '加密', '审计', '权限', '认证', '信创', '国密', '备份', '容灾')),
    ('SVC', '服务要求', ('售后', '服务', '运维', '培训', '质保', '响应', '驻场', '巡检', '保障')),
    ('IMPL', '实施要求', ('实施', '工期', '进度', '里程碑', '交付', '验收', '上线', '试运行', '组织')),
)


def _safe_source(input_path: str) -> Path:
    path = Path(str(input_path or '')).expanduser().resolve()
    if not path.is_file():
        raise FileNotFoundError(f'Bid document does not exist: {input_path}')
    if path.suffix.lower() not in SUPPORTED_SUFFIXES:
        raise ValueError(f'Unsupported bid document format: {path.suffix or "(none)"}')
    return path


def _decode_bytes(content: bytes) -> tuple[str, str]:
    for encoding in ('utf-8-sig', 'utf-8', 'gb18030', 'gbk', 'big5'):
        try:
            return content.decode(encoding), encoding
        except UnicodeDecodeError:
            continue
    return content.decode('utf-8', errors='replace'), 'utf-8-replace'


def _heading_level(text: str, style: str = '') -> int | None:
    style_match = re.search(r'(?:Heading|标题)\s*([1-6])', style, re.I)
    if style_match:
        return min(4, int(style_match.group(1)))
    stripped = text.strip()
    if re.match(r'^第[一二三四五六七八九十百零〇\d]+章(?:\s|　|$)', stripped):
        return 1
    if re.match(r'^[一二三四五六七八九十百零〇]+[、.．]\s*\S', stripped) \
            and len(stripped) <= 80:
        return 1
    numeric = re.match(r'^(\d+(?:\.\d+){0,3})[、.．\s　]+\S', stripped)
    if numeric and len(stripped) <= 80:
        return min(4, numeric.group(1).count('.') + 1)
    return None


def _table_markdown(rows: Iterable[Iterable[str]]) -> list[str]:
    values = [[re.sub(r'\s+', ' ', str(cell or '')).strip() for cell in row] for row in rows]
    values = [row for row in values if any(row)]
    if not values:
        return []
    width = max(len(row) for row in values)
    values = [row + [''] * (width - len(row)) for row in values]
    output = ['| ' + ' | '.join(values[0]) + ' |', '|' + '|'.join(['---'] * width) + '|']
    output.extend('| ' + ' | '.join(row) + ' |' for row in values[1:])
    return output


def _parse_docx(path: Path) -> tuple[str, dict[str, Any]]:
    from docx import Document
    from docx.table import Table
    from docx.text.paragraph import Paragraph

    document = Document(str(path))
    lines: list[str] = []
    sections: list[dict[str, Any]] = []
    tables = 0
    paragraph_index = 0
    for child in document.element.body.iterchildren():
        if child.tag.endswith('}p'):
            paragraph = Paragraph(child, document)
            text = paragraph.text.strip()
            if not text:
                continue
            paragraph_index += 1
            level = _heading_level(text, paragraph.style.name if paragraph.style else '')
            if level:
                lines.append(f"\n{'#' * level} {text}\n")
                sections.append({'paragraph': paragraph_index, 'level': level, 'title': text})
            else:
                lines.append(text)
        elif child.tag.endswith('}tbl'):
            table = Table(child, document)
            rows = [[cell.text.replace('\n', '<br>') for cell in row.cells] for row in table.rows]
            tables += 1
            lines.extend(['', f'**表 {tables}**', *_table_markdown(rows), ''])
    return '\n'.join(lines).strip(), {
        'parser': 'python-docx', 'paragraphs': paragraph_index,
        'tables': tables, 'section_positions': sections,
    }


def _parse_pdf(path: Path) -> tuple[str, dict[str, Any]]:
    try:
        import fitz
    except ImportError as exc:
        raise RuntimeError('PyMuPDF is required for PDF parsing.') from exc
    document = fitz.open(str(path))
    lines: list[str] = []
    sections: list[dict[str, Any]] = []
    for page_no, page in enumerate(document, 1):
        lines.append(f'\n<!-- 第 {page_no} 页 -->\n')
        for raw in page.get_text('text').splitlines():
            text = raw.strip()
            if not text:
                continue
            level = _heading_level(text)
            if level:
                lines.append(f"{'#' * level} {text}")
                sections.append({'page': page_no, 'level': level, 'title': text})
            else:
                lines.append(text)
    metadata = {'parser': 'pymupdf', 'pages': len(document), 'section_positions': sections}
    document.close()
    return '\n'.join(lines).strip(), metadata


def _parse_html(path: Path) -> tuple[str, dict[str, Any]]:
    from bs4 import BeautifulSoup

    raw, encoding = _decode_bytes(path.read_bytes())
    soup = BeautifulSoup(raw, 'html.parser')
    lines: list[str] = []
    sections: list[dict[str, Any]] = []
    tables = 0
    for element in soup.find_all(['h1', 'h2', 'h3', 'h4', 'h5', 'h6', 'p', 'li', 'table']):
        if element.find_parent('table') and element.name != 'table':
            continue
        if re.fullmatch(r'h[1-6]', element.name or ''):
            level = min(4, int(element.name[1]))
            text = element.get_text(' ', strip=True)
            if text:
                lines.append(f"\n{'#' * level} {text}\n")
                sections.append({'level': level, 'title': text})
        elif element.name == 'table':
            rows = [[cell.get_text(' ', strip=True) for cell in row.find_all(['th', 'td'])]
                    for row in element.find_all('tr')]
            tables += 1
            lines.extend(['', f'**表 {tables}**', *_table_markdown(rows), ''])
        else:
            text = element.get_text(' ', strip=True)
            if text:
                lines.append(('- ' if element.name == 'li' else '') + text)
    return '\n'.join(lines).strip(), {
        'parser': 'beautifulsoup4', 'encoding': encoding,
        'tables': tables, 'section_positions': sections,
    }


def _strip_rtf(text: str) -> str:
    text = re.sub(r"\\'([0-9a-fA-F]{2})", lambda m: bytes.fromhex(m.group(1)).decode('cp1252', 'ignore'), text)
    text = re.sub(r'\\u(-?\d+)\??', lambda m: chr(int(m.group(1)) % 65536), text)
    text = re.sub(r'\\(?:par|line)\b', '\n', text)
    text = re.sub(r'\\[a-zA-Z]+-?\d*\s?', '', text)
    text = re.sub(r'[{}]', '', text)
    return re.sub(r'\n{3,}', '\n\n', text).strip()


def _parse_plain(path: Path) -> tuple[str, dict[str, Any]]:
    text, encoding = _decode_bytes(path.read_bytes())
    parser = 'markdown-copy' if path.suffix.lower() in {'.md', '.markdown'} else 'decoded-text'
    if path.suffix.lower() == '.rtf':
        text, parser = _strip_rtf(text), 'rtf-text-fallback'
    if parser != 'markdown-copy':
        converted: list[str] = []
        for line in text.splitlines():
            stripped = line.strip()
            level = _heading_level(stripped)
            converted.append(f"{'#' * level} {stripped}" if level else line)
        text = '\n'.join(converted)
    sections = []
    for line_no, line in enumerate(text.splitlines(), 1):
        match = re.match(r'^(#{1,4})\s+(.+)', line.strip())
        if match:
            sections.append({'line': line_no, 'level': len(match.group(1)), 'title': match.group(2)})
    return text.strip(), {'parser': parser, 'encoding': encoding, 'section_positions': sections}


def _parse_legacy_doc(path: Path) -> tuple[str, dict[str, Any]]:
    converter = shutil.which('soffice') or shutil.which('libreoffice')
    pandoc = shutil.which('pandoc')
    with tempfile.TemporaryDirectory(prefix='bid-doc-convert-') as directory:
        root = Path(directory)
        if converter:
            completed = subprocess.run(
                [converter, '--headless', '--convert-to', 'docx', '--outdir', str(root), str(path)],
                capture_output=True, text=True, timeout=300, check=False,
            )
            converted = root / f'{path.stem}.docx'
            if completed.returncode == 0 and converted.is_file():
                text, metadata = _parse_docx(converted)
                metadata['converted_via'] = Path(converter).name
                return text, metadata
        if pandoc:
            converted = root / f'{path.stem}.md'
            completed = subprocess.run(
                [pandoc, str(path), '-o', str(converted)], capture_output=True,
                text=True, timeout=180, check=False,
            )
            if completed.returncode == 0 and converted.is_file():
                text, metadata = _parse_plain(converted)
                metadata['converted_via'] = 'pandoc'
                return text, metadata
    raise RuntimeError(
        'Legacy .doc parsing requires LibreOffice or pandoc in the LazyMind runtime. '
        'Please convert the document to .docx or .pdf and retry.'
    )


def parse_bid_document(input_path: str) -> dict[str, Any]:
    """Parse a runtime bid-document path into full Markdown text and metadata.

    Args:
        input_path: Exact local path from the workflow remote_inputs mapping.

    Returns:
        A dict containing raw_text and metadata for artifact persistence.
    """
    path = _safe_source(input_path)
    suffix = path.suffix.lower()
    warnings: list[str] = []
    if suffix == '.docx':
        raw_text, metadata = _parse_docx(path)
    elif suffix == '.pdf':
        raw_text, metadata = _parse_pdf(path)
    elif suffix in {'.html', '.htm'}:
        raw_text, metadata = _parse_html(path)
    elif suffix == '.doc':
        raw_text, metadata = _parse_legacy_doc(path)
    else:
        raw_text, metadata = _parse_plain(path)
        if suffix == '.rtf' and metadata['parser'] == 'rtf-text-fallback':
            warnings.append('RTF used the pure-text fallback; inspect complex tables manually.')
    if len(raw_text.strip()) < 100:
        raise ValueError('Parsed bid text is unexpectedly short; the source may be scanned or encrypted.')
    content = path.read_bytes()
    metadata.update({
        'source_filename': path.name,
        'source_format': suffix.lstrip('.'),
        'source_size': len(content),
        'source_sha256': hashlib.sha256(content).hexdigest(),
        'parsed_at': datetime.now(timezone.utc).isoformat(),
        'character_count': len(raw_text),
        'chinese_character_count': len(HAN.findall(raw_text)),
        'section_count': len(metadata.get('section_positions') or []),
        'warnings': warnings,
    })
    return {'raw_text': raw_text, 'metadata': metadata}


def _paragraphs(raw_text: str) -> list[tuple[int, str]]:
    result: list[tuple[int, str]] = []
    buffer: list[str] = []
    buffer_is_heading = False
    first_line = 1

    def flush() -> None:
        nonlocal buffer, buffer_is_heading
        if buffer and not (buffer_is_heading and len(buffer) == 1):
            result.append((first_line, ' '.join(buffer)))
        buffer = []
        buffer_is_heading = False

    for line_no, raw in enumerate(str(raw_text or '').splitlines(), 1):
        line = raw.strip()
        if (not line or line.startswith('<!--') or BULLET_ONLY.fullmatch(line)
                or re.fullmatch(r'\|?\s*:?-{3,}.*', line)):
            flush()
            continue

        # DOCX/HTML tables are normalized to Markdown. Each data row is one source
        # requirement and must not be merged with adjacent rows before classification.
        if line.startswith('|') and line.endswith('|'):
            flush()
            cells = [cell.strip() for cell in line.strip('|').split('|') if cell.strip()]
            if cells:
                result.append((line_no, ' | '.join(cells)))
            continue

        normalized = line.lstrip('# ').strip() if line.startswith('#') else line
        source_item = SOURCE_ITEM_ID.match(normalized)
        starts_item = bool(line.startswith('#') or source_item or LIST_ITEM.match(normalized))
        if starts_item:
            buffered_source = SOURCE_ITEM_ID.match(buffer[0]) if buffer else None
            if (source_item and buffered_source
                    and buffered_source.group('code').upper() in {'DISQ', 'D'}):
                buffer.append(normalized)
                buffer_is_heading = False
                continue
            flush()
            first_line = line_no
            buffer = [normalized]
            buffer_is_heading = line.startswith('#')
            continue

        if buffer and len(' '.join(buffer)) + len(line) > 700:
            flush()
        if not buffer:
            first_line = line_no
        buffer.append(line.strip('| '))
        buffer_is_heading = False
    flush()
    return [(line, re.sub(r'\s+', ' ', text).strip()) for line, text in result if len(text) >= 8]


def _topic(text: str) -> tuple[str, str, list[str]] | None:
    source_id = SOURCE_ITEM_ID.match(text)
    source_code = source_id.group('code').upper() if source_id else ''
    if source_code in {'DISQ', 'D'}:
        return None
    if source_code in {code for code, _, _ in TOPICS}:
        for code, label, keywords in TOPICS:
            if code == source_code:
                hits = [keyword for keyword in keywords if keyword in text]
                return code, label, hits

    candidates: list[tuple[int, int, str, str, list[str]]] = []
    for priority, (code, label, keywords) in enumerate(TOPICS):
        hits = [keyword for keyword in keywords if keyword in text]
        if hits:
            candidates.append((len(hits), -priority, code, label, hits))
    if not candidates:
        return None
    _, _, code, label, hits = max(candidates)
    return code, label, hits


def _suggest_chapter(code: str) -> str:
    return {
        'BG': '项目背景/需求分析', 'FUNC': '功能设计/集成接口',
        'PERF': '总体架构/非功能设计', 'SEC': '安全架构/运维保障',
        'SVC': '售后服务/培训运维', 'IMPL': '实施计划/验收方案',
    }[code]


def extract_technical_requirements(raw_text: str) -> dict[str, Any]:
    """Mechanically extract categorized, source-grounded technical requirements.

    Args:
        raw_text: Complete Markdown text produced by parse_bid_document.

    Returns:
        Markdown candidate list plus category counts for expert review.
    """
    if len(str(raw_text or '').strip()) < 50:
        raise ValueError('raw_text is empty or too short.')
    counters: Counter[str] = Counter()
    records: list[dict[str, Any]] = []
    seen: set[str] = set()
    for line_no, text in _paragraphs(raw_text):
        assigned = _topic(text)
        if not assigned:
            continue
        code, label, hits = assigned
        fingerprint = re.sub(r'\W+', '', text)[:180]
        if fingerprint in seen:
            continue
        seen.add(fingerprint)
        counters[code] += 1
        records.append({
            'id': f'{code}-{counters[code]:03d}', 'category': label,
            'line': line_no, 'triggers': hits[:8],
            'numbers': list(dict.fromkeys(match.group(0).strip() for match in NUMBER_PATTERN.finditer(text)))[:12],
            'standards': list(dict.fromkeys(match.group(0) for match in STANDARD_PATTERN.finditer(text)))[:8],
            'policies': list(dict.fromkeys(match.group(0) for match in POLICY_PATTERN.finditer(text)))[:6],
            'excerpt': text[:700], 'suggested_chapter': _suggest_chapter(code),
        })
    lines = ['# 技术要求清单', '', '> 本清单由确定性扫描生成，已按单一主类归档；正文必须覆盖全部 ID。', '']
    for code, label, _ in TOPICS:
        matches = [record for record in records if record['id'].startswith(code + '-')]
        if not matches:
            continue
        lines.extend([f'## {label}', ''])
        for record in matches:
            lines.extend([
                f"### {record['id']}",
                f"- **来源位置**：解析文本第 {record['line']} 行",
                f"- **触发关键词**：{'、'.join(record['triggers']) or '—'}",
                f"- **关键数字**：{'、'.join(record['numbers']) or '—'}",
                f"- **引用标准**：{'、'.join(record['standards']) or '—'}",
                f"- **政策依据**：{'、'.join(record['policies']) or '—'}",
                f"- **原文摘录**：{record['excerpt']}",
                f"- **建议章节**：{record['suggested_chapter']}", '',
            ])
    lines.extend(['## 提取统计', ''])
    lines.extend(f'- {code}：{counters[code]} 项' for code, _, _ in TOPICS)
    return {'markdown': '\n'.join(lines).strip(), 'counts': dict(counters), 'total': len(records)}


PRIMARY_DISQ = (
    '废标', '否决', '无效投标', '否则视为', '不接受偏离', '不得偏离', '实质性不响应', '★', '▲',
)
RISK_DISQ = ('必须', '应当', '不得', '严禁', '资格要求', '资质要求', '项目经理', '工期',
             '质保', '响应时间', '等保', '信创', '国密', '本地化部署', '源代码', '知识产权')


def _disq_category(text: str) -> str:
    for label, words in (
        ('资格审查', ('资格', '资质', '证书', '业绩')), ('人员要求', ('人员', '项目经理', '工程师')),
        ('工期要求', ('工期', '进度', '交付', '里程碑')), ('售后要求', ('售后', '质保', '响应', '服务')),
        ('安全合规', ('安全', '等保', '信创', '国密', '本地化')), ('知识产权', ('知识产权', '源代码')),
    ):
        if any(word in text for word in words):
            return label
    return '技术参数'


def extract_disqualification_items(raw_text: str) -> dict[str, Any]:
    """Scan bid text for explicit rejection clauses and high-risk mandatory clauses.

    Args:
        raw_text: Complete parsed bid Markdown.

    Returns:
        Markdown with D-NNN items and explicit/risk counts.
    """
    records: list[dict[str, Any]] = []
    seen: set[str] = set()
    for line_no, paragraph in _paragraphs(raw_text):
        pieces = re.split(r'(?<=[。；;！？!?])\s*', paragraph)
        for piece in pieces:
            text = piece.strip()
            if len(text) < 8:
                continue
            explicit_hits = [word for word in PRIMARY_DISQ if word in text]
            risk_hits = [word for word in RISK_DISQ if word in text]
            if not explicit_hits and not risk_hits:
                continue
            fingerprint = re.sub(r'\W+', '', text)[:180]
            if fingerprint in seen:
                continue
            seen.add(fingerprint)
            explicit = bool(explicit_hits)
            numbers = list(dict.fromkeys(match.group(0).strip() for match in NUMBER_PATTERN.finditer(text)))[:10]
            records.append({
                'line': line_no, 'text': text[:700], 'explicit': explicit,
                'hits': explicit_hits + risk_hits, 'numbers': numbers,
                'category': _disq_category(text),
                'stars': 5 if explicit else 4 if numbers or any(x in text for x in ('必须', '不得', '等保')) else 3,
            })
    lines = ['# 废标项（实质性条款）清单', '',
             '> 明确否决条款与高风险提醒分区展示；正文必须逐条应答。', '']
    index = 0
    for explicit, heading in ((True, '明确否决条款'), (False, '高风险提醒')):
        lines.extend([f'## {heading}', ''])
        subset = [record for record in records if record['explicit'] is explicit]
        if not subset:
            lines.extend(['- 未扫描到候选项，仍需结合全文人工复核。', ''])
            continue
        for record in subset:
            index += 1
            record_id = f'D-{index:03d}'
            response = {
                '资格审查': '在项目理解中声明满足，并在商务附件提供证明材料。',
                '人员要求': '在实施组织中逐项列明人员、角色、证书与投入安排。',
                '工期要求': '在实施计划中给出一致的总工期、里程碑和验收节点。',
                '售后要求': '在售后服务中给出质保期、服务时段和分级响应承诺。',
                '安全合规': '在安全架构中给出标准、控制措施、测评及交付证据。',
                '知识产权': '在项目理解和交付方案中明确权属、授权及源代码安排。',
                '技术参数': '在功能或技术架构章节以原值明确应答并给出验证方式。',
            }[record['category']]
            lines.extend([
                f'### 废标项 {record_id}',
                f"- **类别**：{record['category']}",
                f"- **风险等级**：{'★' * record['stars']}",
                f"- **来源位置**：解析文本第 {record['line']} 行",
                f"- **触发依据**：{'、'.join(record['hits'])}",
                f"- **关键指标**：{'、'.join(record['numbers']) or '—'}",
                f"- **招标原文**：{record['text']}",
                f'- **应对要求**：{response}',
                '- **是否已应答**：□ 是 / ☑ 否', '',
            ])
    return {
        'markdown': '\n'.join(lines).strip(), 'total': len(records),
        'explicit_count': sum(record['explicit'] for record in records),
        'risk_count': sum(not record['explicit'] for record in records),
    }


def _json_object(value: Any, name: str) -> dict[str, Any]:
    current = value
    for _ in range(5):
        if isinstance(current, dict):
            nested = next((
                current[key] for key in ('data', 'outline', 'normalized_outline')
                if key in current
            ), current)
            if nested is current:
                return json.loads(json.dumps(current, ensure_ascii=False))
            current = nested
            continue
        text = re.sub(
            r'^```(?:json)?\s*|\s*```$', '', str(current or '').strip(), flags=re.I,
        )
        if not text:
            break
        try:
            current = json.loads(text)
        except (TypeError, json.JSONDecodeError) as exc:
            raise ValueError(f'{name} must be a valid JSON object.') from exc
    raise ValueError(f'{name} must be a JSON object.')


def _target_number(value: Any) -> int:
    if isinstance(value, (int, float)):
        target = int(value)
    else:
        raw = str(value or '')
        ten_thousand = re.search(r'(\d+(?:\.\d+)?)\s*万', raw)
        if ten_thousand:
            target = int(float(ten_thousand.group(1)) * 10000)
        else:
            match = re.search(r'\d[\d,，]*', raw)
            target = int(re.sub(r'[,，]', '', match.group(0))) if match else 0
    if target < 1000 or target > 500000:
        raise ValueError('word_target must be between 1000 and 500000 Chinese characters.')
    return target


def normalize_bid_parameters(output_format: str, word_target: str,
                             use_default_docx_template: str) -> dict[str, Any]:
    """Validate and normalize user-confirmed proposal generation parameters."""
    raw_format = str(output_format or '').strip().lower()
    if raw_format in {'md', 'markdown', '.md', 'markdown文件'} or 'markdown' in raw_format:
        normalized_format = 'md'
    elif raw_format in {'docx', 'word', '.docx', 'word文档', 'word文件'} \
            or 'docx' in raw_format or 'word' in raw_format:
        normalized_format = 'docx'
    else:
        raise ValueError('output_format must be md/Markdown or docx/Word.')

    target = _target_number(word_target)
    raw_style = str(use_default_docx_template or '').strip().lower()
    negative = ('不使用', '不用', '否', 'false', 'no', 'plain', '基础样式', '简洁样式', '不适用')
    positive = ('使用', '默认', '是', 'true', 'yes')
    if any(token in raw_style for token in negative):
        use_default = False
    elif any(token in raw_style for token in positive):
        use_default = True
    else:
        raise ValueError(
            'use_default_docx_template must explicitly say whether to use the default template.'
        )

    return {
        'output_format': normalized_format,
        'word_target': target,
        'use_default_docx_template': use_default if normalized_format == 'docx' else False,
        'docx_style_mode': (
            'default_bid_template' if normalized_format == 'docx' and use_default
            else 'plain_word_styles' if normalized_format == 'docx'
            else 'not_applicable'
        ),
        'display_summary': (
            f'导出格式：{normalized_format.upper()}；目标字数：{target}；'
            + (
                f'DOCX 样式：{"默认投标模板" if use_default else "Word 基础样式"}'
                if normalized_format == 'docx' else 'DOCX 样式：不适用'
            )
        ),
    }


def _walk_chapters(chapters: list[Any], parent_number: str = '', expected_level: int = 1,
                   ancestry: tuple[str, ...] = ()) -> Iterable[tuple[dict[str, Any], tuple[str, ...]]]:
    for raw in chapters:
        if not isinstance(raw, dict):
            continue
        yield raw, ancestry
        children = raw.get('children') if isinstance(raw.get('children'), list) else []
        if children:
            yield from _walk_chapters(children, str(raw.get('number') or ''), expected_level + 1,
                                      ancestry + (str(raw.get('title') or ''),))


def _leaves(chapters: list[Any]) -> list[dict[str, Any]]:
    return [node for node, _ in _walk_chapters(chapters)
            if not (isinstance(node.get('children'), list) and node['children'])]


def _set_parent_targets(nodes: list[Any]) -> int:
    total = 0
    for node in nodes:
        if not isinstance(node, dict):
            continue
        children = node.get('children') if isinstance(node.get('children'), list) else []
        if children:
            node['target_words'] = _set_parent_targets(children)
        total += max(0, int(node.get('target_words') or 0))
    return total


def validate_and_allocate_outline(outline_json: str, requirements_markdown: str,
                                  disqualification_markdown: str, word_target: str) -> dict[str, Any]:
    """Validate outline schema, normalize targets, and check complete ID coverage.

    Args:
        outline_json: Candidate outline object or JSON string.
        requirements_markdown: Authoritative requirement-ID Markdown.
        disqualification_markdown: Authoritative D-NNN Markdown.
        word_target: User-supplied total Chinese-character target.

    Returns:
        valid flag, normalized_outline, Markdown report, and diagnostics.
    """
    outline = _json_object(outline_json, 'outline_json')
    target = _target_number(word_target)
    chapters = outline.get('chapters')
    if not isinstance(chapters, list) or not chapters:
        return {'valid': False, 'normalized_outline': outline,
                'report': '# 大纲检查报告\n\nFAIL：chapters 必须是非空数组。',
                'errors': ['chapters must be a non-empty list'], 'warnings': []}
    errors: list[str] = []
    warnings: list[str] = []

    def visit(nodes: list[Any], parent: str = '', level: int = 1) -> None:
        for index, raw in enumerate(nodes, 1):
            if not isinstance(raw, dict):
                raw = {'title': str(raw or '').strip() or f'章节{index}', 'children': []}
                nodes[index - 1] = raw
                warnings.append(f'{parent or "root"} 第 {index} 项已从标量修复为章节对象')
            number = str(index) if not parent else f'{parent}.{index}'
            try:
                raw_level = int(raw.get('level') or level)
            except (TypeError, ValueError):
                raw_level = level
            raw_number = str(raw.get('number') or number)
            title = re.sub(r'\s+', ' ', str(raw.get('title') or '')).strip()
            if raw_level != level or level > 4:
                warnings.append(f'{raw_number or number} level 已归一化为 {min(level, 4)}')
            if raw_number != number:
                warnings.append(f'{raw_number or "(空)"} 编号已归一化为 {number}')
            if not title:
                title = f'章节{number}'
                warnings.append(f'{number} 空标题已自动补全')
            cleaned_title = ILLEGAL_TITLE.sub(' ', title).strip()
            if cleaned_title != title:
                warnings.append(f'{number} 标题中的非法字符已替换')
            if len(cleaned_title) >= 10:
                warnings.append(f'{number} 标题“{cleaned_title}”较长，但不阻断执行')
            title = cleaned_title or f'章节{number}'
            raw['level'], raw['number'], raw['title'] = level, number, title
            raw.setdefault('bid_requirements_refs', [])
            raw.setdefault('disqualification_refs', [])
            children = raw.get('children')
            if children is None:
                raw['children'] = []
            elif not isinstance(children, list):
                warnings.append(f'{number} children 不是数组，已按叶子章节处理')
                raw['children'] = []
            elif children:
                if level >= 4:
                    warnings.append(f'{number} 超过四级的子章节已提升并合并到第四级')
                    raw['children'] = []
                else:
                    visit(children, number, level + 1)

    visit(chapters)
    leaves = _leaves(chapters)
    if len(leaves) > 30:
        warnings.append(f'叶子章节 {len(leaves)} 个，超过建议上限 30，但不阻断执行')
    if len(leaves) < 3:
        warnings.append(f'叶子章节仅 {len(leaves)} 个，正文颗粒度可能不足')

    weights = [max(1, int(leaf.get('target_words') or 1)) for leaf in leaves]
    weight_sum = sum(weights) or len(leaves) or 1
    # Keep the normal 100-character floor when the requested total permits it.
    # Small smoke-test targets may legitimately have more leaves than target/100;
    # in that case use a dynamic positive floor instead of subtracting the whole
    # overflow from the final leaf (which previously produced negative targets).
    minimum = min(100, max(1, target // len(leaves))) if leaves else 0
    remaining = max(0, target - minimum * len(leaves))
    extras = [(remaining * weight) // weight_sum for weight in weights]
    remainder = remaining - sum(extras)
    if remainder:
        ranked = sorted(
            range(len(weights)),
            key=lambda index: ((remaining * weights[index]) % weight_sum, -index),
            reverse=True,
        )
        for index in ranked[:remainder]:
            extras[index] += 1
    allocated = [minimum + extra for extra in extras]
    if leaves and target < 100 * len(leaves):
        warnings.append(
            f'目标字数不足以按每个叶子 100 字分配，已使用动态下限 {minimum} 字'
        )
    for leaf, amount in zip(leaves, allocated):
        leaf['target_words'] = amount
    _set_parent_targets(chapters)
    outline['total_word_target'] = target
    outline.setdefault('$schema', 'bid-tech-proposal/outline.schema.json')
    outline.setdefault('color_scheme', 'blue')

    required_ids = set(REQ_ID.findall(str(requirements_markdown or '')))
    disq_ids = set(DISQ_ID.findall(str(disqualification_markdown or '')))
    mapped_req: set[str] = set()
    mapped_disq: set[str] = set()
    for leaf in leaves:
        req_refs = leaf.get('bid_requirements_refs')
        disq_refs = leaf.get('disqualification_refs')
        if not isinstance(req_refs, list):
            req_refs = REQ_ID.findall(str(req_refs or ''))
            warnings.append(f"{leaf.get('number')} 技术要求引用已归一化为数组")
        if not isinstance(disq_refs, list):
            disq_refs = DISQ_ID.findall(str(disq_refs or ''))
            warnings.append(f"{leaf.get('number')} 废标引用已归一化为数组")
        req_refs = list(dict.fromkeys(
            ref for item in req_refs for ref in REQ_ID.findall(str(item or ''))
        ))
        disq_refs = list(dict.fromkeys(
            ref for item in disq_refs for ref in DISQ_ID.findall(str(item or ''))
        ))
        unknown_req = set(req_refs) - required_ids
        unknown_disq = set(disq_refs) - disq_ids
        if unknown_req:
            warnings.append(
                f"{leaf.get('number')} 已移除不存在的需求 ID：{', '.join(sorted(unknown_req))}"
            )
        if unknown_disq:
            warnings.append(
                f"{leaf.get('number')} 已移除不存在的废标 ID：{', '.join(sorted(unknown_disq))}"
            )
        req_refs = [ref for ref in req_refs if ref in required_ids]
        disq_refs = [ref for ref in disq_refs if ref in disq_ids]
        leaf['bid_requirements_refs'] = req_refs
        leaf['disqualification_refs'] = disq_refs
        mapped_req.update(req_refs)
        mapped_disq.update(disq_refs)
    missing_req = sorted(required_ids - mapped_req)
    missing_disq = sorted(disq_ids - mapped_disq)
    if leaves:
        def assign(refs: list[str], field: str) -> None:
            for ref in refs:
                prefix = ref.split('-', 1)[0]
                keywords = next((item[2] for item in TOPICS if item[0] == prefix), ())
                ranked = sorted(
                    enumerate(leaves),
                    key=lambda item: (
                        -sum(word in str(item[1].get('title') or '') for word in keywords),
                        len(item[1].get(field) or []), item[0],
                    ),
                )
                selected = ranked[0][1]
                selected.setdefault(field, []).append(ref)

        if missing_req:
            assign(missing_req, 'bid_requirements_refs')
            warnings.append('未映射技术要求 ID 已按章节语义自动分配：' + ', '.join(missing_req))
        if missing_disq:
            assign(missing_disq, 'disqualification_refs')
            warnings.append('未映射废标 ID 已自动分配至最相关章节：' + ', '.join(missing_disq))
        mapped_req.update(missing_req)
        mapped_disq.update(missing_disq)

    valid = not errors
    lines = ['# 大纲检查报告', '', f"## {'PASS' if valid else 'FAIL'}", '',
             f'- 目标总字数：{target}', f'- 一级章节：{len(chapters)}',
             f'- 叶子章节：{len(leaves)}', f'- 技术要求映射：{len(mapped_req)}/{len(required_ids)}',
             f'- 废标项映射：{len(mapped_disq)}/{len(disq_ids)}',
             f'- 叶子字数合计：{sum(int(leaf.get("target_words") or 0) for leaf in leaves)}']
    if errors:
        lines.extend(['', '## 必须修复', *[f'- {value}' for value in errors]])
    if warnings:
        lines.extend(['', '## 提醒', *[f'- {value}' for value in warnings]])
    return {'valid': valid, 'normalized_outline': outline, 'report': '\n'.join(lines),
            'errors': errors, 'warnings': warnings, 'leaf_count': len(leaves)}


def _normalized_section_title(value: str) -> str:
    text = re.sub(r'^\s*\d+(?:\.\d+)*[、.．\s　]+', '', str(value or ''))
    return re.sub(r'[\s\W_]+', '', text, flags=re.UNICODE).lower()


def _section_title_similarity(expected: str, actual: str) -> float:
    left, right = _normalized_section_title(expected), _normalized_section_title(actual)
    if not left or not right:
        return 0.0
    if left == right:
        return 1.0
    if min(len(left), len(right)) >= 4 and (left in right or right in left):
        return 0.95
    left_pairs = {left[index:index + 2] for index in range(max(0, len(left) - 1))}
    right_pairs = {right[index:index + 2] for index in range(max(0, len(right) - 1))}
    if not left_pairs or not right_pairs:
        return 0.0
    return 2 * len(left_pairs & right_pairs) / (len(left_pairs) + len(right_pairs))


def _section_anchors(markdown_text: str) -> list[dict[str, Any]]:
    anchors: list[dict[str, Any]] = []
    cursor = 0
    for line in str(markdown_text or '').splitlines(keepends=True):
        stripped = line.strip()
        heading = re.match(r'^(#{1,6})\s+(.+?)\s*$', stripped)
        if heading:
            raw_title = heading.group(2).strip()
            number_match = re.match(r'^(\d+(?:\.\d+)*)[、.．\s　]+(.+)$', raw_title)
            anchors.append({
                'position': cursor,
                'title': number_match.group(2).strip() if number_match else raw_title,
                'number': number_match.group(1) if number_match else '',
                'kind': 'heading',
                'level': len(heading.group(1)),
            })
        else:
            # Shared Writer sometimes renders planned leaf labels as a bold lead
            # instead of a Markdown heading. Treat only a leading bold span as a
            # compatibility anchor; ordinary bold text later in a paragraph is ignored.
            bold = re.match(r'^\*\*(.+?)\*\*', stripped)
            if bold:
                anchors.append({
                    'position': cursor,
                    'title': bold.group(1).rstrip('。；:：').strip(),
                    'number': '',
                    'kind': 'bold',
                    'level': 3,
                })
        cursor += len(line)
    return anchors


def _section_character_counts(markdown_text: str, leaves: list[dict[str, Any]]) -> list[dict[str, Any]]:
    text = str(markdown_text or '')
    anchors = _section_anchors(text)
    matched: list[dict[str, Any] | None] = []
    cursor = 0
    for leaf in leaves:
        title = str(leaf.get('title') or '').strip()
        number = str(leaf.get('number') or '').strip()
        best: tuple[float, int, str] | None = None
        for index in range(cursor, len(anchors)):
            anchor = anchors[index]
            number_match = bool(number and anchor['number'] == number)
            score = _section_title_similarity(title, anchor['title'])
            # A matching numeric prefix is useful ordering evidence, but must never
            # turn an unrelated heading into a valid leaf section on its own.
            if number_match and score >= 0.35:
                score = max(score, 0.9)
            if score < 0.58:
                continue
            expected_level = min(5, max(3, int(leaf.get('level') or 2) + 1))
            canonical = (
                _normalized_section_title(title) == _normalized_section_title(anchor['title'])
                and anchor['kind'] == 'heading' and anchor['level'] == expected_level
            )
            mode = 'exact' if canonical else ('number' if number_match else 'compatible')
            if best is None or score > best[0]:
                best = (score, index, mode)
                if score >= 1.0:
                    break
        if best is None:
            matched.append(None)
            continue
        score, index, mode = best
        anchor = dict(anchors[index])
        anchor.update({'match_mode': mode, 'match_score': round(score, 3)})
        matched.append(anchor)
        cursor = index + 1

    matched_positions = [item['position'] for item in matched if item]
    results = []
    for leaf, anchor in zip(leaves, matched):
        title = str(leaf.get('title') or '').strip()
        target = max(1, int(leaf.get('target_words') or 0))
        if anchor is None:
            results.append({'number': leaf.get('number'), 'title': title, 'found': False,
                            'actual': 0, 'target': target, 'deviation_pct': 100.0,
                            'match_mode': 'missing', 'matched_title': ''})
            continue
        start = int(anchor['position'])
        next_positions = [position for position in matched_positions if position > start]
        end = min(next_positions) if next_positions else len(text)
        actual = len(HAN.findall(text[start:end]))
        results.append({
            'number': leaf.get('number'), 'title': title, 'found': True,
            'actual': actual, 'target': target,
            'deviation_pct': round(abs(actual - target) * 100 / target, 2),
            'shortfall_pct': round(max(0, target - actual) * 100 / target, 2),
            'excess_pct': round(max(0, actual - target) * 100 / target, 2),
            'match_mode': anchor['match_mode'], 'matched_title': anchor['title'],
            'match_score': anchor['match_score'],
        })
    return results


def validate_proposal_package(markdown_text: str, docx_path: str, outline_json: str,
                              requirements_markdown: str, disqualification_markdown: str,
                              word_target: str, image_manifest_json: str,
                              output_format: str = 'docx') -> dict[str, Any]:
    """Validate final Markdown and the selected delivery file produced by the workflow.

    Args:
        markdown_text: Complete final proposal Markdown.
        docx_path: Backward-compatible parameter name for the resolved final `.md` or
            `.docx` delivery artifact path.
        outline_json: Validated outline object or JSON string.
        requirements_markdown: Requirement-ID Markdown.
        disqualification_markdown: D-NNN Markdown.
        word_target: User-supplied total Chinese-character target.
        image_manifest_json: Image manifest object or JSON string.
        output_format: User-confirmed `md` or `docx` delivery format.

    Returns:
        Markdown report and structured summary with PASS/WARN/FAIL status.
    """
    outline = _json_object(outline_json, 'outline_json')
    manifest = _json_object(image_manifest_json, 'image_manifest_json')
    target = _target_number(word_target)
    raw_format = str(output_format or '').strip().lower()
    if raw_format in {'md', 'markdown', '.md'}:
        normalized_format = 'md'
    elif raw_format in {'docx', 'word', '.docx'}:
        normalized_format = 'docx'
    else:
        raise ValueError('output_format must be canonical md or docx.')
    path = Path(str(docx_path or '')).expanduser().resolve()
    package_valid = False
    markdown_file_matches = False
    embedded_images = 0
    delivery_error = ''
    if normalized_format == 'docx':
        package_valid = path.is_file() and path.suffix.lower() == '.docx' and path.stat().st_size > 5000
    else:
        package_valid = path.is_file() and path.suffix.lower() in {'.md', '.markdown'} \
            and path.stat().st_size > 200
        if package_valid:
            try:
                markdown_file_matches = path.read_text(encoding='utf-8').strip() == str(markdown_text or '').strip()
                package_valid = markdown_file_matches
            except (OSError, UnicodeError) as exc:
                package_valid, delivery_error = False, str(exc)
    if normalized_format == 'docx' and package_valid:
        try:
            with zipfile.ZipFile(path) as archive:
                names = set(archive.namelist())
                package_valid = {'[Content_Types].xml', 'word/document.xml'}.issubset(names)
                embedded_images = len([name for name in names if name.startswith('word/media/')])
        except (OSError, zipfile.BadZipFile) as exc:
            package_valid, delivery_error = False, str(exc)

    actual_words = len(HAN.findall(str(markdown_text or '')))
    total_deviation = round(abs(actual_words - target) * 100 / target, 2)
    leaves = _leaves(outline.get('chapters') or [])
    leaf_results = _section_character_counts(markdown_text, leaves)
    missing_leaves = [item for item in leaf_results if not item['found']]
    leaf_warnings = [
        item for item in leaf_results
        if item['found'] and item.get('shortfall_pct', 0) > 10
    ]
    compatible_headings = [
        item for item in leaf_results if item['found'] and item.get('match_mode') != 'exact'
    ]

    req_ids = sorted(set(REQ_ID.findall(str(requirements_markdown or ''))))
    disq_ids = sorted(set(DISQ_ID.findall(str(disqualification_markdown or ''))))
    uncovered_req = [item for item in req_ids if item not in markdown_text]
    uncovered_disq = [item for item in disq_ids if item not in markdown_text]
    effect_entries = manifest.get('effects') if isinstance(manifest.get('effects'), list) else []
    manifest_images = (1 if manifest.get('architecture') else 0) + len(effect_entries)

    failures: list[str] = []
    warnings: list[str] = []
    if not package_valid:
        failures.append(
            'DOCX 文件不存在、为空或不是有效的 Office Open XML 包'
            if normalized_format == 'docx'
            else 'Markdown 交付文件不存在、为空或与最终正文不一致'
        )
    if missing_leaves:
        failures.append('缺少叶子章节：' + '、'.join(str(item['number']) for item in missing_leaves))
    if uncovered_disq:
        failures.append('未应答废标项：' + '、'.join(uncovered_disq))
    if actual_words < target:
        failures.append(f'总字数不足：{actual_words}/{target}，缺少 {target - actual_words} 个中文字符')
    if uncovered_req:
        warnings.append('未覆盖技术要求：' + '、'.join(uncovered_req))
    if leaf_warnings:
        warnings.append('叶子章节低于分配目标 10% 以上：' + '、'.join(str(item['number']) for item in leaf_warnings))
    if compatible_headings:
        warnings.append(
            f'兼容识别了 {len(compatible_headings)} 个非规范叶子标题；建议恢复为有效大纲中的原始标题'
        )
    if manifest_images < 6:
        warnings.append(f'图像清单仅 {manifest_images} 张，低于 1+5 的最低要求')
    if normalized_format == 'docx' and package_valid and embedded_images < manifest_images:
        warnings.append(f'DOCX 嵌入图片 {embedded_images}/{manifest_images}，存在缺图')
    status = 'FAIL' if failures else 'WARN' if warnings else 'PASS'
    summary = {
        'status': status, 'target_chinese_characters': target,
        'actual_chinese_characters': actual_words, 'total_deviation_pct': total_deviation,
        'word_target_mode': 'minimum', 'word_target_met': actual_words >= target,
        'word_shortfall': max(0, target - actual_words),
        'word_excess': max(0, actual_words - target),
        'leaf_count': len(leaves), 'missing_leaf_count': len(missing_leaves),
        'leaf_word_warning_count': len(leaf_warnings),
        'compatible_heading_count': len(compatible_headings),
        'compatible_headings': [{
            'number': item['number'], 'expected': item['title'],
            'actual': item.get('matched_title'), 'mode': item.get('match_mode'),
        } for item in compatible_headings],
        'requirements_total': len(req_ids), 'requirements_uncovered': uncovered_req,
        'disqualification_total': len(disq_ids), 'disqualification_uncovered': uncovered_disq,
        'manifest_image_count': manifest_images, 'docx_embedded_image_count': embedded_images,
        'output_format': normalized_format, 'delivery_valid': package_valid,
        'delivery_filename': path.name,
        'delivery_size': path.stat().st_size if path.is_file() else 0,
        'markdown_file_matches': markdown_file_matches if normalized_format == 'md' else None,
        'docx_valid': package_valid if normalized_format == 'docx' else None,
        'docx_filename': path.name if normalized_format == 'docx' else '',
        'docx_size': path.stat().st_size if normalized_format == 'docx' and path.is_file() else 0,
        'failures': failures, 'warnings': warnings,
    }
    lines = ['# 技术方案校验报告', '', f'## 总体结论：{status}', '',
             '## 文档与字数', f'- 导出格式：{normalized_format.upper()}',
             f'- 交付文件：{path.name or "—"}',
             f'- 交付文件有效：{"是" if package_valid else "否"}',
             f'- 文件大小：{summary["delivery_size"]} bytes',
             (f'- 中文字符：{actual_words} / 最低目标 {target}（已达到，超出 {total_deviation}%）'
              if actual_words >= target else
              f'- 中文字符：{actual_words} / 最低目标 {target}（未达到，缺少 {target - actual_words}）'),
             (f'- 图片：清单 {manifest_images} 张，DOCX 嵌入 {embedded_images} 张'
              if normalized_format == 'docx' else
              f'- 图片：清单 {manifest_images} 张（Markdown 正文与图片附件分别交付）'), '',
             '## 覆盖结果', f'- 章节：{len(leaves) - len(missing_leaves)}/{len(leaves)}',
             f'- 技术要求：{len(req_ids) - len(uncovered_req)}/{len(req_ids)}',
             f'- 废标项：{len(disq_ids) - len(uncovered_disq)}/{len(disq_ids)}', '',
             '## 分章字数']
    lines.extend(
        f"- {item['number']} {item['title']}：{item['actual']}/最低目标 {item['target']}"
        + (f"，不足 {item.get('shortfall_pct', 0)}%" if item['found'] and item['actual'] < item['target']
           else f"，已达到（超出 {item.get('excess_pct', 0)}%）" if item['found']
           else '')
        + ('' if item['found'] else '（缺失）')
        + (f"（兼容识别：{item.get('matched_title')}）"
           if item['found'] and item.get('match_mode') != 'exact' else '')
        for item in leaf_results
    )
    if failures:
        lines.extend(['', '## 必须修复', *[f'- {item}' for item in failures]])
    if warnings:
        lines.extend(['', '## 建议调整', *[f'- {item}' for item in warnings]])
    if delivery_error:
        lines.extend(['', f'- 交付文件检查异常：{delivery_error}'])
    return {'report': '\n'.join(lines), 'summary': summary}


def _workflow_artifact_payload(path: str) -> Any:
    source = Path(str(path or '')).expanduser().resolve()
    if not source.is_file():
        raise FileNotFoundError(f'Workflow input does not exist: {path}')
    text = source.read_text(encoding='utf-8')
    try:
        value = json.loads(text)
    except json.JSONDecodeError:
        return text
    if isinstance(value, dict):
        if 'data' in value:
            return value['data']
        if 'text' in value:
            return value['text']
    return value


def _bid_outline_from_markdown(markdown: str, fallback_title: str) -> dict[str, Any]:
    """Project editable Markdown onto the bid outline schema deterministically."""
    headings = [
        (len(match.group(1)), match.group(2).strip())
        for line in str(markdown or '').splitlines()
        if (match := MARKDOWN_HEADING.match(line.strip()))
    ]
    if headings and headings[0][0] == 1:
        document_title = headings[0][1]
        body = headings[1:]
    else:
        document_title = str(fallback_title or '投标技术方案').strip()
        body = headings
    if not body:
        body = [(2, title) for title in (
            '项目理解', '需求分析', '总体架构', '功能设计',
            '安全设计', '实施交付', '运维服务', '验收方案',
        )]

    base = min(level for level, _ in body)
    roots: list[dict[str, Any]] = []
    stack: list[dict[str, Any]] = []
    previous_level = 1
    for raw_level, raw_title in body:
        level = min(4, max(1, raw_level - base + 1), previous_level + 1)
        previous_level = level
        title = re.sub(r'^\s*(?:第[^章]{1,8}章|\d+(?:\.\d+)*[、.．]?)\s*', '', raw_title)
        title = ILLEGAL_TITLE.sub(' ', title).strip() or f'章节{len(roots) + 1}'
        node: dict[str, Any] = {
            'title': title,
            'level': level,
            'number': '',
            'target_words': 1,
            'bid_requirements_refs': [],
            'disqualification_refs': [],
            'children': [],
        }
        while len(stack) >= level:
            stack.pop()
        if level == 1 or not stack:
            node['level'] = 1
            roots.append(node)
            stack = [node]
        else:
            stack[-1]['children'].append(node)
            stack.append(node)

    return {
        '$schema': 'bid-tech-proposal/outline.schema.json',
        'project_name': document_title,
        'project_full_name': document_title,
        'color_scheme': 'blue',
        'chapters': roots,
    }


def normalize_bid_outline_from_inputs(outline_document_path: str) -> dict[str, Any]:
    """Normalize the approved outline using bound input paths, not model-copied JSON.

    This wrapper keeps large requirement bodies and machine JSON outside the SubAgent
    prompt. Markdown headings are the sole structural source of truth; trace IDs and word
    allocations are deterministically rebuilt from the current Attempt inputs.
    """
    from lazymind.chat.engine.subagent.context import require_context

    inputs = require_context().params.get('remote_inputs') or {}
    required = ['technical_requirements', 'disqualification_items', 'generation_parameters']
    missing = [slot for slot in required if not str(inputs.get(slot) or '').strip()]
    if missing:
        raise ValueError('Required outline input paths are missing: ' + ', '.join(missing))
    parameters = _workflow_artifact_payload(inputs['generation_parameters'])
    if not isinstance(parameters, dict):
        parameters = _json_object(parameters, 'generation_parameters')
    markdown = str(_workflow_artifact_payload(outline_document_path) or '')
    candidate = _bid_outline_from_markdown(
        markdown,
        str(parameters.get('project_name') or parameters.get('project_full_name') or ''),
    )
    return validate_and_allocate_outline(
        outline_json=candidate,
        requirements_markdown=str(
            _workflow_artifact_payload(inputs['technical_requirements']) or ''
        ),
        disqualification_markdown=str(
            _workflow_artifact_payload(inputs['disqualification_items']) or ''
        ),
        word_target=str(parameters.get('word_target') or ''),
    )


def validate_proposal_from_inputs() -> dict[str, Any]:
    """Validate the final package directly from current Attempt input paths.

    Large Markdown, requirement, and outline bodies never cross the model/tool JSON
    boundary; only the compact validation result is returned to the SubAgent.
    """
    from lazymind.chat.engine.subagent.context import require_context
    inputs = require_context().params.get('remote_inputs') or {}
    required = [
        'final_proposal_markdown', 'final_proposal', 'effective_outline',
        'technical_requirements', 'disqualification_items',
        'generation_parameters', 'image_manifest',
    ]
    missing = [slot for slot in required if not isinstance(inputs.get(slot), str) or not inputs.get(slot)]
    if missing:
        raise ValueError('Required validation input paths are missing: ' + ', '.join(missing))
    parameters = _workflow_artifact_payload(inputs['generation_parameters'])
    if not isinstance(parameters, dict):
        raise ValueError('generation_parameters must contain a JSON object.')
    return validate_proposal_package(
        markdown_text=str(_workflow_artifact_payload(inputs['final_proposal_markdown']) or ''),
        docx_path=inputs['final_proposal'],
        outline_json=_workflow_artifact_payload(inputs['effective_outline']),
        requirements_markdown=str(_workflow_artifact_payload(inputs['technical_requirements']) or ''),
        disqualification_markdown=str(_workflow_artifact_payload(inputs['disqualification_items']) or ''),
        word_target=str(parameters.get('word_target') or ''),
        image_manifest_json=_workflow_artifact_payload(inputs['image_manifest']),
        output_format=str(parameters.get('output_format') or ''),
    )
