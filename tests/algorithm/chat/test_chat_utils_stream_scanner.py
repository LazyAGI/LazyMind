from types import SimpleNamespace

from chat.utils.stream_scanner import (
    CitationPlugin,
    ImagePlugin,
    IncrementalScanner,
    MarkdownImageHoldPlugin,
)


def test_citation_plugin_collects_sources_and_rewrites_images():
    node = SimpleNamespace(
        text='See image ![fig](fig.png)',
        metadata={'images': ['https://cdn.example.com/path/fig.png'], 'page': 3},
        global_metadata={'file_name': 'Doc.md', 'docid': 'doc-1', 'kb_id': 'kb-1'},
        _uid='uid-1',
        _group='group-a',
    )
    plugin = CitationPlugin({1: node})

    end, replacement = plugin.match('ref [[1]] tail', 4)

    assert replacement == '[1](#source "Doc.md")'
    assert end == 9
    assert plugin.collect() == [{
        'index': 1,
        'segment_number': -1,
        'document_id': 'doc-1',
        'page': 3,
        'bbox': [],
        'dataset_id': 'kb-1',
        'file_name': 'Doc.md',
        'segement_id': 'uid-1',
        'content': 'See image ![fig](https://cdn.example.com/path/fig.png)',
        'group_name': 'group-a',
    }]


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
    refs = {
        1: SimpleNamespace(
            text='source text',
            metadata={},
            global_metadata={'file_name': 'Source.md', 'docid': 'doc-1', 'kb_id': 'kb-1'},
            _uid='uid-1',
            _group='group-a',
        )
    }
    scanner = IncrementalScanner([CitationPlugin(refs)], initial_state='BODY')

    first = scanner.feed('hello <thi')
    second = scanner.feed('nk>plan</think> cite [[1]]')
    tail = scanner.flush()

    assert first == [('text', 'hello ')]
    assert second == [
        ('think', 'plan'),
        ('text', ' cite '),
        ('text', '[1](#source "Source.md")'),
    ]
    assert tail == []
