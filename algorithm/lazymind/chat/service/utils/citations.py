from __future__ import annotations

import re
from collections import OrderedDict
from html import escape
from typing import Any, Optional

from .static_file_url import (
    basename_from_path,
    static_file_url_from_any,
)
from .stream_scanner import (
    BasePlugin,
    IncrementalScanner,
    MarkdownImageHoldPlugin,
)

CITATION_REFS_KEY = '_citation_sources'
CITATION_KEY_MAP_KEY = '_citation_key_map'
IMAGE_URL_REGISTRY_KEY = '_image_url_registry'
CITATION_DOC_KEY_MAP_KEY = '_citation_doc_key_map'
CITATION_NEXT_DOC_KEY = '_citation_next_doc_index'
CITATION_DOC_CHUNK_NEXT_KEY = '_citation_next_chunk_index_map'
CITATION_INDEX_PATTERN = r'\d+\.\d+'
CITATION_PATTERN = re.compile(r'\[\[(' + CITATION_INDEX_PATTERN + r')\]\]')
SOURCE_LINK_PATTERN = re.compile(r'\[(\d+)\]\(#source-(' + CITATION_INDEX_PATTERN + r')(?:\s+"[^"]*")?\)')
SOURCE_REF_PATTERN = re.compile(r'\[\[(' + CITATION_INDEX_PATTERN + r')\]\]')


def register_image_url(config: dict[str, Any], path_or_url: str) -> None:
    signed = static_file_url_from_any(path_or_url)
    if not signed:
        return
    registry = config[IMAGE_URL_REGISTRY_KEY]
    registry[signed] = signed
    base = basename_from_path(signed)
    if base:
        registry[base] = signed
    static_ref = _extract_static_files_ref(signed) or _extract_static_files_ref(path_or_url)
    if static_ref:
        registry[static_ref] = signed


def _extract_static_files_ref(url: str) -> str:
    marker = '/static-files/'
    trimmed = (url or '').strip()
    idx = trimmed.find(marker)
    if idx < 0:
        return ''
    ref = trimmed[idx:]
    return ref.split('?', 1)[0]


def build_citation_key(item: dict[str, Any]) -> Optional[str]:
    uid = item.get('uid') or item.get('segement_id')
    if uid:
        return f'uid:{uid}'
    docid = item.get('docid') or item.get('document_id')
    group = item.get('group') or item.get('group_name')
    number = item.get('number') or item.get('segment_number')
    if docid and group and number is not None:
        return f'node:{docid}:{group}:{number}'
    text = item.get('text') or item.get('content')
    if docid and text:
        return f'text:{docid}:{str(text)[:80]}'
    return None


def build_document_citation_key(item: dict[str, Any]) -> Optional[str]:
    metadata = item.get('metadata') if isinstance(item.get('metadata'), dict) else {}
    global_md = item.get('global_metadata') if isinstance(item.get('global_metadata'), dict) else {}
    docid = item.get('docid') or item.get('document_id') or global_md.get('docid')
    if not docid:
        return None
    dataset_id = item.get('kb_id') or item.get('dataset_id') or global_md.get('kb_id') or metadata.get('kb_id') or ''
    return f'doc:{dataset_id}:{docid}'


def split_citation_index(index: Any) -> tuple[int | None, int | None]:
    if isinstance(index, str) and '.' in index:
        document_index, chunk_index = index.split('.', 1)
        if document_index.isdigit() and chunk_index.isdigit():
            return int(document_index), int(chunk_index)
    if isinstance(index, int) and index > 0:
        return index, None
    if isinstance(index, str) and index.isdigit():
        return int(index), None
    return None, None


def file_name_from_item(item: dict[str, Any]) -> str:
    metadata = item.get('metadata') if isinstance(item.get('metadata'), dict) else {}
    global_md = item.get('global_metadata') if isinstance(item.get('global_metadata'), dict) else {}
    return (
        item.get('file_name')
        or global_md.get('file_name')
        or metadata.get('file_name')
        or metadata.get('source')
        or 'title_example'
    )


def build_source_node_from_item(index: Any, item: dict[str, Any]) -> dict[str, Any]:
    metadata = item.get('metadata') if isinstance(item.get('metadata'), dict) else {}
    global_md = item.get('global_metadata') if isinstance(item.get('global_metadata'), dict) else {}
    content = item.get('text') if item.get('text') is not None else item.get('content', '')
    document_index, chunk_index = split_citation_index(index)
    source = {
        'file_id': '',
        'file_name': file_name_from_item(item),
        'document_id': item.get('docid') or item.get('document_id') or global_md.get('docid', ''),
        'segement_id': item.get('uid') or item.get('segement_id') or '',
        'dataset_id': item.get('kb_id') or item.get('dataset_id') or global_md.get('kb_id', ''),
        'index': index,
        'display_index': item.get('display_index') or document_index or index,
        'document_index': item.get('document_index') or document_index or index,
        'chunk_index': item.get('chunk_index') if item.get('chunk_index') is not None else chunk_index,
        'content': content or '',
        'group_name': item.get('group') or item.get('group_name') or '',
        'segment_number': (
            metadata.get('store_num')
            or metadata.get('lazyllm_store_num')
            or item.get('number')
            or item.get('segment_number')
            or -1
        ),
        'page': metadata.get('page', -1),
        'bbox': metadata.get('bbox', []),
        'metadata': metadata,
    }
    image_url = metadata.get('image_url') or item.get('image_url')
    if isinstance(image_url, str) and image_url.strip():
        source['image_url'] = image_url.strip()
    image_markdown = item.get('image_markdown')
    if isinstance(image_markdown, str) and image_markdown.strip():
        source['image_markdown'] = image_markdown.strip()
    return source


def register_citation_item(item: dict[str, Any], config: dict[str, Any]) -> dict[str, Any]:
    text = item.get('text') if item.get('text') is not None else item.get('content')
    if not text:
        return item

    refs = config[CITATION_REFS_KEY]
    key_map = config[CITATION_KEY_MAP_KEY]
    doc_key_map = config[CITATION_DOC_KEY_MAP_KEY]
    doc_chunk_next_map = config[CITATION_DOC_CHUNK_NEXT_KEY]
    key = build_citation_key(item)
    if not key:
        return item

    index = key_map.get(key)
    if index is None:
        doc_key = build_document_citation_key(item)
        if not doc_key:
            return item
        document_index = doc_key_map.get(doc_key)
        if document_index is None:
            document_index = int(config.get(CITATION_NEXT_DOC_KEY) or 1)
            config[CITATION_NEXT_DOC_KEY] = document_index + 1
            doc_key_map[doc_key] = document_index
        chunk_index = int(doc_chunk_next_map.get(doc_key) or 1)
        doc_chunk_next_map[doc_key] = chunk_index + 1
        index = f'{document_index}.{chunk_index}'
        key_map[key] = index
        refs[index] = build_source_node_from_item(index, item)
        signed = static_file_url_from_any(str(text))
        if signed:
            register_image_url(config, signed)

    item['citation_index'] = index
    item['ref'] = f'[[{index}]]'
    return item


def annotate_citations(result: Any, config: dict[str, Any]) -> Any:
    if isinstance(result, dict):
        if any(k in result for k in ('text', 'content', 'uid', 'docid', 'document_id')):
            register_citation_item(result, config)
        if isinstance(result.get('items'), list):
            result['items'] = [
                annotate_citations(item, config) if isinstance(item, dict) else item
                for item in result['items']
            ]
        if isinstance(result.get('current_node'), dict):
            result['current_node'] = annotate_citations(result['current_node'], config)
        return result
    if isinstance(result, list):
        return [
            annotate_citations(item, config) if isinstance(item, dict) else item
            for item in result
        ]
    return result


def reset_citation_state(config: dict[str, Any]) -> None:
    config[CITATION_REFS_KEY] = {}
    config[CITATION_KEY_MAP_KEY] = {}
    config[CITATION_DOC_KEY_MAP_KEY] = {}
    config[CITATION_NEXT_DOC_KEY] = 1
    config[CITATION_DOC_CHUNK_NEXT_KEY] = {}
    config[IMAGE_URL_REGISTRY_KEY] = {}


def citation_source(config: dict[str, Any], index: str) -> Optional[dict[str, Any]]:
    refs = config.get(CITATION_REFS_KEY)
    if not isinstance(refs, dict):
        return None
    source = refs.get(index) or refs.get(str(index))
    return source if isinstance(source, dict) else None


class CitationDisplayMapper:
    def __init__(self) -> None:
        self._doc_display_map: dict[str, int] = {}
        self._next_display_index = 1

    def display_index_for(self, index: str) -> int:
        document_index, _ = split_citation_index(index)
        key = str(document_index or index)
        display_index = self._doc_display_map.get(key)
        if display_index is None:
            display_index = self._next_display_index
            self._next_display_index += 1
            self._doc_display_map[key] = display_index
        return display_index

    def source_with_display_index(self, index: str, source: dict[str, Any]) -> dict[str, Any]:
        mapped_source = dict(source)
        mapped_source['display_index'] = self.display_index_for(index)
        return mapped_source


def citation_link(index: str, source: dict[str, Any], display_index: Any = None) -> str:
    document_index, _ = split_citation_index(index)
    display_index = display_index or source.get('display_index') or source.get('document_index') or document_index
    title = escape(str(source.get('file_name') or 'title'), quote=True)
    return f'[{display_index}](#source-{index} "{title}")'


def rewrite_citations(text: str, config: dict[str, Any]) -> tuple[str, list[dict[str, Any]]]:
    collected: OrderedDict[str, dict[str, Any]] = OrderedDict()
    display_mapper = CitationDisplayMapper()

    def _collect(index: str, source: dict[str, Any]) -> dict[str, Any]:
        mapped_source = display_mapper.source_with_display_index(index, source)
        collected.setdefault(index, mapped_source)
        return mapped_source

    def _replace(match: re.Match) -> str:
        index = match.group(1)
        source = citation_source(config, index)
        if not source:
            return ''
        mapped_source = _collect(index, source)
        return citation_link(index, source, display_index=mapped_source['display_index'])

    rewritten = CITATION_PATTERN.sub(_replace, text)

    def _replace_link(match: re.Match) -> str:
        index = match.group(2)
        source = citation_source(config, index)
        if not source:
            return match.group(0)
        mapped_source = _collect(index, source)
        return citation_link(index, source, display_index=mapped_source['display_index'])

    rewritten = SOURCE_LINK_PATTERN.sub(_replace_link, rewritten)

    return rewritten, list(collected.values())


class ConfigCitationPlugin(BasePlugin):
    prefix_set = {'['}
    _pat = CITATION_PATTERN
    _link_pat = SOURCE_LINK_PATTERN

    def __init__(self, config: dict[str, Any]):
        self._config = config
        self._collected: OrderedDict[str, dict[str, Any]] = OrderedDict()
        self._display_mapper = CitationDisplayMapper()

    def _collect(self, index: str, source: dict[str, Any]) -> dict[str, Any]:
        mapped_source = self._display_mapper.source_with_display_index(index, source)
        self._collected.setdefault(index, mapped_source)
        return mapped_source

    def match(self, src: str, pos: int):
        link_match = self._link_pat.match(src, pos)
        if link_match:
            index = link_match.group(2)
            source = citation_source(self._config, index)
            if source:
                mapped_source = self._collect(index, source)
                return (
                    link_match.end(),
                    citation_link(index, source, display_index=mapped_source['display_index']),
                )
            return (link_match.end(), link_match.group(0))

        match = self._pat.match(src, pos)
        if not match:
            return None
        index = match.group(1)
        source = citation_source(self._config, index)
        if not source:
            return (match.end(), '')
        mapped_source = self._collect(index, source)
        return (match.end(), citation_link(index, source, display_index=mapped_source['display_index']))

    def collect(self) -> list[dict[str, Any]]:
        return list(self._collected.values())

    def last_incomplete_pos(self, buf: str) -> int | None:
        last_double = buf.rfind('[[')
        if last_double != -1 and ']]' not in buf[last_double + 2:]:
            return last_double
        source_link_start = buf.rfind('](#source')
        if source_link_start != -1 and ')' not in buf[source_link_start:]:
            open_bracket = buf.rfind('[', 0, source_link_start)
            if open_bracket != -1:
                return open_bracket
        if buf.endswith('['):
            return len(buf) - 1
        return None


def build_stream_citation_scanner(
    config: dict[str, Any],
) -> tuple[IncrementalScanner, ConfigCitationPlugin]:
    plugin = ConfigCitationPlugin(config)
    return IncrementalScanner(
        [plugin, MarkdownImageHoldPlugin()],
        initial_state='BODY',
    ), plugin
