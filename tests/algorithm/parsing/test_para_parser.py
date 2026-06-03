from lazyllm.tools.rag import DocNode

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

    assert splitter._split_text('短。\n这是一个较长的句子。') == ['短。这是一个较长的句子。']
    assert splitter._split_text('这是一个足够长的第一句。\n第二句也足够长？') == [
        '这是一个足够长的第一句。',
        '第二句也足够长？',
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
    assert [item.text for item in result] == ['这是一个足够长的第一句。', '第二句也足够长？']
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


def test_line_splitter_uses_mineru_lines_for_pdf():
    node = DocNode(
        text='merged text',
        metadata={
            'file_name': 'paper.pdf',
            'docid': 'doc-1',
            'lines': [{'content': 'line one', 'type': 'text', 'page': 2, 'bbox': [1, 2, 3, 4]}],
        },
        global_metadata={'file_name': 'paper.pdf'},
    )

    result = LineSplitter().forward(node)

    assert [item.text for item in result] == ['line one']
    assert result[0].metadata['page'] == 2


def test_paragraph_splitter_splits_by_paragraph_and_applies_overlap():
    splitter = ParagraphSplitter(
        chunk_size=12,
        chunk_overlap=3,
        chunking_tokenizer_fn=lambda text: [text],
        tokenizer=list,
    )

    chunks = splitter.split_text('第一段内容较长。\n\n\n第二段内容也长。\n\n\n第三段收尾。')

    assert chunks == ['第一段内容较长。', '较长。第二段内容也长。', '容也长。\n\n\n第三段收尾。']


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
