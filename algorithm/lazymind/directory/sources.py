"""Normalize returned source evidence without performing source I/O."""
from __future__ import annotations

import json
import os
import re
import socket
from html import unescape
from urllib.parse import urlparse
from datetime import datetime, timezone

from lazyllm.tools.agent import ToolExecutionDisposition
from lazyllm.tools.fs.supplier.googledrive import GoogleDriveFS


def source_identity(provider, namespace, resource_id):
    source = 'feishu' if provider == 'feishu_wiki' else provider
    if source == 'google_drive':
        resource_id = GoogleDriveFS.file_id_from_path(resource_id)
    return {'source': source, 'namespace': namespace, 'resource_id': str(resource_id)}


def _decoded(value):
    if isinstance(value, str):
        try:
            return json.loads(value)
        except (TypeError, ValueError):
            return value
    return value


def _tool_value(value):
    """Decode the existing MCP adaptor envelope, never arbitrary prose fragments."""
    value = _decoded(value)
    if isinstance(value, dict):
        value = _decoded(value.get('value', value))
    if isinstance(value, str):
        prefix = 'Tool call result:\nReceived text message:\n'
        if value.startswith(prefix):
            return _decoded(value[len(prefix):])
    if isinstance(value, dict):
        if value.get('structuredContent') is not None:
            return _decoded(value['structuredContent'])
        if isinstance(value.get('content'), list):
            texts = [i['text'] for i in value['content']
                     if isinstance(i, dict) and i.get('type') == 'text' and isinstance(i.get('text'), str)]
            if len(texts) == 1:
                return _decoded(texts[0])
    return value


def _notion_id(value):
    if not isinstance(value, str):
        return None
    if value.startswith(('http://', 'https://')):
        parsed = urlparse(value)
        host = (parsed.hostname or '').lower()
        if not any(host == domain or host.endswith('.' + domain)
                   for domain in ('notion.com', 'notion.so', 'notion.site')):
            return None
        value = parsed.path.rstrip('/').rsplit('/', 1)[-1]
        match = re.search(
            r'(?:^|-)([0-9a-fA-F]{32}|[0-9a-fA-F]{8}(?:-[0-9a-fA-F]{4}){3}-[0-9a-fA-F]{12})$', value)
    else:
        match = re.fullmatch(r'([0-9a-fA-F]{32}|[0-9a-fA-F]{8}(?:-[0-9a-fA-F]{4}){3}-[0-9a-fA-F]{12})', value)
    return match[1].replace('-', '').lower() if match else None


def _is_agent_history(path):
    return isinstance(path, str) and any(token in path.replace('\\', '/').rstrip('/') + '/' for token in (
        '/.codex/sessions/', '/.codex/archived_sessions/', '/.claude/projects/', '/opencode/', '/.hermes/',
    ))


def _history_messages(value):
    """Recognize complete returned Codex/Claude JSONL message records, not summaries."""
    value = _tool_value(value)
    clipped, offset = set(), 1
    if isinstance(value, dict):
        clipped = set(value.get('truncated_lines') or [])
        offset = value.get('offset') or 1
        value = value.get('text') or value.get('content') or ''
    if not isinstance(value, str):
        return False
    for number, line in enumerate(value.splitlines(), offset):
        if number in clipped:
            continue
        item = _decoded(re.sub(r'^\s*\d+:\s?', '', line))
        if not isinstance(item, dict):
            continue
        if item.get('type') == 'response_item':
            message = item.get('payload')
            if not isinstance(message, dict) or message.get('type') != 'message':
                continue
        elif item.get('type') in {'user', 'assistant'}:
            message = item.get('message')
        else:
            continue
        if not isinstance(message, dict) or message.get('role') not in {'user', 'assistant'}:
            continue
        content = message.get('content')
        if isinstance(content, str) and content.strip():
            return True
        if isinstance(content, list) and any(
            isinstance(part, dict) and part.get('type') in {'text', 'input_text', 'output_text'}
            and isinstance(part.get('text'), str) and part['text'].strip() for part in content
        ):
            return True
    return False


def _rows(value, inherited=None):
    """Walk provider envelopes while retaining account identity, not credentials."""
    inherited = dict(inherited or {})
    value = _decoded(value)
    if isinstance(value, list):
        for item in value:
            yield from _rows(item, inherited)
    elif isinstance(value, dict):
        for name in ('mailbox', 'account_id', 'workspace_id'):
            if isinstance(value.get(name), str):
                inherited[name] = value[name]
        containers = ('results', 'items', 'files', 'messages', 'pages', 'data', 'value', 'matches', 'result_meta')
        nested = [value[k] for k in containers if isinstance(value.get(k), (dict, list))]
        if any(k in value for k in (
            'id', 'path', 'url', 'page_id', 'document_id', 'file_id', 'resource_id',
            'google_drive_path', 'node_token', 'obj_token', 'token',
        )):
            yield {**inherited, **value}
        for child in nested:
            metadata = dict(inherited)
            if child is value.get('result_meta'):
                title = value.get('title') or value.get('title_highlighted')
                if isinstance(title, str):
                    metadata['title'] = unescape(re.sub(r'</?(?:em|b|h|mark|highlight)>', '', title))
            yield from _rows(child, metadata)


def normalize_observation(record, configuration=None) -> dict | None:
    name = record.tool_name
    args = record.validated_arguments or _decoded(record.arguments) or {}
    if not isinstance(args, dict):
        return None
    service = (configuration.names.get(name, '') if configuration else '')
    lower = (service + ' ' + name).lower()
    source = next((v for k, v in (
        ('msp_notion', 'notion'), ('notion', 'notion'), ('feishu', 'feishu'),
        ('googledrive', 'google_drive'), ('google_drive', 'google_drive'), ('mail', 'mail'),
    ) if k in lower), '')
    local = name in {'native_search', 'glob', 'grep', 'read', 'read_file_resource', 'search_file_resource', 'shell'}
    if not source and not local:
        return None
    if not source:
        source = 'local'
    search = any(k in name.lower() for k in ('search', 'find', 'grep', 'glob'))
    read = any(k in name.lower() for k in ('read', 'fetch', 'retrieve'))
    metadata = source in {'feishu', 'google_drive', 'notion'} and name.lower().endswith(('_info', '_resolve_link'))
    if not search and not read and not metadata and name != 'shell':
        return None  # Mutations and credential/configuration results are not evidence.
    # Never persist credentials or arbitrary command text as directory scope.
    scope = {k: args[k] for k in (
        'query', 'keyword', 'keywords', 'path', 'root', 'target', 'pattern',
        'mailbox', 'folder', 'space_id', 'node_id', 'folder_id',
        'drive_id', 'after', 'before', 'offset', 'limit', 'page_token', 'source_type', 'match', 'kind',
    ) if k in args}
    operation = 'search' if search else 'metadata' if metadata else 'read'
    observation_source = ('agent_conversations' if source == 'local' and any(
        _is_agent_history(args.get(k)) for k in ('path', 'root', 'target', 'file_path')) else source)
    executed = record.disposition is ToolExecutionDisposition.EXECUTED
    if not executed:
        if record.reason == 'deduplicated':
            return None
        return {'call_id': record.call_id, 'source': observation_source, 'operation': operation,
                'scope': scope, 'status': 'not_executed', 'incomplete': True, 'candidates': []}
    # Cloud filesystem readers return body strings; JSON inside the body is
    # not a tool envelope and must not supply resource/account identity.
    raw = (record.result if source in {'feishu', 'google_drive'} and operation == 'read'
           and isinstance(record.result, str) else _decoded(record.result))
    failed = isinstance(raw, dict) and (raw.get('ok') is False or raw.get('isError') is True)
    returned = raw.get('value', raw) if isinstance(raw, dict) else record.result
    plain_document = source in {'feishu', 'google_drive'} and operation == 'read' and isinstance(returned, str)
    value = returned if plain_document else _tool_value(returned)
    failed = failed or isinstance(value, dict) and (
        value.get('ok') is False or value.get('isError') is True or bool(value.get('error')))
    if isinstance(value, str) and value.startswith((
        'Failed to call MCP tool ', 'Error processing content from MCP tool ', 'Missing required parameters:',
    )):
        failed = True
    text = json.dumps(value, ensure_ascii=False, default=str)
    partial = isinstance(value, dict) and bool(
        value.get('has_more') or value.get('truncated') or value.get('next_page_token')
        or value.get('next_offset') or value.get('eof') is False,
    )
    if source == 'google_drive' and operation == 'search' and isinstance(value, list):
        partial = len(value) >= int(args.get('limit') or 20)
    if isinstance(value, dict) and int(value.get('offset') or 1) > 1:
        partial = True
    status = 'failed' if failed else 'partial' if partial else 'success'
    if failed and re.search(r'\b(?:unauthorized|forbidden|99991679)\b|HTTP (?:401|403)\b', text, re.I):
        status = 'denied'
    if isinstance(value, dict) and value.get('status') in {'unavailable', 'partial', 'error', 'denied'}:
        status = str(value['status'])
        failed = status in {'unavailable', 'error', 'denied'}
        partial = partial or status == 'partial'
    if name == 'shell':
        # A command string or arbitrary stdout does not authenticate a resource locator.
        return {'call_id': record.call_id, 'source': source, 'operation': operation,
                'scope': {}, 'status': status if failed else 'unparsed',
                'incomplete': True, 'candidates': []}
    # A document containing JSON is still body text, not provider metadata.
    rows = [] if plain_document else list(_rows(value))
    if name == 'glob' and isinstance(value, dict):
        rows = [{'path': p} for p in value.get('paths', []) if isinstance(p, str)]
    path = args.get('path') or args.get('target') or args.get('file_path') or args.get('url_or_path')
    if source == 'local' and operation == 'read' and name != 'shell' and isinstance(path, str):
        returned = value if isinstance(value, dict) else {}
        content = value if isinstance(value, str) else returned.get('content') or returned.get('text', '')
        rows = [{**returned, 'path': returned.get('path') or path,
                 'content': content}]
    if source == 'notion' and operation == 'read' and not rows and isinstance(args.get('id'), str):
        # Unknown strings may be errors or unparsed envelopes, not verified content.
        rows = [{'id': args['id']}]
    if source in {'feishu', 'google_drive', 'notion'} and operation == 'read' and not rows and isinstance(path, str):
        rows = [{'path': path, 'url': path if path.startswith('http') else '', 'content': text}]
    items = []
    gaps = False
    scopes = _decoded((getattr(configuration, 'tool_config', {}) or {}).get('document_directory_scopes', {}))
    if not isinstance(scopes, dict):
        scopes = {}
    now = datetime.now(timezone.utc).isoformat()
    for row in rows[:1000]:
        namespace = str(row.get('account_id') or row.get('workspace_id') or '')
        if not namespace and source not in {'local', 'mail'}:
            identities = scopes.get(service, [])
            if len(identities) == 1:
                namespace = identities[0]
            elif service.startswith('mcp:'):
                runtime = (getattr(configuration, 'services', {}).get(service, {}).get('runtime') or {})
                oauth = runtime.get('oauth') or {}
                if not oauth.get('grant_id'):
                    gaps = True
                    continue
                namespace = f"{service}:{oauth['grant_id']}:{oauth.get('grant_version', 0)}"
            else:
                gaps = True
                continue
        rid = next((row[k] for k in (
            'id', 'page_id', 'document_id', 'file_id', 'resource_id', 'node_token', 'obj_token', 'token',
        ) if row.get(k)), None)
        if source == 'google_drive':
            try:
                rid = GoogleDriveFS.file_id_from_path(
                    rid or row.get('name') or row.get('path') or row.get('google_drive_path'))
            except ValueError:
                gaps = True
                continue
        if source == 'feishu':
            rid = row.get('node_token') or rid
        if source == 'feishu' and not rid:
            match = re.search(r'(?:^|/)(?:~node|~docx|~doc|wiki|docx|docs|sheets|file)/([^/?#]+)',
                              str(row.get('url') or row.get('path') or ''))
            rid = match[1] if match else None
        locator = {k: row[k] for k in (
            'id', 'url', 'path', 'thread_id', 'folder', 'mailbox', 'session_id',
            'start_message_id', 'end_message_id', 'offset', 'end_line', 'eof', 'next_offset',
            'line_unit', 'google_drive_path', 'web_url',
        ) if isinstance(row.get(k), (str, int))}
        if isinstance(row.get('truncated_lines'), list):
            locator['truncated_lines'] = row['truncated_lines']
        if source == 'local':
            path = row.get('path')
            if not isinstance(path, str) or not os.path.isabs(path):
                gaps = True
                continue
            rid = os.path.realpath(path)
            locator['path'] = rid
            namespace = f'{socket.gethostname()}:{os.name}'
            if _is_agent_history(rid):
                # A history file hit alone cannot identify a conversation window.
                if all(row.get(k) for k in ('session_id', 'start_message_id', 'end_message_id')):
                    rid = ':'.join(str(row[k]) for k in ('session_id', 'start_message_id', 'end_message_id'))
                elif (operation == 'read' and row.get('line_unit') == 'physical'
                      and row.get('offset') and row.get('end_line')
                      and row['end_line'] >= row['offset']):
                    locator['range_kind'] = 'physical_lines'
                    rid += f":physical:{row['offset']}:{row['end_line']}"
                else:
                    gaps = True
                    continue
                item_source = 'agent_conversations'
            else:
                item_source = source
        else:
            item_source = source
        if source == 'mail':
            mailbox = row.get('mailbox') or args.get('mailbox')
            if not mailbox:
                continue
            namespace = str(mailbox).lower()
            # Provider UIDs may be reused. Prefer RFC Message-ID when returned.
            if row.get('thread_id') and str(row['thread_id']).startswith('<'):
                rid = row['thread_id']
            locator['mailbox'] = mailbox
        if source == 'notion':
            identities = [_notion_id(v) for v in (rid, row.get('url'), row.get('web_url')) if v]
            if operation == 'read':
                identities.append(_notion_id(args.get('id')))
            known = {v for v in identities if v}
            if len(known) != 1:
                gaps = True
                continue
            rid = known.pop()
            for url in (row.get('url'), row.get('web_url'), row.get('id'),
                        args.get('id') if operation == 'read' else None):
                if isinstance(url, str) and url.startswith(('https://', 'http://')) and _notion_id(url) == rid:
                    locator['url'] = url
                    break
        if not rid:
            gaps = True
            continue
        snippet = row.get('content') or row.get('text') or row.get('body') or row.get('snippet') or ''
        if not isinstance(snippet, str):
            snippet = json.dumps(snippet, ensure_ascii=False)
        read = operation == 'read' and not failed and bool(snippet) and row.get('version_stable', True)
        if operation == 'read' and not read:
            gaps = True
        # Compare like-for-like provider versions only. A rendered excerpt or
        # reader response is not a stable fingerprint of the source document.
        version = row.get('content_version') or next(
            (f'{field}:{row[field]}' for field in ('version', 'page_last_edited_at', 'modified_time', 'mtime')
             if row.get(field) is not None), '')
        if source == 'google_drive' and version and not row.get('content_version'):
            version = 'source_revision:' + version
        items.append({**source_identity(item_source, namespace, rid),
                      'title': str(row.get('title') or row.get('name') or row.get('subject') or '')[:1000],
                      'locator': locator,
                      'excerpt': '' if failed else snippet.encode('utf-8')[:6000].decode('utf-8', 'ignore'),
                      'read_state': ('partial' if partial else 'read') if read else 'unread',
                      'read_at': now if read else None,
                      'access_state': ('denied' if status == 'denied' else 'unavailable' if failed
                                       else 'accessible' if read else 'unknown'),
                      'content_version': version, 'version_stable': row.get('version_stable'),
                      'trashed': row.get('trashed')})
    incomplete = (gaps or partial or failed or len(rows) > 1000
                  or not items and operation == 'read' or name == 'shell')
    # Retain metadata without turning observations into a document body store.
    if len(items) > 100:
        for item in items:
            item['excerpt'] = ''
        incomplete = True
    if operation == 'search' and not items and not failed:
        # Only an actual structured empty list establishes zero hits.
        empty = isinstance(value, list) and not value or isinstance(value, dict) and any(
            value.get(k) == [] for k in ('results', 'items', 'files', 'messages', 'paths'))
        status = 'zero' if empty else 'unknown'
        incomplete = bool(incomplete) or not empty
    return {'call_id': record.call_id, 'source': observation_source, 'operation': operation, 'scope': scope,
            'status': status, 'incomplete': bool(incomplete), 'candidates': items}
