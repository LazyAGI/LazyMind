"""Preserve visible images across MuPDF's text-redaction stream rewrite."""

import math
import fitz

_SCALE = fitz.Matrix(2, 2)


def image_signature(page):
    return [
        (i['digest'], tuple(i['bbox']), tuple(i['transform']))
        for i in page.get_image_info(hashes=True)
    ]


def _same_image(a, b):
    return a[0] == b[0] and all(
        math.isclose(x, y, rel_tol=0, abs_tol=0.0001)
        for xs, ys in zip(a[1:], b[1:])
        for x, y in zip(xs, ys)
    )


def assert_images_preserved(before, original, page):
    # Page.get_image_info caches results even across apply_redactions. Never
    # validate against that stale cache.
    current = page.parent[page.number]
    after = image_signature(current)
    if before == after:
        return
    missing = []
    cursor = 0
    for item in before:
        if cursor < len(after) and _same_image(item, after[cursor]):
            cursor += 1
        else:
            missing.append(item)
    error = f'original images changed on page {page.number + 1}'
    if cursor != len(after):
        raise ValueError(error)
    if not missing:
        return
    rendered = current.get_pixmap(matrix=_SCALE, alpha=False, annots=False)
    if original is None or (original.irect, original.n) != (rendered.irect, rendered.n):
        raise ValueError(error)
    # MuPDF can drop fully clipped image draw calls, even with images=NONE.
    # Compare their footprints in FULL page renders: rendering tiny clips
    # separately changes image resampling and can create false differences.
    src, dst = original.samples_mv, rendered.samples_mv
    for _, bbox, _ in missing:
        rect = (fitz.Rect(bbox) * _SCALE).irect & original.irect
        for y in range(rect.y0, rect.y1):
            start = (y - original.y) * original.stride + (rect.x0 - original.x) * original.n
            end = start + rect.width * original.n
            if src[start:end] != dst[start:end]:
                raise ValueError(error)


def redact_text_preserving_images(page):
    before = image_signature(page)
    original = page.get_pixmap(matrix=_SCALE, alpha=False, annots=False) if before else None
    page.apply_redactions(
        images=fitz.PDF_REDACT_IMAGE_NONE,
        graphics=fitz.PDF_REDACT_LINE_ART_NONE,
        text=fitz.PDF_REDACT_TEXT_REMOVE,
    )
    assert_images_preserved(before, original, page)
