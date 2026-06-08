from __future__ import annotations

import shlex
from typing import Any, Dict, Optional
from urllib.parse import unquote, urlparse, urlsplit, urlunsplit

from sqlalchemy.engine import URL


DEFAULT_SQLALCHEMY_POSTGRES_DRIVER = 'psycopg2'


def normalize_postgres_sqlalchemy_url(
    url: str,
    *,
    driver: str = DEFAULT_SQLALCHEMY_POSTGRES_DRIVER,
    normalize_psycopg3: bool = True,
    canonicalize_postgres_alias: bool = True,
) -> str:
    normalized = url.strip()
    if not normalized:
        raise RuntimeError('postgres connection url is required')

    parts = urlsplit(normalized)
    scheme = (parts.scheme or '').lower()
    postgres_scheme = 'postgresql' if canonicalize_postgres_alias else scheme
    if scheme in {'postgresql', 'postgres'}:
        return urlunsplit((f'{postgres_scheme}+{driver}', parts.netloc, parts.path, parts.query, parts.fragment))
    if scheme in {'postgresql+psycopg', 'postgres+psycopg'} and driver == 'psycopg2' and normalize_psycopg3:
        return urlunsplit((f'{postgres_scheme.split("+", 1)[0]}+{driver}',
                           parts.netloc, parts.path, parts.query, parts.fragment))
    if scheme.startswith('postgres+'):
        if not canonicalize_postgres_alias:
            return normalized
        dialect = scheme.split('+', 1)[1]
        return urlunsplit((f'postgresql+{dialect}', parts.netloc, parts.path, parts.query, parts.fragment))
    if scheme.startswith('postgresql+'):
        return normalized
    raise RuntimeError(f'unsupported database scheme for postgres connection: {parts.scheme}')


def postgres_dsn_to_sqlalchemy_url(
    dsn: str,
    *,
    driver: str = DEFAULT_SQLALCHEMY_POSTGRES_DRIVER,
    normalize_psycopg3: bool = True,
    canonicalize_postgres_alias: bool = True,
) -> str:
    if '://' in dsn:
        return normalize_postgres_sqlalchemy_url(
            dsn,
            driver=driver,
            normalize_psycopg3=normalize_psycopg3,
            canonicalize_postgres_alias=canonicalize_postgres_alias,
        )

    parts: Dict[str, str] = {}
    for token in shlex.split(dsn):
        if '=' not in token:
            continue
        key, value = token.split('=', 1)
        parts[key.strip()] = value.strip()
    if not parts:
        raise ValueError('invalid database dsn')
    if not (parts.get('host') or '').strip():
        raise ValueError('database host is required')
    database = (parts.get('dbname') or parts.get('database') or '').strip()
    if not database:
        raise ValueError('database name is required')
    try:
        port = int(parts['port']) if parts.get('port') else 5432
    except ValueError as exc:
        raise ValueError('invalid database port') from exc
    return str(URL.create(
        f'postgresql+{driver}',
        username=parts.get('user') or None,
        password=parts.get('password') or None,
        host=parts['host'],
        port=port,
        database=database,
    ))


def normalize_postgres_connection_url(
    *,
    url: Optional[str] = None,
    dsn: Optional[str] = None,
    driver: str = DEFAULT_SQLALCHEMY_POSTGRES_DRIVER,
    normalize_psycopg3: bool = True,
    canonicalize_postgres_alias: bool = True,
) -> str:
    if dsn and dsn.strip():
        return postgres_dsn_to_sqlalchemy_url(
            dsn,
            driver=driver,
            normalize_psycopg3=normalize_psycopg3,
            canonicalize_postgres_alias=canonicalize_postgres_alias,
        )
    if url and url.strip():
        return normalize_postgres_sqlalchemy_url(
            url,
            driver=driver,
            normalize_psycopg3=normalize_psycopg3,
            canonicalize_postgres_alias=canonicalize_postgres_alias,
        )
    raise RuntimeError('postgres connection config is required')


def parse_postgres_url_to_db_config(url: Optional[str], *, default_db_name: str = 'app') -> Optional[Dict[str, Any]]:
    if not url or not url.strip():
        return None
    try:
        parsed = urlparse(url)
        db_type = (parsed.scheme or 'postgresql').split('+')[0]
        if db_type != 'postgresql':
            raise ValueError(f'unsupported database scheme: {parsed.scheme or db_type}')
        if not parsed.hostname:
            raise ValueError('database host is required')
        return {
            'db_type': 'postgresql',
            'user': unquote(parsed.username) if parsed.username else '',
            'password': unquote(parsed.password) if parsed.password else '',
            'host': parsed.hostname or '',
            'port': parsed.port or 5432,
            'db_name': (parsed.path or '/').lstrip('/') or default_db_name,
        }
    except (AttributeError, TypeError) as exc:
        raise ValueError('invalid database url') from exc
