"""Render the same verified resume content to styled HTML, PDF and editable Word.

The model supplies facts, never HTML/CSS. All styles and file resources are local.
PDF uses the PyMuPDF Story engine already shipped with the desktop runtime.
"""
from __future__ import annotations

import base64
import copy
import html
import io
import json
import re
from pathlib import Path
from typing import Any, Optional


TEMPLATES = {
    'business': {'name': '商务蓝', 'description': '清晰单栏 · 稳重专业', 'accent': '#224c70'},
    'modern': {'name': '现代色带', 'description': '顶部色带 · 鲜明层次', 'accent': '#263f56'},
    'sidebar': {'name': '双栏侧边', 'description': '侧栏技能 · 主栏经历', 'accent': '#245d66'},
    'editorial': {'name': '创意杂志', 'description': '大字标题 · 暖色细节', 'accent': '#935e46'},
    'campus': {'name': '校园清新', 'description': '浅色分区 · 教育优先', 'accent': '#387464'},
    'minimal': {'name': '黑白经典', 'description': '简洁单栏 · 文本清晰', 'accent': '#20252b'},
}
_MAX_INPUT_CHARACTERS = 200_000
_MAX_PHOTO_BYTES = 10 * 1024 * 1024
_MAX_PHOTO_PIXELS = 25_000_000


def _text(value: Any) -> str:
    if value is None:
        return ''
    if not isinstance(value, (str, int, float)) or isinstance(value, bool):
        raise ValueError('Resume text fields must contain strings or numbers.')
    return str(value).strip()


def _normalize_resume(resume: dict) -> dict:
    if not isinstance(resume, dict):
        raise ValueError('resume must be an object.')
    if len(json.dumps(resume, ensure_ascii=False)) > _MAX_INPUT_CHARACTERS:
        raise ValueError('Resume exceeds the 200,000 character limit.')
    basics = resume.get('basics') or {}
    if not isinstance(basics, dict):
        raise ValueError('basics must be an object.')
    output = {
        'basics': {
            key: _text(basics.get(key))
            for key in ('name', 'title', 'email', 'phone', 'location')
        },
        'summary': _text(resume.get('summary')),
        'sections': [],
    }
    links = basics.get('links') or []
    if not isinstance(links, list):
        raise ValueError('basics.links must be a list.')
    output['basics']['links'] = []
    for link in links:
        if isinstance(link, dict):
            label, url = _text(link.get('label')), _text(link.get('url'))
            output['basics']['links'].append(f'{label}: {url}' if label and url else label or url)
        else:
            output['basics']['links'].append(_text(link))
    sections = resume.get('sections') or []
    if not isinstance(sections, list):
        raise ValueError('sections must be a list.')
    for section in sections:
        if not isinstance(section, dict) or not isinstance(section.get('items', []), list):
            raise ValueError('Every section must contain an items list.')
        normalized = {'title': _text(section.get('title')), 'items': []}
        for item in section.get('items', []):
            if not isinstance(item, dict):
                raise ValueError('Each resume item must be an object.')
            bullets = item.get('bullets') or []
            if not isinstance(bullets, list):
                raise ValueError('item.bullets must be a list.')
            title, subtitle = _text(item.get('title')), _text(item.get('subtitle'))
            normalized['items'].append({
                'title': title, 'subtitle': '' if subtitle == title else subtitle,
                'date': _text(item.get('date')), 'bullets': [_text(bullet) for bullet in bullets],
            })
        output['sections'].append(normalized)
    if not output['basics']['name']:
        raise ValueError('basics.name is required.')
    return output


def _photo_bytes(photo_path: Optional[str], photo_fit: str) -> Optional[bytes]:
    if photo_fit not in {'contain', 'cover'}:
        raise ValueError('photo_fit must be contain or cover.')
    if not photo_path:
        return None
    from PIL import Image, ImageOps, UnidentifiedImageError

    path = Path(photo_path)
    if not path.is_file() or path.stat().st_size > _MAX_PHOTO_BYTES:
        raise ValueError('Photo must be an existing image no larger than 10 MiB.')
    try:
        with Image.open(path) as source:
            if source.format not in {'PNG', 'JPEG', 'WEBP'}:
                raise ValueError('Photo must be a PNG, JPEG or WebP image.')
            if source.width * source.height > _MAX_PHOTO_PIXELS:
                raise ValueError('Photo exceeds 25 megapixels.')
            image = ImageOps.exif_transpose(source).convert('RGBA')
            background = Image.new('RGBA', image.size, 'white')
            background.alpha_composite(image)
            image = background.convert('RGB')
            if photo_fit == 'cover':
                image = ImageOps.fit(image, (540, 720), method=Image.Resampling.LANCZOS)
            else:
                image.thumbnail((540, 720), Image.Resampling.LANCZOS)
                canvas = Image.new('RGB', (540, 720), 'white')
                canvas.paste(image, ((540 - image.width) // 2, (720 - image.height) // 2))
                image = canvas
            buffer = io.BytesIO()
            image.save(buffer, format='PNG', optimize=True)
            return buffer.getvalue()
    except (UnidentifiedImageError, OSError, Image.DecompressionBombError) as exc:
        raise ValueError('Photo is not a readable supported image.') from exc


def _escape(value: str) -> str:
    return html.escape(value, quote=True).replace('\n', '<br>')


def _contact_lines(basics: dict) -> list[str]:
    return [basics[key] for key in ('phone', 'email', 'location') if basics[key]] + basics['links']


def _header(resume: dict, photo_uri: Optional[str], template: str, *, compact: bool = False) -> str:
    basics = resume['basics']
    kicker = '<p class="kicker">个人简历 / RESUME</p>' if template == 'editorial' else ''
    contacts = '<br>'.join(_escape(value) for value in _contact_lines(basics))
    left = (f'{kicker}<h1>{_escape(basics["name"])}</h1>'
            f'<p class="role">{_escape(basics["title"])}</p>'
            f'<p class="contacts">{contacts}</p>')
    if photo_uri:
        photo = f'<img class="portrait" src="{photo_uri}" alt="用户上传的简历照片">'
        if compact:
            return f'<div class="header compact"><div class="portrait-holder">{photo}</div>{left}</div>'
        return ('<div class="header"><table class="header-table"><tr>'
                f'<td>{left}</td><td class="photo-cell">{photo}</td></tr></table></div>')
    return f'<div class="header">{left}</div>'


def _section(section: dict, index: Optional[int] = None, date_map: Optional[dict] = None,
             section_id: str = '', heading_groups: Optional[dict] = None) -> str:
    number = f'<span class="section-number">{index:02d}</span> ' if index is not None else ''
    body = [f'<div class="section"><h2 id="{section_id}">{number}{_escape(section["title"])}</h2>']
    if heading_groups is not None:
        heading_groups[section_id] = section_id
    for item_index, item in enumerate(section['items']):
        heading, date = _escape(item['title']), _escape(item['date'])
        if date_map is not None and date and len(item['date']) <= 32:
            identity = f'resume-date-{len(date_map)}'
            date_map[identity] = item['date']
            if heading_groups is not None and item_index == 0:
                heading_groups[identity] = section_id
            body.append(f'<h3 class="dated-heading" id="{identity}">{heading or "&#160;"}</h3>')
        elif date_map is not None and date:
            body.append(f'<h3>{heading}</h3><p class="subtitle">{date}</p>')
        elif date_map is not None and heading:
            identity = f'resume-date-{len(date_map)}'
            date_map[identity] = ''
            if heading_groups is not None and item_index == 0:
                heading_groups[identity] = section_id
            body.append(f'<h3 class="undated-heading" id="{identity}">{heading}</h3>')
        elif heading or date:
            body.append('<table class="entry-heading"><tr>'
                        f'<td><h3>{heading}</h3></td><td class="date">{date}</td></tr></table>')
        if item['subtitle']:
            body.append(f'<p class="subtitle">{_escape(item["subtitle"])}</p>')
        if item['bullets']:
            body.append('<ul>' + ''.join(f'<li>{_escape(bullet)}</li>' for bullet in item['bullets']) + '</ul>')
    body.append('</div>')
    return ''.join(body)


def _sections(resume: dict, template: str, date_map: Optional[dict] = None,
              heading_groups: Optional[dict] = None) -> str:
    sections = list(resume['sections'])
    if template == 'campus':
        sections.sort(key=lambda item: 0 if re.search(r'教育|学历|education', item['title'], re.I) else 1)
    return ''.join(_section(section, index + 1 if template == 'editorial' else None, date_map,
                            f'resume-section-{index}', heading_groups)
                   for index, section in enumerate(sections))


def _summary(resume: dict) -> str:
    return (f'<div class="summary"><h2>个人概述</h2><p>{_escape(resume["summary"])}</p></div>'
            if resume['summary'] else '')


def _split_sidebar(resume: dict) -> tuple[dict, dict]:
    side, main = copy.deepcopy(resume), copy.deepcopy(resume)
    side['summary'] = ''
    side['sections'] = [section for section in resume['sections']
                        if re.search(r'技能|语言|联系|skill|language|contact', section['title'], re.I)]
    main['sections'] = [section for section in resume['sections'] if section not in side['sections']]
    return side, main


def _css(template: str) -> str:
    accent = TEMPLATES[template]['accent']
    base = f'''
    * {{box-sizing:border-box}}
    body {{margin:0;font-family:Arial,"Noto Sans CJK SC","Microsoft YaHei",sans-serif;
      font-size:10.4pt;line-height:1.5;color:#29323c;background-color:white;}}
    h1,h2,h3,p,ul {{margin:0}}
    h1 {{font-size:27pt;line-height:1.2;color:{accent};margin-bottom:5pt;font-weight:bold;}}
    h2 {{font-size:12pt;line-height:1.35;color:{accent};font-weight:bold;
      padding-bottom:4pt;margin-top:15pt;margin-bottom:7pt;border-bottom:1px solid {accent};
      page-break-after:avoid;}}
    h3 {{font-size:10.8pt;line-height:1.4;font-weight:bold;}}
    p {{margin-bottom:5pt;}}
    ul {{padding-left:15pt;margin-top:4pt;margin-bottom:8pt;}}
    li {{padding-left:2pt;margin-bottom:4pt;}}
    .role {{font-size:13pt;font-weight:bold;color:{accent};margin-bottom:7pt;}}
    .contacts {{font-size:9pt;color:#586571;line-height:1.45;overflow-wrap:anywhere;}}
    .header {{padding-bottom:9pt;border-bottom:2px solid {accent};}}
    table {{width:100%;border-collapse:collapse;}}
    td {{vertical-align:top;padding:0;}}
    .header-table td {{vertical-align:middle;}}
    .entry-heading {{margin-top:9pt;page-break-after:avoid;}}
    .dated-heading {{margin-top:9pt;margin-right:140pt;min-height:22pt;page-break-after:avoid;}}
    .undated-heading {{margin-top:9pt;page-break-after:avoid;}}
    .date {{font-size:9pt;text-align:right;color:#65717c;width:138pt;padding-left:9pt;white-space:normal;}}
    .subtitle {{font-size:9.8pt;color:#5b6873;margin-top:2pt;page-break-after:avoid;}}
    .photo-cell {{width:64pt;text-align:right;}}
    .portrait {{width:54pt;height:72pt;object-fit:contain;}}
    .portrait-holder {{padding-bottom:12pt;}}
    .summary p {{color:#394955;}}
    .section-number {{font-size:9pt;color:#8a929a;}}
    .kicker {{font-size:8pt;letter-spacing:2px;color:#8d827a;margin-bottom:10pt;}}
    '''
    variants = {
        'business': '.header{padding-top:3pt} h2{letter-spacing:1px}',
        'modern': f'''.header{{background-color:{accent};padding:17pt 19pt;border-bottom:0;}}
          .header h1,.header .role{{color:white}} .header .contacts{{color:#e2ebf1}}
          h2{{border-bottom:0;border-left:4px solid {accent};padding-left:8pt;}}
          .section{{padding:0 3pt;}}''',
        'sidebar': f'''h1{{font-size:24pt}} h2{{font-size:11.5pt}}
          .sidebar{{background:#edf4f4;padding:20pt 14pt;width:30%;}}
          .sidebar .header{{border-bottom:0}} .sidebar .portrait{{margin-bottom:14pt;width:66pt;height:88pt}}
          .sidebar .contacts{{font-size:8.3pt;overflow-wrap:anywhere}}
          .sidebar h2{{color:{accent};border-bottom-color:#c1d5d6;}}
          .sidebar ul{{padding-left:11pt;}}
          .sidebar h3,.sidebar li{{font-size:9pt}} .main-column{{padding:16pt 0 0 20pt;}}
          .resume-columns{{display:flex;align-items:stretch;min-height:980px;}}
          .main-column{{width:70%;}}''',
        'editorial': f'''h1{{font-family:Georgia,serif;font-size:36pt;letter-spacing:2px;}}
          .header{{border-bottom:3px solid #242932;padding-bottom:17pt;}}
          .role{{font-size:11pt;color:#5b5048;font-weight:normal;}}
          h2{{border-bottom:1px solid #d7cbc3;color:{accent};padding-bottom:6pt;margin-top:18pt;}}
          .summary{{border-left:3px solid {accent};padding-left:12pt;margin-top:15pt;}}
          .summary h2{{margin-top:0;border:0;}}''',
        'campus': '''.header{border-bottom:0;padding:14pt;background-color:#edf5ef;}
          h1{font-size:25pt} h2{border-bottom:0;background-color:#edf5ef;padding:5pt 8pt;}
          .entry-heading{margin-top:10pt} .role{font-weight:normal}''',
        'minimal': '''h1{font-size:25pt;text-align:center;color:#20252b}
          .role,.contacts{text-align:center;color:#40454c}
          .header{border-bottom:1px solid #343b43;padding-bottom:12pt}
          h2{color:#20252b;font-size:11pt;border-bottom:1px solid #acb2b9;letter-spacing:1px}
          .subtitle,.date{color:#4d535a}''',
    }
    return base + variants[template]


def _render_html(resume: dict, template: str, photo_uri: Optional[str]) -> str:
    if template == 'sidebar':
        side, main = _split_sidebar(resume)
        content = ('<div class="resume-columns"><aside class="sidebar">'
                   + _header(side, photo_uri, template, compact=True) + _sections(side, template)
                   + '</aside><main class="main-column">' + _summary(main) + _sections(main, template)
                   + '</main></div>')
    else:
        content = _header(resume, photo_uri, template) + _summary(resume) + _sections(resume, template)
    screen = '''
      html{background:#e8edf2;padding:28px 12px}
      body{max-width:210mm;min-height:297mm;margin:auto;padding:17mm 18mm;
        box-shadow:0 8px 35px #23384b22;}
      @media print{html{background:white;padding:0}body{padding:0;box-shadow:none;max-width:none}
        @page{size:A4;margin:17mm 18mm}}
      @media(max-width:650px){body{padding:22px 18px}.date{width:100px;font-size:8pt}
        .resume-columns{display:block}.sidebar,.main-column{width:100%;padding:18px}}
    '''
    return ('<!doctype html><html lang="zh-CN"><head><meta charset="utf-8">'
            '<meta name="viewport" content="width=device-width,initial-scale=1">'
            '<meta http-equiv="Content-Security-Policy" content="default-src \'none\'; '
            'img-src data:; style-src \'unsafe-inline\'">'
            f'<title>{_escape(resume["basics"]["name"])} · 简历</title>'
            f'<style>{_css(template)}{screen}</style></head><body>{content}</body></html>')


def _story_document(body: str, css: str, rect: Any, content_rect: Any, archive: Any,
                    positions: Optional[list] = None, heading_groups: Optional[dict] = None):
    import fitz

    forced_breaks = set()
    heading_groups = heading_groups or {}
    max_attempts = 64
    for attempt in range(max_attempts):
        layout_positions = []
        breaks = ''.join(f'#{identity}{{page-break-before:always;}}' for identity in sorted(forced_breaks))
        story = fitz.Story(body, user_css=css + breaks, archive=archive)
        document = story.write_with_links(
            lambda page_number, filled: (rect, content_rect, None),
            positionfn=lambda position, current=layout_positions: current.append(vars(position).copy()),
        )
        # A section title and its first item share one break anchor. Breaking
        # both separately creates a page containing only the section title.
        # Repair the earliest orphan, then reflow before considering downstream
        # headings: moving an earlier block changes their available space.
        orphans = [position for position in layout_positions
                   if position.get('id') and position.get('heading') in {2, 3}
                   and position.get('open_close') == 1
                   and position['rect'][3] > content_rect.y1 - 54
                   and position['rect'][1] > content_rect.y0 + 54
                   and heading_groups.get(position['id'], position['id']) not in forced_breaks]
        if not orphans or attempt == max_attempts - 1:
            if positions is not None:
                positions.extend(layout_positions)
            return document
        earliest = min(orphans, key=lambda position: (position['page_num'], position['rect'][1]))
        forced_breaks.add(heading_groups.get(earliest['id'], earliest['id']))
        document.close()


def _draw_dates(document: Any, positions: list, date_map: dict, right: float):
    import fitz

    for position in positions:
        date = date_map.get(position.get('id'))
        if not date or position.get('open_close') != 1:
            continue
        top = position['rect'][1]
        rect = fitz.Rect(right - 130, top, right, top + 27)
        document[position['page_num'] - 1].insert_htmlbox(
            rect, f'<div style="font-family:sans-serif;font-size:9pt;color:#65717c;'
                  f'text-align:right;line-height:1.35">{_escape(date)}</div>',
        )


def _render_pdf(resume: dict, template: str, photo: Optional[bytes], path: str) -> int:
    import fitz

    rect = fitz.paper_rect('a4')
    margin = 42
    content_rect = rect + (margin, margin, -margin, -margin)
    archive = fitz.Archive()
    if photo:
        archive.add((photo, 'portrait.png'))
    photo_uri = 'portrait.png' if photo else None
    css = _css(template)
    date_map, positions, heading_groups = {}, [], {}
    if template == 'sidebar':
        side, main = _split_sidebar(resume)
        split_x = 190
        main_rect = fitz.Rect(split_x + 20, margin, rect.width - margin, rect.height - margin)
        side_rect = fitz.Rect(margin, margin, split_x - 12, rect.height - margin)
        main_body = _summary(main) + _sections(main, template, date_map, heading_groups)
        document = _story_document(main_body or '<p></p>', css, rect, main_rect, archive, positions, heading_groups)
        side_css = (css + '.contacts{font-size:8.3pt;}h1{font-size:24pt;}.portrait{width:60pt;height:80pt;}'
                    'h3,li{font-size:9pt;}')
        sidebar = _story_document(
            _header(side, photo_uri, template, compact=True) + _sections(side, template),
            side_css, rect, side_rect, archive,
        )
        while len(document) < len(sidebar):
            document.new_page(width=rect.width, height=rect.height)
        for number, page in enumerate(document):
            page.draw_rect(fitz.Rect(25, 24, split_x, rect.height - 26),
                           color=None, fill=(0.93, 0.96, 0.96), overlay=False)
            if number < len(sidebar):
                page.show_pdf_page(rect, sidebar, number)
        sidebar.close()
        _draw_dates(document, positions, date_map, main_rect.x1 - 12)
    else:
        # Story does not enforce HTML table widths. Reserve a right-hand photo
        # column explicitly rather than letting an oversized header table clip it.
        if photo:
            css += ('.header{padding-bottom:30pt;}.header h1,.header .role,.header .contacts'
                    '{margin-right:82pt;}')
        body = (_header(resume, None, template) + _summary(resume)
                + _sections(resume, template, date_map, heading_groups))
        document = _story_document(body, css, rect, content_rect, archive, positions, heading_groups)
        _draw_dates(document, positions, date_map, content_rect.x1 - 12)
        if photo:
            padding = {'modern': (19, 17), 'campus': (14, 14), 'business': (0, 3)}.get(template, (0, 0))
            right = content_rect.x1 - padding[0] - 8
            top = content_rect.y0 + padding[1] + 3
            document[0].insert_image(fitz.Rect(right - 54, top, right, top + 72), stream=photo)
    count = len(document)
    for number, page in enumerate(document):
        page.insert_text((rect.width - 80, rect.height - 22), f'{number + 1} / {count}',
                         fontsize=8, color=(0.48, 0.52, 0.57))
    document.set_metadata({'title': f'{resume["basics"]["name"]} - Resume',
                           'creator': 'LazyMind Resume Studio', 'subject': TEMPLATES[template]['name']})
    document.save(path, garbage=4, deflate=True)
    document.close()
    return count


def render_resume_files(
    resume: dict, output_dir: str, template: str = 'business', photo_path: Optional[str] = None,
    photo_fit: str = 'contain', filename: str = 'resume',
) -> dict:
    """Create real local files from one content object; never invent missing fields.

    The caller owns workspace authorization and publishing the returned files.
    Images are re-encoded, embedded and never fetched from external URLs.
    ``filename`` is a plain stem; all three formats share it and the template id.
    """
    if template not in TEMPLATES:
        raise ValueError(f'Unknown resume template: {template}. Choose from {", ".join(TEMPLATES)}.')
    filename = str(filename or 'resume').strip()
    if (not filename or filename in {'.', '..'} or len(filename) > 160
            or re.search(r'[/\\\x00-\x1f]', filename)):
        raise ValueError('filename must be a plain file name no longer than 160 characters.')
    normalized = _normalize_resume(resume)
    photo = _photo_bytes(photo_path, photo_fit)
    directory = Path(output_dir)
    directory.mkdir(parents=True, exist_ok=True)
    stem = f'{filename}-{template}'
    paths = {extension: directory / f'{stem}.{extension}' for extension in ('html', 'pdf', 'docx')}
    photo_uri = f'data:image/png;base64,{base64.b64encode(photo).decode()}' if photo else None
    paths['html'].write_text(_render_html(normalized, template, photo_uri), encoding='utf-8')
    page_count = _render_pdf(normalized, template, photo, str(paths['pdf']))
    from .docx_renderer import render_docx

    render_docx(normalized, str(paths['docx']), template, photo)
    mime = {'html': 'text/html', 'pdf': 'application/pdf',
            'docx': 'application/vnd.openxmlformats-officedocument.wordprocessingml.document'}
    files = [{'path': str(paths[extension].resolve()), 'filename': paths[extension].name,
              'mime': mime[extension], 'format': extension} for extension in ('pdf', 'docx', 'html')]
    return {'files': files, 'page_count': page_count, 'template': template,
            'template_name': TEMPLATES[template]['name'], 'has_photo': bool(photo), 'warnings': []}
