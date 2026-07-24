from __future__ import annotations

import re
from dataclasses import dataclass
from typing import Literal
from urllib.parse import urlsplit


_MARKDOWN_IMAGE = re.compile(r'!\[([^\]]*)\]\(([^)\s]+)\)')


@dataclass(frozen=True, slots=True)
class OutboundPart:
    kind: Literal['text', 'image']
    text: str = ''
    source: str = ''


def split_outbound_parts(reply: str) -> list[OutboundPart]:
    """Split LazyMind's Markdown image contract into ordered channel parts."""
    parts: list[OutboundPart] = []
    cursor = 0
    for match in _MARKDOWN_IMAGE.finditer(reply):
        source = match.group(2).strip()
        if not _is_core_static_file(source):
            continue
        _append_text(parts, reply[cursor:match.start()])
        parts.append(
            OutboundPart(
                kind='image',
                text=match.group(1).strip(),
                source=source,
            )
        )
        cursor = match.end()
    _append_text(parts, reply[cursor:])
    return parts


def _append_text(parts: list[OutboundPart], value: str) -> None:
    text = value.strip()
    if text:
        parts.append(OutboundPart(kind='text', text=text))


def _is_core_static_file(source: str) -> bool:
    parsed = urlsplit(source)
    return parsed.path.startswith('/static-files/')
