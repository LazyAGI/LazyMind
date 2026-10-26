from pathlib import Path
import lazyllm
from lazyllm.tools.rag.doc_node import DocNode
from lazyllm.tools.rag.readers.readerBase import LazyLLMReaderBase
from lazymind.common.base_ingestion import load_artifact


class RetainedReader(LazyLLMReaderBase):
    """Reuse native roots, then let the regular model-aware transforms run."""
    def __init__(self, fallback):
        super().__init__()
        # This reader validates the durable artifact's source hash itself.
        self.use_cache(False)
        self._fallback = fallback

    def _load_data(self, file, extra_info=None, **kwargs):
        route = lazyllm.globals.config['dynamic_ocr_configs'] or {}
        if (Path(file).suffix.lower() == '.pdf'
                and str(route.get('ocr_type') or '').strip().lower() not in ('', 'none')):
            # Explicit OCR selection can enrich native text with layout/images.
            # Never hide that request behind a previous native-only parse.
            return self._fallback(file, extra_info=extra_info, **kwargs)
        data = load_artifact(file)
        if data is None:
            return self._fallback(file, extra_info=extra_info, **kwargs)
        return [DocNode(text=node['text'], metadata={**(extra_info or {}), **node['metadata']})
                for node in data['roots']]
