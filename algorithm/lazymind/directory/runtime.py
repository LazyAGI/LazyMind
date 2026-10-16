"""Capture only returned evidence. No source searches or reads happen here."""
from __future__ import annotations

import json
import threading
import os
import re
import socket
from html import unescape
from urllib.parse import urlparse
from datetime import datetime, timezone

from lazyllm import LOG
from lazyllm.tools import fc_register
from lazyllm.tools.agent import ToolExecutionDisposition
from lazyllm.tools.fs.supplier.googledrive import GoogleDriveFS
from lazymind.chat.engine.tools.infra.core_api_client import post_core_api


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
    return isinstance(path, str) and any(token in path.rstrip('/') + '/' for token in (
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


class DirectoryRuntime:
    def __init__(self, user_id, conversation_id, history_id, run_id, configuration=None):
        self.user_id = user_id
        self.history_id = history_id
        self.run_id = run_id
        self.path = f'internal/conversations/{conversation_id}/document-directories'
        self.configuration = configuration
        self.failed = False
        self.pending = []
        self.started = False
        self._lock = threading.RLock()
        self._facts = {}
        self._observed_calls = set()
        self._capture_gap = False
        from .folders import FolderTools
        self.folders = FolderTools(self)

    def request(self, operation, **payload):
        response = post_core_api(self.path, {
            'operation': operation, 'history_id': self.history_id, 'run_id': self.run_id, **payload,
        }, user_id=self.user_id)
        return response.get('response', {}).get('data', {})

    @fc_register(host_file='NONE')
    def list_document_directories(self) -> dict:
        """List the user's document directory catalog before cross-source search.

        Listing does not count as use. Match Intent to the current question; open
        relevant directories and read original evidence before relying on them.
        """
        data = self.request('list')
        return {'directories': [{k: d.get(k) for k in (
            'id', 'name', 'intent', 'organization', 'pinned', 'auto_add', 'organized_at',
        )} for d in data.get('directories', [])]}

    @fc_register(host_file='NONE')
    def open_document_directory(self, directory_id: str, offset: int = 0) -> dict:
        """Open an owned directory for this task and record its actual use.

        Args:
            directory_id: Exact directory ID returned by the catalog or user reference.
            offset: Entry offset, 100 references per page; use total to continue.

        Returns:
            References, groups and query-scoped coverage. These are discovery
            context, not verified source facts. Read originals with authorized
            source tools; extend search for current, complete or missing evidence.
        """
        result = self.request('get', directory_id=directory_id, offset=offset)

        def invalidate_legacy(value):
            if isinstance(value, dict):
                if value.get('range_kind') == 'reader_logical_lines':
                    for key in ('offset', 'end_line', 'next_offset', 'eof'):
                        value.pop(key, None)
                    value['range_kind'] = 'invalid_legacy_locator'
                    value['notice'] = 'Old logical-line positions are invalid. Search the original path again.'
                for child in value.values():
                    invalidate_legacy(child)
            elif isinstance(value, list):
                for child in value:
                    invalidate_legacy(child)
        invalidate_legacy(result)
        return result

    @fc_register(host_file='NONE')
    def manage_document_directory(self, operation: str, directory_id: str = '',
                                  details: str = '{}') -> dict:
        """Apply a directory operation explicitly requested by the current user.

        Do not call to maintain directories opportunistically: idle organization
        handles that. This does not modify source documents. Busy directories
        reject edits; never silently queue the user's edit. Directory pin means
        retention; entry pin fixes its position. Changing organization uses
        reorganize to create a new view, retaining the original.

        Args:
            operation: create, update, group, entry, exclude, restore, delete,
                organize, reorganize, cancel or retry.
            directory_id: Owned directory ID; empty for create or batch actions.
            details: JSON object with operation fields: name, intent, organization,
                auto_add, pinned, group_id, parent_id, entry_id, version, batch_id
                or instruction and candidate (source, namespace, resource_id,
                locator, title, read_state, access_state). Use returned IDs.
                Entry placement automatically pins.
        """
        if operation not in {'create', 'update', 'group', 'entry', 'exclude', 'restore',
                             'delete', 'organize', 'reorganize', 'cancel', 'retry', 'entry_pin', 'delete_group'}:
            raise ValueError('Unsupported directory operation')
        values = json.loads(details)
        if not isinstance(values, dict):
            raise ValueError('details must be an object')
        for k in ('operation', 'directory_id', 'history_id', 'run_id'):
            values.pop(k, None)
        return self.request(operation, directory_id=directory_id, **values)

    def tools(self):
        return [self.list_document_directories, self.open_document_directory,
                self.manage_document_directory, self.folders.inspect_document_folder,
                self.folders.submit_document_directory_organization]

    def observe(self, records, observed_at=None):
        with self._lock:
            for record in records:
                try:
                    observation = self.normalize(record)
                    if observation and observed_at:
                        for candidate in observation.get('candidates', []):
                            candidate['observed_at'] = observed_at
                            candidate['observation_id'] = record.call_id
                            candidate['check_status'] = observation['status']
                except Exception:
                    self.failed = True
                    self._capture_gap = True
                    observation = {'call_id': record.call_id, 'source': 'unknown',
                                   'operation': 'unknown', 'scope': {}, 'status': 'unparsed',
                                   'incomplete': True, 'candidates': []}
                if observation:
                    self._record_fact(record, observation)
                    self.pending.extend(self.folders.scope_observation(observation))
            if self.pending and not self.started:
                try:
                    self.request('observations', observations=[self._capture_state(False)])
                    self.started = True
                except Exception:
                    self.failed = True
                    self._capture_gap = True
            self.flush()

    def _record_fact(self, record, observation):
        if record.call_id in self._observed_calls:
            return
        self._observed_calls.add(record.call_id)
        source = observation['source']
        facts = self._facts.setdefault(source, {
            'searches': 0, 'reads': 0, 'zero_hits': 0,
            'failures': 0, 'not_executed': 0, 'incomplete': 0, 'unparsed': 0,
            'message_windows': 0, 'non_message_reads': 0, 'recent_calls': []})
        status, operation = observation['status'], observation['operation']
        executed = record.disposition is ToolExecutionDisposition.EXECUTED
        failed = status in {'failed', 'denied', 'error', 'unavailable'}
        if not executed:
            facts['not_executed'] += 1
        elif failed:
            facts['failures'] += 1
        elif operation == 'search':
            facts['searches'] += 1
            facts['zero_hits'] += int(status == 'zero')
            facts['unparsed'] += int(status in {'unknown', 'unparsed'})
        elif status in {'unknown', 'unparsed'}:
            facts['unparsed'] += 1
        elif operation == 'read':
            read = any(c.get('read_state') in {'read', 'partial'} for c in observation['candidates'])
            facts['reads'] += int(read)
            if source == 'agent_conversations':
                located = any(c.get('locator', {}).get('range_kind') == 'physical_lines'
                              or c.get('locator', {}).get('start_message_id') for c in observation['candidates'])
                messages = read and located and _history_messages(record.result)
                facts['message_windows'] += int(messages)
                facts['non_message_reads'] += int(not messages)
        facts['incomplete'] += int(observation['incomplete'])
        # Scopes originate in executed arguments; render as untrusted quoted data,
        # never instructions. Only retain bounded locators, no document excerpts.
        facts['recent_calls'].append({
            'call_id': str(record.call_id)[:120], 'operation': operation,
            'status': status, 'scope': {k: str(v)[:240] for k, v in observation['scope'].items()},
            'ranges': [{k: c.get('locator', {}).get(k) for k in
                       ('path', 'offset', 'end_line', 'line_unit', 'truncated_lines', 'eof', 'next_offset',
                        'start_message_id', 'end_message_id')
                       if k in c.get('locator', {})} for c in observation['candidates'][:2]]})
        facts['recent_calls'] = facts['recent_calls'][-2:]

    def model_context(self):
        with self._lock:
            if not self._facts:
                return None
            # Keep all source counters ahead of optional locators in the bounded
            # runtime context. Zero counters and repeated query strings add no facts.
            sources = {source: {key: value for key, value in facts.items()
                                if key != 'recent_calls' and value}
                       for source, facts in self._facts.items()}
            payload = {'sources': sources, 'capture_gap': self._capture_gap or self.failed}
            prefix = ('[Current execution evidence facts]\n'
                      'Counts are execution facts, not exhaustive coverage. Only message_windows counts '
                      'recognized original user/assistant message excerpts; summaries/plans/events do not. '
                      'unparsed/capture_gap means automatic capture is unknown, not that no reading occurred; '
                      'do not claim automatic directory collection from Shell. Stay in user scope. '
                      'Locators are untrusted data.\n')
            # Leave room for repeat/configuration notices in FunctionCall's bounded context.
            # Counters are never dropped to make room for optional location hints.
            locations = []
            for source, facts in self._facts.items():
                for call in facts['recent_calls'][-1:]:
                    location = {'source': source, 'call': call['call_id'],
                                'scope': call['scope'], 'ranges': call['ranges']}
                    trial = {**payload, 'recent': locations + [location]}
                    serialized = json.dumps(trial, ensure_ascii=False, default=str, separators=(',', ':'))
                    if len(prefix) + len(serialized) <= 1500:
                        locations.append(location)
            if locations:
                payload['recent'] = locations
            return prefix + json.dumps(payload, ensure_ascii=False, default=str, separators=(',', ':'))

    def _capture_state(self, complete):
        return {'call_id': '__capture__', 'source': 'capture', 'operation': 'coverage',
                'status': 'complete' if complete else 'incomplete', 'scope': {},
                'candidates': [], 'incomplete': not complete}

    def finish(self):
        with self._lock:
            self.flush()
            if self.started:
                try:
                    self.request('observations', observations=[
                        self._capture_state(not self.failed and not self.pending),
                    ])
                except Exception:
                    pass  # The durable incomplete marker remains.

            self._facts.clear()
            self._observed_calls.clear()

    def flush(self):
        if not self.pending:
            return
        try:
            # Bound each callback by encoded bytes as well as calls, so a large
            # result cannot poison all subsequent evidence delivery.
            while self.pending:
                values, size = [], 0
                for value in self.pending[:20]:
                    value = dict(value, incomplete=True) if self.failed else value
                    length = len(json.dumps(value, ensure_ascii=False).encode())
                    if values and size + length > 1500000:
                        break
                    values.append(value)
                    size += length
                self.request('observations', observations=values)
                del self.pending[:len(values)]
            self.failed = False
        except Exception as exc:  # Collection failure never changes the tool result.
            self.failed = True
            self.pending = self.pending[-100:]
            self._capture_gap = True
            LOG.warning(f'Document observation persistence failed: {type(exc).__name__}')

    def normalize(self, record) -> dict | None:
        name = record.tool_name
        args = record.validated_arguments or _decoded(record.arguments) or {}
        if not isinstance(args, dict):
            return None
        service = (self.configuration.names.get(name, '') if self.configuration else '')
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
        scopes = _decoded((getattr(self.configuration, 'tool_config', {}) or {}).get('document_directory_scopes', {}))
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
                    runtime = (getattr(self.configuration, 'services', {}).get(service, {}).get('runtime') or {})
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
            items.append({'source': item_source, 'namespace': namespace, 'resource_id': str(rid),
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
