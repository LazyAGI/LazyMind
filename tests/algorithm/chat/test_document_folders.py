import copy
import os
import subprocess

import pytest

from lazymind.directory.folders import FolderTools, cloud_page, cloud_root, node_key, resolve_folder_access
from lazymind.chat.engine.tools.workspace_context import WorkspaceContext, workspace_permission_scope


class InventoryStore:
    """Only persistence is substituted; enumeration and permission declarations are real."""
    configuration = None

    def __init__(self):
        self.nodes = {}
        self.inventory = None
        self.submits = 0

    def flush(self):
        pass

    def request(self, operation, **data):
        if operation == 'folder_begin':
            self.inventory = {'id': 'inventory', 'source': data['source'], 'state': data['state'],
                              'version': 1, 'status': 'scanning'}
        elif operation == 'folder_checkpoint':
            assert data['inventory_version'] == self.inventory['version']
            for item in data['nodes']:
                old = self.nodes.get(item['node_key'])
                if old and item['listed']:
                    old['listed'] = True
                else:
                    self.nodes[item['node_key']] = copy.deepcopy(item)
            self.inventory.update(state=data['state'], status=data['status'], version=self.inventory['version'] + 1)
        elif operation == 'folder_submit':
            if self.inventory['status'] != 'complete':
                raise ValueError('inventory_incomplete')
            self.submits += 1
            return {'collection_state': 'pending', 'classification_state': 'pending'}
        result = {'inventory': copy.deepcopy(self.inventory)}
        if operation == 'folder_page':
            rows = sorted(self.nodes.values(), key=lambda n: n['path'])
            offset = data.get('offset', 0)
            result.update(nodes=copy.deepcopy(rows[offset:offset + 100]), total=len(rows))
        return result


def trusted():
    return workspace_permission_scope(WorkspaceContext(trusted_local=True))


def test_recursive_inventory_retains_all_files_and_empty_directories(tmp_path):
    for directory in ['a', 'a/deep', 'b', '.hidden', 'empty']:
        (tmp_path / directory).mkdir(parents=True, exist_ok=True)
    for i in range(106):
        (tmp_path / 'a' / f'{i}.txt').write_text('notes')
    for path in ['a/deep/same.txt', 'b/same.txt', '.hidden/private.txt', '.gitignore', 'binary.bin']:
        (tmp_path / path).write_bytes(b'\xff\x00')
    (tmp_path / 'link').symlink_to(tmp_path / 'a', target_is_directory=True)
    if os.name == 'nt':
        subprocess.run(['attrib', '+H', str(tmp_path / '.hidden')], check=True, capture_output=True)
    store = InventoryStore()
    with trusted():
        tools = FolderTools(store)
        result = tools.inspect_document_folder(str(tmp_path))
        while result['inventory']['status'] != 'complete':
            result = tools.inspect_document_folder(str(tmp_path), inventory_id='inventory')
        paths = {n['path'] for n in store.nodes.values() if n['kind'] != 'directory'}
        assert len(paths) == 112
        assert 'a/deep/same.txt' in paths and '.hidden/private.txt' in paths and 'link' in paths
        assert not any(path.startswith('link/') for path in paths)
        assert store.nodes[node_key('empty')]['listed']
        assert result['logical_directory_created'] is False
        receipt = tools.submit_document_directory_organization('inventory', 'test')
        assert receipt['classification_state'] == 'pending'
    assert (tmp_path / 'binary.bin').read_bytes() == b'\xff\x00'


def test_inventory_denial_then_resume(monkeypatch, tmp_path):
    import lazymind.directory.folders as module
    (tmp_path / 'restricted').mkdir()
    (tmp_path / 'restricted' / 'file.txt').write_text('keep')
    original = module.local_page

    def denied(source, current):
        if current['path'] == 'restricted':
            raise PermissionError('permission denied')
        return original(source, current)
    monkeypatch.setattr(module, 'local_page', denied)
    store = InventoryStore()
    with trusted():
        tools = FolderTools(store)
        result = tools.inspect_document_folder(str(tmp_path))
        assert result['inventory']['status'] == 'incomplete'
        with pytest.raises(ValueError, match='inventory_incomplete'):
            tools.submit_document_directory_organization('inventory', 'test')
        monkeypatch.setattr(module, 'local_page', original)
        # Resuming a different process/runtime uses the saved queue, not memory.
        tools = FolderTools(store)
        result = tools.inspect_document_folder(str(tmp_path), inventory_id='inventory')
        assert result['inventory']['status'] == 'complete'
        assert any(n['path'] == 'restricted/file.txt' for n in store.nodes.values())


@pytest.mark.skipif(os.name != 'nt', reason='requires native Windows drive roots')
def test_windows_drive_root_can_be_pinned(tmp_path):
    from lazymind.directory.folders import directory_stat, local_directory
    with local_directory(tmp_path.anchor) as directory:
        assert directory_stat(directory).st_dev == tmp_path.stat().st_dev
        assert os.path.isdir(directory)


@pytest.mark.skipif(os.name != 'nt', reason='requires native Windows handles and junctions')
def test_windows_junction_is_retained_but_never_traversed(tmp_path):
    root, outside = tmp_path / '资料', tmp_path / 'outside'
    root.mkdir()
    outside.mkdir()
    (outside / 'private.txt').write_text('outside')
    junction = root / 'linked'
    subprocess.run(['cmd', '/c', 'mklink', '/J', str(junction), str(outside)], check=True, capture_output=True)
    try:
        store = InventoryStore()
        with trusted():
            result = FolderTools(store).inspect_document_folder(str(root))
            assert result['inventory']['status'] == 'complete'
            assert store.nodes[node_key('linked')]['kind'] == 'link'
            assert not any('private.txt' in n['path'] for n in store.nodes.values())
            with pytest.raises(Exception, match='path_invalid'):
                FolderTools(InventoryStore()).inspect_document_folder(str(junction))
    finally:
        os.rmdir(junction)


@pytest.mark.skipif(os.name != 'nt', reason='requires native Windows directory sharing')
def test_windows_inventory_holds_ancestors_until_page_finishes(tmp_path):
    from lazymind.directory.folders import local_directory
    parent = tmp_path / 'parent'
    child = parent / 'child'
    child.mkdir(parents=True)
    with local_directory(str(child)) as directory:
        assert os.path.isdir(directory)
        with pytest.raises(OSError):
            os.rename(parent, tmp_path / 'renamed')
    os.rename(parent, tmp_path / 'renamed')
    assert (tmp_path / 'renamed' / 'child').is_dir()


@pytest.mark.skipif(os.name != 'nt', reason='requires native Windows directory identity')
def test_windows_changed_directory_cannot_resume_page(tmp_path):
    from lazymind.directory.folders import local_page
    for i in range(101):
        (tmp_path / f'{i}.txt').write_text('data')
    source = {'root': str(tmp_path), 'provider': 'local', 'namespace': 'windows'}
    current = {'path': '', 'key': 'root'}
    _, _, cursor, identity = local_page(source, current)
    (tmp_path / 'new.txt').write_text('new')
    with pytest.raises(Exception, match='changed during inventory'):
        local_page(source, dict(current, cursor=cursor, identity=identity))


@pytest.mark.skipif(os.name != 'nt', reason='requires native Windows access control')
def test_windows_denied_subtree_prevents_complete_inventory(tmp_path):
    import getpass
    denied = tmp_path / 'denied'
    denied.mkdir()
    (denied / 'private.txt').write_text('data')
    user = getpass.getuser()
    subprocess.run(['icacls', str(denied), '/deny', f'{user}:(RD)'], check=True, capture_output=True)
    try:
        with trusted():
            store = InventoryStore()
            tools = FolderTools(store)
            assert tools.inspect_document_folder(str(tmp_path))['inventory']['status'] == 'incomplete'
            with pytest.raises(ValueError, match='inventory_incomplete'):
                tools.submit_document_directory_organization('inventory', 'denied')
    finally:
        subprocess.run(['icacls', str(denied), '/remove:d', user], check=True, capture_output=True)


@pytest.mark.skipif(os.name != 'nt', reason='requires native Windows traversal ACLs')
def test_windows_known_child_does_not_require_listing_ancestors(tmp_path):
    import getpass
    parent = tmp_path / 'parent'
    child = parent / 'child'
    child.mkdir(parents=True)
    (child / 'allowed.txt').write_text('data')
    user = getpass.getuser()
    # No inheritance flags: only the parent loses directory-list permission.
    subprocess.run(['icacls', str(parent), '/deny', f'{user}:(RD)'], check=True, capture_output=True)
    try:
        with trusted():
            store = InventoryStore()
            result = FolderTools(store).inspect_document_folder(str(child))
            assert result['inventory']['status'] == 'complete'
            assert store.nodes[node_key('allowed.txt')]['kind'] == 'file'
    finally:
        subprocess.run(['icacls', str(parent), '/remove:d', user], check=True, capture_output=True)


def test_inventory_declares_host_access_and_rejects_unbound_cloud_runtime(tmp_path):
    with trusted():
        resolved = resolve_folder_access({'provider': 'local', 'root': str(tmp_path)})
        assert resolved.files[0].path == str(tmp_path) and resolved.files[0].operation == 'read'
    with workspace_permission_scope(WorkspaceContext(local_runtime=False)):
        with pytest.raises(Exception, match='authorized local workspace'):
            resolve_folder_access({'provider': 'local', 'root': str(tmp_path)})


@pytest.mark.parametrize('provider', ['google_drive', 'feishu', 'feishu_wiki'])
def test_cloud_pages_keep_stable_ids_and_wiki_documents(provider):
    payload = ({'files': [{'id': 'f1', 'name': 'same', 'mimeType': 'application/vnd.google-apps.folder'},
                          {'id': 'f2', 'name': 'same', 'mimeType': 'application/pdf'}], 'nextPageToken': 'next'}
               if provider == 'google_drive' else
               {'data': {'items' if provider == 'feishu_wiki' else 'files': [
                   {'node_token': 'f1', 'token': 'f1', 'title': 'same', 'name': 'same', 'type': 'folder'},
                   {'node_token': 'f2', 'token': 'f2', 'title': 'same', 'name': 'same', 'type': 'docx'}],
                   'has_more': True, 'page_token': 'next'}})

    def get_page(*args, **kwargs):
        if provider == 'feishu_wiki':
            assert kwargs['params']['page_size'] <= 50
        return payload
    if provider == 'google_drive':
        from lazyllm.tools.fs.supplier.googledrive import GoogleDriveFS
        client = GoogleDriveFS(dynamic_auth=True, skip_instance_cache=True)
        client._get = get_page
    else:
        from lazyllm.tools.fs.supplier.feishu import FeishuFS, FeishuWikiFS
        client = (FeishuWikiFS if provider == 'feishu_wiki' else FeishuFS)(dynamic_auth=True, skip_instance_cache=True)
        client._get = get_page
    source = {'provider': provider, 'namespace': 'account', 'space_id': 'space'}
    nodes, children, cursor, _ = cloud_page(source, {'key': 'root', 'path': '', 'id': 'root'}, client)
    assert cursor == 'next' and len(nodes) == 2 and nodes[0]['node_key'] != nodes[1]['node_key']
    if provider == 'feishu_wiki':
        assert len(children) == 2 and all(n['candidate'] for n in nodes)
    payload.clear()
    payload.update({'incompleteSearch': True} if provider == 'google_drive' else {'data': {'has_more': True}})
    with pytest.raises(ValueError):
        cloud_page(source, {'key': 'root', 'path': '', 'id': 'root'}, client)
    if provider != 'google_drive':
        payload.clear()
        payload.update({'code': 99991672, 'msg': 'Access denied'})
        with pytest.raises(ValueError, match='rejected enumeration'):
            cloud_page(source, {'key': 'root', 'path': '', 'id': 'root'}, client)


@pytest.mark.parametrize('failure', ['', 'incomplete', 'repeated_token', 'token_cycle', 'malformed', 'missing_id'])
def test_google_supplier_pagination_traversal_and_resume(monkeypatch, failure):
    from lazyllm.tools.fs.supplier.googledrive import GoogleDriveFS

    client = GoogleDriveFS(dynamic_auth=True, skip_instance_cache=True)
    requests = []
    timestamp = '2026-10-16T00:00:00.123Z'

    def get(url, params):
        if url.endswith('/files/root'):
            return {'name': 'Root', 'mimeType': 'application/vnd.google-apps.folder', 'driveId': 'drive'}
        assert params['driveId'] == 'drive' and params['corpora'] == 'drive'
        token = params.get('pageToken', '')
        requests.append((params['q'], token))
        if "'child' in parents" in params['q']:
            return {'files': [{'id': 'nested', 'name': 'nested.txt', 'modifiedTime': timestamp}]}
        if not token:
            return {'files': [
                {'id': 'child', 'name': 'Child', 'mimeType': 'application/vnd.google-apps.folder'},
                {'id': 'shortcut', 'name': 'Shortcut', 'mimeType': 'application/vnd.google-apps.shortcut'},
            ], 'nextPageToken': 'next'}
        if failure == 'incomplete':
            return {'files': [], 'incompleteSearch': True}
        if failure == 'repeated_token':
            return {'files': [], 'nextPageToken': token}
        if failure == 'token_cycle':
            return {'files': [], 'nextPageToken': 'second' if token == 'next' else 'next'}
        if failure == 'malformed':
            return {'files': {}}
        if failure == 'missing_id':
            return {'files': [{'name': 'Unknown file'}]}
        return {'files': [{'id': 'last', 'name': 'Last.txt', 'modifiedTime': timestamp}]}

    monkeypatch.setattr(client, '_get', get)
    monkeypatch.setattr(FolderTools, '_cloud', lambda *a, **kw: ('account', client))
    store = InventoryStore()
    tools = FolderTools(store)
    with trusted():
        result = tools.inspect_document_folder('root', provider='google_drive')
        if failure:
            assert result['inventory']['status'] == 'incomplete'
            assert store.inventory['state']['queue']
            assert any(not n['listed'] for n in store.nodes.values() if n['kind'] == 'directory')
            failure = ''
            result = FolderTools(store).inspect_document_folder(
                'root', provider='google_drive', inventory_id='inventory')
        assert result['inventory']['status'] == 'complete'
    resources = [n for n in store.nodes.values() if n.get('candidate')]
    assert {n['candidate']['resource_id'] for n in resources} == {'shortcut', 'nested', 'last'}
    assert next(n for n in resources if n['name'] == 'Shortcut')['kind'] == 'link'
    assert not any("'shortcut' in parents" in q for q, _ in requests)
    for item in resources:
        if item['name'] != 'Shortcut':
            assert item['candidate']['content_version'] == timestamp
        assert item['candidate']['locator']['path'].startswith('/drive/drive/')


@pytest.mark.parametrize('space_id', ['space', ''])
def test_wiki_root_resolution_retains_root_document(monkeypatch, space_id):
    from lazyllm.tools.fs.supplier.feishu import FeishuWikiFS

    client = FeishuWikiFS(dynamic_auth=True, skip_instance_cache=True)
    requests = []

    def get(url, params):
        requests.append((url, params))
        if url.endswith('/wiki/v2/spaces/get_node'):
            assert params == {'token': 'root'}
            return {'code': 0, 'data': {'node': {
                'node_token': 'root', 'space_id': space_id, 'title': 'Root document',
                'obj_type': 'docx', 'obj_token': 'doc-root', 'has_child': False,
            }}}
        assert url.endswith('/wiki/v2/spaces/space/nodes')
        assert params['parent_node_token'] == 'root'
        return {'code': 0, 'data': {'items': [], 'has_more': False}}

    monkeypatch.setattr(client, '_get', get)
    monkeypatch.setattr(FolderTools, '_cloud', lambda *a, **kw: ('account', client))
    if not space_id:
        with pytest.raises(ValueError, match='Wiki space identity unavailable'):
            cloud_root('feishu_wiki', 'root', client)
        return
    store = InventoryStore()
    with trusted():
        result = FolderTools(store).inspect_document_folder(
            'https://example.feishu.cn/wiki/root', provider='feishu_wiki')
    assert result['inventory']['status'] == 'complete'
    assert result['inventory']['source']['space_id'] == 'space'
    root = store.nodes[node_key('root')]
    assert root['name'] == 'Root document'
    assert root['candidate']['resource_id'] == 'root'
    assert root['candidate']['locator']['path'] == '/~node/root'
    assert len(requests) == 2
