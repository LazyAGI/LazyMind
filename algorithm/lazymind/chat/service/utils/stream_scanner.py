# lazymind/utils/stream_scanner.py
from __future__ import annotations

import re
from abc import ABC, abstractmethod
from typing import Dict, List, Tuple
from rapidfuzz import fuzz
# Qwen-style think delimiters (lengths 7 and 8; must stay in sync with parsers elsewhere)
_THINK_OPEN = '<think>'
_THINK_CLOSE = '</think>'


# ============================================================
# BasePlugin
# ============================================================
class BasePlugin(ABC):
    prefix_set: set[str]

    @abstractmethod
    def match(self, src: str, pos: int) -> Tuple[int, str] | None:
        ...

    def last_incomplete_pos(self, buf: str) -> int | None:
        return None

    def collect(self) -> List[Dict[str, str]]:
        return []


# ============================================================
# ImagePlugin  ![alt](url)
# ============================================================
class ImagePlugin(BasePlugin):
    prefix_set = {'!'}
    # Use non-greedy matching for alt and url, allowing alt to contain parentheses etc.
    _pat = re.compile(r'!\[(.*?)\]\((.*?)\)')

    def __init__(self, url_map: Dict[str, str]):
        self.url_map = url_map

    def match(self, src: str, pos: int):
        m = self._pat.match(src, pos)
        if not m:
            return None
        alt, url = m.group(1), m.group(2)
        if url in self.url_map:
            return (m.end(), f'![{alt}]({self.url_map[url]})')
        # fuzzy match: find the most similar image with similarity > 80%
        best_key = None
        best_score = 0

        for k in self.url_map.keys():
            score = fuzz.ratio(url, k)  # 0 ~ 100
            if score >= 80 and score > best_score:
                best_score = score
                best_key = k

        if best_key:
            mapped = self.url_map[best_key]
            return (m.end(), f'![{alt}]({mapped})')

        return (m.end(), '')

    def last_incomplete_pos(self, buf: str) -> int | None:
        """
        More precise detection of whether an image token is unclosed:
        - Search for the last '![', then check in order for ']', '(', ')'.
        - Only consider the token complete when all these structures are present; otherwise return last_img to hold until next chunk.  # noqa: E501
        """
        last_img = buf.rfind('![')
        if last_img == -1:
            if buf.endswith('!'):
                return len(buf) - 1
            return None

        # Search for ']' (end of alt) starting from last_img + 2
        alt_end = buf.find(']', last_img + 2)
        if alt_end == -1:
            # alt not closed
            return last_img

        # Search for '(' to start url after alt_end
        paren_start = buf.find('(', alt_end + 1)
        if paren_start == -1:
            # '(' not found (url part not reached yet)
            return last_img

        # Search for ')' to end url after paren_start
        paren_end = buf.find(')', paren_start + 1)
        if paren_end == -1:
            # url not closed
            return last_img

        # If all found, a complete '![...](...)'  exists; return None (no unclosed)
        return None


def markdown_image_incomplete_pos(buf: str) -> int | None:
    return ImagePlugin({}).last_incomplete_pos(buf)


class MarkdownImageHoldPlugin(BasePlugin):
    """Keep unclosed ``![alt](url)`` tokens in the scanner buffer across chunks."""

    prefix_set = {'!'}

    def match(self, src: str, pos: int):
        return None

    def last_incomplete_pos(self, buf: str) -> int | None:
        return markdown_image_incomplete_pos(buf)


# ============================================================
# IncrementalScanner
# ============================================================
class IncrementalScanner:
    """BODY / THINK state streaming parser."""

    def __init__(self, plugins: List[BasePlugin], initial_state: str = 'BODY'):
        self.plugins = plugins
        self.state = initial_state
        self.buf = ''

    # ---------------- helpers ----------------
    @staticmethod
    def _partial_tag_start(buf: str, tag: str) -> int | None:
        """If the buffer ends with an incomplete prefix of `tag`, return the start index of that prefix in the buffer.
        E.g. buf="<thi" & tag="`think`" -> return len(buf)-4.
        Returns None for a complete match or no prefix.
        """
        n = len(tag)
        # Only consider strict "tail is a proper prefix of tag"; complete match does not count
        for k in range(n - 1, 0, -1):
            if buf.endswith(tag[:k]):
                return len(buf) - k
        return None

    # ---------------- public ----------------
    def feed(self, chunk: str) -> List[Tuple[str, str]]:
        self.buf += chunk
        out: List[Tuple[str, str]] = []
        i = seg_start = 0

        while i < len(self.buf):
            # ---- think toggle ----
            if self.state == 'BODY' and self.buf.startswith(_THINK_OPEN, i):
                if i > seg_start:
                    out.append(('text', self.buf[seg_start:i]))
                i += len(_THINK_OPEN)
                seg_start = i
                self.state = 'THINK'
                continue
            if self.state == 'THINK' and self.buf.startswith(_THINK_CLOSE, i):
                if i > seg_start:
                    out.append(('think', self.buf[seg_start:i]))
                i += len(_THINK_CLOSE)
                seg_start = i
                self.state = 'BODY'
                continue

            # ---- plugin match attempt ----
            handled = False
            for pl in self.plugins:
                if self.buf[i] not in pl.prefix_set:
                    continue
                res = pl.match(self.buf, i)
                if res:
                    end, replacement = res
                    if i > seg_start:
                        out.append((self._field(), self.buf[seg_start:i]))
                    out.append((self._field(), replacement))
                    i, seg_start, handled = end, end, True
                    break
            if not handled:
                i += 1

        # ---- safe zone cutoff ----
        cut = len(self.buf)
        # 1) unclosed token reported by plugin
        for pl in self.plugins:
            pos = pl.last_incomplete_pos(self.buf)
            if pos is not None and pos >= seg_start and pos < cut:
                cut = pos
        # 2) incomplete prefix of THINK tag (`think` / `/think`)
        for tag in (_THINK_OPEN, _THINK_CLOSE):
            pos = self._partial_tag_start(self.buf, tag)
            if pos is not None and pos >= seg_start and pos < cut:
                cut = pos

        if cut > seg_start:
            out.append((self._field(), self.buf[seg_start:cut]))
        self.buf = self.buf[cut:]
        return [p for p in out if p[1]]

    def flush(self) -> List[Tuple[str, str]]:
        tail = self.feed('')
        if self.buf:
            tail.append((self._field(), self.buf))
            self.buf = ''
        return tail

    # ---------------- helpers ----------------
    def _field(self) -> str:
        return 'think' if self.state == 'THINK' else 'text'
