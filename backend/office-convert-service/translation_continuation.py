"""Continue oversized paragraphs at their reading position, never in an appendix."""

import re
import unicodedata
import fitz
from translation_layout import paint


def lines(text, width, size, fonts):
    text = unicodedata.normalize('NFKC', text.replace('\n', ' '))
    cache = {}
    for ch in set(text):
        if unicodedata.category(ch).startswith('C'):
            continue
        font = next((f for f in fonts if f.has_glyph(ord(ch))), None)
        if font is None:
            font = fonts[0]  # Missing glyphs remain visible as font .notdef; diagnosed by caller.
        cache[ch] = (font, font.text_length(ch, fontsize=size))
    row = []
    x = 0
    for token in re.findall('[A-Za-z0-9_]+|.', text):
        chars = [(ch, *cache[ch]) for ch in token if ch in cache]
        total = sum(c[2] for c in chars)
        if row and total <= width and x + total > width:
            yield row
            row, x = [], 0
        for ch, font, advance in chars:
            if row and x + advance > width:
                yield row
                row, x = [], 0
            row.append((x, 0, ch, font, size))
            x += advance
    if row:
        yield row


def continue_in_place(document, overflow, items, fonts, diagnostics=None):
    """Split source pages into intact horizontal bands around overflowing prose.

    Source bands retain vector graphics and images at their original scale.
    Later content follows the paragraph, even when it requires several pages.
    """
    diagnostics = diagnostics if diagnostics is not None else []
    source = fitz.open(stream=document.tobytes(), filetype='pdf')
    output = fitz.open()
    page_map = {item['id']: set() for item in items}
    source_regions = {pno: [] for pno in range(len(source))}
    outline = source.get_toc(simple=False)
    try:
        def recompose_page(pno, original):
            pending = [i for i in overflow if i['page'] == pno]
            source_items = [i for i in items if i['page'] == pno]
            if not pending:
                dest = len(output)
                output.insert_pdf(source, from_page=pno, to_page=pno, links=False)
                source_regions[pno].append((fitz.Rect(original.rect), dest, 0))
                for item in source_items:
                    page_map[item['id']].add(dest + 1)
                return
            width, height = original.rect.width, original.rect.height
            top, bottom = min(24, height * 0.1), height - min(24, height * 0.1)
            current = None
            cursor = 0
            current_runs = []

            def flush():
                nonlocal current_runs
                if current_runs:
                    paint(output[current], current_runs)
                    current_runs = []

            def new_page():
                nonlocal current, cursor
                flush()
                current = len(output)
                output.new_page(width=width, height=height)
                cursor = top

            # Move a cut upwards until it no longer bisects any source glyph,
            # image or graphic. This also handles another column at the same y.
            obstacles = [
                fitz.Rect(s['bbox'])
                for b in original.get_text('dict')['blocks']
                for line in b.get('lines', [])
                for s in line['spans']
            ]
            obstacles += [fitz.Rect(d['rect']) for d in original.get_drawings()]
            obstacles += [fitz.Rect(i['bbox']) for i in original.get_image_info()]
            cuts = []
            for item in pending:
                cut = max(0, item['rect'].y0)
                while True:
                    crossing = [
                        r.y0
                        for r in obstacles
                        if r.y0 < cut - 0.01 and r.y1 > cut + 0.01
                    ]
                    if not crossing:
                        break
                    cut = max(0, min(crossing))
                cuts.append((cut, item))
            cuts.sort(
                key=lambda pair: (pair[0], pair[1]['rect'].y0, pair[1]['rect'].x0)
            )

            def band(y0, y1):
                nonlocal cursor
                if y1 - y0 < 0.01:
                    return
                occupied = [r for r in obstacles if r.y1 >= y0 and r.y0 <= y1]
                if not occupied:
                    return
                # Reclaim blank space around the displaced source fragment.
                if y0 > 0:
                    y0 = max(y0, min(r.y0 for r in occupied) - 2)
                y1 = min(y1, max(r.y1 for r in occupied) + 2)
                if current is None or cursor + y1 - y0 > height + 0.01:
                    new_page()
                    # An original full-height band must retain its scale.
                    if y0 == 0 or y1 - y0 > bottom - top:
                        cursor = 0
                flush()
                clip = fitz.Rect(0, y0, width, y1)
                with fitz.open() as strip:
                    strip.insert_pdf(source, from_page=pno, to_page=pno)
                    page = strip[0]
                    # Physically remove out-of-band text: PDF clipping alone
                    # otherwise duplicates hidden text in copy/search results.
                    if y0 > 0:
                        page.add_redact_annot(fitz.Rect(0, 0, width, y0), fill=False)
                    if y1 < height:
                        page.add_redact_annot(
                            fitz.Rect(0, y1, width, height), fill=False
                        )
                    page.apply_redactions(
                        images=fitz.PDF_REDACT_IMAGE_NONE,
                        graphics=fitz.PDF_REDACT_LINE_ART_NONE,
                    )
                    output[current].show_pdf_page(
                        fitz.Rect(0, cursor, width, cursor + y1 - y0),
                        strip,
                        0,
                        clip=clip,
                    )
                for item in source_items:
                    if item not in pending and (item['rect'] & clip).get_area() > 0:
                        page_map[item['id']].add(current + 1)
                source_regions[pno].append((clip, current, cursor - y0))
                cursor += y1 - y0

            previous = 0
            for cut, item in cuts:
                band(previous, cut)
                previous = cut
                if current is None:
                    new_page()
                cursor += 4
                size = max(9, min(12, item['size']))
                left = max(18, min(item['rect'].x0, width - 40))
                # Continuations use the whole body width for single-column
                # papers, and both columns for genuinely two-column papers.
                left_blocks = [
                    i['rect']
                    for i in source_items
                    if i['rect'].width > width * 0.2 and i['rect'].x1 < width * 0.55
                ]
                right_blocks = [
                    i['rect']
                    for i in source_items
                    if i['rect'].width > width * 0.2 and i['rect'].x0 > width * 0.45
                ]
                two_columns = len(left_blocks) >= 2 and len(right_blocks) >= 2
                if two_columns:
                    left = min(r.x0 for r in left_blocks)
                    right = min(r.x0 for r in right_blocks)
                    available = min(right - left - 12, width - 18 - right)
                else:
                    right = left
                    available = width - 18 - left
                if available < size * 2:
                    left, right, available, two_columns = 18, 18, width - 36, False
                column = 0
                column_top = cursor
                used_bottom = cursor
                ascent = max(f.ascender for f in fonts) * size
                descent = max(-f.descender for f in fonts) * size
                for row in lines(item['text'], available, size, fonts):
                    if cursor + ascent + descent > bottom:
                        if (
                            two_columns
                            and column == 0
                            and column_top + ascent + descent <= bottom
                        ):
                            column = 1
                            cursor = column_top
                        else:
                            new_page()
                            column = 0
                            column_top = cursor
                            used_bottom = cursor
                    baseline = cursor + ascent
                    current_runs.extend(
                        (x + (right if column else left), baseline, ch, font, s)
                        for x, _, ch, font, s in row
                    )
                    page_map[item['id']].add(current + 1)
                    cursor += size * 1.5
                    used_bottom = max(used_bottom, cursor)
                cursor = used_bottom + 6
            band(previous, height)
            flush()

        for pno, original in enumerate(source):
            recompose_page(pno, original)

        def destination(pno, point):
            regions = source_regions.get(pno, [])
            if not regions:
                return None
            point = fitz.Point(point or (0, 0))
            region = next((r for r in regions if r[0].contains(point)), None)
            if region is None:
                region = min(regions, key=lambda r: abs(r[0].y0 - point.y))
                point.y = max(region[0].y0, min(region[0].y1, point.y))
            return region[1], fitz.Point(point.x, point.y + region[2])

        # Rebuild existing references after every destination has been mapped.
        # Page-by-page insert_pdf otherwise drops cross-page internal links.
        for pno, page in enumerate(source):
            for link in page.get_links():
                for clip, target_page, shift in source_regions[pno]:
                    rect = fitz.Rect(link['from']) & clip
                    if rect.is_empty:
                        continue
                    copied = {key: value for key, value in link.items() if key not in {'xref', 'id'}}
                    copied['from'] = rect + (0, shift, 0, shift)
                    if link['kind'] == fitz.LINK_GOTO:
                        target = destination(link.get('page', -1), link.get('to'))
                        if target is None:
                            continue
                        copied['page'], copied['to'] = target
                    if link['kind'] in {fitz.LINK_URI, fitz.LINK_GOTO, fitz.LINK_GOTOR, fitz.LINK_LAUNCH}:
                        output[target_page].insert_link(copied)

        for entry in outline:
            target = destination(entry[2] - 1, entry[3].get('to'))
            if entry[3].get('kind') == fitz.LINK_GOTO and target is not None:
                entry[2] = target[0] + 1
                entry[3]['page'], entry[3]['to'] = target

        # Verify the complete paragraph across its consecutive pages before
        # publishing the recomposed document. No truncation at a page boundary.
        def compact(value):
            return ''.join(
                c
                for c in unicodedata.normalize('NFKC', value)
                if not c.isspace() and not unicodedata.category(c).startswith('C')
            )

        for item in overflow:
            rendered = ''.join(
                output[p - 1].get_text() for p in sorted(page_map[item['id']])
            )
            if compact(item['text']) not in compact(rendered):
                diagnostics.append({'code': 'continuation_text_mismatch',
                                   'block_id': item['id'], 'pages': sorted(page_map[item['id']])})
        # Source pages are only replaced once every band and paragraph exists.
        old_count = len(document)
        document.insert_pdf(output)
        document.delete_pages(0, old_count - 1)
        if outline:
            document.set_toc(outline)
        return [
            {'block_id': key, 'pages': sorted(pages)} for key, pages in page_map.items()
        ]
    finally:
        output.close()
        source.close()
