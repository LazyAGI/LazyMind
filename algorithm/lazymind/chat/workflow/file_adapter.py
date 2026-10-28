"""LazyMind Host Attachment to stable Workflow Input Resource adapter."""
from __future__ import annotations

import mimetypes
from dataclasses import dataclass
from pathlib import Path
from typing import Any

from .client import WorkflowClient


@dataclass(frozen=True)
class InputResource:
    resource_id: str
    name: str
    mime_type: str
    size: int
    content_hash: str
    revision: int


class LazyMindHostFileAdapter:
    def __init__(self, base_url: str, user_id: str, *, transport: Any):
        self.client = WorkflowClient(base_url, user_id, host='lazymind', transport=transport)

    def import_attachment(self, path: str) -> InputResource:
        source = Path(path)
        content = source.read_bytes()
        return self._import(source.name, mimetypes.guess_type(source.name)[0] or 'application/octet-stream', content)

    def import_text(self, name: str, value: str) -> InputResource:
        """Import a scalar Workflow binding without pretending it is a Host file."""
        return self._import(name, 'text/plain; charset=utf-8', value.encode('utf-8'))

    def _import(self, name: str, mime_type: str, content: bytes) -> InputResource:
        result = self.client.import_input_resource(
            name,
            mime_type,
            content,
        ).result
        # The Host-private path and transport capability are deliberately discarded here.
        return InputResource(
            resource_id=str(result['resource_id']), name=str(result['name']),
            mime_type=str(result['mime_type']), size=int(result['size']),
            content_hash=str(result['content_hash']), revision=int(result['revision']),
        )
