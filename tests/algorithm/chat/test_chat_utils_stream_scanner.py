from types import SimpleNamespace

import pytest

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
    assert plugin.streamed_indices == ('3.1', '3.2', '5.1')


def test_citation_plugin_records_every_output_occurrence_including_unknown_links():
    config = {
        CITATION_REFS_KEY: {
            '1.1': {'file_name': 'Source.md'},
        },
    }
    plugin = ConfigCitationPlugin(config)
    scanner = IncrementalScanner([plugin], initial_state='BODY')

    scanner.feed(
        'first [[1.1]], repeated [[1.1]], '
        'and [9](#source-9.1 "Not registered yet")',
    )

    assert plugin.streamed_indices == ('1.1', '1.1', '9.1')


def _source_scanner_with_plugin():
    config = {
        CITATION_REFS_KEY: {
            '1.1': {
                'file_name': 'Source.md',
            },
        },
    }
    plugin = ConfigCitationPlugin(config)
    return IncrementalScanner([plugin], initial_state='BODY'), plugin


def _source_scanner():
    scanner, _ = _source_scanner_with_plugin()
    return scanner


def test_incremental_scanner_does_not_rewrite_citations_in_inline_code():
    scanner = _source_scanner()
    text = ''.join(part for _, part in scanner.feed('see `[[1.1]]` done'))
    assert text == 'see `[[1.1]]` done'


def test_incremental_scanner_does_not_rewrite_citations_in_fenced_code():
    scanner = _source_scanner()
    text = ''.join(part for _, part in scanner.feed('```python\n[[1.1]]\n```\n'))
    assert '[[1.1]]' in text
    assert '#source-' not in text


def test_incremental_scanner_strips_citations_inside_editable_fences():
    scanner, plugin = _source_scanner_with_plugin()
    chunks = ['```edit', 'able\nDraft [[1.', '1]] and [1](#source-1.1 "Source.md")\n```\nsee [[1.1]]']
    text = ''.join(
        part
        for chunk in chunks
        for _, part in scanner.feed(chunk)
    )
    text += ''.join(part for _, part in scanner.flush())

    assert '```editable\nDraft  and \n```' in text
    assert '[[1.1]]' not in text.split('```')[1]
    assert text.endswith('[1](#source-1.1 "Source.md")')
    assert plugin.streamed_indices == ('1.1',)
    assert plugin.collect()[0]['index'] == '1.1'


def test_incremental_scanner_rewrites_citations_after_fenced_code():
    scanner = _source_scanner()
    text = ''.join(part for _, part in scanner.feed('```\n[[1.1]]\n```\nsee [[1.1]]'))
    assert '```\n[[1.1]]\n```' in text
    assert '[1](#source-1.1 "Source.md")' in text


def test_incremental_scanner_supports_tilde_fences_and_resumes_after_close():
    scanner, plugin = _source_scanner_with_plugin()
    text = ''.join(
        part
        for _, part in scanner.feed('~~~python\n[[1.1]]\n~~~\nafter [[1.1]]')
    )

    assert '~~~python\n[[1.1]]\n~~~' in text
    assert text.endswith('[1](#source-1.1 "Source.md")')
    assert plugin.streamed_indices == ('1.1',)


@pytest.mark.parametrize('indent', range(4))
def test_incremental_scanner_supports_zero_to_three_space_fence_indent(indent):
    scanner, plugin = _source_scanner_with_plugin()
    spaces = ' ' * indent
    text = ''.join(
        part
        for _, part in scanner.feed(
            f'{spaces}```python\n[[1.1]]\n{spaces}```\nafter [[1.1]]'
        )
    )

    assert '[[1.1]]' in text
    assert text.endswith('[1](#source-1.1 "Source.md")')
    assert plugin.streamed_indices == ('1.1',)


def test_incremental_scanner_holds_split_fence_opener_and_closer():
    scanner, plugin = _source_scanner_with_plugin()
    chunks = [
        '  ``',
        '`python\n[[1.1]]\n  ``',
        '`\nafter [[1.1]]',
    ]
    text = ''.join(
        part
        for chunk in chunks
        for _, part in scanner.feed(chunk)
    )

    assert '  ```python\n[[1.1]]\n  ```' in text
    assert text.endswith('[1](#source-1.1 "Source.md")')
    assert plugin.streamed_indices == ('1.1',)


def test_chunk_starting_mid_line_does_not_create_a_fence():
    scanner, plugin = _source_scanner_with_plugin()
    first = scanner.feed('prefix ')
    second = scanner.feed('```code``` [[1.1]]')
    text = ''.join(part for _, part in first + second)

    assert text == 'prefix ```code``` [1](#source-1.1 "Source.md")'
    assert plugin.streamed_indices == ('1.1',)


def test_inline_triple_backticks_can_be_split_across_chunks():
    scanner, plugin = _source_scanner_with_plugin()
    chunks = [
        'before ``',
        '`[[1.1]]``',
        '` after [[1.1]]',
    ]
    text = ''.join(
        part
        for chunk in chunks
        for _, part in scanner.feed(chunk)
    )

    assert '```[[1.1]]```' in text
    assert text.endswith('[1](#source-1.1 "Source.md")')
    assert plugin.streamed_indices == ('1.1',)


def test_citations_inside_code_are_not_collected_or_held_as_incomplete_tokens():
    scanner, plugin = _source_scanner_with_plugin()
    first = scanner.feed('~~~\n[[')
    second = scanner.feed('1.1]]\n~~~\n')

    assert ''.join(part for _, part in first + second) == '~~~\n[[1.1]]\n~~~\n'
    assert plugin.streamed_indices == ()
    assert plugin.collect() == []


def test_fence_closer_requires_matching_character_length_and_clean_suffix():
    scanner, plugin = _source_scanner_with_plugin()
    text = ''.join(
        part
        for _, part in scanner.feed(
            '````\n'
            '[[1.1]]\n'
            '~~~\n'
            '[[1.1]]\n'
            '```\n'
            '[[1.1]]\n'
            '````not-a-close\n'
            '[[1.1]]\n'
            '````\n'
            'after [[1.1]]'
        )
    )

    assert text.count('[[1.1]]') == 4
    assert text.endswith('[1](#source-1.1 "Source.md")')
    assert plugin.streamed_indices == ('1.1',)
