import os
import copy
import functools
import inspect
import itertools
import re
from pathlib import Path
from typing import Any, List, Union
from urllib.parse import urlparse

import lazyllm
from lazyllm import ModuleBase, LOG
from lazyllm.tools.rag import DocNode
from lazyllm.tools.rag.doc_node import ImageDocNode

from config import config as _cfg
from parsing.utils import normalize_image_file
from processor.table_image_map import merge_table_image_maps, normalize_table_image_map, serialize_table_image_map


class ParagraphType:
    Caption = 'caption'
    Footnote = 'footnote'
    Formula = 'formula'
    Index_text = 'index_text'
    Page_footer = 'page_footer'
    Page_header = 'page_header'
    Picture = 'picture'
    Figure = 'figure'
    Section_header = 'section_header'
    Table = 'table'
    Text = 'text'
    Title = 'title'

    Block = 'block'
    Clause_text = 'clause_text'
    Time_text = 'time_text'
    Water_mark = 'water_mark'
    Preface = 'preface'


SECTION_HEADER_PATTERNS = []

NUMBER_PATTERN = [r'[一二三四五六七八九十百千万零壹贰叁肆伍陆柒捌玖拾佰仟]', r'[１２３４５６７８９0-9]', r'[a-zA-Z]']

NORMAL_PATTERN_TEMPLATE = {
    r'^(\s*第\s*{num}+\s*([{index_type}]+))(?:\s*(.+))': ['篇', '章', '卷', '回', '节', '条'],
    r'^(\s*{num}({index_type})\s)(?:\s*(.+))': ['、'],
    # r'^(\s*[\(（]?{num}+[\)）])(?:\s*(.+))': [''],
}

# Maximum number of levels for pure numeric indices (1.1.1 NUMBER_INDEX_MAX_LEVEL = 3)
NUMBER_INDEX_MAX_LEVEL = 4


def _generate_normal_index_patterns() -> list:
    # depends on NORMAL_PATTERN_TEMPLATE && NUMBER_PATTERN
    # build pattern list for Chinese and bracket styles
    return [
        re.compile(pattern_template.format(num=num, index_type=index_type))
        for num in NUMBER_PATTERN
        for pattern_template, index_types in NORMAL_PATTERN_TEMPLATE.items()
        for index_type in index_types
    ]


def _generate_number_index_patterns() -> list:
    # build patterns list for pure numeric index
    def _generate_index_pattern(level, model: str = 'loose') -> list:
        """Generate regex for the specified level."""
        pattern_leve_temp = rf'([\.．]\s*{NUMBER_PATTERN[1]}{{1,3}})'
        pattern_leve = pattern_leve_temp * level

        sep = r'\s?' if model == 'loose' else r'\s'

        # Standard pure numeric index followed by content; must not start with: 'digit', ')', '）', ']', '】'
        end_with = r'(?:\s*([^\d\)）\]】]\s*[^\d\)）\]】].*))'

        if level == 1:
            # When level is 1.1, to avoid misidentifying floats as indices, require a space after the index
            pattern = rf'^(\s*{NUMBER_PATTERN[1]}{{1,2}}{pattern_leve}{sep}){end_with}'
        else:
            pattern = rf'^(\s*{NUMBER_PATTERN[1]}{{1,2}}{pattern_leve}{sep}){end_with}'
        return re.compile(pattern)

    return [_generate_index_pattern(level) for level in range(1, NUMBER_INDEX_MAX_LEVEL)][::-1]


def _generate_letter_number_index_patterns() -> list:
    """Generate patterns list for letter.number.number style, e.g. B.0.1"""
    patterns = []

    # letter.number.number pattern, e.g. B.0.1, A.1.2, etc.
    letter_num_pattern = r'^(\s*[a-zA-Z]\.\d+\.\d+)(?:\s*(.+))'
    patterns.append(re.compile(letter_num_pattern))

    return patterns


NORMAL_PATTERNS = _generate_normal_index_patterns()
NUMBER_PATTERNS = _generate_number_index_patterns()
LETTER_NUMBER_PATTERNS = _generate_letter_number_index_patterns()
INDEX_PATTERNS = NORMAL_PATTERNS + NUMBER_PATTERNS + LETTER_NUMBER_PATTERNS

TIME_PATTERNS = [
    re.compile(r'.{0,100}?\s*\n\s*(\d{4}年\d{1,2}月\d{1,2}日)\s*\n?$'),
    re.compile(r'^\s*(\d{4}年\d{1,2}月\d{1,2}日)\s*$'),
    re.compile(r'.{0,100}?\s*\n\s*([零○0０〇ＯΟ一二三四五六七八九十]{4}年'
               r'[一二三四五六七八九十]{1,2}月'
               r'[一二三四五六七八九十]{1,3}日)\s*\n?$'),
    re.compile(r'^\s*([零○0０〇ＯΟ一二三四五六七八九十]{4}年'
               r'[一二三四五六七八九十]{1,2}月'
               r'[一二三四五六七八九十]{1,3}日)\s*$'),
]


def _reset_node_index(nodes) -> List[DocNode]:
    result = []
    for index, node in enumerate(nodes):
        node._metadata['index'] = index
        result.append(node)
    return result


def _match(node: Union[DocNode, str], patterns: List) -> Union[re.Match, bool]:
    if not patterns:
        return False
    for pattern in patterns:
        if isinstance(node, DocNode):
            match = re.match(pattern=pattern, string=node.text.strip())
            if match:
                return match
        elif isinstance(node, str):
            match = re.match(pattern=pattern, string=node.strip())
            if match:
                return match
        else:
            return False


def _is_url(s: str) -> bool:
    try:
        res = urlparse(s)
        return bool(res.scheme and (res.netloc or res.scheme == 'file'))
    except Exception as exc:
        LOG.error(f'_is_url error: {exc}')
        return False


def _extract_image_path(node: DocNode) -> str:
    text = (node.text or '').strip()
    if text:
        match = re.search(r'!\[.*?\]\((.*?)\)', text)
        if match and match.group(1):
            return match.group(1)
    metadata = node.metadata
    for key in ('image_url', 'image_path', 'img_path', 'image', 'img'):
        if metadata.get(key):
            return metadata[key]

    for line in metadata.get('lines', []) or []:
        if line.get('image_url'):
            return line['image_url']
        if line.get('image_path'):
            return line['image_path']
    return ''


class LayoutNodeParser(ModuleBase):
    """
    Classify nodes via regex ->
    node.metadata['type'] =
        ParagraphType.Index_text:    numbered headingF
        ParagraphType.Time_text:    timestamp
        ...
    """

    def __init__(self, num_workers: int = 0, return_trace: bool = False, **kwargs):
        super().__init__(return_trace=return_trace, **kwargs)

    def forward(self, document: List[DocNode], **kwargs) -> List[DocNode]:
        # replaced by nodes after document
        result_nodes = []
        nodes = sorted(document, key=lambda x: x.metadata['file_name'])
        for _file_name, group in itertools.groupby(nodes, key=lambda x: x.metadata['file_name']):
            grouped_nodes = list(group)
            # grouped_nodes = _split_nodes(nodes=grouped_nodes, sep='\n\n')
            grouped_nodes = _reset_node_index(nodes=grouped_nodes)

            if len(grouped_nodes) == 0:
                continue

            _parsed_nodes = self._parse_nodes(nodes=grouped_nodes, **kwargs)

            result_nodes.extend(_reset_node_index(nodes=_parsed_nodes))
        return result_nodes

    @classmethod
    def class_name(cls) -> str:
        return 'LayoutNodeParser'

    def _parse_nodes(
        self,
        nodes: List[DocNode],
        **kwargs: Any,
    ) -> List[DocNode]:
        result = []
        for node in nodes:
            node._metadata['text_type'] = ParagraphType.Text
            if _match(node, TIME_PATTERNS):
                node._metadata['text_type'] = ParagraphType.Time_text
            elif _match(node, INDEX_PATTERNS):
                node._metadata['text_type'] = ParagraphType.Index_text
            result.append(node)
        return result


class TableConverterNode(ModuleBase):
    def __init__(self, num_workers: int = 0, return_trace: bool = False, **kwargs):
        super().__init__(return_trace=return_trace, **kwargs)

    def forward(self, document: List[DocNode], **kwargs) -> List[DocNode]:
        return self._parse_nodes(document)

    @classmethod
    def class_name(cls) -> str:
        return 'ProcessTableNode'

    def _html_table_to_markdown(self, html_table) -> str:
        import pandas as pd
        from io import StringIO
        if not html_table or not html_table.strip():
            return ''
        try:
            df = pd.read_html(StringIO(html_table))
            df_no_header = df[0]
            df_no_header.columns = [''] * len(df_no_header.columns)
            md = df_no_header.to_markdown(index=False)
            return md
        except Exception as e:
            LOG.info(f'[CustomMineruPDFReader] Error converting HTML table to Markdown: {e}')
            return str(html_table)

    def _parse_nodes(self, document: List[DocNode], **kwargs) -> List[DocNode]:

        for node in document:
            node_type = node.metadata.get('type', 'text')
            if node_type == 'table':
                table_body = node.metadata.get('table_body')
                table_caption = node.metadata.get('table_caption')
                table_footnote = node.metadata.get('table_footnote')
                if not table_body or not table_body.strip():
                    continue
                markdown_table = self._html_table_to_markdown(table_body)
                parts = [p for p in [table_caption, markdown_table, table_footnote] if p]
                node._content = '\n'.join(parts) + '\n' if parts else ''
                # find table image from lines
                lines = node.metadata.get('lines', [])
                table_image = None
                for line in lines:
                    if line.get('type', 'text') == 'table' and line.get('image_path', None):
                        true_image_path = os.path.join('images', line['image_path'])
                        table_image = f'![{table_caption}]({true_image_path})'
                        break
                # set table_image_map
                if table_image:
                    node.metadata['table_image'] = table_image
                    node.metadata['table_image_map'] = serialize_table_image_map(
                        [{'content': markdown_table, 'image': table_image}]
                    )
                node.metadata.pop('table_body', None)
        return document


class ImageNodeLoader(ModuleBase):
    '''Load images from MinerU image cache and emit ImageDocNodes.'''

    def __init__(self, num_workers: int = 0, return_trace: bool = False, **kwargs):
        super().__init__(return_trace=return_trace, **kwargs)
        self._default_cache_dir = _cfg['ocr_cache_dir']
        self._normalized_root = Path(_cfg['shared_upload_dir']) / 'normalized_images'
        self._normalized_root.mkdir(parents=True, exist_ok=True)

    def forward(self, document: List[DocNode], **kwargs) -> List[ImageDocNode]:
        return self._parse_nodes(document)

    @classmethod
    def class_name(cls) -> str:
        return 'ImageNodeLoader'

    def _get_image_cache_dir(self, node: DocNode) -> str:
        cache_dir = node.global_metadata.get('image_cache_dir')
        if cache_dir:
            return str(cache_dir)
        return self._default_cache_dir

    def _resolve_cached_image_path(self, image_path: str, image_cache_dir: str) -> str:
        if not image_path or not image_cache_dir:
            return ''

        path_obj = Path(image_path)
        if path_obj.is_absolute():
            if path_obj.is_file() and path_obj.stat().st_size > 0:
                return str(path_obj.resolve())
            return ''

        if _is_url(image_path):
            return ''

        cache_root = Path(image_cache_dir)
        rel = image_path.lstrip('/').replace('..', '_')
        local_path = cache_root / rel
        if local_path.is_file() and local_path.stat().st_size > 0:
            return str(local_path.resolve())
        return ''

    def _is_image_node(self, node: DocNode) -> bool:
        text = (node.text or '').strip()
        if re.search(r'images\/[^\s\)]+\.(jpg|jpeg|png|gif|bmp|webp|tiff|tif)', text, flags=re.I):
            return True
        if str(node.metadata.get('type', '')).lower() in {
            ParagraphType.Picture, ParagraphType.Figure, 'image', 'img'
        }:
            return True
        return bool(_extract_image_path(node))

    def _normalize_image_file(self, image_path: str) -> str:
        return normalize_image_file(image_path=image_path, normalized_root=self._normalized_root)

    def _parse_nodes(self, document: List[DocNode], **kwargs) -> List[ImageDocNode]:
        image_nodes = []
        seen_paths = set()
        for node in document:
            if not self._is_image_node(node):
                continue
            image_cache_dir = self._get_image_cache_dir(node)
            text = (node.text or '').strip()
            image_paths = re.findall(r'!\[.*?\]\((.*?)\)', text)
            if not image_paths:
                image_path = _extract_image_path(node)
                if image_path:
                    image_paths = [image_path]
            for image_path in image_paths:
                if not image_path:
                    continue
                try:
                    local_image_path = self._resolve_cached_image_path(image_path, image_cache_dir)
                    if not local_image_path:
                        raise FileNotFoundError(
                            f'image not found in cache: {image_path} (cache_dir={image_cache_dir})'
                        )
                    normalized_path = self._normalize_image_file(local_image_path)
                    if normalized_path in seen_paths:
                        continue
                    seen_paths.add(normalized_path)
                    source_path = os.path.abspath(local_image_path)
                    metadata = {
                        'source_path': source_path,
                        'normalized_source_path': normalized_path,
                        'file_name': os.path.basename(source_path),
                        'file_ext': Path(source_path).suffix.lower() or '.jpg',
                        'file_type': 'image',
                        'is_pure_image': True,
                        'image_url': local_image_path,
                    }
                    image_nodes.append(ImageDocNode(image_path=normalized_path, metadata=metadata))
                except Exception as exc:
                    LOG.warning(f'[ImageNodeLoader] load image failed: {image_path}, error: {exc}')
                    continue
        return image_nodes


class NodeTextClear(ModuleBase):
    """
    1. Mainly clean up possible empty nodes
    2. Escape or remove known characters
    """

    def __init__(self, num_workers: int = 0, **kwargs):
        super().__init__(**kwargs)

    def forward(self, document: List[DocNode], **kwargs) -> List[DocNode]:
        return self._parse_nodes(document, **kwargs)

    @classmethod
    def class_name(cls) -> str:
        return 'NodeTextClear'

    def _parse_nodes(
        self,
        nodes: List[DocNode],
        **kwargs: Any,
    ) -> List[DocNode]:
        def _text_clear(result, node: DocNode):
            import unicodedata
            node._content = unicodedata.normalize('NFKC', node.text)
            if not node.text:
                return result
            result.append(node)
            return result

        return functools.reduce(_text_clear, nodes, [])


class GroupNodeParser(ModuleBase):
    """
    Merge plain text into index text, control node length
    Returns list[list[DocNode]]
    """

    def __init__(self, num_workers: int = 0, return_trace: bool = False, **kwargs):
        super().__init__(return_trace=return_trace, **kwargs)

    def forward(self, document: List[DocNode], **kwargs) -> List[List[DocNode]]:
        return self._parse_nodes(document)

    @classmethod
    def class_name(cls) -> str:
        return 'GroupNodeParser'

    def _parse_nodes(
        self,
        nodes: List[DocNode],
        max_length: int = 2048,
        **kwargs: Any,
    ) -> List[List[DocNode]]:
        node_group = []
        cur_group = []
        cur_title_list = []
        toc_pattern = r'^目\s{0,4}[次录]$'
        for node in nodes:
            if not node.text.strip():
                continue
            text_type = node.metadata.get('text_type', 'text')
            text_level = node.metadata.get('text_level', 0)

            if text_level:  # process heading node
                if not cur_title_list:
                    cur_title_list = [node._content.strip()]
                    if cur_group:
                        node_group.append(cur_group[:])
                    cur_group = [node]
                elif re.match(toc_pattern, cur_title_list[-1]):
                    if self._is_toc_node(node):
                        cur_group.append(node)
                    else:
                        node_group.append(cur_group[:])
                        cur_group = [node]
                        cur_title_list = [node._content.strip()]
                else:
                    while cur_title_list:
                        if self.is_parent_child_title(cur_title_list[-1], node._content):
                            node._metadata['title'] = '\n'.join(cur_title_list)
                            cur_title_list.append(node._content.strip())
                            cur_group.append(node)
                            break
                        else:
                            cur_title_list.pop(-1)
                            if cur_group:
                                node_group.append(cur_group[:])
                                cur_group = []
                    if not cur_group:
                        cur_title_list = [node._content.strip()]
                        cur_group = [node]
            elif cur_title_list and re.match(toc_pattern, cur_title_list[-1]):
                # special handling for table of contents
                if self._is_toc_node(node):
                    cur_group.append(node)
                else:
                    node_group.append(cur_group[:])
                    cur_group = [node]
            elif text_type == 'index_text':
                while cur_title_list and not self.is_parent_child_title(
                    cur_title_list[-1], node._content, direct_parent=False
                ):
                    cur_title_list.pop(-1)
                if cur_title_list:
                    node._metadata['title'] = '\n'.join(cur_title_list)
                if cur_group:
                    if cur_group[-1].metadata.get('text_level', 0):
                        cur_group.append(node)
                    else:
                        node_group.append(cur_group[:])
                        cur_group = [node]
            else:
                if cur_title_list:
                    node._metadata['title'] = '\n'.join(cur_title_list)
                cur_group.append(node)

        if cur_group:
            node_group.append(cur_group[:])

        res = []
        for group in node_group:
            res.extend(self._process_group(group, max_length=max_length))
        return res

    def _process_group(self, nodes: List[DocNode], max_length: int = 2048) -> List[List[DocNode]]:
        if sum(len(node._content) for node in nodes) <= max_length:
            return [nodes]
        result = []  # store final grouped results
        current_group = []  # current group being processed
        current_length = 0  # total length of current group

        for node in nodes:
            node_length = len(node._content)  # get length of current node

            # if node length > 2048, further splitting is needed
            if node_length > max_length:
                if current_group:  # if current group has content, save it first
                    result.append(current_group[:])

                # split large node
                split_nodes = self._split_large_node(node)
                for split_node in split_nodes:
                    result.append([split_node])

                current_group = []
                current_length = 0
                continue

            if current_length + node_length > max_length:
                if current_group:
                    result.append(current_group[:])
                current_group = [node]
                current_length = node_length
            else:
                current_group.append(node)
                current_length += node_length

        if current_group:
            result.append(current_group[:])

        return result

    def _split_large_node(self, node: DocNode, max_length: int = 2048) -> List[DocNode]:
        content = node._content
        result_nodes = []

        # try splitting by \n first
        lines = content.split('\n')
        current_chunks = []
        current_length = 0

        for line in lines:
            if len(line) > max_length:
                # save currently accumulated chunks first
                if current_chunks:
                    result_nodes.append(self._create_split_node(current_chunks, node.metadata))
                    current_chunks = []
                    current_length = 0

                # force-split overlong lines
                for i in range(0, len(line), max_length):
                    chunk_text = line[i:i + max_length]
                    result_nodes.append(self._create_split_node([chunk_text], node.metadata))
            elif current_length + len(line) + max(0, len(current_chunks) - 1) > max_length:
                # save current chunks
                if current_chunks:
                    result_nodes.append(self._create_split_node(current_chunks, node.metadata))
                current_chunks = [line]
                current_length = len(line)
            else:
                current_chunks.append(line)
                current_length += len(line)

        if current_chunks:
            result_nodes.append(self._create_split_node(current_chunks, node.metadata))

        return result_nodes

    def _create_split_node(self, current_chunks, metadata):
        """
        Create sub-nodes after splitting
        """
        # merge chunks into text content
        content = '\n'.join(current_chunks)
        metadata = copy.deepcopy(metadata)

        # add caption and footnote for table-type nodes
        is_table = metadata.get('type', 'text') == 'table'
        if is_table:
            table_caption = metadata.get('table_caption', '')
            table_footnote = metadata.get('table_footnote', '')
            if table_caption and not content.lstrip().startswith(table_caption):
                content = f'{table_caption}\n{content}'
            if table_footnote and not content.rstrip().endswith(table_footnote):
                content = f'{content.rstrip()}\n\n{table_footnote}'
            table_image_map = normalize_table_image_map(metadata.get('table_image_map'))
            if table_image_map:
                metadata['table_image_map'] = serialize_table_image_map(
                    [{'content': content, 'image': table_image_map[0]['image']}]
                )

        # create new node, inheriting metadata from original node
        new_node = DocNode(text=content, metadata=copy.deepcopy(metadata))
        new_node.metadata['lines'] = [{
            'content': new_node._content,
            'bbox': new_node.metadata.get('bbox', []),
            'type': new_node.metadata.get('type', 'text'),
            'page': new_node.metadata.get('page', 0),
        }]

        return new_node

    def _extract_heading_numbers(self, title: str) -> list:
        # extract numeric level prefix from heading, e.g. '3.2.1' -> ['3', '2', '1']
        match = re.match(r'^(\d+(?:\.\d+)*)(?=\D|$)', title.strip())
        return match.group(1).split('.') if match else []

    def is_parent_child_title(self, title1: str, title2: str, direct_parent: bool = True) -> bool:
        """
        Determine whether two headings have a parent-child relationship, e.g. '3 xxx' and '3.2 xxx'.
        """

        nums1 = self._extract_heading_numbers(title1)
        nums2 = self._extract_heading_numbers(title2)
        if not nums1 or not nums2:
            return False
        if direct_parent:
            return len(nums2) == len(nums1) + 1 and nums2[:len(nums1)] == nums1
        else:
            return nums2[:len(nums1)] == nums1

    def _is_toc_node(self, node: DocNode) -> bool:

        node_type = node.metadata.get('type')
        if node_type == 'list':
            return True

        if node_type == 'text':
            content = node._content.strip().replace('.', '').replace('·', '')
            # (end with digit or roman number) and length <= 30
            if not content or len(content) > 30:
                return False
            if content[-1].isdigit():
                return True
            if '\u2160' <= content[-1] <= '\u2188' or content[-1] == 'I':
                return True

        return False


class MergeNodeParser(ModuleBase):
    """
    Merge nodes in the same group, merge bbox
    """

    def __init__(self, num_workers: int = 0, return_trace: bool = False, **kwargs):
        super().__init__(return_trace=return_trace, **kwargs)

    def forward(self, document: List[List[DocNode]], **kwargs) -> List[DocNode]:
        return self._parse_nodes(document)

    @classmethod
    def class_name(cls) -> str:
        return 'MergeNodeParser'

    def _parse_nodes(
        self,
        nodes: List[List[DocNode]],
        **kwargs: Any,
    ) -> List[DocNode]:
        res = []

        for group in nodes:
            node = self._process_group(group)
            if node:
                res.append(node)
        return res

    def _process_group(self, nodes: List[DocNode]) -> List[DocNode]:
        if not nodes:
            return None
        metadata = copy.deepcopy(nodes[0].metadata)
        bboxs = []
        context = []
        lines = []
        table_image_map = []
        for node in nodes:
            if not node._content.strip():
                continue
            context.append(node._content)
            if node.metadata.get('table_image_map', None):
                table_image_map = merge_table_image_maps(table_image_map, node.metadata['table_image_map'])
            if node.metadata.get('page', None) is not None and node.metadata.get('bbox', None):
                bboxs.append([node.metadata.get('page')] + node.metadata.get('bbox'))
                lines.append({
                    'content': node._content,
                    'bbox': node.metadata.get('bbox', []),
                    'type': node.metadata.get('type', 'text'),
                    'page': node.metadata.get('page', 0),
                })
        if not context:
            return None
        metadata['lines'] = lines
        if table_image_map:
            metadata['table_image_map'] = serialize_table_image_map(table_image_map)

        if bboxs:
            bbox = self._merge_bbox(bboxs)
            metadata['bbox'] = bbox

        node = DocNode(text='\n'.join(context), metadata=metadata)
        return node

    def _merge_bbox(self, bboxs):
        if not bboxs:
            return None
        page_number = bboxs[0][0]

        x_mins = [b[1] for b in bboxs if b[0] == page_number]
        y_mins = [b[2] for b in bboxs if b[0] == page_number]
        x_maxs = [b[3] for b in bboxs if b[0] == page_number]
        y_maxs = [b[4] for b in bboxs if b[0] == page_number]

        merged_bbox = [min(x_mins), min(y_mins), max(x_maxs), max(y_maxs)]
        return merged_bbox


class GroupFilterNodeParser(ModuleBase):
    def __init__(self, num_workers: int = 0, return_trace: bool = False, **kwargs):
        super().__init__(return_trace=return_trace, **kwargs)

    def forward(self, document: List[List[DocNode]], **kwargs) -> List[List[DocNode]]:
        return self._parse_nodes(document)

    @classmethod
    def class_name(cls) -> str:
        return 'GroupFilterNodeParser'

    def _parse_nodes(self, nodes: List[List[DocNode]], **kwargs: Any) -> List[List[DocNode]]:
        new_nodes = []
        for group in nodes:
            if not group:
                continue
            first_node = group[0]
            if first_node._content.strip() and re.match(r'^目\s{0,4}[次录]$', first_node._content.strip()):
                continue
            new_nodes.append(group)
        return new_nodes


class NodeParser:
    def transform(self, document: DocNode, **kwargs) -> List[Union[str, DocNode]]:
        return

    def batch_forward(self, documents, node_group, **kwargs):
        nodes = self._parse_nodes(documents)
        for node in nodes:
            node._group = node_group
        return nodes

    @staticmethod
    def _parse_nodes(
        nodes,
        **kwargs: Any,
    ) -> List[DocNode]:
        raw_nodes = list(nodes)

        with lazyllm.pipeline() as parser_ppl:
            parser_ppl.clear_parser = NodeTextClear()
            parser_ppl.table_converter = TableConverterNode()
            parser_ppl.layout_parser = LayoutNodeParser()
            parser_ppl.group_nodes = GroupNodeParser()
            parser_ppl.group_filter_nodes = GroupFilterNodeParser()
            parser_ppl.merge_nodes = MergeNodeParser()

        nodes = parser_ppl(nodes)
        # use parallel? text-img
        # can be moved into mineruPdfReader
        extracted_img_nodes = ImageNodeLoader()(raw_nodes)
        nodes.extend(extracted_img_nodes)
        embed_keys = ['file_name', 'title']
        del_keys = ['list_type', 'code_type', 'text_type', 'table_caption', 'table_footnote']
        for ind, node in enumerate(nodes):
            node._metadata['index'] = ind
            for key in del_keys:
                node._metadata.pop(key, None)

            node.excluded_embed_metadata_keys = [key for key in node._metadata.keys() if key not in embed_keys]
            node.excluded_llm_metadata_keys = node.excluded_embed_metadata_keys[:]

        return nodes

    def __call__(self, nodes, **kwargs):
        return self._parse_nodes(nodes, **kwargs)


def parser_code_hash():
    from hashlib import sha256

    parser = NodeParser()
    parser_code = inspect.getsource(parser._parse_nodes)
    parser_hash = sha256((parser_code).encode('utf-8')).hexdigest()
    return parser_hash
