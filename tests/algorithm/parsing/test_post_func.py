from lazyllm.tools.rag import DocNode
from lazymind.processor.engine.table_image_map import normalize_table_image_map, serialize_table_image_map

from lazymind.parsing.engine.transform.post_func import (
    GroupFilterNodeParser,
    GroupNodeParser,
    LayoutNodeParser,
    MergeNodeParser,
    NodeParser,
    NodeTextClear,
    ParagraphType,
    TableConverterNode,
    _match,
    parser_code_hash,
)


def test_node_text_clear_normalizes_full_width_text_and_drops_empty_nodes():
    nodes = [
        DocNode(text='ＡＢＣ１２３', metadata={'file_name': 'a.pdf'}),
        DocNode(text='', metadata={'file_name': 'a.pdf'}),
    ]

    result = NodeTextClear().forward(nodes)

    assert isinstance(result, list)
    assert [node.text for node in result] == ['ABC123']
    assert result[0].metadata['file_name'] == 'a.pdf'


def test_layout_node_parser_marks_time_index_and_plain_text():
    nodes = [
        DocNode(text='普通正文', metadata={'file_name': 'b.pdf'}),
        DocNode(text='2024年1月2日', metadata={'file_name': 'a.pdf'}),
        DocNode(text='1.1 标题内容', metadata={'file_name': 'a.pdf'}),
    ]

    result = LayoutNodeParser().forward(nodes)

    assert isinstance(result, list)
    assert [node.metadata['file_name'] for node in result] == ['a.pdf', 'a.pdf', 'b.pdf']
    assert [node.metadata['index'] for node in result] == [0, 1, 0]
    assert result[0].metadata['text_type'] == ParagraphType.Time_text
    assert result[1].metadata['text_type'] == ParagraphType.Index_text
    assert result[2].metadata['text_type'] == ParagraphType.Text


def test_match_handles_empty_patterns_strings_docnodes_and_unknown_types():
    assert _match('1.1 标题', []) is False
    assert _match(123, [r'.+']) is False
    assert _match('1.1 标题', [r'^\d+\.\d+\s+(.+)'])
    assert _match(DocNode(text='2024年1月2日'), [r'^\d{4}年\d{1,2}月\d{1,2}日$'])


def test_table_converter_preserves_non_table_and_builds_table_image_map(monkeypatch):
    converter = TableConverterNode()
    monkeypatch.setattr(converter, '_html_table_to_markdown', lambda html: '| A |\n|---|\n| 1 |')
    table_node = DocNode(
        text='raw table',
        metadata={
            'type': 'table',
            'table_body': '<table><tr><td>1</td></tr></table>',
            'table_caption': '表1',
            'table_footnote': '注：说明',
            'lines': [{'type': 'table', 'image_path': 'table.png'}],
        },
    )
    text_node = DocNode(text='plain', metadata={'type': 'text'})

    result = converter.forward([table_node, text_node])

    assert result == [table_node, text_node]
    assert table_node.text == '表1\n| A |\n|---|\n| 1 |\n注：说明\n'
    assert table_node.metadata['table_image'] == '![表1](images/table.png)'
    assert normalize_table_image_map(table_node.metadata['table_image_map']) == [
        {'content': '| A |\n|---|\n| 1 |', 'image': '![表1](images/table.png)'}
    ]
    assert 'table_body' not in table_node.metadata


def test_table_converter_skips_empty_table_body_and_handles_invalid_html():
    converter = TableConverterNode()

    assert converter._html_table_to_markdown('') == ''
    assert converter._html_table_to_markdown('not a table') == 'not a table'

    node = DocNode(text='unchanged', metadata={'type': 'table', 'table_body': '   '})

    assert converter.forward([node])[0].text == 'unchanged'


def test_merge_node_parser_merges_text_bbox_lines_and_table_image_map():
    table_map = serialize_table_image_map([{'content': 'table markdown', 'image': '![表](images/table.png)'}])
    group = [
        DocNode(
            text='first',
            metadata={'file_name': 'a.pdf', 'page': 1, 'bbox': [10, 20, 30, 40], 'type': 'text'},
        ),
        DocNode(
            text='table markdown',
            metadata={
                'file_name': 'a.pdf',
                'page': 1,
                'bbox': [5, 25, 35, 45],
                'type': 'table',
                'table_image_map': table_map,
            },
        ),
        DocNode(
            text='second page',
            metadata={'file_name': 'a.pdf', 'page': 2, 'bbox': [1, 2, 3, 4], 'type': 'text'},
        ),
    ]

    result = MergeNodeParser().forward([group])

    assert isinstance(result, list)
    assert len(result) == 1
    assert result[0].text == 'first\ntable markdown\nsecond page'
    assert result[0].metadata['bbox'] == [5, 20, 35, 45]
    assert result[0].metadata['lines'] == [
        {'content': 'first', 'bbox': [10, 20, 30, 40], 'type': 'text', 'page': 1},
        {'content': 'table markdown', 'bbox': [5, 25, 35, 45], 'type': 'table', 'page': 1},
        {'content': 'second page', 'bbox': [1, 2, 3, 4], 'type': 'text', 'page': 2},
    ]
    assert normalize_table_image_map(result[0].metadata['table_image_map']) == [
        {'content': 'table markdown', 'image': '![表](images/table.png)'}
    ]


def test_merge_node_parser_ignores_empty_groups_and_empty_content():
    empty_text = DocNode(text='   ', metadata={'file_name': 'a.pdf'})

    result = MergeNodeParser().forward([[], [empty_text]])

    assert result == []


def test_group_node_parser_splits_large_table_and_refreshes_table_image_map():
    table_map = serialize_table_image_map([{'content': 'old content', 'image': '![表](images/table.png)'}])
    node = DocNode(
        text='row1\nrow2',
        metadata={
            'type': 'table',
            'table_caption': '表1',
            'table_footnote': '注：说明',
            'table_image_map': table_map,
            'page': 1,
            'bbox': [1, 2, 3, 4],
        },
    )

    result = GroupNodeParser()._process_group([node], max_length=4)

    assert isinstance(result, list)
    assert all(isinstance(group, list) for group in result)
    split_node = result[0][0]
    assert split_node.text.startswith('表1\n')
    assert split_node.text.endswith('注：说明')
    assert split_node.metadata['lines'][0]['type'] == 'table'
    assert normalize_table_image_map(split_node.metadata['table_image_map'])[0]['image'] == '![表](images/table.png)'


def test_group_node_parser_title_relationships_and_toc_detection():
    parser = GroupNodeParser()

    assert parser.is_parent_child_title('1 标题', '1.1 子标题') is True
    assert parser.is_parent_child_title('1 标题', '2.1 其他') is False
    assert parser.is_parent_child_title('1 标题', '1.1.1 孙标题', direct_parent=False) is True
    assert parser._is_toc_node(DocNode(text='第一章 1', metadata={'type': 'text'})) is True
    assert parser._is_toc_node(DocNode(text='条目', metadata={'type': 'list'})) is True
    assert parser._is_toc_node(DocNode(text='普通正文', metadata={'type': 'text'})) is False


def test_group_node_parser_groups_titles_toc_index_and_plain_text():
    nodes = [
        DocNode(text='目 录', metadata={'text_level': 1, 'type': 'text'}),
        DocNode(text='第一章 1', metadata={'type': 'text'}),
        DocNode(text='正文开始', metadata={'type': 'text'}),
        DocNode(text='1 标题', metadata={'text_level': 1, 'type': 'text'}),
        DocNode(text='1.1 子标题', metadata={'text_level': 2, 'type': 'text'}),
        DocNode(text='1.1.1 条款', metadata={'text_type': 'index_text', 'type': 'text'}),
        DocNode(text='普通正文', metadata={'type': 'text'}),
    ]

    result = GroupNodeParser().forward(nodes)

    assert isinstance(result, list)
    assert all(isinstance(group, list) for group in result)
    assert [node.text for node in result[0]] == ['目 录', '第一章 1']
    assert result[-1][-1].metadata['title'] == '1 标题\n1.1 子标题'


def test_group_filter_node_parser_removes_table_of_contents_group():
    toc_group = [DocNode(text='目 录', metadata={'type': 'text'})]
    body_group = [DocNode(text='正文', metadata={'type': 'text'})]

    assert GroupFilterNodeParser().forward([toc_group, body_group]) == [body_group]


def test_node_parser_sets_indexes_cleans_metadata_and_exclusions(monkeypatch, tmp_path):
    import lazymind.parsing.engine.transform.post_func as post_func_module
    upload_dir = str(tmp_path / 'uploads')
    monkeypatch.setitem(post_func_module._cfg._impl, 'shared_upload_dir', upload_dir)
    monkeypatch.setitem(post_func_module._cfg._impl, 'rag_image_path_prefix', str(tmp_path / 'images'))
    (tmp_path / 'uploads' / 'normalized_images').mkdir(parents=True)

    parsed_node = DocNode(
        text='content',
        metadata={
            'file_name': 'a.pdf',
            'title': '标题',
            'text_type': 'text',
            'table_caption': '表1',
            'custom': 'value',
        },
    )

    class FakePipeline:
        def __enter__(self):
            return self

        def __exit__(self, exc_type, exc, tb):
            return False

        def __call__(self, nodes):
            return [parsed_node]

    monkeypatch.setattr(post_func_module.lazyllm, 'pipeline', lambda: FakePipeline())

    result = NodeParser().batch_forward([DocNode(text='raw')], node_group='block')

    assert isinstance(result, list)
    assert result[0]._group == 'block'
    assert result[0].metadata['index'] == 0
    assert 'text_type' not in result[0].metadata
    assert 'table_caption' not in result[0].metadata
    assert set(result[0].excluded_embed_metadata_keys) == {'custom', 'index'}
    assert set(result[0].excluded_llm_metadata_keys) == {'custom', 'index'}


def test_parser_code_hash_returns_sha256_string():
    value = parser_code_hash()

    assert isinstance(value, str)
    assert len(value) == 64


# ---------------------------------------------------------------------------
# GroupNodeParser._process_group — plain-text splitting path
# ---------------------------------------------------------------------------

def test_process_group_returns_single_group_when_total_fits():
    nodes = [
        DocNode(text='a' * 100, metadata={'file_name': 'a.pdf'}),
        DocNode(text='b' * 100, metadata={'file_name': 'a.pdf'}),
    ]
    result = GroupNodeParser()._process_group(nodes, max_length=300)

    assert result == [nodes]


def test_process_group_splits_when_total_exceeds_max_length():
    nodes = [
        DocNode(text='a' * 60, metadata={'file_name': 'a.pdf'}),
        DocNode(text='b' * 60, metadata={'file_name': 'a.pdf'}),
        DocNode(text='c' * 60, metadata={'file_name': 'a.pdf'}),
    ]
    result = GroupNodeParser()._process_group(nodes, max_length=100)

    assert len(result) >= 2
    all_nodes = [n for group in result for n in group]
    assert len(all_nodes) == 3


def test_process_group_splits_oversized_single_node():
    node = DocNode(
        text='x' * 5000,
        metadata={'file_name': 'a.pdf', 'type': 'text', 'page': 1, 'bbox': [0, 0, 1, 1]},
    )
    result = GroupNodeParser()._process_group([node], max_length=2048)

    assert len(result) >= 2
    for group in result:
        assert len(group) == 1
        assert len(group[0].text) <= 2048


def test_process_group_preserves_metadata_on_split_nodes():
    node = DocNode(
        text='\n'.join(['line'] * 200),
        metadata={'file_name': 'a.pdf', 'type': 'text', 'page': 3, 'bbox': [0, 0, 10, 10]},
    )
    result = GroupNodeParser()._process_group([node], max_length=200)

    for group in result:
        for n in group:
            assert n.metadata['file_name'] == 'a.pdf'
            assert n.metadata['page'] == 3


# ---------------------------------------------------------------------------
# NodeParser — real pipeline integration (no mock)
# ---------------------------------------------------------------------------

def test_node_parser_full_pipeline_cleans_and_merges(monkeypatch, tmp_path):
    import lazymind.parsing.engine.transform.post_func as post_func_module
    monkeypatch.setitem(post_func_module._cfg._impl, 'shared_upload_dir', str(tmp_path / 'uploads'))
    monkeypatch.setitem(post_func_module._cfg._impl, 'rag_image_path_prefix', str(tmp_path / 'images'))
    (tmp_path / 'uploads' / 'normalized_images').mkdir(parents=True)
    nodes = [
        DocNode(
            text='ＡＢＣ正文内容',
            metadata={'file_name': 'doc.pdf', 'type': 'text', 'page': 1, 'bbox': [0, 0, 10, 10]},
        ),
    ]
    result = NodeParser().batch_forward(nodes, node_group='block')

    assert isinstance(result, list)
    assert len(result) >= 1
    assert result[0]._group == 'block'
    assert result[0].metadata['index'] == 0
    assert 'ABC正文内容' in result[0].text
    assert 'text_type' not in result[0].metadata
    assert 'table_caption' not in result[0].metadata


def test_node_parser_drops_empty_nodes(monkeypatch, tmp_path):
    import lazymind.parsing.engine.transform.post_func as post_func_module
    monkeypatch.setitem(post_func_module._cfg._impl, 'shared_upload_dir', str(tmp_path / 'uploads'))
    monkeypatch.setitem(post_func_module._cfg._impl, 'rag_image_path_prefix', str(tmp_path / 'images'))
    (tmp_path / 'uploads' / 'normalized_images').mkdir(parents=True)
    nodes = [
        DocNode(text='', metadata={'file_name': 'doc.pdf', 'type': 'text'}),
        DocNode(text='有效内容', metadata={'file_name': 'doc.pdf', 'type': 'text', 'page': 1, 'bbox': [0, 0, 5, 5]}),
    ]
    result = NodeParser().batch_forward(nodes, node_group='block')

    texts = [n.text for n in result]
    assert '' not in texts
    assert any('有效内容' in t for t in texts)
