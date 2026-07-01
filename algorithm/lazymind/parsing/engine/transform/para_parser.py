import copy
from dataclasses import dataclass
from typing import Callable, List, Optional, Sequence, Tuple
from pathlib import Path
from pydantic import Field, PrivateAttr

from lazyllm.tools.rag import NodeTransform
from lazyllm.module import ModuleBase
from lazyllm.tools.rag import DocNode


def split_by_regex(regex: str) -> Callable[[str], List[str]]:
    import re

    return lambda text: re.findall(regex, text)


def split_by_sentence_tokenizer() -> Callable[[str], List[str]]:
    import nltk

    tokenizer = nltk.tokenize.PunktSentenceTokenizer()

    # get the spans and then return the sentences
    # using the start index of each span
    # instead of using end, use the start of the next span if available
    def split(text: str) -> List[str]:
        spans = list(tokenizer.span_tokenize(text))
        sentences = []
        for i, span in enumerate(spans):
            start = span[0]
            if i < len(spans) - 1:
                end = spans[i + 1][0]
            else:
                end = len(text)
            sentences.append(text[start:end])

        return sentences

    return split


def split_text_keep_separator(text: str, separator: str) -> List[str]:
    parts = text.split(separator)
    result = [separator + s if i > 0 else s for i, s in enumerate(parts)]
    return [s for s in result if s]


def split_by_sep(sep: str, keep_sep: bool = True) -> Callable[[str], List[str]]:
    if keep_sep:
        return lambda text: split_text_keep_separator(text, sep)
    else:
        return lambda text: text.split(sep)


def split_by_char() -> Callable[[str], List[str]]:
    return lambda text: list(text)


SENTENCE_CHUNK_OVERLAP = 200
CHUNKING_REGEX = r'[^。？?！!.\n]+[。？?！!.\n]*|[。？?！!.\n]+'
DEFAULT_PARAGRAPH_SEP = '\n\n\n'

DEFAULT_CHUNK_SIZE = 1024


@dataclass
class _Split:
    text: str  # the split text
    is_sentence: bool  # save whether this is a full sentence
    token_size: int  # token length of split text


class NormalLineSplitter(NodeTransform):
    def __init__(self, **kwargs):
        super().__init__()

    def sig_fields(self) -> dict:
        # No content-affecting parameters; behavior is fully determined by class identity.
        return {}

    def _split_para(self, para_list):
        out_para = []
        for index in range(len(para_list)):
            if len(para_list[index]) > 10 or index == len(para_list) - 1:
                out_para.append(para_list[index])
            else:
                para_list[index + 1] = f'{para_list[index]}{para_list[index + 1]}'

        return out_para

    def _split_text(self, text: str) -> List[str]:
        def _split_fun(text: str):
            fun = split_by_regex(CHUNKING_REGEX)
            return fun(text)

        text_list = _split_fun(text=text)
        return self._split_para(text_list)

    def forward(self, document: DocNode, **kwargs) -> List[DocNode]:
        result = []
        nodes = document if isinstance(document, list) else [document]
        for node in nodes:
            metadata = node.metadata
            global_metadata = node.global_metadata
            split_text = self._split_text(node.text)
            result.extend([
                DocNode(
                    text=text,
                    metadata=copy.deepcopy(metadata),
                    global_metadata=copy.deepcopy(global_metadata),
                )
                for text in split_text
            ])
        return result


class ParagraphSplitter(ModuleBase):
    chunk_size: int = Field(
        default=DEFAULT_CHUNK_SIZE,
        description='The token chunk size for each chunk.',
        gt=0,
    )
    chunk_overlap: int = Field(
        default=SENTENCE_CHUNK_OVERLAP,
        description='The token overlap of each chunk when splitting.',
        gte=0,
    )
    separator: str = Field(default=' ', description='Default separator for splitting into words')
    paragraph_separator: str = Field(default=DEFAULT_PARAGRAPH_SEP, description='Separator between paragraphs.')
    secondary_chunking_regex: str = Field(
        default=CHUNKING_REGEX, description='Backup regex for splitting into sentences.'
    )

    _chunking_tokenizer_fn: Callable[[str], List[str]] = PrivateAttr()
    _tokenizer: Callable = PrivateAttr()
    _split_fns: List[Callable] = PrivateAttr()
    _sub_sentence_split_fns: List[Callable] = PrivateAttr()

    def __init__(
        self,
        separator: str = ' ',
        num_workers: int = 0,
        chunk_size: int = DEFAULT_CHUNK_SIZE,
        chunk_overlap: int = SENTENCE_CHUNK_OVERLAP,
        tokenizer: Optional[Callable] = None,
        paragraph_separator: str = DEFAULT_PARAGRAPH_SEP,
        chunking_tokenizer_fn: Optional[Callable[[str], List[str]]] = None,
        secondary_chunking_regex: str = CHUNKING_REGEX,
        include_metadata: bool = True,
        include_prev_next_rel: bool = True,
        id_func: Optional[Callable[[int, DocNode], str]] = None,
        **kwargs,
    ):
        if chunk_overlap > chunk_size:
            raise ValueError(
                f'Got a larger chunk overlap ({chunk_overlap}) than chunk size " f"({chunk_size}), should be smaller.'
            )
        self.chunk_size = chunk_size or DEFAULT_CHUNK_SIZE
        self.chunk_overlap = chunk_overlap or SENTENCE_CHUNK_OVERLAP
        self.separator = separator or ' '
        self.paragraph_separator = paragraph_separator or DEFAULT_PARAGRAPH_SEP
        self.secondary_chunking_regex = secondary_chunking_regex or CHUNKING_REGEX
        self._chunking_tokenizer_fn = chunking_tokenizer_fn or split_by_sentence_tokenizer()
        self._tokenizer = tokenizer or (lambda x: x)

        self._split_fns = [
            self._split_para,
        ]

        self._sub_sentence_split_fns = [
            self._split_line,
        ]
        super().__init__(**kwargs)

    @classmethod
    def class_name(cls) -> str:
        return 'ParagraphSplitter'

    def split_text(self, text: str) -> List[str]:
        return self._split_text(text, chunk_size=self.chunk_size)

    def _run_component(self, document: Sequence[DocNode], **kwargs) -> List[DocNode]:
        result = []
        for node in document:
            metadata = copy.deepcopy(node.metadata)
            split_text = self.split_text(node.text)
            result.extend(split_text)
        return [DocNode(text=text, metadata=metadata) for text in result]

    # def _parse_nodes(
    #     self, nodes: Sequence[DocNode], show_progress: bool = False, **kwargs: Any
    # ) -> List[DocNode]:
    #     all_nodes: List[DocNode] = []
    #     nodes_with_progress = get_tqdm_iterable(nodes, show_progress, 'Parsing nodes')
    #     for node in nodes_with_progress:
    #         splits = self.split_text(node.get_content())

    #         all_nodes.extend(
    #             build_nodes_from_splits(splits, node, id_func=self.id_func)
    #         )

    #     return all_nodes

    def _split_text(self, text: str, chunk_size: int) -> List[str]:
        """
        _Split incoming text and return chunks with overlap size.

        Has a preference for complete sentences, phrases, and minimal overlap.
        """
        if text == '':
            return [text]

        splits = self._split(text, chunk_size)
        chunks = self._merge(splits, chunk_size)

        return chunks

    def _split_para(self, text: str) -> List[_Split]:
        fun = split_by_sep(self.paragraph_separator, keep_sep=True)
        para_list = fun(text)

        out_para = []
        for index in range(len(para_list)):
            if len(para_list[index]) > 10 or index == len(para_list) - 1:
                out_para.append(para_list[index])
            else:
                para_list[index + 1] = f'{para_list[index]}{para_list[index + 1]}'

        return out_para

    def _get_sub_split_text(self, text):
        _split_fns = [
            split_by_regex(self.secondary_chunking_regex),
            split_by_regex(r'[^》）\)、]+[》）\)、]?'),
            split_by_sep(self.separator),
            split_by_char(),
        ]

        for split_fn in _split_fns:
            splits = split_fn(text)
            if len(splits) > 1:
                return splits

    def _split_line(self, text: str) -> List[_Split]:
        splits = self._get_sub_split_text(text=text)

        def _merge_list(splits, chunk_size):
            combined_strings = []
            current_chunk = ''

            for string in splits:
                if len(current_chunk) + len(string) <= chunk_size:
                    current_chunk += string
                else:
                    combined_strings.append(current_chunk)
                    current_chunk = string

            # process the last chunk
            if current_chunk:
                combined_strings.append(current_chunk)

            return combined_strings

        new_splits = []
        for split in splits:
            if len(split) < self.chunk_size:
                new_splits.append(split)
            else:
                new_splits.extend(self._get_sub_split_text(text=split))

        return _merge_list(splits=new_splits, chunk_size=self.chunk_size)

    def _split(self, text: str, chunk_size: int) -> List[_Split]:
        r"""Break text into splits that are smaller than chunk size.

        The order of splitting is:
        1. split by paragraph separator
        2. split by chunking tokenizer (default is nltk sentence tokenizer)
        3. split by second chunking regex (default is "[^,\.;]+[,\.;]?")
        4. split by default separator (" ")

        """
        token_size = self._token_size(text)
        if self._token_size(text) <= chunk_size:
            return [_Split(text, is_sentence=True, token_size=token_size)]

        text_splits_by_fns, is_sentence = self._get_splits_by_fns(text)

        text_splits = []
        for text_split_by_fns in text_splits_by_fns:
            token_size = self._token_size(text_split_by_fns)
            if token_size <= chunk_size:
                text_splits.append(
                    _Split(
                        text_split_by_fns,
                        is_sentence=is_sentence,
                        token_size=token_size,
                    )
                )
            else:
                recursive_text_splits = self._split(text_split_by_fns, chunk_size=chunk_size)
                text_splits.extend(recursive_text_splits)
        return text_splits

    def _merge(self, splits: List[_Split], chunk_size: int) -> List[str]:
        """Merge splits into chunks."""
        chunks: List[str] = []
        cur_chunk: List[Tuple[str, int]] = []  # list of (text, length)
        last_chunk: List[Tuple[str, int]] = []
        cur_chunk_len = 0
        new_chunk = True
        cur_chunk_size = chunk_size + self.chunk_overlap

        def close_chunk() -> None:
            nonlocal cur_chunk, last_chunk, cur_chunk_len, new_chunk

            chunks.append(''.join([text for text, length in cur_chunk]))
            last_chunk = cur_chunk
            pre_overlap = int(cur_chunk_len / 5)
            cur_chunk = []
            cur_chunk_len = 0
            new_chunk = True

            # cur_chunk_size = chunk_size + self.chunk_overlap + pre_overlap

            if len(last_chunk) > 0:
                last_index = len(last_chunk) - 1

                while last_index >= 0:
                    if cur_chunk_len + last_chunk[last_index][1] <= self.chunk_overlap + pre_overlap:
                        text, length = last_chunk[last_index]
                        cur_chunk_len += length
                        cur_chunk.insert(0, (text, length))
                        last_index -= 1
                    else:
                        if cur_chunk_len < self.chunk_overlap:
                            text, length = last_chunk[last_index]
                            text = text[length - (self.chunk_overlap + pre_overlap - cur_chunk_len):]
                            sub_splits = self._get_sub_split_text(text=text)
                            if len(sub_splits) > 1:
                                text = ''.join(sub_splits[1:])
                            else:
                                text = sub_splits[0]

                            length = len(text)
                            cur_chunk_len += length
                            cur_chunk.insert(0, (text, length))
                            last_index -= 1
                        break

        while len(splits) > 0:
            cur_split = splits[0]
            if cur_split.token_size > cur_chunk_size:
                raise ValueError('Single token exceeded chunk size')
            if cur_chunk_len + cur_split.token_size > cur_chunk_size and not new_chunk:
                # if adding split to current chunk exceeds chunk size: close out chunk
                close_chunk()
            else:
                if (
                    cur_split.is_sentence
                    or cur_chunk_len + cur_split.token_size <= cur_chunk_size
                    or new_chunk  # new chunk, always add at least one split
                ):
                    # add split to chunk
                    cur_chunk_len += cur_split.token_size
                    cur_chunk.append((cur_split.text, cur_split.token_size))
                    splits.pop(0)
                    new_chunk = False
                else:
                    # close out chunk
                    close_chunk()

        # handle the last chunk
        if not new_chunk:
            chunk = ''.join([text for text, length in cur_chunk])
            chunks.append(chunk)

        # run postprocessing to remove blank spaces
        return self._postprocess_chunks(chunks)

    def _postprocess_chunks(self, chunks: List[str]) -> List[str]:
        """Post-process chunks.
        Remove whitespace only chunks and remove leading and trailing whitespace.
        """
        new_chunks = []
        for chunk in chunks:
            stripped_chunk = chunk.strip()
            if stripped_chunk == '':
                continue
            new_chunks.append(stripped_chunk)
        return new_chunks

    def _token_size(self, text: str) -> int:
        return len(self._tokenizer(text))

    def _get_splits_by_fns(self, text: str) -> Tuple[List[str], bool]:
        for split_fn in self._split_fns:
            splits = split_fn(text)
            if len(splits) > 1:
                return splits, True

        for split_fn in self._sub_sentence_split_fns:
            splits = split_fn(text)
            if len(splits) > 1:
                break

        return splits, False


class MineruLineSplitter(NodeTransform):
    def __init__(self, **kwargs):
        super().__init__()

    def sig_fields(self) -> dict:
        return {}

    def forward(self, document: DocNode, **kwargs) -> List[DocNode]:
        result = []
        nodes = document if isinstance(document, list) else [document]
        for node in nodes:
            _metadata = copy.deepcopy(node.metadata)
            global_metadata = copy.deepcopy(node.global_metadata)
            lines = _metadata.pop('lines', [])
            for line in lines:
                metadata = {'type': line.get('type', 'text'), 'page': line.get('page', 0), 'bbox': line.get('bbox', [])}
                result.append(DocNode(text=line.get('content', ''),
                                      metadata=_metadata | metadata,
                                      global_metadata=copy.deepcopy(global_metadata)))
        return result


class LineSplitter(NodeTransform):
    def __init__(self, **kwargs):
        super().__init__()
        self._normal_spliter = NormalLineSplitter()
        self._mineru_spliter = MineruLineSplitter()

    def sig_fields(self) -> dict:
        return {}

    def forward(self, document: DocNode, **kwargs) -> List[DocNode]:
        result = []
        nodes = document if isinstance(document, list) else [document]
        for node in nodes:
            file_type = Path(node.global_metadata.get('file_name', '')).suffix
            if file_type.lower() == '.pdf' and node.metadata.get('lines'):
                result.extend(self._mineru_spliter(node))
            else:
                result.extend(self._normal_spliter(node))
        return result
