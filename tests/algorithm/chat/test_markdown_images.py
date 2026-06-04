from lazymind.chat.service.utils.markdown_images import rewrite_markdown_image_urls
from lazymind.chat.service.utils.static_file_url import static_file_url_from_full_path


def test_rewrite_markdown_image_urls_replaces_hallucinated_host(tmp_path, monkeypatch):
    upload_root = tmp_path / 'uploads'
    image = upload_root / 'normalized_images' / 'exp9' / 'frame.jpg'
    image.parent.mkdir(parents=True)
    image.write_bytes(b'jpg')

    monkeypatch.setenv('LAZYMIND_UPLOAD_ROOT', str(upload_root))
    monkeypatch.setenv('LAZYMIND_FILE_URL_SIGN_SECRET', 'test-secret')

    raw_url = (
        'https://ext.lazymind.ai:19537/var/lib/lazymind/uploads/'
        'normalized_images/exp9/frame.jpg'
    )
    markdown = f'![frame]({raw_url})'
    expected = static_file_url_from_full_path(str(image))
    rewritten = rewrite_markdown_image_urls(
        markdown,
        url_map={'frame.jpg': expected},
    )
    assert expected in rewritten
    assert 'ext.lazymind.ai' not in rewritten
