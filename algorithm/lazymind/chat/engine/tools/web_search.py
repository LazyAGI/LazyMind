from __future__ import annotations

import json
import secrets
from concurrent.futures import ThreadPoolExecutor
from typing import Any, Dict, List, Optional

import lazyllm

from lazymind.chat.engine.tools.infra import (
    fetch_url_content,
    tool_success,
)
from lazymind.chat.service.utils.citations import normalize_external_url


def _agentic_config() -> Dict[str, Any]:
    config = lazyllm.globals.get('agentic_config')
    if not isinstance(config, dict):
        config = {}
        lazyllm.globals['agentic_config'] = config
    return config


def _page_source_keys(page: Dict[str, Any]) -> List[str]:
    metadata = page.get('metadata') if isinstance(page.get('metadata'), dict) else {}
    values = [metadata.get('canonical_url'), page.get('canonical_url'), page.get('final_url'), page.get('url')]
    return list(dict.fromkeys(key for key in map(normalize_external_url, values) if key))


def _ancestor_source_keys(page_ref: str) -> set[str]:
    navigation = _agentic_config().get('web_navigation_state') or {}
    keys: set[str] = set()
    ancestor_ref: Optional[str] = page_ref
    while ancestor_ref:
        ancestor = navigation.get(ancestor_ref)
        if not isinstance(ancestor, dict):
            break
        keys.update(ancestor.get('source_keys') or [])
        fallback = normalize_external_url(ancestor.get('final_url'))
        if fallback:
            keys.add(fallback)
        ancestor_ref = ancestor.get('parent_page_ref')
    return keys


def _normalize_page_links(page: Dict[str, Any]) -> List[Dict[str, Any]]:
    links = []
    seen = set()
    for item in page.get('links') or []:
        if not isinstance(item, dict) or not item.get('target_url'):
            continue
        key = normalize_external_url(item['target_url'])
        if not key or key in seen:
            continue
        seen.add(key)
        links.append({**item, 'id': len(links) + 1})
    return links


def _navigation_snapshot(page: Dict[str, Any]) -> Dict[str, Any]:
    return {
        'final_url': page.get('final_url') or page.get('url') or '',
        'source_keys': _page_source_keys(page),
        'parent_page_ref': page.get('parent_page_ref'),
        'via_link_id': page.get('via_link_id'),
        'depth': int(page.get('depth') or 0),
        'links': {
            int(item['id']): str(item['target_url'])
            for item in (page.get('links') or [])
            if isinstance(item, dict)
            and str(item.get('id') or '').isdigit()
            and normalize_external_url(item.get('target_url'))
        },
    }


def restore_web_navigation_state(history: List[Dict[str, Any]]) -> Dict[str, Any]:
    navigation: Dict[str, Any] = {}

    def restore_page(page: Any) -> None:
        if not isinstance(page, dict):
            return
        page_ref = str(page.get('page_ref') or '').strip()
        if page_ref:
            navigation[page_ref] = _navigation_snapshot(page)
        for item in page.get('results') or []:
            if isinstance(item, dict) and item.get('success') is not False:
                restore_page(item.get('result'))

    for message in history or []:
        if message.get('role') != 'tool' or message.get('name') != 'url_fetch':
            continue
        content = message.get('content')
        try:
            payload = json.loads(content) if isinstance(content, str) else content
        except json.JSONDecodeError:
            continue
        if not isinstance(payload, dict) or payload.get('success') is False:
            continue
        restore_page(payload.get('result', payload))
    return navigation


def _fetch_page(
    url: str,
    *,
    parent_page_ref: Optional[str] = None,
    via_link_id: Optional[int] = None,
    depth: int = 0,
    ancestor_source_keys: Optional[set[str]] = None,
) -> Dict[str, Any]:
    page = fetch_url_content(url)
    if ancestor_source_keys and set(_page_source_keys(page)) & ancestor_source_keys:
        raise ValueError('navigation_cycle')
    navigation = _agentic_config().setdefault('web_navigation_state', {})
    page_ref = f'page_{secrets.token_hex(6)}'
    while page_ref in navigation:
        page_ref = f'page_{secrets.token_hex(6)}'
    page['links'] = _normalize_page_links(page)
    page.update({
        'page_ref': page_ref,
        'parent_page_ref': parent_page_ref,
        'via_link_id': via_link_id,
        'depth': depth,
    })
    navigation[page_ref] = _navigation_snapshot(page)
    return page


def _click_target(page_ref: str, link_id: int) -> tuple[str, int, set[str]]:
    navigation = _agentic_config().get('web_navigation_state') or {}
    snapshot = navigation.get(page_ref)
    if not isinstance(snapshot, dict):
        raise ValueError('page_ref is invalid or expired')
    target = (snapshot.get('links') or {}).get(link_id)
    if not target:
        raise ValueError('link_id does not exist in page_ref')

    ancestor_keys = _ancestor_source_keys(page_ref)
    target_key = normalize_external_url(target)
    if target_key and target_key in ancestor_keys:
        raise ValueError('navigation_cycle')
    return str(target), int(snapshot.get('depth') or 0) + 1, ancestor_keys


def url_fetch(
    url: str = '',
    urls: Optional[List[str]] = None,
    page_ref: str = '',
    link_id: Optional[int] = None,
) -> Dict[str, Any]:
    """Fetch readable content from one or more public web pages.

    Use this for public web pages. Do not use it for authenticated cloud-file
    URLs such as Feishu/Lark Wiki or Docs and Notion; use CloudFileToolkit for
    those links instead. Never invent or guess a URL: use a URL supplied by the
    user or returned by a search tool. When several public URLs need inspection, pass all of
    them in `urls` in one call instead of relying on multiple parallel tool
    calls. The pages are fetched concurrently with bounded concurrency.

    Args:
        url: One absolute URL, or a domain/path that can be normalized to HTTPS.
            Use this for a single page and omit it when `urls` is supplied. When
            following a page link, omit it; a redundant URL is accepted only when
            it matches the target identified by page_ref and link_id.
        urls: Public URLs to fetch as one batch. Duplicate URLs are fetched once.
        page_ref: A page reference returned by an earlier url_fetch call. Supply it
            together with link_id to follow one of that page's links.
        link_id: A page-local link ID returned with page_ref. Never guess this value.

    Returns:
        For one URL, the existing page metadata and extracted text payload. For
        a batch, a dict containing total/succeeded/failed counts and one result
        per URL; an individual failure does not discard successful pages.
    """
    click_mode = bool(str(page_ref or '').strip()) or link_id is not None
    fetch_options: Dict[str, Any] = {}
    if click_mode:
        if not str(page_ref or '').strip() or link_id is None:
            raise ValueError('page_ref and link_id are required together')
        if urls:
            raise ValueError('urls cannot be combined with page_ref/link_id')
        parent_page_ref = str(page_ref).strip()
        target, depth, ancestor_keys = _click_target(parent_page_ref, int(link_id))
        provided_url = str(url or '').strip()
        if provided_url and normalize_external_url(provided_url) != normalize_external_url(target):
            raise ValueError('url does not match the page_ref/link_id target')
        requested = [target]
        fetch_options = {
            'parent_page_ref': parent_page_ref,
            'via_link_id': int(link_id),
            'depth': depth,
            'ancestor_source_keys': ancestor_keys,
        }
    else:
        requested = [str(item).strip() for item in (urls or []) if str(item).strip()]
        if str(url or '').strip():
            requested.insert(0, str(url).strip())
        requested = list(dict.fromkeys(requested))
    if not requested:
        raise ValueError('url or urls is required')
    if len(requested) == 1:
        return tool_success('url_fetch', _fetch_page(requested[0], **fetch_options))

    def fetch_one(item: str) -> Dict[str, Any]:
        try:
            return {'url': item, 'success': True, 'result': _fetch_page(item)}
        except Exception as exc:
            return {
                'url': item,
                'success': False,
                'error': f'{type(exc).__name__}: {exc}',
            }

    with ThreadPoolExecutor(max_workers=min(len(requested), 5)) as executor:
        results = list(executor.map(fetch_one, requested))
    succeeded = sum(bool(item['success']) for item in results)
    return tool_success('url_fetch', {
        'total': len(results),
        'succeeded': succeeded,
        'failed': len(results) - succeeded,
        'results': results,
    })
