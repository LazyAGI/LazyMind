"""Authorized folder enumeration. The model receives pages, never owns the manifest."""
from __future__ import annotations

import hashlib
import json
import os
import re
import socket
import stat
from contextlib import contextmanager
from datetime import datetime, timezone
from urllib.parse import urlparse

from lazyllm.tools import fc_register
from lazyllm.tools.agent import ToolExecutionError
from lazymind.chat.engine.tools.host_file_resolution import FileResolution
from lazymind.chat.engine.tools.workspace_context import get_workspace_permission_context
from lazymind.chat.engine.tools.host_access_guard import get_host_access_guard


def resolve_folder_access(arguments):
    resolution = FileResolution()
    if arguments.get('provider', 'local') == 'local':
        permission = get_workspace_permission_context()
        if not permission or not permission.local_runtime or not (permission.active or permission.trusted_local):
            raise ToolExecutionError('An authorized local workspace is required.')
        root = arguments.get('root', '')
        arguments['root'] = resolution.local(root)
        if os.name == 'nt':
            # Keep the authorized spelling so a junction cannot disappear into
            # canonicalization before the handle-based component checks.
            root = os.path.abspath(root)
            if os.path.normcase(root) != os.path.normcase(arguments['root']):
                raise ToolExecutionError('path_invalid')
            arguments['root'] = root
    return resolution.finish(arguments)


def node_key(identity):
    return hashlib.sha256(identity.encode()).hexdigest()


def candidate(source, resource_id, title, locator):
    return {'source': 'feishu' if source['provider'] == 'feishu_wiki' else source['provider'],
            'namespace': source['namespace'], 'resource_id': resource_id, 'title': title,
            'locator': locator, 'read_state': 'unread', 'access_state': 'accessible'}


def node(identity, parent, path, name, kind, item=None, listed=False):
    return {'node_key': node_key(identity), 'parent_key': parent, 'path': path,
            'name': name, 'kind': kind, 'listed': listed, 'candidate': item}


@contextmanager
def local_directory(root, relative=''):
    """Open each component without following links, including during rename races."""
    guard = get_host_access_guard()
    if guard:
        guard.validate()
    if os.path.normcase(os.path.realpath(root)) != os.path.normcase(root):
        raise ToolExecutionError('path_invalid')
    if os.name == 'nt':
        from .windows_directory import pinned_directory
        with pinned_directory(root, relative) as directory:
            yield directory
        return
    flags = os.O_RDONLY | getattr(os, 'O_DIRECTORY', 0) | getattr(os, 'O_NOFOLLOW', 0)
    descriptor = os.open(root, flags)
    try:
        for part in relative.split('/') if relative else []:
            if part in ('', '.', '..'):
                raise ToolExecutionError('path_invalid')
            child = os.open(part, flags, dir_fd=descriptor)
            os.close(descriptor)
            descriptor = child
        yield descriptor
    finally:
        os.close(descriptor)


def directory_stat(directory):
    return os.fstat(directory) if isinstance(directory, int) else os.stat(directory, follow_symlinks=False)


def local_page(source, current):
    root, relative = source['root'], current['path']
    with local_directory(root, relative) as fd:
        info = directory_stat(fd)
        identity = [info.st_dev, info.st_ino, info.st_mtime_ns]
        if current.get('identity') and current['identity'] != identity:
            raise ToolExecutionError('Directory changed during inventory; restart inventory.')
        with os.scandir(fd) as iterator:
            names = sorted(entry.name for entry in iterator)
        start = current.get('cursor', 0)
        nodes, children = [], []
        for name in names[start:start + 100]:
            info = (os.stat(name, dir_fd=fd, follow_symlinks=False) if isinstance(fd, int)
                    else os.stat(os.path.join(fd, name), follow_symlinks=False))
            path = '/'.join(filter(None, [relative, name]))
            absolute = os.path.join(root, *path.split('/'))
            kind = ('link' if stat.S_ISLNK(info.st_mode) or getattr(info, 'st_file_attributes', 0) & 0x400
                    else 'directory' if stat.S_ISDIR(info.st_mode)
                    else 'file' if stat.S_ISREG(info.st_mode) else 'special')
            item = None if kind == 'directory' else candidate(source, absolute, name, {'path': absolute, 'kind': kind})
            if item:
                item['content_version'] = f'mtime:{info.st_mtime_ns}:{info.st_size}'
            entry = node(path, current['key'], path, name, kind, item)
            nodes.append(entry)
            if kind == 'directory':
                children.append({'key': entry['node_key'], 'path': path, 'id': path})
        after = directory_stat(fd)
        if [after.st_dev, after.st_ino, after.st_mtime_ns] != identity:
            raise ToolExecutionError('Directory changed during inventory; restart inventory.')
        next_cursor = start + len(nodes) if start + len(nodes) < len(names) else None
        return nodes, children, next_cursor, identity


def cloud_client(provider):
    if provider == 'google_drive':
        from lazyllm.tools.fs.supplier.googledrive import GoogleDriveFS
        return GoogleDriveFS(dynamic_auth=True, skip_instance_cache=True)
    from lazyllm.tools.fs.supplier.feishu import FeishuFS, FeishuWikiFS
    return (FeishuWikiFS if provider == 'feishu_wiki' else FeishuFS)(dynamic_auth=True, skip_instance_cache=True)


def cloud_root(provider, root, client):
    parsed = urlparse(root)
    if parsed.scheme:
        if parsed.scheme != 'https':
            raise ValueError('Use an HTTPS folder link.')
        host = (parsed.hostname or '').lower()
        if provider == 'google_drive':
            if host != 'drive.google.com':
                raise ValueError('Expected a Google Drive folder link.')
            match = re.search(r'/folders/([A-Za-z0-9_-]+)', parsed.path)
        else:
            if not any(host == d or host.endswith('.' + d) for d in ('feishu.cn', 'feishu.com', 'larksuite.com')):
                raise ValueError('Expected a Feishu/Lark folder or Wiki link.')
            match = re.search(r'/(?:wiki)/([A-Za-z0-9_-]+)' if provider == 'feishu_wiki'
                              else r'/(?:drive/folder|folder)/([A-Za-z0-9_-]+)', parsed.path)
        if not match:
            raise ValueError('Expected a folder or Wiki subtree, not a document link.')
        identifier = match[1]
    else:
        identifier = root.strip('/')
        if not re.fullmatch(r'[A-Za-z0-9_-]+', identifier):
            raise ValueError('Use a stable folder ID or supported folder URL.')
    if provider == 'google_drive':
        info = client.info('/' + identifier)
        if info.get('type') != 'directory':
            raise ValueError('The selected resource is not a folder.')
        return identifier, info['title'], {'drive_id': info.get('drive_id', '')}, None
    if provider == 'feishu_wiki':
        info = client.resolve_wiki_ref('/~node/' + identifier)
        if not info.get('space_id'):
            raise ValueError('Wiki space identity unavailable.')
        return identifier, info.get('title') or identifier, {'space_id': info['space_id']}, info
    info = client.folder_info(identifier)
    return identifier, info.get('name') or identifier, {}, None


def cloud_page(source, current, client):
    provider = source['provider']
    cursor = current.get('cursor')
    if provider == 'google_drive':
        response = client.list_page(folder_id=current['id'], drive_id=source.get('drive_id', ''),
                                    page_size=100, page_token=cursor or '')
    else:
        kwargs = {'space_id': source['space_id']} if provider == 'feishu_wiki' else {}
        response = client.list_page(current['id'], page_size=50 if provider == 'feishu_wiki' else 100,
                                    page_token=cursor or '', **kwargs)
    if response.get('incomplete_search'):
        raise ValueError('Provider returned incompleteSearch.')
    items, next_cursor = response['items'], response.get('next_page_token') or None
    if next_cursor and (next_cursor == cursor or next_cursor in current.get('page_tokens', [])):
        raise ValueError('Provider repeated continuation token.')
    if not isinstance(items, list):
        raise ValueError('Provider returned invalid folder contents.')
    nodes, children = [], []
    for item in items:
        if provider == 'google_drive':
            identifier, name = item.get('name'), item.get('title')
            mime, url, modified = item.get('mime_type'), item.get('web_url'), item.get('modified_time_raw')
        else:
            identifier = item.get('node_token') if provider == 'feishu_wiki' else item.get('id') or item.get('token')
            name = item.get('name') or item.get('title')
            mime, url = item.get('mimeType'), item.get('webViewLink') or item.get('url')
            modified = item.get('modifiedTime') or item.get('modified_time')
        if not identifier:
            raise ValueError('Provider omitted resource identity.')
        name = name or identifier
        path = '/'.join(filter(None, [current['path'], name]))
        kind = ('link' if mime == 'application/vnd.google-apps.shortcut'
                or item.get('node_type') == 'shortcut' or item.get('type') == 'shortcut'
                else 'directory' if provider == 'feishu_wiki' or item.get('type') == 'folder'
                or mime == 'application/vnd.google-apps.folder' else 'file')
        locator = {'id': identifier, 'path': ('/~node/' if provider == 'feishu_wiki' else '/') + identifier}
        if source.get('drive_id'):
            locator['path'] = f"/drive/{source['drive_id']}/{identifier}"
        locator['url'] = url or ''
        if provider == 'feishu_wiki':
            locator['url'] = f'https://feishu.cn/wiki/{identifier}'
        resource = (None if kind == 'directory' and provider != 'feishu_wiki'
                    else candidate(source, identifier, name, locator))
        if resource:
            resource['content_version'] = str(modified or '')
        entry = node(current['key'] + '/' + identifier, current['key'], path, name, kind, resource)
        nodes.append(entry)
        if kind == 'directory':
            if identifier in current.get('ancestors', []):
                raise ValueError('Provider returned a cyclic folder hierarchy.')
            children.append({'key': entry['node_key'], 'path': path, 'id': identifier,
                             'ancestors': [*current.get('ancestors', []), current['id']]})
    return nodes, children, next_cursor, None


class FolderTools:
    def __init__(self, runtime):
        self.runtime = runtime
        self.active = {}

    def scope_observation(self, observation):
        if observation.get('operation') != 'read' or not self.active:
            return [observation]
        # Split mixed-source results; never suppress unrelated discovery observations.
        groups = {}
        for item in observation.get('candidates', []):
            owner = ''
            for inventory_id, source in reversed(list(self.active.items())):
                if item.get('namespace') != source['namespace']:
                    continue
                if source['provider'] == 'local':
                    path = item.get('resource_id', '')
                    try:
                        matches = os.path.commonpath([source['root'], path]) == source['root']
                    except ValueError:
                        matches = False
                else:
                    # Membership is checked by Core against the durable node table.
                    matches = item.get('source') in {'feishu', 'google_drive'}
                if matches:
                    owner = inventory_id
                    break
            groups.setdefault(owner, []).append(item)
        if not groups:
            return [observation]
        return [{**observation, 'call_id': observation['call_id'] + (':' + owner if owner else ''),
                 'inventory_id': owner, 'candidates': items} for owner, items in groups.items()]

    def _cloud(self, provider, account):
        configuration = self.runtime.configuration
        if configuration is None:
            raise ToolExecutionError('Cloud connection configuration unavailable.')
        service = 'googledrive' if provider == 'google_drive' else 'feishu'
        names = [name for name, value in configuration.names.items() if value == service]
        if not names:
            raise ToolExecutionError('Cloud connection unavailable. Connect the requested source first.')
        status = configuration.prepare(names[:1])
        if status:
            raise ToolExecutionError(json.dumps(status, ensure_ascii=False))
        scopes = configuration.tool_config.get('document_directory_scopes', {})
        if isinstance(scopes, str):
            scopes = json.loads(scopes)
        identities = scopes.get(service, [])
        if not identities or (not account and len(identities) != 1) or (account and account not in identities):
            raise ToolExecutionError('Select a connected account; ambiguous or mismatched account identity.')
        selected = account or identities[0]
        tokens = configuration.tool_config.get(service, [])
        tokens = tokens if isinstance(tokens, list) else [tokens]
        if len(tokens) != len(identities) or not tokens[identities.index(selected)]:
            raise ToolExecutionError('Connection identity and credential mapping unavailable.')
        client = cloud_client(provider)
        token = tokens[identities.index(selected)]
        # Bind this client only; never change the request-wide credentials of other tools.
        client.bind_access_token(token)
        return selected, client

    @fc_register(host_file=resolve_folder_access, exclusive=True)
    def inspect_document_folder(self, root: str, provider: str = 'local', inventory_id: str = '',
                                account: str = '', offset: int = 0, continue_scan: bool = True) -> dict:
        """Recursively inventory an explicitly selected folder, including hidden files and empty directories.

        Args:
            root: Authorized absolute local folder path, or cloud folder/Wiki URL or stable ID. Required on resume too.
            provider: local, google_drive, feishu (Drive folder), or feishu_wiki (Wiki subtree).
            inventory_id: Exact ID returned earlier; empty starts a fresh inventory.
            account: Connected cloud account identity when specified; never credentials.
            offset: Offset for browsing persisted inventory pages (100 nodes per page).
            continue_scan: Continue scanning if incomplete; false only browses saved metadata.

        Call until inventory.status is complete before submitting. Browse the returned overview and
        representative files with authorized readers. A failed body read does not remove a file.
        An inaccessible subtree prevents whole-folder submission. Links are collected but never followed.
        This tool does not create a logical directory. Do not claim collection or classification yet.
        """
        if provider not in {'local', 'google_drive', 'feishu', 'feishu_wiki'} or offset < 0:
            raise ValueError('Unsupported provider or page offset.')
        client = None
        if provider == 'local':
            # Revalidate even when invoked directly rather than through the executor.
            root = resolve_folder_access({'root': root, 'provider': provider}).arguments['root']
            namespace = f'{socket.gethostname()}:{os.name}'
        else:
            namespace, client = self._cloud(provider, account)
        if inventory_id:
            saved = self.runtime.request('folder_page', inventory_id=inventory_id, offset=0)
            inv = saved['inventory']
            source = inv['source']
            if source['provider'] != provider or source['root'] != root or source['namespace'] != namespace:
                raise ValueError('Inventory source or account changed; start a new inventory.')
            if provider != 'local':
                _, _, identity, _ = cloud_root(provider, root, client)
                if any(source.get(key) != value for key, value in identity.items()):
                    raise ValueError('Cloud root identity changed; start a new inventory.')
        else:
            source = {'provider': provider, 'root': root, 'namespace': namespace}
            root_item = None
            if provider == 'local':
                with local_directory(root) as fd:
                    info = directory_stat(fd)
                    source['identity'] = [info.st_dev, info.st_ino]
                identifier, name = '', os.path.basename(root) or root
            else:
                identifier, name, extra, root_item = cloud_root(provider, root, client)
                source.update(extra)
            source['name'] = name
            root_node = node('root', '', '', name, 'directory',
                             candidate(source, identifier, name, {'path': '/~node/' + identifier, 'url': root})
                             if root_item else None)
            state = {'queue': [{'key': root_node['node_key'], 'path': '', 'id': identifier}],
                     'errors': [], 'file_types': {root_item.get('obj_type', 'document'): 1} if root_item else {},
                     'started_at': datetime.now(timezone.utc).isoformat()}
            inv = self.runtime.request('folder_begin', source=source, state=state)['inventory']
            inv = self.runtime.request('folder_checkpoint', inventory_id=inv['id'], inventory_version=inv['version'],
                                       nodes=[root_node], state=state, status='scanning')['inventory']
        self.active[inv['id']] = source
        if continue_scan and inv['status'] != 'complete':
            state = inv['state']
            state['errors'] = []
            for _ in range(10):
                if not state['queue']:
                    break
                current = state['queue'][0]
                try:
                    if provider == 'local':
                        with local_directory(root) as fd:
                            info = directory_stat(fd)
                            if [info.st_dev, info.st_ino] != source['identity']:
                                raise ToolExecutionError('Root identity changed; start a new inventory.')
                        nodes, children, cursor, identity = local_page(source, current)
                    else:
                        nodes, children, cursor, identity = cloud_page(source, current, client)
                    if cursor is None:
                        parent_node = {'node_key': current['key'], 'parent_key': '', 'path': current['path'],
                                       'name': current['path'].rsplit('/', 1)[-1] or source['name'],
                                       'kind': 'directory', 'listed': True}
                        nodes.append(parent_node)
                        state['queue'].pop(0)
                    else:
                        current['cursor'] = cursor
                        if provider != 'local':
                            current.setdefault('page_tokens', []).append(cursor)
                        if identity:
                            current['identity'] = identity
                    state['queue'].extend(children)
                except Exception as exc:
                    state['errors'] = [{'path': current['path'], 'error': type(exc).__name__, 'message': str(exc)[:500]}]
                    nodes = []
                status = 'incomplete' if state['errors'] else 'scanning' if state['queue'] else 'complete'
                for entry in nodes:
                    if entry.get('candidate'):
                        file_type = (os.path.splitext(entry['name'])[1].lower()
                                     or ('document' if provider == 'feishu_wiki' else entry['kind']))
                        types = state.setdefault('file_types', {})
                        types[file_type] = types.get(file_type, 0) + 1
                if status == 'complete':
                    state['finished_at'] = datetime.now(timezone.utc).isoformat()
                inv = self.runtime.request('folder_checkpoint', inventory_id=inv['id'], inventory_version=inv['version'],
                                           nodes=nodes, state=state, status=status)['inventory']
                if status != 'scanning':
                    break
        result = self.runtime.request('folder_page', inventory_id=inv['id'], offset=offset)
        # Traversal queue is an internal checkpoint, not model context.
        result['inventory']['state'] = {k: v for k, v in result['inventory']['state'].items() if k != 'queue'}
        result.update(stage='inventory_only', logical_directory_created=False,
                      notice='One-time accessible inventory. Links are retained without following targets.')
        return result

    @fc_register(host_file='NONE', exclusive=True)
    def submit_document_directory_organization(self, inventory_id: str, name: str,
                                               organization: str = '按资料类型组织', summary: str = '') -> dict:
        """Submit a complete inspected folder inventory for deterministic collection and background classification.

        Args:
            inventory_id: Exact complete inventory returned by inspect_document_folder.
            name: User-facing logical directory name.
            organization: Default 按资料类型组织. Change only for an explicit user rule; retaining source structure
                does not change this default. Do not enumerate file assignments.
            summary: Brief understanding from actual metadata and sampled reads, including reading limitations.

        All references are retained, even unread or unclassified. The new directory is pinned for retention,
        auto-add is disabled, and the same inventory submits only once. Respect receipt states: pending
        collection is not collected, and pending classification is not classified.
        """
        if inventory_id not in self.active:
            raise ValueError('Inspect this inventory in the current run before submitting it.')
        self.runtime.flush()
        return self.runtime.request('folder_submit', inventory_id=inventory_id, name=name,
                                    organization=organization, instruction=summary)
