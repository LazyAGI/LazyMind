import lazymind.chat.service.utils.static_file_url as sfu_mod

from lazymind.chat.service.utils.static_file_url import (
    _upload_root,
    basename_from_path,
    local_path_from_static_file_url,
    resolve_local_image_path,
    static_file_url_from_any,
    static_file_url_from_full_path,
)


def _mock_upload_root(monkeypatch, upload_root):
    monkeypatch.setattr(sfu_mod, '_upload_root', lambda: str(upload_root.resolve()))


def test_upload_root_prefers_env_over_config_default(tmp_path, monkeypatch):
    upload_root = tmp_path / 'local-uploads'
    upload_root.mkdir()
    monkeypatch.setenv('LAZYMIND_UPLOAD_ROOT', str(upload_root))
    monkeypatch.delenv('LAZYMIND_SHARED_UPLOAD_DIR', raising=False)
    assert _upload_root() == str(upload_root.resolve())


def test_local_path_from_marker_uses_env_upload_root(tmp_path, monkeypatch):
    upload_root = tmp_path / 'local-uploads'
    image = upload_root / 'tmp' / 'users' / 'u1' / 'dog.jpg'
    image.parent.mkdir(parents=True)
    image.write_bytes(b'jpg')
    monkeypatch.setenv('LAZYMIND_UPLOAD_ROOT', str(upload_root))
    monkeypatch.delenv('LAZYMIND_SHARED_UPLOAD_DIR', raising=False)

    remapped = local_path_from_static_file_url(
        '/var/lib/lazymind/uploads/tmp/users/u1/dog.jpg'
    )
    assert remapped == str(image.resolve())


def test_static_file_url_from_full_path_signs_upload_relative_path(tmp_path, monkeypatch):
    upload_root = tmp_path / 'uploads'
    image = upload_root / 'normalized_images' / 'exp9' / 'frame.jpg'
    image.parent.mkdir(parents=True)
    image.write_bytes(b'jpg')

    _mock_upload_root(monkeypatch, upload_root)
    monkeypatch.setenv('LAZYMIND_FILE_URL_SIGN_SECRET', 'test-secret')

    signed = static_file_url_from_full_path(str(image))
    assert signed.startswith('/static-files/normalized_images/exp9/frame.jpg?')
    assert 'expires=' in signed
    assert 'sig=' in signed


def test_static_file_url_from_any_strips_external_host_prefix(tmp_path, monkeypatch):
    upload_root = tmp_path / 'uploads'
    image = upload_root / 'normalized_images' / 'exp9' / 'frame.jpg'
    image.parent.mkdir(parents=True)
    image.write_bytes(b'jpg')

    _mock_upload_root(monkeypatch, upload_root)
    monkeypatch.setenv('LAZYMIND_FILE_URL_SIGN_SECRET', 'test-secret')

    raw = (
        'https://ext.lazymind.ai:19537/var/lib/lazymind/uploads/'
        'normalized_images/exp9/frame.jpg'
    )
    signed = static_file_url_from_any(raw)
    assert signed.startswith('/static-files/normalized_images/exp9/frame.jpg?')

    local = local_path_from_static_file_url(signed)
    assert local == str(image.resolve())
    assert resolve_local_image_path(signed) == str(image.resolve())


def test_basename_from_path_handles_urls_and_paths():
    assert basename_from_path('https://example.test/assets/chart.png?token=1') == 'chart.png'
    assert basename_from_path('/tmp/chart.png') == 'chart.png'
    assert basename_from_path('./nested/chart.png') == 'chart.png'
    assert basename_from_path(r'C:\Users\test\chart.png') == 'chart.png'
    assert basename_from_path('/static-files/%E6%88%AA%E5%B1%8F%202026-06-25%2010.18.13.png?token=1') == (
        '截屏 2026-06-25 10.18.13.png'
    )


def test_static_file_url_rejects_decoded_path_traversal(tmp_path, monkeypatch):
    upload_root = tmp_path / 'uploads'
    upload_root.mkdir()
    _mock_upload_root(monkeypatch, upload_root)

    assert local_path_from_static_file_url('/static-files/../secret.txt') == ''
    assert local_path_from_static_file_url('/static-files/%2e%2e/secret.txt') == ''
    assert local_path_from_static_file_url(r'/static-files/..\secret.txt') == ''
