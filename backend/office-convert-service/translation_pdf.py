"""Adaptive in-page PDF translation; never append overflow pages."""

import math
import unicodedata
import fitz
from pdf_image_preservation import redact_text_preserving_images
from translation_continuation import continue_in_place
from translation_layout import geometry, source_spans, fit_text, paint, visual_regions


def visible(text):
    return "".join(
        c
        for c in unicodedata.normalize("NFKC", text)
        if not c.isspace() and not unicodedata.category(c).startswith("C")
    )


def intersects(a, b):
    return (fitz.Rect(a) & fitz.Rect(b)).get_area() > 0.1


def graphics_signature(page):
    def stable(value):
        if isinstance(value, float):
            return round(value, 3)
        if isinstance(value, (tuple, list, fitz.Rect, fitz.Point, fitz.Quad)):
            return tuple(stable(v) for v in value)
        return value

    return [
        tuple(
            stable(d.get(k))
            for k in (
                "items",
                "fill",
                "color",
                "width",
                "fill_opacity",
                "stroke_opacity",
            )
        )
        for d in page.get_drawings()
    ]


def render_preserving_pages(document, blocks, translations, font_path):
    fitz.TOOLS.set_small_glyph_heights(True)
    source_pages = len(document)
    fonts = [
        fitz.Font(fontfile=str(font_path)),
        fitz.Font(fontfile="/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf"),
    ]
    original_graphics = [graphics_signature(p) for p in document]
    spans = [source_spans(p) for p in document]
    shapes = [geometry(p) for p in document]
    visuals = [visual_regions(p) for p in document]
    items = []
    known = set()
    for block in blocks:
        key = str(block.get("id", ""))
        if not key or key in known:
            raise ValueError("invalid or duplicate translation block id")
        known.add(key)
        text = str(translations.get(key, "")).strip()
        if not text:
            raise ValueError(f"missing translation for block {key}")
        pno = int(block.get("page", 1)) - 1
        bbox = block.get("bbox")
        if not 0 <= pno < source_pages or not isinstance(bbox, list) or len(bbox) != 4:
            raise ValueError(f"invalid location for block {key}")
        page = document[pno]
        sx = page.rect.width / float(block.get("pageWidth") or page.rect.width)
        sy = page.rect.height / float(block.get("pageHeight") or page.rect.height)
        rect = fitz.Rect(
            float(bbox[0]) * sx,
            float(bbox[1]) * sy,
            float(bbox[2]) * sx,
            float(bbox[3]) * sy,
        )
        if (
            not all(math.isfinite(v) for v in rect)
            or rect.is_empty
            or not page.rect.contains(rect)
        ):
            raise ValueError(f"out-of-page location for block {key}")
        items.append(
            {
                "id": key,
                "page": pno,
                "rect": rect,
                "text": text,
                "source": block,
                "size": float(block.get("fontSize") or 10),
            }
        )
    if not items or any(str(k) not in known for k in translations):
        raise ValueError("translation manifest does not match layout")

    prepared = []
    overflow = []
    unchanged = 0
    preserved = []
    for item in items:
        page = document[item["page"]]
        rect = item["rect"]
        page_spans = spans[item["page"]]
        own = [
            s
            for s in page_spans
            if (rect & fitz.Rect(s["bbox"])).get_area()
            / max(fitz.Rect(s["bbox"]).get_area(), 0.01)
            > 0.5
        ]
        if visible(item["text"]) == visible(str(item["source"].get("text", ""))):
            unchanged += 1
            continue
        # Rotated marginal metadata is page furniture, not horizontal prose.
        if own and all(s["protected"] for s in own):
            preserved.append(item["id"])
            unchanged += 1
            continue
        if any(
            (rect & region).get_area() / max(rect.get_area(), 0.01) > 0.6
            for region in visuals[item["page"]]
        ):
            preserved.append(item["id"])
            continue
        editable = [s for s in own if not s["protected"]]
        obstacles = shapes[item["page"]] + [
            fitz.Rect(s["bbox"]) for s in page_spans if s not in editable
        ]
        # Other paragraph boxes reserve column boundaries even if a short line
        # leaves white space in that paragraph. Prior plans reserve claimed space.
        neighbours = [
            other["rect"]
            for other in items
            if other is not item
            and other["page"] == item["page"]
            and not intersects(rect, other["rect"])
        ]
        prior = [x["fit"]["rect"] for x in prepared if x["page"] == item["page"]]
        obstacles += prior
        blockers = obstacles + neighbours
        fitting = fit_text(
            item["text"],
            rect,
            page.rect,
            blockers,
            obstacles,
            fonts,
            item["size"],
            "center" if item["source"].get("styleLevel") == "title" else "left",
        )
        if fitting is None:
            item["editable"] = editable
            overflow.append(item)
            continue
        item["fit"] = fitting
        item["editable"] = editable
        prepared.append(item)
    # Validate every block on an empty page before deleting any source text.
    for item in prepared:
        with fitz.open() as probe:
            p = probe.new_page(
                width=document[item["page"]].rect.width,
                height=document[item["page"]].rect.height,
            )
            paint(p, item["fit"]["runs"])
            if visible(item["text"]) != visible(p.get_text()):
                raise ValueError(f"translation extraction mismatch: {item['id']}")
    for pno in range(source_pages):
        page = document[pno]
        page_items = [i for i in prepared + overflow if i["page"] == pno]
        for item in page_items:
            for span in item["editable"]:
                # Shrink to the centre of the glyph row; adjacent math glyphs
                # and superscripts must not be touched by a broad block redaction.
                r = fitz.Rect(span["bbox"])
                r.y0 += r.height * 0.45
                r.y1 = r.y0 + 0.2
                page.add_redact_annot(r, fill=False, cross_out=False)
        if page_items:
            redact_text_preserving_images(page)
            for item in page_items:
                if "fit" in item:
                    paint(page, item["fit"]["runs"])
        if graphics_signature(page) != original_graphics[pno]:
            raise ValueError(f"original vector graphics changed on page {pno+1}")
        for item in page_items:
            if "fit" in item and visible(item["text"]) not in visible(page.get_text()):
                raise ValueError(f"final translated text lost: {item['id']}")
        removed = [s for i in page_items for s in i["editable"]]
        text = visible(page.get_text())
        for span in spans[pno]:
            if span not in removed and visible(span["text"]) not in text:
                raise ValueError(
                    f'protected source text lost on page {pno+1}: {span["text"]}'
                )
    if len(document) != source_pages:
        raise ValueError("source page count changed")
    page_map = continue_in_place(document, overflow, items, fonts) if overflow else []
    return {
        "warning_count": len(preserved),
        "rendered_block_count": len(items),
        "requested_block_count": len(items),
        "supplemental_block_count": 0,
        "source_page_count": source_pages,
        "supplements": page_map,
        "continuation_page_count": len(document) - source_pages,
        "layout_preserved": True,
        "adaptive_layout_version": 1,
        "preserved_visual_block_ids": preserved,
        "minimum_font_size": min(
            [i["fit"]["size"] for i in prepared]
            + [max(9, min(12, i["size"])) for i in overflow],
            default=0,
        ),
        "expanded_block_count": sum(
            not i["rect"].contains(i["fit"]["rect"]) for i in prepared
        ),
    }
