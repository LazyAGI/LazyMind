"""Project existing source metadata into bounded, document-specific display hints."""
from __future__ import annotations

import hashlib
import re
from collections import defaultdict
from typing import Any

from pydantic import BaseModel, ConfigDict, Field


class SourceRange(BaseModel):
    model_config = ConfigDict(extra='forbid')
    start: int = Field(ge=0)
    end: int = Field(gt=0)


class CodeFenceDisplay(SourceRange):
    language: str = Field(pattern=r'^[A-Za-z0-9_+.-]{1,40}$')


class ImageDisplay(SourceRange):
    width: int = Field(gt=0, le=10000)
    height: int | None = Field(default=None, gt=0, le=10000)


class WriterRenderContext(BaseModel):
    model_config = ConfigDict(extra='forbid')
    source_hash: str
    code_fences: list[CodeFenceDisplay] = Field(default_factory=list)
    images: list[ImageDisplay] = Field(default_factory=list)


_FENCE = re.compile(r'(?m)^ {0,3}(`{3,}|~{3,})([^\n]*)\n')
_IMAGE = re.compile(r'!\[(?:\\.|[^\]\\])*\]\(<?([^\s<>]+?)>?(?:\s+[\'"][^\n]*?[\'"])?\)')
_DIMENSIONS = re.compile(r'!\[\[[^\]\n]+\|([1-9]\d{0,4})(?:x([1-9]\d{0,4}))?\]\]')


def source_fences(source: str) -> list[tuple[int, int]]:
    """Return complete fence spans so examples cannot become image instances."""
    ranges, cursor = [], 0
    while opening := _FENCE.search(source, cursor):
        marker = opening[1]
        closing = re.compile(r'(?m)^ {0,3}' + re.escape(marker[0])
                             + '{' + str(len(marker)) + r',}[ \t]*(?:\r?\n|$)').search(source, opening.end())
        end = closing.end() if closing else len(source)
        ranges.append((opening.start(), end))
        if len(ranges) > 1000:
            break
        cursor = end
    return ranges


def project_source_display(source: str, metadata: Any) -> dict | None:
    # Display is optional; bound processing independently of the document limit.
    if not isinstance(metadata, dict) or len(source) > 2_000_000:
        return None
    fence_entries = metadata.get('code_fences', [])
    image_entries = metadata.get('images', {})
    if not isinstance(fence_entries, list) or not isinstance(image_entries, dict) \
            or len(fence_entries) + len(image_entries) > 1000:
        return None
    context = WriterRenderContext(source_hash=hashlib.sha256(source.encode()).hexdigest())
    fence_spans = source_fences(source)
    if len(fence_spans) > 1000:
        return None
    spans_by_text = defaultdict(list)
    for start, end in fence_spans:
        spans_by_text[source[start:end]].append((start, end))
    languages = defaultdict(list)
    for entry in fence_entries:
        if isinstance(entry, dict) and isinstance(entry.get('display'), str) \
                and isinstance(entry.get('language'), str) \
                and re.fullmatch(r'[A-Za-z0-9_+.-]{1,40}', entry['language']):
            languages[entry['display']].append(entry['language'])
    for text, values in languages.items():
        spans = spans_by_text.get(text, [])
        # After deletion/insertion a repeated source can be ambiguous. Never guess.
        if len(spans) == len(values):
            context.code_fences.extend(CodeFenceDisplay(start=start, end=end, language=language)
                                       for (start, end), language in zip(spans, values))

    excluded = list(fence_spans)
    for match in re.finditer(r'(`+)[\s\S]*?\1', source):
        excluded.append((match.start(), match.end()))
        if len(excluded) > 2000:
            return None
    occurrences = defaultdict(list)
    for index, match in enumerate(_IMAGE.finditer(source)):
        if index >= 1000:
            return None
        occurrences[match[1]].append(match)
    for reference, entry in image_entries.items():
        if not isinstance(entry, dict):
            continue
        variants = entry.get('raw_variants', [])
        matches = occurrences.get(reference, [])
        visible = [match for match in matches if not any(start <= match.start() < end for start, end in excluded)]
        # Current Providers also bridge examples; older metadata may contain
        # only displayed images. Align instances before filtering examples out.
        if isinstance(variants, list) and len(variants) != len(matches) and len(variants) == len(visible):
            matches = visible
        if not isinstance(variants, list) or len(variants) != len(matches):
            continue
        for raw, match in zip(variants, matches):
            if match not in visible:
                continue
            dimensions = _DIMENSIONS.fullmatch(raw) if isinstance(raw, str) else None
            if dimensions:
                width, height = int(dimensions[1]), int(dimensions[2]) if dimensions[2] else None
                if width <= 10000 and (height is None or height <= 10000):
                    context.images.append(ImageDisplay(start=match.start(), end=match.end(), width=width, height=height))
    if not context.images and not context.code_fences:
        return None
    context.code_fences.sort(key=lambda item: item.start)
    context.images.sort(key=lambda item: item.start)
    return context.model_dump(exclude_none=True)
