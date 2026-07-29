import lazyllm

from lazymind.chat.engine.subagent import tools


def test_find_user_attachment_accepts_remote_url_without_local_file():
    old_config = lazyllm.globals.get('agentic_config')
    try:
        lazyllm.globals['agentic_config'] = {
            'files': ['https://filecdn-images.xingyeai.com/tool/edit_images/image_0_bear.png'],
            'history_files_per_turn': {
                '1': ['https://filecdn-images.xingyeai.com/tool/edit_images/image_0_bear.png'],
            },
        }
        result = tools.find_user_attachment('image_0_bear.png', turn=1)
    finally:
        lazyllm.globals['agentic_config'] = old_config or {}

    assert result['success'] is True
    payload = result['result']
    assert payload['status'] == 'ok'
    assert payload['filename'] == 'image_0_bear.png'
    assert payload['url'] == 'https://filecdn-images.xingyeai.com/tool/edit_images/image_0_bear.png'


def test_find_user_attachment_remaps_docker_upload_marker(tmp_path, monkeypatch):
    upload_root = tmp_path / 'uploads'
    image = upload_root / 'tmp' / 'users' / 'u1' / 'files' / 'upload_a' / 'dog.jpg'
    image.parent.mkdir(parents=True)
    image.write_bytes(b'jpg')

    docker_path = '/var/lib/lazymind/uploads/tmp/users/u1/files/upload_a/dog.jpg'
    monkeypatch.setenv('LAZYMIND_UPLOAD_ROOT', str(upload_root))
    monkeypatch.setattr(tools, '_sign_static_file_url', lambda path: f'/static-files/signed?path={path}')

    old_config = lazyllm.globals.get('agentic_config')
    try:
        lazyllm.globals['agentic_config'] = {
            'files': [docker_path],
            'history_files_per_turn': {'1': [docker_path]},
        }
        result = tools.find_user_attachment('dog.jpg', turn=1)
    finally:
        lazyllm.globals['agentic_config'] = old_config or {}

    assert result['success'] is True
    payload = result['result']
    assert payload['status'] == 'ok'
    assert payload['path'] == str(image.resolve())
    assert payload['url'].startswith('/static-files/signed?')


def test_sign_static_file_url_skips_remote_and_empty():
    assert tools._sign_static_file_url('') is None
    assert tools._sign_static_file_url('https://example.com/a.jpg') is None
    assert tools._sign_static_file_url('/static-files/a.jpg?expires=1&sig=x') == (
        '/static-files/a.jpg?expires=1&sig=x'
    )


def test_sign_static_file_url_posts_paths_array(monkeypatch):
    calls = {}

    class _Resp:
        status_code = 200

        @staticmethod
        def json():
            return {'urls': {'/tmp/a.jpg': '/static-files/a.jpg?expires=1&sig=x'}}

    class _Httpx:
        @staticmethod
        def post(url, json=None, timeout=None):
            calls['url'] = url
            calls['json'] = json
            return _Resp()

    class _Cfg:
        def __getitem__(self, key):
            if key == 'core_api_url':
                return 'http://core:8000'
            raise KeyError(key)

    import importlib
    import sys

    cfg_mod = importlib.import_module('lazymind.config')
    monkeypatch.setitem(sys.modules, 'httpx', _Httpx)
    monkeypatch.setattr(cfg_mod, 'config', _Cfg(), raising=False)

    signed = tools._sign_static_file_url('/tmp/a.jpg')
    assert signed == '/static-files/a.jpg?expires=1&sig=x'
    assert calls['json'] == {'paths': ['/tmp/a.jpg']}
