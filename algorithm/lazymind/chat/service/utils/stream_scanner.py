# lazymind/utils/stream_scanner.py
from __future__ import annotations

import re
from abc import ABC, abstractmethod
from dataclasses import dataclass
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

    def match_in_code(self, src: str, pos: int) -> Tuple[int, str] | None:
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


@dataclass
class MarkdownCodeScan:
    in_code: list[bool]
    states: list['MarkdownCodeState']
    hold_from: int | None = None


def fence_language(info: str) -> str:
    token = info.strip().split(None, 1)
    return token[0] if token else ''


@dataclass
class MarkdownCodeState:
    """Track Markdown fenced and inline code across incremental chunks."""

    inline_ticks: int = 0
    fence_char: str | None = None
    fence_len: int = 0
    fence_info: str = ''
    line_prefix_spaces: int | None = 0

    @property
    def active(self) -> bool:
        return bool(self.inline_ticks or self.fence_char)

    @property
    def is_editable_fence(self) -> bool:
        return bool(self.fence_char) and self.fence_info.casefold() == 'editable'

    def copy(self) -> 'MarkdownCodeState':
        return MarkdownCodeState(
            inline_ticks=self.inline_ticks,
            fence_char=self.fence_char,
            fence_len=self.fence_len,
            fence_info=self.fence_info,
            line_prefix_spaces=self.line_prefix_spaces,
        )

    def _clear_fence(self) -> None:
        self.fence_char = None
        self.fence_len = 0
        self.fence_info = ''

    def _consume_char(self, char: str) -> None:
        if char == '\n':
            self.line_prefix_spaces = 0
        elif self.line_prefix_spaces is not None:
            if char == ' ' and self.line_prefix_spaces < 3:
                self.line_prefix_spaces += 1
            else:
                self.line_prefix_spaces = None

    def _consume_run(self, char: str, length: int) -> None:
        for _ in range(length):
            self._consume_char(char)

    def scan(self, text: str, *, final: bool = False) -> MarkdownCodeScan:
        state = self.copy()
        in_code: list[bool] = []
        states = [state.copy()]
        i = 0

        while i < len(text):
            char = text[i]
            if char not in ('`', '~'):
                in_code.append(state.active)
                state._consume_char(char)
                states.append(state.copy())
                i += 1
                continue

            run_len = 1
            while i + run_len < len(text) and text[i + run_len] == char:
                run_len += 1
            run_end = i + run_len
            if run_end == len(text) and not final:
                return MarkdownCodeScan(in_code, states, hold_from=i)

            was_in_code = state.active
            if state.fence_char:
                if (
                    char == state.fence_char
                    and state.line_prefix_spaces is not None
                    and run_len >= state.fence_len
                ):
                    line_end = text.find('\n', run_end)
                    suffix_end = len(text) if line_end == -1 else line_end
                    suffix = text[run_end:suffix_end]
                    if not suffix.strip(' \t'):
                        if line_end == -1 and not final:
                            return MarkdownCodeScan(in_code, states, hold_from=i)
                        state._clear_fence()
            elif state.inline_ticks:
                if char == '`' and run_len == state.inline_ticks:
                    state.inline_ticks = 0
            elif (
                state.line_prefix_spaces is not None
                and run_len >= 3
                and char in ('`', '~')
            ):
                line_end = text.find('\n', run_end)
                if line_end == -1 and not final:
                    return MarkdownCodeScan(in_code, states, hold_from=i)
                state.fence_char = char
                state.fence_len = run_len
                state.fence_info = fence_language(
                    text[run_end:line_end if line_end != -1 else len(text)],
                )
            elif char == '`':
                state.inline_ticks = run_len

            state._consume_run(char, run_len)
            delimiter_is_code = was_in_code or state.active or char == '`'
            for _ in range(run_len):
                in_code.append(delimiter_is_code)
                states.append(state.copy())
            i = run_end

        return MarkdownCodeScan(in_code, states)


def transform_outside_markdown_code(content: str, transform) -> str:
    """Apply ``transform`` to prose spans while preserving Markdown code."""

    scan = MarkdownCodeState().scan(content, final=True)
    output: list[str] = []
    start = 0
    while start < len(content):
        is_code = scan.in_code[start]
        end = start + 1
        while end < len(content) and scan.in_code[end] == is_code:
            end += 1
        fragment = content[start:end]
        output.append(fragment if is_code else transform(fragment))
        start = end
    return ''.join(output)


def transform_editable_fence_spans(content: str, transform) -> str:
    """Apply ``transform`` only to `` ```editable `` / ``~~~editable`` spans."""

    scan = MarkdownCodeState().scan(content, final=True)
    output: list[str] = []
    start = 0
    while start < len(content):
        is_code = scan.in_code[start]
        end = start + 1
        while end < len(content) and scan.in_code[end] == is_code:
            end += 1
        fragment = content[start:end]
        editable = any(
            state.is_editable_fence for state in scan.states[start + 1:end + 1]
        )
        output.append(transform(fragment) if is_code and editable else fragment)
        start = end
    return ''.join(output)


# ============================================================
# IncrementalScanner
# ============================================================
class IncrementalScanner:
    """BODY / THINK state streaming parser."""

    def __init__(self, plugins: List[BasePlugin], initial_state: str = 'BODY'):
        self.plugins = plugins
        self.state = initial_state
        self.buf = ''
        self.code_state = MarkdownCodeState()

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
        return self._feed(chunk, final=False)

    def _feed(self, chunk: str, *, final: bool) -> List[Tuple[str, str]]:
        self.buf += chunk
        out: List[Tuple[str, str]] = []
        code_scan = self.code_state.scan(self.buf, final=final)
        cut = code_scan.hold_from if code_scan.hold_from is not None else len(self.buf)

        if not final:
            for pl in self.plugins:
                pos = pl.last_incomplete_pos(self.buf)
                if pos is None or pos >= cut:
                    continue
                in_code_at_pos = (
                    pos < len(code_scan.in_code) and code_scan.in_code[pos]
                )
                editable_at_pos = (
                    pos < len(code_scan.states)
                    and code_scan.states[pos].is_editable_fence
                )
                if not in_code_at_pos or editable_at_pos:
                    cut = pos
            for tag in (_THINK_OPEN, _THINK_CLOSE):
                pos = self._partial_tag_start(self.buf, tag)
                if (
                    pos is not None
                    and pos < cut
                    and (pos >= len(code_scan.in_code) or not code_scan.in_code[pos])
                ):
                    cut = pos

        i = seg_start = 0

        while i < cut:
            in_code = code_scan.in_code[i]
            # ---- think toggle ----
            if not in_code and self.state == 'BODY' and self.buf.startswith(_THINK_OPEN, i):
                if i > seg_start:
                    out.append(('text', self.buf[seg_start:i]))
                i += len(_THINK_OPEN)
                seg_start = i
                self.state = 'THINK'
                continue
            if not in_code and self.state == 'THINK' and self.buf.startswith(_THINK_CLOSE, i):
                if i > seg_start:
                    out.append(('think', self.buf[seg_start:i]))
                i += len(_THINK_CLOSE)
                seg_start = i
                self.state = 'BODY'
                continue

            # ---- markdown code: skip plugins, except dropping citations
            # inside editable writing fences ----
            if in_code:
                if code_scan.states[i].is_editable_fence:
                    handled = False
                    for pl in self.plugins:
                        if self.buf[i] not in pl.prefix_set:
                            continue
                        res = pl.match_in_code(self.buf, i)
                        if res:
                            end, replacement = res
                            if end > cut:
                                i = cut
                                break
                            if i > seg_start:
                                out.append((self._field(), self.buf[seg_start:i]))
                            if replacement:
                                out.append((self._field(), replacement))
                            i, seg_start, handled = end, end, True
                            break
                    if handled:
                        continue
                i += 1
                continue

            # ---- plugin match attempt ----
            handled = False
            for pl in self.plugins:
                if self.buf[i] not in pl.prefix_set:
                    continue
                res = pl.match(self.buf, i)
                if res:
                    end, replacement = res
                    if end > cut:
                        i = cut
                        break
                    if i > seg_start:
                        out.append((self._field(), self.buf[seg_start:i]))
                    out.append((self._field(), replacement))
                    i, seg_start, handled = end, end, True
                    break
            if not handled:
                i += 1

        if cut > seg_start:
            out.append((self._field(), self.buf[seg_start:cut]))
        self.code_state = code_scan.states[cut].copy()
        self.buf = self.buf[cut:]
        return [p for p in out if p[1]]

    def flush(self) -> List[Tuple[str, str]]:
        return self._feed('', final=True)

    # ---------------- helpers ----------------
    def _field(self) -> str:
        return 'think' if self.state == 'THINK' else 'text'
