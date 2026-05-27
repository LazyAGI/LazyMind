from __future__ import annotations

import os
from typing import Any, Dict, List
from urllib.parse import urlparse

from lazyllm import LOG, ModuleBase
from lazyllm.components.formatter import encode_query_with_filepaths

from lazymind.chat.engine.tools.infra import extract_text_from_model_output

_IMAGE_DESCRIBE_PROMPT = (
    "Given the user query and the attached image(s), return one concise plain-text sentence "
    "that captures the image information most relevant to answering the query."
)

_REMOTE_SCHEMES = ("http", "https", "file")


class QueryImageRewriter(ModuleBase):
    """Augment the user query with VLM-derived descriptions of attached images.

    Inaccessible local paths are silently skipped (with a warning); remote URLs
    are passed through untouched. When all paths are filtered out, the query
    is returned unchanged so the downstream pipeline behaves as if no images
    were attached.

    Uses a VLM (vision-language model) so image bytes are understood; a plain LLM
    role cannot consume ``encode_query_with_filepaths`` multimodal payloads.
    """

    def __init__(self, vlm: Any, return_trace: bool = False, **kwargs):
        super().__init__(return_trace=return_trace, **kwargs)
        self.vlm = vlm

    def _extract_paths(self, payload: Dict[str, Any]) -> List[str]:
        paths = payload.get("image_files") or []
        if not isinstance(paths, list):
            return []
        return [str(path).strip() for path in paths if str(path).strip()]

    @staticmethod
    def _is_remote(path: str) -> bool:
        try:
            parsed = urlparse(path)
        except Exception:
            return False
        return parsed.scheme in _REMOTE_SCHEMES

    def _filter_accessible_paths(self, paths: List[str]) -> List[str]:
        valid: List[str] = []
        for p in paths:
            if self._is_remote(p):
                valid.append(p)
                continue
            if os.path.isfile(p) and os.access(p, os.R_OK):
                valid.append(p)
            else:
                LOG.warning(f"[QueryImageRewriter] skip inaccessible image path: {p}")
        return valid

    def forward(self, input: Dict[str, Any], **kwargs) -> Dict[str, Any]:
        payload = dict(input or {})
        query = str(payload.get("query", "")).strip()
        image_paths = self._filter_accessible_paths(self._extract_paths(payload))
        if not query or not image_paths:
            return payload

        prompt_query = f"User query: {query}\nInstruction: {_IMAGE_DESCRIBE_PROMPT}"
        encoded_query = encode_query_with_filepaths(prompt_query, image_paths)
        priority = payload.get("priority", 0)
        vlm_output = self.vlm(
            encoded_query,
            stream_output=False,
            llm_chat_history=[],
            lazyllm_files=None,
            priority=priority,
        )
        image_desc = extract_text_from_model_output(vlm_output)
        if image_desc:
            payload["query"] = f"{query}\nImage context: {image_desc}"
        return payload
