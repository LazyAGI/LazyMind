from __future__ import annotations

import re
from collections import OrderedDict
from html import escape
from typing import Any, Optional
from urllib.parse import parse_qsl, urlencode, urlsplit, urlunsplit

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
EXTERNAL_SOURCE_KEY_MAP_KEY = '_external_source_key_map'
SEARCHED_SOURCE_INDICES_KEY = '_searched_source_indices'
CITED_SOURCE_INDICES_KEY = '_cited_source_indices'
CITATION_INDEX_PATTERN = r'\d+\.\d+'
CITATION_PATTERN = re.compile(r'\[\[(' + CITATION_INDEX_PATTERN + r')\]\]')
SOURCE_LINK_PATTERN = re.compile(r'\[(\d+)\]\(#source-(' + CITATION_INDEX_PATTERN + r')(?:\s+"[^"]*")?\)')
SOURCE_REF_PATTERN = re.compile(r'\[\[(' + CITATION_INDEX_PATTERN + r')\]\]')
_TRACKING_QUERY_KEYS = {
    'dclid', 'fbclid', 'gclid', 'igshid', 'mc_cid', 'mc_eid', 'msclkid', 'mkt_tok',
}
_SOURCE_ROLE_KEYS = {
    'cited': CITED_SOURCE_INDICES_KEY,
    'searched': SEARCHED_SOURCE_INDICES_KEY,
}
_SOURCE_ROLE_ORDER = ('cited', 'searched')


def register_image_url(config: dict[str, Any], path_or_url: str) -> None:
    signed = static_file_url_from_any(path_or_url)
    if not signed:
        return
    registry = config.get(IMAGE_URL_REGISTRY_KEY)
    if not isinstance(registry, dict):
        registry = {}
        config[IMAGE_URL_REGISTRY_KEY] = registry
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


def normalize_external_url(value: Any) -> str:
    url = str(value or '').strip()
    if not url:
        return ''
    try:
        parsed = urlsplit(url)
        port = parsed.port
    except ValueError:
        return ''
    scheme = parsed.scheme.lower()
    hostname = (parsed.hostname or '').rstrip('.').lower()
    if scheme not in {'http', 'https'} or not hostname or parsed.username or parsed.password:
        return ''

    host = f'[{hostname}]' if ':' in hostname else hostname
    if port is not None and not ((scheme == 'http' and port == 80) or (scheme == 'https' and port == 443)):
        host = f'{host}:{port}'
    path = parsed.path
    if path == '/':
        path = ''
    elif path.endswith('/'):
        path = path.rstrip('/')
    query = urlencode([
        (key, value)
        for key, value in parse_qsl(parsed.query, keep_blank_values=True)
        if not key.lower().startswith('utm_') and key.lower() not in _TRACKING_QUERY_KEYS
    ])
    return urlunsplit((scheme, host, path, query, ''))


def _external_url_key(value: Any) -> str:
    normalized = normalize_external_url(value)
    return f'url:{normalized}' if normalized else ''


def _external_doi(item: dict[str, Any]) -> str:
    extra = item.get('extra') if isinstance(item.get('extra'), dict) else {}
    doi = str(item.get('doi') or extra.get('doi') or '').strip().lower()
    return re.sub(r'^(?:doi:|https?://(?:dx\.)?doi\.org/)', '', doi)


def _external_source_keys(item: dict[str, Any]) -> list[str]:
    extra = item.get('extra') if isinstance(item.get('extra'), dict) else {}
    source = str(item.get('source') or item.get('provider') or '').strip().lower()
    values = [item.get('url')]
    keys = [_external_url_key(value) for value in values]

    doi = _external_doi(item)
    if doi:
        keys.append(f'doi:{doi}')

    arxiv_id = str(item.get('arxiv_id') or extra.get('arxiv_id') or '').strip().lower()
    if not arxiv_id:
        match = re.search(r'arxiv\.org/(?:abs|pdf)/([^/?#]+)', str(item.get('url') or ''), re.IGNORECASE)
        arxiv_id = match.group(1).removesuffix('.pdf').lower() if match else ''
    if arxiv_id:
        keys.append(f'arxiv:{arxiv_id}')

    document_id = item.get('doc_id') or item.get('document_id') or extra.get('doc_id')
    if source and document_id not in (None, ''):
        keys.append(f'provider_doc:{source}:{document_id}')
    pageid = item.get('pageid') or extra.get('pageid')
    if pageid not in (None, ''):
        keys.append(f'wikipedia:{pageid}')
    return list(dict.fromkeys(key for key in keys if key))


def _external_page_urls(page: dict[str, Any]) -> list[str]:
    values = [page.get('final_url'), page.get('url')]
    urls: list[str] = []
    seen: set[str] = set()
    for value in values:
        url = str(value or '').strip()
        key = _external_url_key(url)
        if key and key not in seen:
            seen.add(key)
            urls.append(url)
    return urls


def _next_external_citation_index(config: dict[str, Any]) -> str:
    refs = config.setdefault(CITATION_REFS_KEY, {})
    document_index = int(config.get(CITATION_NEXT_DOC_KEY) or 1)
    index = f'{document_index}.1'
    while index in refs:
        document_index += 1
        index = f'{document_index}.1'
    config[CITATION_NEXT_DOC_KEY] = document_index + 1
    return index


def _attach_external_ref(item: dict[str, Any], index: str) -> dict[str, Any]:
    item['citation_index'] = index
    item['ref'] = f'[[{index}]]'
    return item


def mark_source_roles(
    config: dict[str, Any],
    index: Any,
    roles: Any,
) -> None:
    normalized_index = str(index or '').strip()
    if not normalized_index:
        return
    role_values = (roles,) if isinstance(roles, str) else tuple(roles or ())
    unknown = set(role_values).difference(_SOURCE_ROLE_KEYS)
    if unknown:
        raise ValueError(f'unsupported source roles: {sorted(unknown)}')
    for role in _SOURCE_ROLE_ORDER:
        if role not in role_values:
            continue
        indices = config.setdefault(_SOURCE_ROLE_KEYS[role], [])
        if normalized_index not in indices:
            indices.append(normalized_index)


def source_roles(config: dict[str, Any], index: Any) -> list[str]:
    normalized_index = str(index or '').strip()
    return [
        role
        for role in _SOURCE_ROLE_ORDER
        if normalized_index in (config.get(_SOURCE_ROLE_KEYS[role]) or [])
    ]


def _external_image_urls(item: dict[str, Any]) -> list[str]:
    extra = item.get('extra') if isinstance(item.get('extra'), dict) else {}
    candidates = [
        *(item.get('image_urls') or []),
        *(item.get('images') or []),
        *(extra.get('images') or []),
    ]
    urls: list[str] = []
    for image in candidates:
        image_url = str(image.get('url') if isinstance(image, dict) else image).strip()
        if image_url and image_url not in urls:
            urls.append(image_url)
    return urls


def register_external_search_result(
    item: dict[str, Any],
    config: dict[str, Any],
    roles: Any = ('searched',),
) -> dict[str, Any]:
    url = str(item.get('url') or '').strip()
    if not normalize_external_url(url):
        doi = _external_doi(item)
        url = f'https://doi.org/{doi}' if doi else ''
    keys = _external_source_keys(item)
    if not keys:
        return item

    refs = config.setdefault(CITATION_REFS_KEY, {})
    key_map = config.setdefault(EXTERNAL_SOURCE_KEY_MAP_KEY, {})
    image_urls = _external_image_urls(item)
    fetched_content = str(item.get('content') or '').strip()
    snippet = str(item.get('snippet') or '').strip()
    index = next((key_map[key] for key in keys if key_map.get(key) in refs), None)
    source = refs.get(index) if index is not None else None
    if not isinstance(source, dict):
        index = _next_external_citation_index(config)
        source = {
            'source_type': 'external',
            'title': str(item.get('title') or '').strip(),
            'url': url,
            'content': fetched_content or snippet,
        }
        refs[index] = source
    else:
        if not source.get('title') and item.get('title'):
            source['title'] = str(item['title']).strip()
        if not source.get('url'):
            source['url'] = url
        if fetched_content:
            source['content'] = fetched_content
        elif not source.get('content') and snippet:
            source['content'] = snippet
    if image_urls:
        source['image_urls'] = list(dict.fromkeys(image_urls))
    for key in keys:
        key_map[key] = index
    mark_source_roles(config, index, roles)
    return _attach_external_ref(item, index)


def register_existing_sources(
    config: dict[str, Any],
    sources: Any,
) -> None:
    if not isinstance(sources, list):
        return
    refs = config.setdefault(CITATION_REFS_KEY, {})
    for source in sources:
        if not isinstance(source, dict):
            continue
        index = str(source.get('index') or source.get('citation_id') or '').strip()
        if not index:
            continue
        candidate = {
            key: value
            for key, value in source.items()
            if key not in {'source_roles', 'display_index'}
        }
        current = refs.get(index)
        if isinstance(current, dict):
            for key, value in candidate.items():
                if key not in current or current[key] in (None, '', [], {}):
                    current[key] = value
        else:
            refs[index] = candidate
        roles = source.get('source_roles') or ()
        mark_source_roles(config, index, roles)
        document_index, _ = split_citation_index(index)
        if document_index is not None:
            config[CITATION_NEXT_DOC_KEY] = max(
                int(config.get(CITATION_NEXT_DOC_KEY) or 1), document_index + 1,
            )
        if source.get('source_type') == 'external' or source.get('url'):
            key_map = config.setdefault(EXTERNAL_SOURCE_KEY_MAP_KEY, {})
            for key in _external_source_keys(source):
                key_map[key] = index


def materialize_source_views(
    config: dict[str, Any],
    source_views: Any = None,
) -> list[dict[str, Any]]:
    refs = config.get(CITATION_REFS_KEY) or {}
    views: dict[str, dict[str, Any]] = {}
    for source in source_views or []:
        if not isinstance(source, dict):
            continue
        index = str(source.get('index') or source.get('citation_id') or '').strip()
        if index:
            views[index] = source

    ordered_indices: list[str] = []
    for role in _SOURCE_ROLE_ORDER:
        for index in config.get(_SOURCE_ROLE_KEYS[role]) or []:
            normalized_index = str(index)
            if normalized_index not in ordered_indices:
                ordered_indices.append(normalized_index)

    result: list[dict[str, Any]] = []
    for index in ordered_indices:
        source = refs.get(index)
        if not isinstance(source, dict):
            continue
        view = {**source, **views.get(index, {}), 'index': index}
        view['source_roles'] = source_roles(config, index)
        result.append(view)
    return result


def upsert_external_source(
    page: dict[str, Any],
    config: dict[str, Any],
    roles: Any = (),
) -> dict[str, Any]:
    content = str(page.get('content') or '').strip()
    urls = _external_page_urls(page)
    if not content or not urls:
        return page

    refs = config.setdefault(CITATION_REFS_KEY, {})
    key_map = config.setdefault(EXTERNAL_SOURCE_KEY_MAP_KEY, {})
    keys = [_external_url_key(url) for url in urls]
    index = next((key_map[key] for key in keys if key_map.get(key) in refs), None)
    source = refs.get(index) if index is not None else None
    best_url = urls[0]
    if not isinstance(source, dict):
        index = _next_external_citation_index(config)
        source = {
            'source_type': 'external',
            'title': str(page.get('title') or '').strip(),
            'url': best_url,
            'content': content,
        }
        refs[index] = source
    else:
        title = str(page.get('title') or '').strip()
        if title:
            source['title'] = title
        source['url'] = best_url
        source['content'] = content

    for key in keys:
        key_map[key] = index
    mark_source_roles(config, index, roles)
    return _attach_external_ref(page, index)


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
    group = item.get('group') or item.get('group_name') or ''
    if group == 'image':
        return (
            global_md.get('file_name')
            or item.get('file_name')
            or metadata.get('file_name')
            or metadata.get('source')
            or 'title_example'
        )
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
        'source_type': 'knowledge_base',
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
        source['image_url'] = static_file_url_from_any(image_url.strip()) or image_url.strip()
    image_markdown = item.get('image_markdown')
    if isinstance(image_markdown, str) and image_markdown.strip():
        source['image_markdown'] = image_markdown.strip()
    return source


def register_citation_item(
    item: dict[str, Any],
    config: dict[str, Any],
    roles: Any = (),
) -> dict[str, Any]:
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
    mark_source_roles(config, index, roles)
    return item


def annotate_citations(result: Any, config: dict[str, Any], roles: Any = ()) -> Any:
    if isinstance(result, dict):
        if any(k in result for k in ('text', 'content', 'uid', 'docid', 'document_id')):
            register_citation_item(result, config, roles=roles)
        if isinstance(result.get('items'), list):
            result['items'] = [
                annotate_citations(item, config, roles=roles) if isinstance(item, dict) else item
                for item in result['items']
            ]
        if isinstance(result.get('current_node'), dict):
            result['current_node'] = annotate_citations(
                result['current_node'], config, roles=roles,
            )
        return result
    if isinstance(result, list):
        return [
            annotate_citations(item, config, roles=roles) if isinstance(item, dict) else item
            for item in result
        ]
    return result


def reset_citation_state(config: dict[str, Any]) -> None:
    config[CITATION_REFS_KEY] = {}
    config[CITATION_KEY_MAP_KEY] = {}
    config[CITATION_DOC_KEY_MAP_KEY] = {}
    config[CITATION_NEXT_DOC_KEY] = 1
    config[CITATION_DOC_CHUNK_NEXT_KEY] = {}
    config[EXTERNAL_SOURCE_KEY_MAP_KEY] = {}
    config[SEARCHED_SOURCE_INDICES_KEY] = []
    config[CITED_SOURCE_INDICES_KEY] = []
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
        mapped_source['index'] = index
        mapped_source['display_index'] = self.display_index_for(index)
        return mapped_source


def citation_link(index: str, source: dict[str, Any], display_index: Any = None) -> str:
    document_index, _ = split_citation_index(index)
    display_index = display_index or source.get('display_index') or source.get('document_index') or document_index
    title = escape(str(source.get('file_name') or source.get('title') or 'title'), quote=True)
    return f'[{display_index}](#source-{index} "{title}")'


def rewrite_citations(text: str, config: dict[str, Any]) -> tuple[str, list[dict[str, Any]]]:
    collected: OrderedDict[str, dict[str, Any]] = OrderedDict()
    display_mapper = CitationDisplayMapper()

    def _collect(index: str, source: dict[str, Any]) -> dict[str, Any]:
        mark_source_roles(config, index, 'cited')
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
        mark_source_roles(self._config, index, 'cited')
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
