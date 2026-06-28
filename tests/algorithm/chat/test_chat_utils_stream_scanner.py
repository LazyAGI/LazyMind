from types import SimpleNamespace

from lazymind.chat.service.utils.citations import ConfigCitationPlugin, CITATION_REFS_KEY
from lazymind.chat.service.utils.stream_scanner import (
    ImagePlugin,
    IncrementalScanner,
    MarkdownImageHoldPlugin,
)


def test_image_plugin_matches_exact_and_fuzzy_urls():
    plugin = ImagePlugin(
        {
            'chart-final.png': 'https://cdn.example.com/chart-final.png',
        }
    )

    _, exact = plugin.match('![alt](chart-final.png)', 0)
    _, fuzzy = plugin.match('![alt](chart-final-v2.png)', 0)

    assert exact == '![alt](https://cdn.example.com/chart-final.png)'
    assert fuzzy == '![alt](https://cdn.example.com/chart-final.png)'


def test_markdown_image_hold_plugin_keeps_partial_image_across_chunks():
    scanner = IncrementalScanner([MarkdownImageHoldPlugin()], initial_state='BODY')

    first = scanner.feed('intro ![dog](/static-files/path/dog.jpg?sig=abc')
    second = scanner.feed('def)\n\ntail')
    tail = scanner.flush()

    assert first == [('text', 'intro ')]
    assert second == [
        ('text', '![dog](/static-files/path/dog.jpg?sig=abcdef)\n\ntail'),
    ]
    assert tail == []


def test_incremental_scanner_handles_partial_think_tags_and_plugins():
    config = {
        CITATION_REFS_KEY: {
            '1.1': {
                'file_name': 'Source.md',
            },
        },
    }
    scanner = IncrementalScanner([ConfigCitationPlugin(config)], initial_state='BODY')

    first = scanner.feed('hello <thi')
    second = scanner.feed('nk>plan</think> cite [[1.1]]')
    tail = scanner.flush()

    assert first == [('text', 'hello ')]
    assert second == [
        ('think', 'plan'),
        ('text', ' cite '),
        ('text', '[1](#source-1.1 "Source.md")'),
    ]
    assert tail == []


def test_citation_plugin_display_numbers_start_from_first_streamed_source():
    config = {
        CITATION_REFS_KEY: {
            '3.1': {'file_name': 'Third.md', 'index': '3.1'},
            '3.2': {'file_name': 'Third.md', 'index': '3.2'},
            '5.1': {'file_name': 'Fifth.md', 'index': '5.1'},
        },
    }
    plugin = ConfigCitationPlugin(config)
    scanner = IncrementalScanner([plugin], initial_state='BODY')

    segments = scanner.feed('cite [[3.1]] then [[3.2]] and [5](#source-5.1 "Fifth.md")')

    assert segments == [
        ('text', 'cite '),
        ('text', '[1](#source-3.1 "Third.md")'),
        ('text', ' then '),
        ('text', '[1](#source-3.2 "Third.md")'),
        ('text', ' and '),
        ('text', '[2](#source-5.1 "Fifth.md")'),
    ]
    assert plugin.collect()[0]['index'] == '3.1'
    assert plugin.collect()[0]['display_index'] == 1
    assert plugin.collect()[2]['index'] == '5.1'
    assert plugin.collect()[2]['display_index'] == 2
