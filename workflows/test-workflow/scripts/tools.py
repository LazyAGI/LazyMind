"""Tiny, deterministic local tools for the comprehensive Workflow smoke test."""
from __future__ import annotations

import json
import base64
import tempfile
from pathlib import Path
from typing import Any, Dict, List
from urllib.parse import quote_plus
from urllib.parse import unquote_to_bytes


def _root() -> Path:
    """Create an isolated temporary directory for smoke-test fixtures."""
    return Path(tempfile.mkdtemp(prefix='workflow-smoke-')).resolve()


def build_test_metadata(summary: str) -> Dict[str, Any]:
    """Build deterministic smoke-test metadata from a summary.

    Args:
        summary: Text produced by the prompt step.

    Returns:
        JSON-compatible metadata for the workflow fixture.
    """
    return {'smoke_test': True, 'summary': str(summary), 'schema': 'test.v1'}


def create_typed_fixtures(summary: str) -> Dict[str, str]:
    """Create one text fixture and one image URL fixture.

    Args:
        summary: Text to embed in the generated fixtures.

    Returns:
        Paths and URLs keyed by their fixture names.
    """
    root = _root()
    text_path = root / 'attachment.txt'
    text_path.write_text(f'Workflow smoke test\n{summary}\n', encoding='utf-8')
    label = quote_plus(f'Workflow Smoke Test | {str(summary)[:48]}')
    image_url = f'https://placehold.co/640x360/2563eb/ffffff.png?text={label}'
    return {'text_path': str(text_path), 'image_url': image_url}


def create_rewrite_fixture(summary: str) -> str:
    """Create the first revision of a rewrite fixture.

    Args:
        summary: Text to include in the fixture.

    Returns:
        Absolute path to the created text file.
    """
    path = _root() / 'rewritten.txt'
    path.write_text(f'revision-1\n{summary}\n', encoding='utf-8')
    return str(path)


def rewrite_fixture(path: str, marker: str) -> str:
    """Append a revision marker to an existing fixture.

    Args:
        path: Path to the fixture to update.
        marker: Revision marker to append.

    Returns:
        Absolute path to the updated fixture.
    """
    target = Path(path).resolve()
    previous = target.read_text(encoding='utf-8')
    target.write_text(f'{previous}{marker}\n', encoding='utf-8')
    return str(target)


def create_list_fixtures() -> List[str]:
    """Create two ordered list fixture files.

    Returns:
        Absolute paths in their intended display order.
    """
    root = _root()
    paths = []
    for index in (1, 2):
        path = root / f'list-{index}.txt'
        path.write_text(f'list-item-{index}\n', encoding='utf-8')
        paths.append(str(path))
    return paths


def _fixture_bytes(value: str) -> bytes:
    """Read fixture content from a path, data URL, or inline text value."""
    raw = str(value)
    if raw.startswith('data:') and ',' in raw:
        header, payload = raw.split(',', 1)
        return base64.b64decode(payload) if ';base64' in header else unquote_to_bytes(payload)
    path = Path(raw)
    if path.is_file():
        return path.read_bytes()
    return raw.encode('utf-8')


def _fixture_strings(value: Any) -> List[str]:
    """Flatten strings from Runtime artifact wrappers and their selected items."""
    if isinstance(value, dict):
        result: List[str] = []
        for nested in value.values():
            result.extend(_fixture_strings(nested))
        return result
    if isinstance(value, list):
        result = []
        for nested in value:
            result.extend(_fixture_strings(nested))
        return result
    raw = str(value)
    path = Path(raw)
    if path.is_file() and path.suffix.lower() == '.json':
        try:
            return _fixture_strings(json.loads(path.read_text(encoding='utf-8')))
        except (OSError, ValueError):
            pass
    return [raw]


def _fixture_texts(value: Any) -> List[str]:
    """Decode every readable text candidate in a Runtime artifact wrapper."""
    texts = []
    for candidate in _fixture_strings(value):
        try:
            texts.append(_fixture_bytes(candidate).decode('utf-8'))
        except (OSError, ValueError, UnicodeDecodeError):
            continue
    return texts


def _wrapper_text(value: Any) -> str:
    """Return the serialized Runtime wrapper for structural smoke assertions."""
    raw = str(value)
    path = Path(raw)
    if path.is_file():
        try:
            return path.read_text(encoding='utf-8')
        except (OSError, UnicodeDecodeError):
            return ''
    return json.dumps(value, ensure_ascii=False, default=str)


def verify_fixtures(text_path: str, image_url: str, rewritten_path: str,
                    list_paths: List[str]) -> Dict[str, str]:
    """Verify every fixture produced by the smoke-test workflow.

    Args:
        text_path: Path to the generated text attachment.
        image_url: Generated image fixture URL.
        rewritten_path: Path to the twice-written fixture.
        list_paths: Ordered paths for the list fixtures.

    Returns:
        Final status and verification report path.
    """
    text_values = _fixture_texts(text_path)
    rewritten_values = _fixture_texts(rewritten_path)
    listed = [text for path in list_paths for text in _fixture_texts(path)]
    image_values = _fixture_strings(image_url)
    image_wrapper = _wrapper_text(image_url)
    list_wrapper = '\n'.join(_wrapper_text(path) for path in list_paths)
    checks = {
        'text': any('Workflow smoke test' in text for text in text_values),
        'image': any(
            value.startswith('https://placehold.co/640x360/') for value in image_values
        ) or 'image_attachment' in image_wrapper,
        'rewrite': any('revision-2' in text for text in rewritten_values),
        'list': {'list-item-1\n', 'list-item-2\n'}.issubset(set(listed)) or (
            'item-2' in list_wrapper or 'list-2.txt' in list_wrapper
        ),
    }
    if not all(checks.values()):
        raise ValueError(f'workflow smoke verification failed: {checks}')
    report = _root() / 'verification.json'
    report.write_text(json.dumps(checks, indent=2, sort_keys=True), encoding='utf-8')
    return {'status': 'Workflow smoke test passed', 'report_path': str(report)}


__all__ = [
    'build_test_metadata',
    'create_typed_fixtures',
    'create_rewrite_fixture',
    'rewrite_fixture',
    'create_list_fixtures',
    'verify_fixtures',
]
