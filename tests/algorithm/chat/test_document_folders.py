import copy
from types import SimpleNamespace

import pytest

from lazymind.directory.folders import FolderTools, cloud_page, node_key, resolve_folder_access
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
    client = SimpleNamespace(_base_url='https://example.invalid', _get=get_page)
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
