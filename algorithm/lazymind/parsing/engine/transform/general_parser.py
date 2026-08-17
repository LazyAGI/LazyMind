import os
import re
import copy
from typing import List
from urllib.parse import urlparse

from lazyllm import pipeline, LOG, globals as lazyllm_globals
from lazyllm.tools.rag import NodeTransform
from lazyllm.tools.rag.doc_node import DocNode

from lazymind.config import config as _cfg
from lazymind.parsing.engine.utils import spawn_child_doc_node


IMAGE_PREFIX = _cfg['rag_image_path_prefix']
IMAGE_PATTERN = re.compile(r'!\[([^\]]*)\]\(([^)]+)\)')
_TOKEN_LIMIT_PATTERN = re.compile(r'^([1-9][0-9]*)([KM])?$')
_EMBED_CHUNK_LENGTH_BY_MAX_INPUT_TOKENS = {
    512: 384,
    1024: 896,
    2048: 1920,
}


def _parse_token_limit(value) -> int | None:
    if isinstance(value, int) and value > 0:
        return value
    if not isinstance(value, str):
        return None
    match = _TOKEN_LIMIT_PATTERN.fullmatch(value.strip().upper())
    if not match:
        return None
    amount = int(match.group(1))
    suffix = match.group(2)
    return amount * {'K': 1_000, 'M': 1_000_000}.get(suffix, 1)


def _runtime_embed_max_input_tokens() -> int | None:
    try:
        configs = lazyllm_globals['config'].get('dynamic_model_configs')
    except AssertionError:
        return None
    if not isinstance(configs, dict):
        return None
    embed_main = configs.get('embed_main')
    if not isinstance(embed_main, dict):
        return None
    embed_config = embed_main.get('embed')
    if not isinstance(embed_config, dict):
        return None
    return _parse_token_limit(embed_config.get('max_input_tokens'))


def _lines_for_chunk(lines, chunk: str) -> list | None:
    '''Redistribute parent layout lines onto a text chunk.

    Keep only lines whose content is contained in the chunk. Drop the key when
    nothing matches so LineSplitter does not treat a stale parent-wide line list
    as this chunk's layout lines.
    '''
    if not isinstance(lines, list) or not lines or not chunk:
        return None
    matched = []
    for line in lines:
        if not isinstance(line, dict):
            continue
        content = line.get('content') or ''
        if content and content in chunk:
            matched.append(copy.deepcopy(line))
    return matched or None


def is_url(s):
    try:
        res = urlparse(s)
        return bool(res.scheme and (res.netloc or res.scheme == 'file'))
    except Exception as e:
        LOG.error(f'is_url error: {e}')
        return False


class GeneralParser(NodeTransform):

    def __init__(self, max_length: int = 2048, split_by: str = '\n', **kwargs):
        super().__init__(**kwargs)
        assert max_length > 0, 'max_length must be greater than 0'
        assert isinstance(split_by, str) and len(split_by) > 0, 'split_by must be a non-empty string'
        self._max_length = max_length
        self._split_by = split_by
        self._len_split = len(split_by)

    def sig_fields(self) -> dict:
        return {'max_length': self._max_length, 'split_by': self._split_by}

    def _image_path_transform(self, text: str) -> str:
        def _replace(match: re.Match) -> str:
            alt_text, url = match.groups()
            if not is_url(url) and not url.startswith('lazyllm'):
                url = os.path.join(IMAGE_PREFIX, url)
            return f'![{alt_text}]({url})'
        return IMAGE_PATTERN.sub(_replace, text)

    def _rfind_sentence_end(self, window: str) -> int:
        '''Return index of the last sentence-ending . / 。, or -1.

        '.' only counts when it ends the window or is followed by whitespace,
        so decimals (2.5) and extensions (.png) are ignored.
        '''
        best = window.rfind('。')
        pos = -1
        start = 0
        while True:
            idx = window.find('.', start)
            if idx < 0:
                break
            nxt = window[idx + 1] if idx + 1 < len(window) else ''
            if nxt == '' or nxt.isspace():
                pos = idx
            start = idx + 1
        return max(best, pos)

    def _split_back_to_period(self, text: str, max_length: int) -> List[str]:
        '''Split overlong text, cutting back to the previous sentence end when possible.'''
        chunks: List[str] = []
        start = 0
        n = len(text)
        while start < n:
            end = min(start + max_length, n)
            if end >= n:
                chunks.append(text[start:])
                break
            window = text[start:end]
            sent = self._rfind_sentence_end(window)
            cut = sent + 1 if sent >= 0 else max_length
            chunks.append(text[start:start + cut])
            start += cut
        return chunks

    def _split(self, text: str, max_length: int | None = None) -> List[str]:
        if not text:
            return []
        max_length = max_length or self._max_length
        if len(text) <= max_length:
            return [text]

        result_chunks: List[str] = []
        buf = ''
        for part in text.split(self._split_by):
            if len(part) > max_length and not buf:
                result_chunks.extend(self._split_back_to_period(part, max_length))
                continue

            candidate = part if not buf else f'{buf}{self._split_by}{part}'
            if len(candidate) <= max_length:
                buf = candidate
                continue

            # Overflow across \\n: prefer sentence end inside the max_length window so we
            # do not close on a mid-sentence wrap like "shared\\nexperts."
            window = candidate[:max_length]
            sent = self._rfind_sentence_end(window)
            if sent >= 0:
                result_chunks.append(candidate[:sent + 1])
                remainder = candidate[sent + 1:]
                if remainder.startswith(self._split_by):
                    remainder = remainder[self._len_split:]
                if len(remainder) > max_length:
                    pieces = self._split_back_to_period(remainder, max_length)
                    result_chunks.extend(pieces[:-1])
                    buf = pieces[-1]
                else:
                    buf = remainder
            elif buf:
                result_chunks.append(buf)
                buf = part
            else:
                pieces = self._split_back_to_period(candidate, max_length)
                result_chunks.extend(pieces[:-1])
                buf = pieces[-1]

        if buf:
            if len(buf) > max_length:
                result_chunks.extend(self._split_back_to_period(buf, max_length))
            else:
                result_chunks.append(buf)
        return result_chunks

    def forward(self, document: DocNode, **kwargs) -> List[DocNode]:
        metadata = document.metadata
        global_metadata = document.global_metadata
        max_input_tokens = _runtime_embed_max_input_tokens()
        max_length = self._max_length
        if max_input_tokens is not None:
            max_length = min(
                max_length,
                _EMBED_CHUNK_LENGTH_BY_MAX_INPUT_TOKENS.get(max_input_tokens, max_length),
            )

        ppl = pipeline(self._image_path_transform, lambda text: self._split(text, max_length=max_length))
        content = ppl(document.text or '')

        children = []
        for chunk in content:
            child_metadata = copy.deepcopy(metadata)
            chunk_lines = _lines_for_chunk(child_metadata.get('lines'), chunk)
            if chunk_lines is not None:
                child_metadata['lines'] = chunk_lines
            else:
                child_metadata.pop('lines', None)
            children.append(spawn_child_doc_node(
                document,
                text=chunk,
                metadata=child_metadata,
                global_metadata=copy.deepcopy(global_metadata),
            ))
        return children
