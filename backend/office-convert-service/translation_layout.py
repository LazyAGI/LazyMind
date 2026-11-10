"""Page-local adaptive typography. No overflow pages or cross-page links.

Inspired by BabelDOC's iterative spacing / whitespace / scaling strategy.
Geometry is measured in PDF points. Planning never mutates the source page.
"""

import re
import unicodedata
import fitz


def geometry(page):
    """Use individual strokes, not a table/chart's enclosing bounding box."""
    obstacles = [fitz.Rect(x["bbox"]) for x in page.get_image_info()]
    for drawing in page.get_drawings():
        for part in drawing["items"]:
            if part[0] == "l":
                a, b = part[1:3]
                obstacles.append(
                    fitz.Rect(
                        min(a.x, b.x), min(a.y, b.y), max(a.x, b.x), max(a.y, b.y)
                    )
                    + (-0.4, -0.4, 0.4, 0.4)
                )
            elif part[0] == "re":
                r = fitz.Rect(part[1])
                if drawing.get("fill") is not None:
                    obstacles.append(r)
                else:
                    obstacles.extend(
                        [
                            fitz.Rect(r.x0, r.y0, r.x1, r.y0 + 0.5),
                            fitz.Rect(r.x0, r.y1 - 0.5, r.x1, r.y1),
                            fitz.Rect(r.x0, r.y0, r.x0 + 0.5, r.y1),
                            fitz.Rect(r.x1 - 0.5, r.y0, r.x1, r.y1),
                        ]
                    )
            else:
                obstacles.append(fitz.Rect(drawing["rect"]))
    return obstacles


def source_spans(page):
    spans = []
    for block in page.get_text("dict")["blocks"]:
        for line in block.get("lines", []):
            rotated = tuple(line.get("dir", (1, 0))) != (1, 0)
            for span in line["spans"]:
                span = dict(span)
                span["protected"] = rotated or bool(
                    re.search(
                        r"(?i)(cmmi|cmsy|cmex|symbol|math|msam|msbm)", span["font"]
                    )
                )
                spans.append(span)
    return spans


def expanded_boxes(rect, page_rect, blockers):
    """Find empty space anchored to the original paragraph, stopping at neighbours.

    Try down first (same column), right first (short lines), then both. Every
    candidate is collision checked, including text that is not in the manifest.
    """
    bounds = fitz.Rect(
        page_rect.x0 + 18, max(24, page_rect.y0), page_rect.x1 - 18, page_rect.y1 - 18
    )
    bounds |= rect  # preserve legitimate source margin text
    candidates = [fitz.Rect(rect)]
    for order in [("bottom", "right"), ("right", "bottom")]:
        r = fitz.Rect(rect)
        for side in order:
            if side == "right":
                limit = bounds.x1
                for b in blockers:
                    if b.y1 > r.y0 and b.y0 < r.y1 and b.x0 >= r.x1 - 0.1:
                        limit = min(limit, b.x0 - 2)
                r.x1 = max(r.x1, limit)
            else:
                limit = bounds.y1
                for b in blockers:
                    if b.x1 > r.x0 and b.x0 < r.x1 and b.y0 >= r.y1 - 0.1:
                        limit = min(limit, b.y0 - 2)
                r.y1 = max(r.y1, limit)
            if not any(
                (r & b).get_area() > 0.1 and (rect & b).get_area() <= 0.1
                for b in blockers
            ):
                if not any(tuple(r) == tuple(c) for c in candidates):
                    candidates.append(fitz.Rect(r))
    return candidates


def intervals(left, right, top, bottom, obstacles):
    result = [(left, right)]
    for b in obstacles:
        if b.y1 <= top or b.y0 >= bottom:
            continue
        updated = []
        for a, z in result:
            if b.x1 <= a or b.x0 >= z:
                updated.append((a, z))
                continue
            if b.x0 > a:
                updated.append((a, b.x0 - 1))
            if b.x1 < z:
                updated.append((b.x1 + 1, z))
        result = updated
    return [(a, b) for a, b in result if b - a > 1]


def layout_text(text, rect, size, spacing, fonts, obstacles, align="left"):
    # Keep Latin words together; allow a long URL/identifier to wrap as a last resort.
    tokens = re.findall(r"\n|[^\S\n]+|[A-Za-z0-9_]+(?:[\-\x27][A-Za-z0-9_]+)*|.", text)
    glyphs = []
    for token in tokens:
        chars = []
        for ch in token:
            font = next((f for f in fonts if f.has_glyph(ord(ch))), None)
            if ch == "\n":
                font = fonts[0]
            if font is None:
                font = fonts[0]  # Keep rendering; the caller records missing glyphs.
            chars.append(
                (ch, font, font.text_length(ch, fontsize=size) if ch != "\n" else 0)
            )
        glyphs.append(chars)
    # Font metrics include blank ascender/descender space; actual ink is inside.
    ascent = max(f.ascender for f in fonts) * size
    descent = max(-f.descender for f in fonts) * size
    baseline = rect.y0 + ascent
    ti = 0
    ci = 0
    runs = []
    while baseline + descent <= rect.y1 + 0.01:
        slots = intervals(
            rect.x0, rect.x1, baseline - ascent, baseline + descent, obstacles
        )
        before = len(runs)
        newline = False
        for left, right in slots:
            x = left
            line = []
            while ti < len(glyphs):
                chars = glyphs[ti]
                if chars[0][0] == "\n":
                    ti += 1
                    ci = 0
                    newline = True
                    break
                width = sum(c[2] for c in chars[ci:])
                if ci == 0 and width > right - x and width <= right - left and line:
                    break
                ch, font, advance = chars[ci]
                if x + advance > right + 0.001:
                    break
                line.append((x, baseline, ch, font, size))
                x += advance
                ci += 1
                if ci == len(chars):
                    ti += 1
                    ci = 0
            if align == "center" and len(slots) == 1:
                offset = (right - x) / 2
                line = [
                    (x + offset, y, ch, font, size) for x, y, ch, font, size in line
                ]
            runs.extend(line)
            if ti == len(glyphs):
                return runs
            if newline:
                break
        baseline += size * spacing if len(runs) > before or newline else 0.5
    return None


def fit_text(
    text, rect, page_rect, blockers, obstacles, fonts, source_size, align="left"
):
    text = "".join(
        c
        for c in unicodedata.normalize("NFKC", text.replace("\n", " "))
        if not unicodedata.category(c).startswith("C")
    )
    boxes = expanded_boxes(rect, page_rect, blockers)
    target = max(6, min(24, source_size))
    minimum = max(5.5, target * 0.55)
    # A necessary area bound rejects pathological expansion before expensive
    # font fitting. The factor allows for side bearings and inter-line overlap.
    widths = {
        c: next(
            (f.text_length(c, fontsize=minimum) for f in fonts if f.has_glyph(ord(c))),
            0,
        )
        for c in set(text)
    }
    if sum(widths[c] for c in text) * minimum * 0.5 > max(b.get_area() for b in boxes):
        return None
    scale = 1.0
    while target * scale >= minimum - 0.01:
        size = target * scale
        # Use whitespace before shrinking text, keep the original box if it fits.
        for box in boxes:
            for spacing in (1.5, 1.4, 1.3, 1.2, 1.1):
                runs = layout_text(text, box, size, spacing, fonts, obstacles, align)
                if runs is not None:
                    ink = fitz.Rect(box.x0, box.y0, box.x0, box.y0)
                    for x, y, ch, font, s in runs:
                        ink |= fitz.Rect(
                            x,
                            y - font.ascender * s,
                            x + font.text_length(ch, fontsize=s),
                            y - font.descender * s,
                        )
                    return {"runs": runs, "rect": ink, "size": size, "spacing": spacing}
        scale = round(scale - 0.05, 2)
    return None


def paint(page, runs):
    writer = fitz.TextWriter(page.rect)
    # Group adjacent characters to keep word selection and PDF extraction sane.
    group = ""
    last = None
    start = None
    for run in runs + [None]:
        if last is not None and (
            run is None
            or run[1] != last[1]
            or run[3] is not last[3]
            or abs(run[0] - last[0] - last[3].text_length(last[2], fontsize=last[4]))
            > 0.01
        ):
            writer.append(start, group, font=last[3], fontsize=last[4])
            group = ""
            start = None
        if run is not None:
            if start is None:
                start = (run[0], run[1])
            group += run[2]
        last = run
    writer.write_text(page, overlay=True)


def visual_regions(page):
    """Keep native chart labels and table cells attached to their geometry.

    They are not prose paragraphs. Translating a flattened multi-cell string
    would destroy cell alignment; preserve these regions until cell-level
    translation is available. Captions outside the region remain translatable.
    """
    regions = [fitz.Rect(i["bbox"]) for i in page.get_image_info()]
    regions += [fitz.Rect(t.bbox) for t in page.find_tables().tables]
    regions += [
        fitz.Rect(r) for r in page.cluster_drawings() if r.width > 40 and r.height > 30
    ]
    rules = []
    for d in page.get_drawings():
        for item in d["items"]:
            if item[0] == "l":
                a, b = item[1:3]
                if abs(a.y - b.y) < 1 and abs(a.x - b.x) > 100:
                    rules.append(fitz.Rect(min(a.x, b.x), a.y, max(a.x, b.x), a.y))
    # Borderless-column tables often have only horizontal ruling.
    groups = []
    for r in sorted(rules, key=lambda r: r.y0):
        group = next(
            (
                g
                for g in groups
                if r.y0 - g[-1].y0 < 85
                and min(r.x1, g[-1].x1) - max(r.x0, g[-1].x0)
                > min(r.width, g[-1].width) * 0.6
            ),
            None,
        )
        if group is None:
            groups.append([r])
        else:
            group.append(r)
    for group in groups:
        if len(group) >= 3:
            regions.append(
                fitz.Rect(
                    min(r.x0 for r in group),
                    group[0].y0,
                    max(r.x1 for r in group),
                    group[-1].y0,
                )
            )
    return regions
