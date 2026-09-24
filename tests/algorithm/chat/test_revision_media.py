import pytest

from lazymind.document_tools.revision import finalize_markdown_revision


def test_revision_media_binds_each_need_and_preserves_existing_images(tmp_path):
    paths = [tmp_path / 'first.png', tmp_path / 'second.png']
    for path in paths:
        path.write_bytes(b'image')
    library = {
        'assets': {str(i): {'local_path': str(path)} for i, path in enumerate(paths)},
        'visual_need_asset_ids': {'first': ['0'], 'second': ['1']},
    }
    source = '# Title\n\n![existing](docs/original.png)'
    markdown = source + '\n\n![A](media-placeholder://first)\n\n![B](media-placeholder://second)'
    result = finalize_markdown_revision(markdown, library, source=source)
    assert result == source + f'\n\n![A]({paths[0].as_posix()})\n\n![B]({paths[1].as_posix()})'
    for invalid in (
        markdown.replace('media-placeholder://first', 'docs/invented.png'),
        markdown.replace('media-placeholder://first', 'media-placeholder://wrong'),
        source,
    ):
        with pytest.raises(ValueError):
            finalize_markdown_revision(invalid, library, source=source)
    paths[0].unlink()
    with pytest.raises(ValueError, match='unavailable: first'):
        finalize_markdown_revision(markdown, library, source=source)


def test_revision_without_media_preserves_existing_images_and_rejects_new_paths():
    source = '![original](relative.png)\n\n```md\n![example](example.png)\n```'
    assert finalize_markdown_revision(source, source=source) == source
    with pytest.raises(ValueError, match='Unregistered'):
        finalize_markdown_revision(source + '\n\n![new](invented.png)', source=source)
    with pytest.raises(ValueError, match='unavailable'):
        finalize_markdown_revision('![new](media-placeholder://missing)')


def test_revision_media_accepts_percent_encoded_whitespace_path(tmp_path):
    image = tmp_path / 'Application Support' / 'image.png'
    image.parent.mkdir()
    image.write_bytes(b'image')
    library = {
        'assets': {'asset-1': {'local_path': str(image)}},
        'visual_need_asset_ids': {'IMAGE-1': ['asset-1']},
    }

    result = finalize_markdown_revision(
        '![Generated](media-placeholder://IMAGE-1)',
        library,
    )

    assert result == f'![Generated](<{image.as_posix()}>)'


def test_revision_media_accepts_windows_normalized_percent_encoded_path():
    from lazymind.document_tools import writing as document_writing

    local_path = r'C:\Users\test\Application Support\image.png'
    markdown = '![Generated](C:/Users/test/Application%20Support/image.png)'
    media_assets = {
        'assets': {
            'asset-1': {
                'local_path': local_path,
            },
        },
        'visual_need_asset_ids': {
            'IMAGE-1': ['asset-1'],
        },
    }

    assert finalize_markdown_revision(markdown, media_assets) == markdown
    assert document_writing.drop_unregistered_markdown_images(
        markdown,
        media_assets,
    ) == markdown
