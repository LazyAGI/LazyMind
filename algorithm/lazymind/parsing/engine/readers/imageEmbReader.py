import os
from pathlib import Path
from typing import List, Optional

from lazyllm.common import retry
from lazyllm.thirdparty import fsspec
from lazyllm.tools.rag.doc_node import ImageDocNode
from lazyllm.tools.rag.readers.readerBase import LazyLLMReaderBase, get_default_fs

from lazymind.config import config as _cfg
from lazymind.parsing.engine.utils import normalize_image_file

RETRY_TIMES = 3


class ImageEmbReader(LazyLLMReaderBase):
    __lazyllm_registry_disable__ = True

    def __init__(self, return_trace: bool = True) -> None:
        super().__init__(return_trace=return_trace)

    def _normalized_root(self) -> Path:
        return Path(_cfg['shared_upload_dir']) / 'normalized_images'

    def _normalize_image_file(self, image_path: str) -> str:
        return normalize_image_file(image_path=image_path, normalized_root=self._normalized_root())

    @retry(stop_after_attempt=RETRY_TIMES)
    def _load_data(self, file: Path, fs: Optional['fsspec.AbstractFileSystem'] = None) -> List[ImageDocNode]:
        if not isinstance(file, Path):
            file = Path(file)

        suffix = file.suffix.lower()
        fs = fs or get_default_fs()
        file_name = file.name
        abs_path = os.path.abspath(str(file))
        normalized_path = self._normalize_image_file(abs_path)

        metadata = {
            'source_path': abs_path,
            'normalized_source_path': normalized_path,
            'file_name': file_name,
            'file_ext': suffix,
            'file_type': 'image',
            'is_pure_image': True,
        }
        return [ImageDocNode(image_path=normalized_path, metadata=metadata)]


__all__ = ['ImageEmbReader']
