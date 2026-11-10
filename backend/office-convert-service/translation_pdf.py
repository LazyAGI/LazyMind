"""Conservative PDF replacement: validate each block before touching its source.

Uncertain or overflowing blocks keep their original content and get linked
supplementary translations. A local failure must never rebuild the source pages.
"""
import math
import re
import unicodedata
import fitz


def visible(text):
    return ''.join(c for c in unicodedata.normalize('NFKC', text)
                   if not c.isspace() and not unicodedata.category(c).startswith('C'))


def intersects(a, b):
    # Zero-height table rules and equation fraction bars also need protection.
    b = fitz.Rect(b)
    b += (-0.5, -0.5, 0.5, 0.5)
    return (a & b).get_area() > 0


def protected_regions(page):
    regions = [fitz.Rect(item['bbox']) for item in page.get_image_info()]
    regions.extend(fitz.Rect(item['rect']) for item in page.get_drawings())
    for block in page.get_text('dict')['blocks']:
        for line in block.get('lines', []):
            direction = line.get('dir', (1, 0))
            if abs(direction[0] - 1) > 0.01 or abs(direction[1]) > 0.01:
                regions.append(fitz.Rect(line['bbox']))
            for span in line.get('spans', []):
                text = span.get('text', '')
                if (re.search(r'(?i)(cmmi|cmsy|cmex|symbol|math|msam|msbm)', span.get('font', ''))
                        or re.search(r'[=√∑∏∫∈≤≥±α-ωΑ-Ω]', text)):
                    regions.append(fitz.Rect(span['bbox']))
    return regions


def fit_block(page, rect, text, font_path, source_size):
    """Try bounded local typography on a disposable page, including extraction."""
    target = max(7.0, min(22.0, source_size))
    minimum = max(7.0, target * 0.75)
    with fitz.open() as probe:
        p = probe.new_page(width=page.rect.width, height=page.rect.height)
        p.insert_font(fontname='Translation', fontfile=str(font_path))
        size = target
        while size >= minimum - 0.01:
            for lineheight in (1.2, 1.1, 1.0):
                shape = p.new_shape()
                room = shape.insert_textbox(rect, text, fontname='Translation', fontsize=size,
                                            lineheight=lineheight, align=fitz.TEXT_ALIGN_LEFT)
                if room >= 0:
                    shape.commit()
                    if visible(text) == visible(p.get_text()):
                        return size, lineheight
                    return None
            size -= 0.5
    return None


def append_translation(document, item, number, font_path, font, fallback, state):
    """Append only this block, retaining every character and bidirectional links."""
    page = document[state['page']] if state.get('page') is not None else None
    cursor = state.get('cursor', 0)
    if page is not None and cursor > 720:
        page = None
    first_page = len(document) if page is None else page.number
    first_y = 28 if page is None else cursor
    positions = []
    needs_heading = True
    current = []
    lines = []
    width = 0.0
    for char in unicodedata.normalize('NFKC', item['text']):
        if char == '\n':
            lines.append(current); current = []; width = 0
            continue
        if char == '\t':
            char = ' '
        if unicodedata.category(char).startswith('C'):
            continue
        selected = font if font.has_glyph(ord(char)) else fallback
        if not selected.has_glyph(ord(char)):
            raise ValueError(f"unsupported translated character U+{ord(char):04X} in {item['id']}")
        advance = selected.text_length(char, fontsize=11)
        if current and width + advance > 499:
            lines.append(current); current = []; width = 0
        current.append((char, selected)); width += advance
    if current:
        lines.append(current)
    for line in lines:
        if page is None or cursor > 790:
            page = document.new_page(width=595, height=842)
            page.insert_font(fontname='Translation', fontfile=str(font_path))
            page.insert_font(fontname='Fallback', fontfile='/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf')
            cursor = 28
            needs_heading = True
        if needs_heading:
            heading = f"补充译文 {number} · 原文第 {item['page'] + 1} 页 · 点击返回原文"
            page.insert_text((48, cursor + 14), heading, fontname='Translation', fontsize=10, color=(0.1,0.3,0.65))
            page.insert_link({'kind':fitz.LINK_GOTO,'from':fitz.Rect(45,cursor,550,cursor+22),
                              'page':item['page'],'to':item['rect'].tl})
            cursor += 42
            needs_heading = False
        positions.append((page.number, cursor))
        x = 48.0
        run = ''
        run_font = font
        for char, selected in line + [('', None)]:
            if selected is not run_font and run:
                page.insert_text((x,cursor),run,fontname='Translation' if run_font is font else 'Fallback',fontsize=11)
                x += run_font.text_length(run,fontsize=11)
                run = ''
            run_font = selected
            run += char
        cursor += 17
    rendered = ''.join(document[i].get_text(clip=fitz.Rect(40,y-13,560,y+4)) for i,y in positions)
    if visible(item['text']) != visible(rendered):
        raise ValueError(f"supplement translation incomplete: {item['id']}")
    source_page = document[item['page']]
    source_page.insert_link({'kind':fitz.LINK_GOTO,'from':item['rect'],'page':first_page,'to':fitz.Point(48,first_y)})
    # An outline does not obscure the original text, formulas or artwork.
    source_page.draw_rect(item['rect'],color=(0.25,0.5,0.8),width=0.4,overlay=True)
    state.update(page=page.number, cursor=cursor+18)
    return first_page, sorted({i+1 for i,_ in positions})


def graphics_signature(page):
    def stable(value):
        if isinstance(value, float): return round(value, 3)
        if isinstance(value, (tuple, list, fitz.Rect, fitz.Point, fitz.Quad)):
            return tuple(stable(v) for v in value)
        return value
    return [tuple(stable(d.get(k)) for k in ('items','fill','color','width','fill_opacity','stroke_opacity'))
            for d in page.get_drawings()]


def render_preserving_pages(document, blocks, translations, font_path):
    source_pages = len(document)
    font = fitz.Font(fontfile=str(font_path))
    fallback = fitz.Font(fontfile='/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf')
    protection = [protected_regions(p) for p in document]
    original_graphics = [graphics_signature(p) for p in document]
    original_images = [[(x['digest'], tuple(x['bbox'])) for x in p.get_image_info(hashes=True)] for p in document]
    original_spans = [[span for block in p.get_text('dict')['blocks'] for line in block.get('lines', []) for span in line['spans']] for p in document]
    items = []
    known = set()
    for block in blocks:
        key = str(block.get('id', ''))
        if not key or key in known:
            raise ValueError('invalid or duplicate translation block id')
        known.add(key)
        text = str(translations.get(key, '')).strip()
        if not text:
            raise ValueError(f'missing translation for block {key}')
        pno = int(block.get('page', 1)) - 1
        bbox = block.get('bbox')
        if not 0 <= pno < source_pages or not isinstance(bbox,list) or len(bbox)!=4:
            raise ValueError(f'invalid location for block {key}')
        page = document[pno]
        sx = page.rect.width / float(block.get('pageWidth') or page.rect.width)
        sy = page.rect.height / float(block.get('pageHeight') or page.rect.height)
        rect = fitz.Rect(float(bbox[0])*sx,float(bbox[1])*sy,float(bbox[2])*sx,float(bbox[3])*sy)
        if not all(math.isfinite(v) for v in rect) or rect.is_empty or not page.rect.contains(rect):
            raise ValueError(f'out-of-page location for block {key}')
        items.append({'id':key,'page':pno,'rect':rect,'text':text,'source':block,
                      'size':float(block.get('fontSize') or 10)})
    if not items or any(str(k) not in known for k in translations):
        raise ValueError('translation manifest does not match layout')
    supplements = []
    prepared = []
    for item in items:
        page = document[item['page']]
        rect = item['rect']
        if visible(item['text']) == visible(str(item['source'].get('text', ''))) and visible(item['text']) in visible(page.get_text(clip=rect)):
            # Numbers, symbols, names and unchanged labels already exist in place.
            continue
        unsafe = any(intersects(rect,region) for region in protection[item['page']])
        unsafe |= any(other is not item and other['page']==item['page'] and
                      (rect & other['rect']).get_area()>1 for other in items)
        fitting = None if unsafe else fit_block(page,rect,item['text'],font_path,item['size'])
        if fitting is None:
            item['reason'] = 'protected_region' if unsafe else 'overflow'
            supplements.append(item)
        else:
            item['fit'] = fitting
            prepared.append(item)
    for pno in range(source_pages):
        page_items = [item for item in prepared if item['page']==pno]
        if not page_items:
            continue
        page = document[pno]
        # All insertions were validated before any source text is removed.
        for item in page_items:
            page.add_redact_annot(item['rect'],fill=False,cross_out=False)
        page.apply_redactions(images=fitz.PDF_REDACT_IMAGE_NONE,
                              graphics=fitz.PDF_REDACT_LINE_ART_NONE,text=fitz.PDF_REDACT_TEXT_REMOVE)
        page.insert_font(fontname='Translation',fontfile=str(font_path))
        for item in page_items:
            size,lineheight = item['fit']
            room = page.insert_textbox(item['rect'],item['text'],fontname='Translation',fontsize=size,
                                       lineheight=lineheight,align=fitz.TEXT_ALIGN_LEFT)
            if room < 0 or visible(item['text']) not in visible(page.get_text(clip=item['rect'])):
                raise ValueError(f"replacement verification failed: {item['id']}")
    for pno in range(source_pages):
        page = document[pno]
        if graphics_signature(page) != original_graphics[pno]:
            raise ValueError(f'original vector graphics changed on page {pno+1}')
        images = [(x['digest'], tuple(x['bbox'])) for x in page.get_image_info(hashes=True)]
        if images != original_images[pno]:
            raise ValueError(f'original images changed on page {pno+1}')
        replaced = [item['rect'] for item in prepared if item['page']==pno]
        text = visible(page.get_text())
        for span in original_spans[pno]:
            if not any(intersects(fitz.Rect(span['bbox']), rect) for rect in replaced):
                if visible(span['text']) not in text:
                    raise ValueError(f'untranslated source text lost on page {pno+1}')
    records = []
    supplement_state = {}
    for number,item in enumerate(supplements,1):
        first, pages = append_translation(document,item,number,font_path,font,fallback,supplement_state)
        records.append({'block_id':item['id'],'source_page':item['page']+1,
                        'supplement_page':first+1,'pages':pages,'reason':item['reason']})
    # Validate that original pages and graphical objects survived publication.
    if len(document)<source_pages:
        raise ValueError('source pages missing')
    return {'warning_count':len(supplements),'rendered_block_count':len(items),
            'requested_block_count':len(items),'supplemental_block_count':len(supplements),
            'source_page_count':source_pages,'supplements':records,'layout_preserved':True}
