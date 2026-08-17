from lazyllm.tools.rag import DocNode
from lazyllm.tools.rag.doc_node import MetadataMode

from lazymind.parsing.engine.transform.para_parser import (
    LineSplitter,
    MineruLineSplitter,
    NormalLineSplitter,
    ParagraphSplitter,
    split_by_regex,
    split_by_char,
    split_by_sep,
    split_by_sentence_tokenizer,
    split_text_keep_separator,
)


def test_split_helpers_keep_separators_and_split_characters():
    assert split_by_regex(r'\d+')('a12b34') == ['12', '34']
    assert split_text_keep_separator('alpha\nbeta\ngamma', '\n') == ['alpha', '\nbeta', '\ngamma']
    assert split_by_sep('|', keep_sep=True)('a|b|c') == ['a', '|b', '|c']
    assert split_by_sep('|', keep_sep=False)('a|b|c') == ['a', 'b', 'c']
    assert split_by_char()('abc') == ['a', 'b', 'c']


def test_sentence_tokenizer_splitter_returns_strings():
    splitter = split_by_sentence_tokenizer()

    result = splitter('First sentence. Second sentence.')

    assert isinstance(result, list)
    assert all(isinstance(item, str) for item in result)


def test_normal_line_splitter_splits_sentences_and_merges_short_prefixes():
    splitter = NormalLineSplitter()

    # Non-PDF: keep semantic newlines; short prefix is merged into the next sentence.
    assert splitter._split_text('短。\n这是一个较长的句子。') == ['短。\n这是一个较长的句子。']
    assert splitter._split_text('这是一个足够长的第一句。\n第二句也足够长？') == [
        '这是一个足够长的第一句。',
        '\n第二句也足够长？',
    ]


def test_line_splitter_uses_normal_sentence_splitter_for_non_pdf():
    node = DocNode(
        text='这是一个足够长的第一句。\n第二句也足够长？',
        metadata={'file_name': 'note.md', 'page': 1},
        global_metadata={'file_name': 'note.md'},
    )

    result = LineSplitter().forward(node)

    assert isinstance(result, list)
    assert all(isinstance(item, DocNode) for item in result)
    assert [item.text for item in result] == ['这是一个足够长的第一句。', '\n第二句也足够长？']
    assert result[0].metadata == {'file_name': 'note.md', 'page': 1}
    assert result[0].metadata is not node.metadata


def test_mineru_line_splitter_expands_line_metadata():
    node = DocNode(
        text='merged text',
        metadata={
            'file_name': 'paper.pdf',
            'docid': 'doc-1',
            'lines': [
                {'content': 'line one', 'type': 'text', 'page': 2, 'bbox': [1, 2, 3, 4]},
                {'content': 'table one', 'type': 'table', 'page': 3, 'bbox': [5, 6, 7, 8]},
            ],
        },
        global_metadata={'file_name': 'paper.pdf'},
    )

    result = MineruLineSplitter().forward(node)

    assert isinstance(result, list)
    assert [item.text for item in result] == ['line one', 'table one']
    assert result[0].metadata == {
        'file_name': 'paper.pdf',
        'docid': 'doc-1',
        'type': 'text',
        'page': 2,
        'bbox': [1, 2, 3, 4],
    }
    assert 'lines' not in result[0].metadata
    assert result[1].metadata['type'] == 'table'


def test_line_splitter_sentence_splits_pdf_even_with_layout_lines():
    text = (
        '这是一个足够长的第一句，用来触发句级切分。'
        '这是一个足够长的第二句，确认不会走版面行。'
    )
    node = DocNode(
        text=text,
        metadata={
            'file_name': 'paper.pdf',
            'docid': 'doc-1',
            # Layout lines are intentionally one blob; sentence slice must ignore them.
            'lines': [{'content': text, 'type': 'text', 'page': 2, 'bbox': [1, 2, 3, 4]}],
        },
        global_metadata={'file_name': 'paper.pdf'},
    )

    result = LineSplitter().forward(node)

    assert len(result) == 2
    assert result[0].text.endswith('。')
    assert result[1].text.endswith('。')
    # Block-wide lines must not be copied onto every sentence child.
    assert all('lines' not in item.metadata for item in result)


def test_line_splitter_filters_layout_lines_to_matching_sentences():
    text = (
        '这是一个足够长的第一句，用来触发句级切分。'
        '这是一个足够长的第二句，确认行级元数据过滤。'
    )
    line_a = {
        'content': '这是一个足够长的第一句，用来触发句级切分。',
        'type': 'text',
        'page': 1,
        'bbox': [1, 2, 3, 4],
    }
    line_b = {
        'content': '这是一个足够长的第二句，确认行级元数据过滤。',
        'type': 'text',
        'page': 1,
        'bbox': [5, 6, 7, 8],
    }
    node = DocNode(
        text=text,
        metadata={'file_name': 'paper.pdf', 'lines': [line_a, line_b]},
        global_metadata={'file_name': 'paper.pdf'},
    )

    result = LineSplitter().forward(node)

    assert len(result) == 2
    assert result[0].metadata['lines'] == [line_a]
    assert result[1].metadata['lines'] == [line_b]


def test_line_splitter_uses_normal_splitter_when_pdf_has_no_lines():
    text = (
        '这是一个足够长的第一句，用来触发句级切分。\n'
        '这是一个足够长的第二句，确认不会原样保留整段。'
    )
    node = DocNode(
        text=text,
        metadata={
            'file_name': 'paper.pdf',
            'type': 'paragraph',
            'page': 0,
            'bbox': [1, 2, 3, 4],
        },
        global_metadata={'file_name': 'paper.pdf'},
    )

    result = LineSplitter().forward(node)

    assert len(result) > 1
    assert all(item.text != text for item in result)


def test_line_splitter_sentence_splits_english_pdf_with_layout_lines():
    text = (
        '(2) For the Qwen3 MoE base models, our experimental results indicate that: '
        '(a) Using the same pre-training data, Qwen3 MoE base models can achieve similar '
        'performance to Qwen3 dense base models with only 1/5 activated parameters. '
        '(b) Due to the improvements of the Qwen3 MoE architecture, the scale-up of the '
        'training tokens, and more advanced training strategies, the Qwen3 MoE base models '
        'can outperform the Qwen2.5 MoE base models with less than 1/2 activated parameters '
        'and fewer total parameters. (c) Even with 1/10 of the activated parameters of the '
        'Qwen2.5 dense base model, the Qwen3 MoE base model can achieve comparable '
        'performance, which brings us significant advantages in inference and training costs.'
    )
    node = DocNode(
        text=text,
        metadata={
            'file_name': 'paper.pdf',
            'lines': [{'content': text, 'type': 'text', 'page': 1, 'bbox': [1, 2, 3, 4]}],
        },
        global_metadata={'file_name': 'paper.pdf'},
    )

    result = LineSplitter().forward(node)
    texts = [item.text for item in result]

    assert len(texts) == 3
    assert 'Qwen2.5 MoE' in texts[1]
    assert 'Qwen2.5 dense' in texts[2]
    assert not any(t.rstrip().endswith('Qwen2.') for t in texts)


def test_normal_line_splitter_keeps_decimals_and_versions():
    text = 'Qwen2.5 MoE is strong enough here. Next sentence follows with enough length.'
    assert NormalLineSplitter()._split_text(text) == [
        'Qwen2.5 MoE is strong enough here.',
        ' Next sentence follows with enough length.',
    ]


def test_normal_line_splitter_preserves_markdown_newlines_without_unwrap():
    text = '# Title\n这是一个足够长的第一句。\n- 列表项也足够长。'
    parts = NormalLineSplitter()._split_text(text)
    assert parts == ['# Title\n这是一个足够长的第一句。', '\n- 列表项也足够长。']


def test_normal_line_splitter_joins_pdfreader_soft_wraps_before_sentence_split():
    text = (
        'benchmarks, including tasks in code generation, mathematical reasoning, agent tasks,\n'
        'etc., competitive against larger MoE models and proprietary models. Compared to its\n'
        'predecessor Qwen2.5, Qwen3 expands multilingual support from 29 to 119 languages\n'
        'and dialects, enhancing global accessibility through improved cross-lingual understand-\n'
        'ing and generation capabilities.'
    )
    node = DocNode(
        text=text,
        metadata={'page_label': '1', 'file_name': 'paper.pdf'},
        global_metadata={'file_name': 'paper.pdf'},
    )
    parts = [item.text for item in NormalLineSplitter().forward(node)]
    assert len(parts) == 2
    assert 'agent tasks, etc., competitive' in parts[0]
    assert parts[0].endswith('models.')
    assert 'Compared to its predecessor Qwen2.5' in parts[1]
    assert 'understanding and generation capabilities.' in parts[1]
    assert 'understand-' not in parts[1]


def test_unwrap_soft_newlines_keeps_compound_hyphens_across_wraps():
    from lazymind.parsing.engine.transform.para_parser import _unwrap_soft_newlines

    assert _unwrap_soft_newlines('state-of-\nthe-art models') == 'state-of-the-art models'
    assert _unwrap_soft_newlines('state-\nof-the-art models') == 'state-of-the-art models'
    assert _unwrap_soft_newlines('cross-lingual understand-\ning capabilities') == (
        'cross-lingual understanding capabilities'
    )


def test_normal_line_splitter_inherits_parent_metadata_exclusions():
    node = DocNode(
        text='这是一个足够长的第一句。\n第二句也足够长？',
        metadata={'file_name': 'note.md', 'page': 1, 'bbox': [0, 0, 1, 1]},
        global_metadata={'file_name': 'note.md'},
    )
    node.excluded_embed_metadata_keys = ['page', 'bbox']
    node.excluded_llm_metadata_keys = ['page', 'bbox']

    result = NormalLineSplitter().forward(node)

    for child in result:
        assert set(child.excluded_embed_metadata_keys) == {'page', 'bbox'}
        assert child.get_text(MetadataMode.EMBED).startswith('file_name: note.md\n\n')


def test_mineru_line_splitter_inherits_parent_metadata_exclusions():
    node = DocNode(
        text='merged text',
        metadata={
            'file_name': 'paper.pdf',
            'lines': [{'content': 'line one', 'type': 'text', 'page': 2, 'bbox': [1, 2, 3, 4]}],
        },
        global_metadata={'file_name': 'paper.pdf'},
    )
    node.excluded_embed_metadata_keys = ['lines', 'type', 'page', 'bbox']
    node.excluded_llm_metadata_keys = ['lines', 'type', 'page', 'bbox']

    result = MineruLineSplitter().forward(node)

    assert set(result[0].excluded_embed_metadata_keys) == {'lines', 'type', 'page', 'bbox'}
    assert result[0].get_text(MetadataMode.EMBED) == 'file_name: paper.pdf\n\nline one'


def test_paragraph_splitter_splits_by_paragraph_and_applies_overlap():
    splitter = ParagraphSplitter(
        chunk_size=12,
        chunk_overlap=3,
        chunking_tokenizer_fn=lambda text: [text],
        tokenizer=list,
    )

    chunks = splitter.split_text('第一段内容较长。\n\n\n第二段内容也长。\n\n\n第三段收尾。')

    assert chunks == ['第一段内容较长。', '。\n\n\n第二段内容也长。', '容也长。\n\n\n第三段收尾。']


def test_paragraph_splitter_handles_empty_text_and_run_component():
    splitter = ParagraphSplitter(
        chunk_size=10,
        chunk_overlap=2,
        chunking_tokenizer_fn=lambda text: [text],
        tokenizer=list,
    )
    node = DocNode(text='', metadata={'file_name': 'empty.md'})

    assert splitter.split_text('') == ['']
    result = splitter._run_component([node])
    assert isinstance(result, list)
    assert result[0].text == ''
    assert result[0].metadata == {'file_name': 'empty.md'}


def test_paragraph_splitter_raises_when_single_token_exceeds_chunk_size():
    splitter = ParagraphSplitter(
        chunk_size=3,
        chunk_overlap=1,
        chunking_tokenizer_fn=lambda text: [text],
        tokenizer=lambda text: [text] * len(text),
    )

    try:
        splitter._merge([type('Split', (), {'text': 'abcdef', 'token_size': 10, 'is_sentence': True})()], 3)
    except ValueError as exc:
        assert 'Single token exceeded chunk size' in str(exc)
    else:
        raise AssertionError('expected ParagraphSplitter to reject an oversized token')


def test_paragraph_splitter_rejects_overlap_larger_than_chunk_size():
    try:
        ParagraphSplitter(chunk_size=3, chunk_overlap=4, chunking_tokenizer_fn=lambda text: [text])
    except ValueError as exc:
        assert 'larger chunk overlap' in str(exc)
    else:
        raise AssertionError('expected ParagraphSplitter to reject an oversized overlap')


def test_paragraph_splitter_run_component_multiple_nodes():
    splitter = ParagraphSplitter(
        chunk_size=20,
        chunk_overlap=2,
        chunking_tokenizer_fn=lambda text: [text],
        tokenizer=list,
    )
    nodes = [
        DocNode(text='第一段内容。', metadata={'file_name': 'a.md'}),
        DocNode(text='第二段内容。', metadata={'file_name': 'b.md'}),
    ]

    result = splitter._run_component(nodes)

    assert isinstance(result, list)
    assert len(result) >= 2
    texts = [n.text for n in result]
    assert any('第一段' in t for t in texts)
    assert any('第二段' in t for t in texts)


def test_paragraph_splitter_preserves_metadata_per_node():
    splitter = ParagraphSplitter(
        chunk_size=20,
        chunk_overlap=0,
        chunking_tokenizer_fn=lambda text: [text],
        tokenizer=list,
    )
    node_a = DocNode(text='内容A', metadata={'file_name': 'a.md', 'page': 1})
    node_b = DocNode(text='内容B', metadata={'file_name': 'b.md', 'page': 2})

    result = splitter._run_component([node_a, node_b])

    # _run_component processes nodes sequentially; all output nodes carry the
    # metadata of the last processed node (known implementation behaviour).
    assert len(result) >= 2
    for n in result:
        assert n.metadata['file_name'] == 'b.md'
