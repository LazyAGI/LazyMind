"""Deterministic smoke fixtures shared by native and MCP execution."""
from __future__ import annotations

import base64
import hashlib
from pathlib import Path
import tempfile
from typing import Any


class SmokeValidationError(ValueError):
    pass


def _summary(summary: str) -> str:
    if not isinstance(summary, str) or not summary.strip() or len(summary.encode('utf-8')) > 1024:
        raise SmokeValidationError('summary must be non-empty text within 1024 UTF-8 bytes.')
    return summary


def _source(summary: str) -> str:
    return f'Workflow smoke test\n{summary}\nrevision-1\n'


def _file(name: str, text: str) -> dict[str, Any]:
    raw = text.encode('utf-8')
    return {'name': name, 'mime_type': 'text/plain', 'content_base64': base64.b64encode(raw).decode(),
            'sha256': hashlib.sha256(raw).hexdigest(), 'size_bytes': len(raw)}


def portable_smoke_generate(summary: str) -> dict[str, Any]:
    """Create smoke-test JSON metadata and source.txt from a short summary."""
    summary = _summary(summary)
    return {'metadata': {'smoke_test': True, 'schema': 'test.v2', 'summary': summary},
            'files': [_file('source.txt', _source(summary))]}


def portable_smoke_update(summary: str, source_text: str) -> dict[str, Any]:
    """Check the saved source text and create two revisions plus two ordered list files."""
    summary = _summary(summary)
    if source_text != _source(summary):
        raise SmokeValidationError('source_text must be the complete saved source.txt content, including newlines.')
    return {'files': [
        _file('revision-1.txt', source_text),
        _file('revision-2.txt', source_text + 'revision-2\n'),
        _file('item-1.txt', 'list-item-1\n'),
        _file('item-2.txt', 'list-item-2\n'),
    ]}


def smoke_verify(summary: str, metadata: dict[str, Any], source_text: str, rewritten_text: str,
                 rewritten_revision: int, list_texts: list[str]) -> dict[str, Any]:
    """Check saved contents, metadata, updated revision number and exact list count/order.

    Supply complete text read from saved files, not paths or reconstructed text.
    rewritten_revision comes from the pinned input's source_revision or saved artifact revision.
    Re-execution can increase the revision number beyond two.
    """
    summary = _summary(summary)
    checks = {
        'metadata': metadata == {'smoke_test': True, 'schema': 'test.v2', 'summary': summary},
        'source': source_text == _source(summary),
        'updated_content': rewritten_text == _source(summary) + 'revision-2\n',
        'updated_revision': type(rewritten_revision) is int and rewritten_revision >= 2,
        'ordered_list': list_texts == ['list-item-1\n', 'list-item-2\n'],
    }
    failures = [name for name, passed in checks.items() if not passed]
    if failures:
        raise SmokeValidationError('Smoke verification failed: ' + ', '.join(failures)
                                   + '. Read the saved artifacts and correct these fields before retrying.')
    return {'status': 'Workflow smoke test passed', 'checks': checks, 'rewritten_revision': rewritten_revision}


def _local_files(result: dict[str, Any]) -> dict[str, Any]:
    root = Path(tempfile.mkdtemp(prefix='workflow-smoke-')).resolve()
    for item in result['files']:
        path = root / item['name']
        path.write_bytes(base64.b64decode(item.pop('content_base64')))
        item['local_path'] = str(path)
    return result


def smoke_generate(summary: str) -> dict[str, Any]:
    """Create smoke-test JSON metadata and a source.txt file; return its local_path."""
    return _local_files(portable_smoke_generate(summary))


def smoke_update(summary: str, source_text: str) -> dict[str, Any]:
    """Check saved source text and return local_path for two revisions and two ordered list files."""
    return _local_files(portable_smoke_update(summary, source_text))
