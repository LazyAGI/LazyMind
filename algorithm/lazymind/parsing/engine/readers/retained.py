import inspect
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

    def _read_fallback(self, file, extra_info=None, **kwargs):
        # ModuleBase.__call__/forward accept arbitrary kwargs, but the concrete
        # reader's _load_data may not. Require an explicit parameter: generic
        # forwarding functions (e.g. _read_native_text) do not establish support.
        target = self._fallback._load_data if isinstance(self._fallback, LazyLLMReaderBase) else self._fallback
        try:
            parameter = inspect.signature(target).parameters.get('extra_info')
        except (TypeError, ValueError):
            parameter = None
        if parameter is not None and parameter.kind in (
                inspect.Parameter.POSITIONAL_OR_KEYWORD, inspect.Parameter.KEYWORD_ONLY):
            kwargs['extra_info'] = extra_info
        nodes = self._fallback(file, **kwargs)
        # Preserve metadata even when the fallback cannot accept it (or only
        # uses it for routing). Keep reader-produced values on conflicts, just
        # as the retained-artifact branch does, without mutating caller data.
        if extra_info:
            for node in [nodes] if isinstance(nodes, DocNode) else (nodes or []):
                node.metadata = {**extra_info, **node.metadata}
        return nodes

    def _load_data(self, file, extra_info=None, **kwargs):
        route = lazyllm.globals.config['dynamic_ocr_configs'] or {}
        if (Path(file).suffix.lower() == '.pdf'
                and str(route.get('ocr_type') or '').strip().lower() not in ('', 'none')):
            # Explicit OCR selection can enrich native text with layout/images.
            # Never hide that request behind a previous native-only parse.
            return self._read_fallback(file, extra_info=extra_info, **kwargs)
        data = load_artifact(file)
        if data is None:
            return self._read_fallback(file, extra_info=extra_info, **kwargs)
        return [DocNode(text=node['text'], metadata={**(extra_info or {}), **node['metadata']})
                for node in data['roots']]
