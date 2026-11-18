"""Editable, deterministic Word layouts for structured resume content."""

from __future__ import annotations

import io
import re
from typing import Any

from docx import Document
from docx.enum.table import WD_CELL_VERTICAL_ALIGNMENT, WD_TABLE_ALIGNMENT
from docx.enum.text import WD_ALIGN_PARAGRAPH, WD_TAB_ALIGNMENT
from docx.oxml import OxmlElement
from docx.oxml.ns import qn
from docx.shared import Cm, Inches, Pt, RGBColor


_THEMES = {
    'business': ('254A71', 'EEF3F8', '商务蓝'),
    'modern': ('285B64', 'EAF4F4', '现代色带'),
    'sidebar': ('344F68', 'EAF0F5', '侧栏履历'),
    'editorial': ('984B38', 'F6F0EC', '创意编辑'),
    'campus': ('287764', 'EBF5EF', '校园清新'),
    'minimal': ('202020', 'F3F3F3', '黑白简洁'),
}
_XML_INVALID = re.compile('[\x00-\x08\x0b\x0c\x0e-\x1f]')


def _text(value: Any) -> str:
    return _XML_INVALID.sub('', str(value if value is not None else '')).strip()


def _font(run: Any, size: float = 10.5, color: str = '303841', bold: bool = False) -> None:
    run.font.name = 'Calibri'
    fonts = run._element.get_or_add_rPr().get_or_add_rFonts()
    fonts.set(qn('w:eastAsia'), 'Microsoft YaHei')
    run.font.size = Pt(size)
    run.font.color.rgb = RGBColor.from_string(color)
    run.font.bold = bold


def _shade(cell: Any, fill: str) -> None:
    shading = OxmlElement('w:shd')
    shading.set(qn('w:fill'), fill)
    cell._tc.get_or_add_tcPr().append(shading)


def _margins(cell: Any, left: int = 130, right: int = 130, top: int = 100, bottom: int = 100) -> None:
    margins = OxmlElement('w:tcMar')
    for side, value in [('left', left), ('right', right), ('top', top), ('bottom', bottom)]:
        item = OxmlElement(f'w:{side}')
        item.set(qn('w:w'), str(value))
        item.set(qn('w:type'), 'dxa')
        margins.append(item)
    cell._tc.get_or_add_tcPr().append(margins)


def _no_borders(table: Any) -> None:
    borders = OxmlElement('w:tblBorders')
    for name in ('top', 'left', 'bottom', 'right', 'insideH', 'insideV'):
        border = OxmlElement(f'w:{name}')
        border.set(qn('w:val'), 'nil')
        borders.append(border)
    table._tbl.tblPr.append(borders)
    table.autofit = False
    table.alignment = WD_TABLE_ALIGNMENT.CENTER


def _rule(paragraph: Any, color: str, thickness: str = '7', fill: str | None = None) -> None:
    properties = paragraph._p.get_or_add_pPr()
    borders = OxmlElement('w:pBdr')
    edge = OxmlElement('w:bottom')
    for name, value in [('val', 'single'), ('sz', thickness), ('space', '5'), ('color', color)]:
        edge.set(qn(f'w:{name}'), value)
    borders.append(edge)
    properties.append(borders)
    if fill:
        shading = OxmlElement('w:shd')
        shading.set(qn('w:fill'), fill)
        properties.append(shading)


def _paragraph(container: Any, text: str = '', *, size: float = 10.5, color: str = '303841',
               bold: bool = False, before: float = 0, after: float = 5,
               keep: bool = False) -> Any:
    paragraph = container.add_paragraph()
    paragraph.paragraph_format.space_before = Pt(before)
    paragraph.paragraph_format.space_after = Pt(after)
    paragraph.paragraph_format.line_spacing = 1.2
    paragraph.paragraph_format.keep_with_next = keep
    paragraph.paragraph_format.widow_control = True
    _font(paragraph.add_run(_text(text)), size=size, color=color, bold=bold)
    return paragraph


def _contacts(basics: dict[str, Any]) -> list[str]:
    result = [_text(basics.get(key)) for key in ('phone', 'email', 'location')]
    for link in basics.get('links', []):
        if isinstance(link, dict):
            label, url = _text(link.get('label')), _text(link.get('url'))
            result.append(f'{label}：{url}' if label and url else label or url)
        else:
            result.append(_text(link))
    return list(filter(None, result))


def _photo(container: Any, photo_bytes: bytes, width: float = 0.86) -> None:
    paragraph = container.add_paragraph()
    paragraph.alignment = WD_ALIGN_PARAGRAPH.CENTER
    paragraph.paragraph_format.space_after = Pt(6)
    paragraph.add_run().add_picture(io.BytesIO(photo_bytes), width=Inches(width))


def _header(container: Any, basics: dict[str, Any], template: str,
            photo_bytes: bytes | None, width: float) -> None:
    accent, light, _ = _THEMES[template]
    left = container
    if photo_bytes:
        table = container.add_table(rows=1, cols=2)
        _no_borders(table)
        table.columns[0].width = Cm(width - 2.9)
        table.columns[1].width = Cm(2.9)
        left, right = table.rows[0].cells
        left.width, right.width = Cm(width - 2.9), Cm(2.9)
        _margins(left, left=0, right=150)
        _margins(right, left=60, right=0)
        right.vertical_alignment = WD_CELL_VERTICAL_ALIGNMENT.CENTER
        _photo(right, photo_bytes)
    name_size = 32 if template == 'editorial' else 27 if template == 'modern' else 25
    name = _paragraph(left, basics.get('name', ''), size=name_size, bold=True,
                      color=accent, after=4, keep=True)
    if template == 'campus':
        name.alignment = WD_ALIGN_PARAGRAPH.CENTER
    if basics.get('title'):
        title = _paragraph(left, basics['title'], size=12, bold=True, color=accent, after=6, keep=True)
        if template == 'campus':
            title.alignment = WD_ALIGN_PARAGRAPH.CENTER
    contact_text = '  ·  '.join(_contacts(basics))
    if contact_text:
        contact = _paragraph(left, contact_text, size=9, color='59636E', after=9)
        if template == 'campus':
            contact.alignment = WD_ALIGN_PARAGRAPH.CENTER
    if template == 'modern':
        _rule(name, accent, '22', fill=light)
    elif template in {'business', 'minimal'}:
        _rule(name, accent, '8' if template == 'business' else '4')
    elif template == 'editorial':
        _rule(name, accent, '12')


def _section_heading(container: Any, title: str, template: str) -> None:
    accent, light, _ = _THEMES[template]
    heading = _paragraph(container, title, size=12 if template != 'editorial' else 14,
                         color=accent, bold=True, before=12, after=7, keep=True)
    if template in {'business', 'minimal'}:
        _rule(heading, accent, '5' if template == 'business' else '3')
    elif template in {'modern', 'campus', 'sidebar'}:
        _rule(heading, accent, '4', fill=light)
    elif template == 'editorial':
        heading.paragraph_format.space_before = Pt(17)
        _rule(heading, accent, '9')


def _item(container: Any, item: dict[str, Any], template: str, width: float) -> None:
    accent = _THEMES[template][0]
    title, date = _text(item.get('title')), _text(item.get('date'))
    if title or date:
        paragraph = _paragraph(container, title, bold=True, before=4, after=3, keep=True)
        paragraph.paragraph_format.tab_stops.add_tab_stop(Cm(width), WD_TAB_ALIGNMENT.RIGHT)
        if date:
            run = paragraph.add_run(('\t' if title else '') + date)
            _font(run, size=9, color='65707B')
    subtitle = _text(item.get('subtitle'))
    if subtitle:
        _paragraph(container, subtitle, size=9.5, color=accent, after=4,
                   keep=bool(item.get('bullets')))
    for bullet in item.get('bullets', []):
        text = _text(bullet)
        if not text:
            continue
        paragraph = _paragraph(container, '• ' + text, after=4)
        paragraph.paragraph_format.left_indent = Cm(0.28)
        paragraph.paragraph_format.first_line_indent = Cm(-0.28)
    # A role with no bullets should not force a blank trailing paragraph onto a new page.
    if (title or date) and not subtitle and not item.get('bullets'):
        paragraph.paragraph_format.keep_with_next = False


def _sections(container: Any, sections: list[dict[str, Any]], template: str, width: float) -> None:
    for section in sections:
        _section_heading(container, _text(section.get('title')), template)
        for item in section.get('items', []):
            _item(container, item, template, width)


def _summary(container: Any, summary: str, template: str) -> None:
    if summary:
        _section_heading(container, '个人简介', template)
        _paragraph(container, summary, after=6)


def _sidebar(document: Any, resume: dict[str, Any], photo_bytes: bytes | None, width: float) -> None:
    accent, light, _ = _THEMES['sidebar']
    table = document.add_table(rows=1, cols=2)
    _no_borders(table)
    table.columns[0].width, table.columns[1].width = Cm(4.7), Cm(width - 4.7)
    left, right = table.rows[0].cells
    left.width, right.width = Cm(4.7), Cm(width - 4.7)
    _shade(left, light)
    _margins(left, 170, 170, 180, 180)
    _margins(right, 260, 20, 100, 100)
    left.vertical_alignment = right.vertical_alignment = WD_CELL_VERTICAL_ALIGNMENT.TOP
    basics = resume.get('basics', {})
    if photo_bytes:
        _photo(left, photo_bytes, width=1.0)
    _paragraph(left, basics.get('name', ''), size=23, bold=True, color=accent, after=8)
    if basics.get('title'):
        _paragraph(left, basics['title'], size=11, bold=True, color=accent, after=11)
    contacts = _contacts(basics)
    if contacts:
        _section_heading(left, '联系方式', 'sidebar')
        for contact in contacts:
            _paragraph(left, contact, size=9, after=6)
    side_sections, main_sections = [], []
    for section in resume.get('sections', []):
        title = _text(section.get('title')).lower()
        target = side_sections if any(token in title for token in (
            '技能', '语言', '证书', 'skills', 'languages', 'certificat',
        )) else main_sections
        target.append(section)
    _sections(left, side_sections, 'sidebar', 3.9)
    _summary(right, resume.get('summary', ''), 'sidebar')
    _sections(right, main_sections, 'sidebar', width - 5.4)


def _configure(document: Any) -> float:
    section = document.sections[0]
    section.page_width, section.page_height = Cm(21), Cm(29.7)
    section.top_margin, section.bottom_margin = Cm(1.5), Cm(1.5)
    section.left_margin, section.right_margin = Cm(1.7), Cm(1.7)
    section.header_distance = section.footer_distance = Cm(0.65)
    normal = document.styles['Normal']
    normal.font.name, normal.font.size = 'Calibri', Pt(10.5)
    normal._element.get_or_add_rPr().get_or_add_rFonts().set(qn('w:eastAsia'), 'Microsoft YaHei')
    normal.paragraph_format.line_spacing = 1.2
    normal.paragraph_format.widow_control = True
    for name in ('Heading 1', 'Heading 2', 'Heading 3'):
        document.styles[name].paragraph_format.keep_with_next = True
    update_fields = OxmlElement('w:updateFields')
    update_fields.set(qn('w:val'), 'true')
    document.settings._element.append(update_fields)
    footer = section.footer.paragraphs[0]
    footer.alignment = WD_ALIGN_PARAGRAPH.RIGHT
    _font(footer.add_run('第 '), 8, '7D8790')
    for index, instruction in enumerate(('PAGE', 'NUMPAGES')):
        field = OxmlElement('w:fldSimple')
        field.set(qn('w:instr'), instruction)
        footer._p.append(field)
        _font(footer.add_run(' / ' if index == 0 else ' 页'), 8, '7D8790')
    return 17.6


def render_docx(resume: dict, output_path: str, template: str, photo_bytes: bytes | None = None) -> None:
    """Write one editable A4 resume, preserving all supplied sections and bullets.

    Photo bytes must already be validated, oriented and fitted to a 3:4 PNG by
    the caller. No paths, URLs, external fonts or resources are fetched here.
    """
    if template not in _THEMES:
        raise ValueError(f'Unknown resume template: {template}')
    document = Document()
    width = _configure(document)
    basics = resume.get('basics', {})
    document.core_properties.title = _text(basics.get('name')) + ' · 简历'
    document.core_properties.subject = _THEMES[template][2]
    document.core_properties.author = _text(basics.get('name'))
    if template == 'sidebar':
        _sidebar(document, resume, photo_bytes, width)
    else:
        _header(document, basics, template, photo_bytes, width)
        _summary(document, resume.get('summary', ''), template)
        _sections(document, resume.get('sections', []), template, width)
    document.save(output_path)
