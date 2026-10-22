"""Native Office decoding for desktop knowledge-base ingestion."""
from pathlib import Path

import lazyllm
from lazyllm.tools.rag.doc_node import DocNode
from lazyllm.tools.rag.readers.readerBase import LazyLLMReaderBase

from lazymind.chat.engine.document_extractor import LocalDocumentReader


class NativeOfficeReader(LazyLLMReaderBase):
    def __init__(self, presentation_reader=None):
        super().__init__()
        self._presentation_reader = presentation_reader

    def _load_data(self, file, extra_info=None, **kwargs):
        # Reuse the lightweight decoder, not the chat attachment wrapper (which
        # truncates long documents). OCR and PDF conversion are not required.
        path = Path(file)
        if path.suffix.lower() == '.docx':
            nodes = LocalDocumentReader._read_docx(path)
        elif path.suffix.lower() == '.pptx':
            route = {**(lazyllm.globals.config['dynamic_ocr_configs'] or {}), **(extra_info or {})}
            if (self._presentation_reader is not None
                    and str(route.get('ocr_type') or '').strip().lower() == 'mineru'
                    and LocalDocumentReader._uses_official_mineru(route)):
                return self._presentation_reader(file, extra_info=extra_info, **kwargs)
            nodes = LocalDocumentReader._read_pptx(path)
        else:
            raise ValueError('Desktop Office reader supports DOCX and PPTX; convert legacy files before uploading.')
        return [
            DocNode(text=node.text, metadata={**(extra_info or {}), **node.metadata})
            for node in nodes
        ]
