"""Read authorized cloud documents without a model, workflow or artifact store.

The trusted caller owns connection/tenant authorization and credential refresh.
This boundary accepts exactly one credential and never fetches credentials itself.
"""

from __future__ import annotations

from contextvars import Context
from contextlib import contextmanager
from hashlib import sha256
from typing import Literal
from urllib.parse import urlsplit
from uuid import uuid4

import lazyllm
import requests
from lazyllm.common import KeyAuthError
from lazyllm.common.globals import new_session
from lazyllm.tools.tool_config_inject import inject_tool_config
from lazyllm.tools.fs import fs_read_limits, FSReadLimitError
from lazyllm.tools.writer.provider import match_writer_provider
from lazyllm.tools.writer.utils.export import export_writer_document
from pydantic import BaseModel, ConfigDict, Field, SecretStr

MAX_CONTENT_CHARS = 2_000_000


class DocumentReadError(Exception):
    def __init__(self, code: str, message: str):
        super().__init__(code, message)
        self.code = code
        self.message = message

    def __str__(self) -> str:
        return self.message


class DocumentConnectionRequest(BaseModel):
    model_config = ConfigDict(extra='forbid', str_strip_whitespace=True)

    user_id: str = Field(min_length=1, max_length=256)
    tenant_id: str = Field(max_length=256)
    source_id: str = Field(min_length=1, max_length=256)
    provider: Literal['feishu', 'notion', 'googledrive']
    tool_config: dict[str, SecretStr] = Field(repr=False)
    algorithm_id: str | None = Field(default=None, max_length=256)


class DocumentReadRequest(DocumentConnectionRequest):
    locator: str = Field(min_length=1, max_length=4096)
    offset: int = Field(default=0, ge=0, le=MAX_CONTENT_CHARS)
    limit: int = Field(default=20000, ge=1, le=100000)
    expected_version: str | None = Field(default=None, max_length=80)


class DocumentReadResponse(BaseModel):
    source_id: str
    provider: str
    document_id: str
    title: str
    source_url: str
    read_locator: str
    content: str
    content_format: Literal['markdown'] = 'markdown'
    original_format: str
    version: str
    offset: int
    total_chars: int
    next_offset: int | None
    warnings: list[str]


def read_document(request: DocumentReadRequest) -> DocumentReadResponse:
    # A fresh context also preserves a calling Chat/Workflow session on return.
    return Context().run(_read_document, request)


@contextmanager
def document_connection(request: DocumentConnectionRequest):
    if set(request.tool_config) != {request.provider}:
        raise DocumentReadError('INVALID_ARGUMENT', 'exactly one matching provider credential is required')
    token = request.tool_config[request.provider].get_secret_value().strip()
    if not token:
        raise DocumentReadError('AUTH_REQUIRED', 'a connection credential is required')
    with new_session(f'document-read-{uuid4().hex}'), fs_read_limits():
        lazyllm.globals.config['dynamic_fs_auth'] = {}
        lazyllm.globals.config['dynamic_tool_auth'] = {}
        inject_tool_config({request.provider: token})
        try:
            yield
        except DocumentReadError:
            raise
        except FSReadLimitError:
            raise DocumentReadError('RESOURCE_LIMIT_EXCEEDED', 'cloud read exceeded its transfer budget') from None
        except MemoryError:
            raise DocumentReadError('RESOURCE_LIMIT_EXCEEDED', 'cloud document requires too much memory') from None
        except (KeyAuthError, PermissionError):
            raise DocumentReadError(
                'ACCESS_DENIED', 'connection credentials or document permissions were rejected') from None
        except FileNotFoundError:
            raise DocumentReadError('NOT_FOUND', 'document is unavailable') from None
        except NotImplementedError:
            raise DocumentReadError('UNSUPPORTED', 'this document format is not supported') from None
        except (requests.Timeout, TimeoutError):
            raise DocumentReadError('TIMEOUT', 'document read timed out') from None
        except requests.HTTPError as exc:
            status = exc.response.status_code if exc.response is not None else 0
            code = {400: 'INVALID_ARGUMENT', 401: 'ACCESS_DENIED', 403: 'ACCESS_DENIED',
                    404: 'NOT_FOUND', 422: 'INVALID_ARGUMENT', 429: 'RATE_LIMITED'}.get(
                        status, 'PROVIDER_UNAVAILABLE')
            raise DocumentReadError(code, 'cloud document request failed') from None
        except ValueError:
            raise DocumentReadError('INVALID_ARGUMENT', 'invalid document or directory locator') from None
        except Exception:
            raise DocumentReadError('PROVIDER_UNAVAILABLE', 'document could not be read or exported') from None


def _read_document(request: DocumentReadRequest) -> DocumentReadResponse:
    if request.offset and not request.expected_version:
        raise DocumentReadError('INVALID_ARGUMENT', 'expected_version is required for subsequent pages')
    try:
        provider = match_writer_provider(request.locator)
        if provider.provider != request.provider:
            raise ValueError('provider mismatch')
        provider.require_capability('load')
        target = provider.resolve(request.locator)
    except (ValueError, NotImplementedError):
        raise DocumentReadError('INVALID_ARGUMENT', 'locator does not identify a supported document') from None

    with document_connection(request):
        loaded = provider.load_document(target)
        return _normalize(loaded, request)


def _browser_url(value: str | None) -> str:
    try:
        parsed = urlsplit(value or '')
        if parsed.scheme == 'https' and parsed.hostname and not parsed.username and not parsed.password:
            return value or ''
    except ValueError:
        pass
    return ''


def _normalize(loaded: dict, request: DocumentReadRequest) -> DocumentReadResponse:
    target = loaded['target_document']
    source = loaded['source_document']
    representation = loaded['representation']
    if loaded['provider'] != request.provider or not target.doc_id:
        raise DocumentReadError('INVALID_PROVIDER_RESULT', 'provider returned an invalid document identity')
    if representation == 'ir':
        try:
            content = export_writer_document(source, 'markdown')
        except ValueError:
            raise DocumentReadError(
                'UNSUPPORTED', 'document contains unsupported or invalid structured content') from None
    elif representation == 'markdown' and isinstance(source, str):
        content = source
    else:
        raise DocumentReadError('INVALID_PROVIDER_RESULT', 'provider returned an unsupported representation')
    if len(content) > MAX_CONTENT_CHARS:
        raise DocumentReadError('CONTENT_TOO_LARGE', 'document exceeds the supported content size')

    # Stable output hash works even when the platform has no revision field.
    identity = '\0'.join((request.tenant_id, request.user_id, request.source_id,
                          request.provider, target.doc_id, target.title or '', content))
    version = 'sha256:' + sha256(identity.encode()).hexdigest()
    if request.expected_version and request.expected_version != version:
        raise DocumentReadError('VERSION_CHANGED', 'document or source changed; restart from the first page')
    if request.offset > len(content):
        raise DocumentReadError('INVALID_ARGUMENT', 'offset exceeds document length')
    end = min(request.offset + request.limit, len(content))
    warnings = list(loaded.get('resource_warnings') or [])
    source_url = _browser_url(target.meta.get('browser_url')) or _browser_url(target.uri)
    if not target.title:
        warnings.append('Document title is unavailable.')
    if not source_url:
        warnings.append('A browser source URL is unavailable; use read_locator to read again.')
    return DocumentReadResponse(
        source_id=request.source_id, provider=request.provider, document_id=target.doc_id,
        title=target.title or '', source_url=source_url, read_locator=target.uri or request.locator,
        content=content[request.offset:end], original_format=target.meta.get('content_format') or representation,
        version=version, offset=request.offset, total_chars=len(content),
        next_offset=end if end < len(content) else None, warnings=warnings,
    )
