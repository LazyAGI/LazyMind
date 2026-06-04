import lazymind.chat.service.utils.static_file_url as sfu_mod

from lazymind.chat.service.utils.static_file_url import (
    basename_from_path,
    local_path_from_static_file_url,
    resolve_local_image_path,
    static_file_url_from_any,
    static_file_url_from_full_path,
)


def _mock_upload_root(monkeypatch, upload_root):
    monkeypatch.setattr(sfu_mod, '_upload_root', lambda: str(upload_root.resolve()))


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
