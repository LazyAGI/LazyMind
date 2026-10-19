from __future__ import annotations

import re
from pathlib import PurePosixPath
from typing import Any


PROTOCOL_VERSION = 1
HEADER_BYTES = 8
MAX_REQUEST_BYTES = 64 * 1024 * 1024
MAX_RESPONSE_BYTES = 4 * 1024 * 1024
SUPPORTED_RUNTIMES = ('python', 'bash', 'node', 'cli')
ENV_NAME = re.compile(r'^[A-Za-z_][A-Za-z0-9_]*$')
DOMAIN_NAME = re.compile(
    r'^(?:\*\.)?(?=.{1,253}$)(?:[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?\.)+'
    r'[A-Za-z]{2,63}$'
)
FORBIDDEN_ENV = frozenset({
    'BASH_ENV', 'ENV', 'HOME', 'IFS', 'LD_LIBRARY_PATH', 'LD_PRELOAD',
    'NODE_OPTIONS', 'PATH', 'PERL5OPT', 'PYTHONHOME', 'PYTHONPATH',
    'RUBYOPT', 'SHELLOPTS', 'TMPDIR',
})


def safe_relative_path(value: Any, label: str, *, allow_dot: bool = False) -> str:
    raw = str(value or '').strip().replace('\\', '/')
    if allow_dot and raw in ('', '.'):
        return '.'
    path = PurePosixPath(raw)
    if not raw or path.is_absolute() or any(part in ('', '.', '..') for part in path.parts):
        raise ValueError(f'{label} must be a relative POSIX path inside the Skill package.')
    return path.as_posix()


def safe_output_pattern(value: Any, label: str = 'declared output') -> str:
    raw = str(value or '').strip().replace('\\', '/')
    if not raw or raw.startswith('/') or '\x00' in raw:
        raise ValueError(f'{label} must be a relative POSIX glob inside the Skill workspace.')
    path = PurePosixPath(raw)
    if any(part in ('', '.', '..') for part in path.parts):
        raise ValueError(f'{label} must be a relative POSIX glob inside the Skill workspace.')
    return path.as_posix()


def normalize_domains(value: Any) -> list[str]:
    if not isinstance(value, list):
        raise ValueError('allowed_domains must be a list of domain names.')
    result = []
    for item in value:
        domain = str(item or '').strip().rstrip('.').lower()
        if not DOMAIN_NAME.fullmatch(domain):
            raise ValueError(f'Invalid allowed domain: {item!r}')
        if domain not in result:
            result.append(domain)
    return result


def error_result(error_type: str, message: str, **fields: Any) -> dict[str, Any]:
    return {
        'status': 'error',
        'error_type': error_type,
        'reason_code': error_type,
        'error': message,
        'stdout': '',
        'stderr': message,
        'exit_code': None,
        'output_files': [],
        **fields,
    }
