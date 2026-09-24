"""Google Drive discovery; Feishu/Notion discovery remains owned by Scan."""

import base64
from contextvars import Context
from hashlib import sha256
import json
from typing import Literal

from lazyllm.tools.fs import FS
from pydantic import BaseModel, Field

from .reading import DocumentConnectionRequest, DocumentReadError, document_connection, _browser_url


class DocumentDiscoveryRequest(DocumentConnectionRequest):
    provider: Literal['googledrive']
    folder_id: str = Field(default='', max_length=256, pattern=r'^[A-Za-z0-9_-]*$')
    drive_id: str = Field(default='', max_length=256, pattern=r'^[A-Za-z0-9_-]*$')
    query: str = Field(default='', max_length=1024)
    query_mode: Literal['name', 'full_text'] = 'name'
    page_size: int = Field(default=100, ge=1, le=100)
    page_token: str = Field(default='', max_length=16384)


class DocumentDiscoveryItem(BaseModel):
    document_id: str
    title: str
    type: Literal['file', 'directory']
    source_url: str
    mime_type: str
    parent_ids: list[str]
    read_locator: str | None
    folder_id: str | None


class DocumentDiscoveryResponse(BaseModel):
    source_id: str
    provider: str
    items: list[DocumentDiscoveryItem]
    next_page_token: str | None
    incomplete: bool
    warnings: list[str]


def browse_documents(request: DocumentDiscoveryRequest) -> DocumentDiscoveryResponse:
    if request.query:
        raise DocumentReadError('INVALID_ARGUMENT', 'use search for a keyword query')
    return Context().run(_discover, request, 'browse')


def search_documents(request: DocumentDiscoveryRequest) -> DocumentDiscoveryResponse:
    if not request.query:
        raise DocumentReadError('INVALID_ARGUMENT', 'search query is required')
    return Context().run(_discover, request, 'search')


def _discover(request, operation):
    scope = request.model_dump(exclude={'tool_config', 'page_token', 'algorithm_id'})
    fingerprint = sha256(json.dumps([operation, scope], sort_keys=True).encode()).hexdigest()
    cursor = ''
    if request.page_token:
        try:
            data = json.loads(base64.b64decode(request.page_token, altchars=b'-_', validate=True))
            if data['scope'] != fingerprint or not isinstance(data['cursor'], str) or not data['cursor']:
                raise ValueError('cursor mismatch')
            cursor = data['cursor']
        except (ValueError, KeyError, TypeError):
            raise DocumentReadError('INVALID_ARGUMENT', 'page token does not match this source or query') from None
    with document_connection(request):
        fs = FS._get_or_create_fs('googledrive', None, '/')
        folder = request.folder_id
        if operation == 'browse' and not folder:
            folder = request.drive_id or 'root'
        page = fs.list_page(folder_id=folder, query=request.query, query_mode=request.query_mode,
                            drive_id=request.drive_id, page_size=request.page_size, page_token=cursor)
        items = []
        for entry in page['items']:
            document_id = entry['name']
            directory = entry['type'] == 'directory'
            items.append(DocumentDiscoveryItem(
                document_id=document_id, title=entry.get('title') or '', type=entry['type'],
                source_url=_browser_url(entry.get('web_url')), mime_type=entry.get('mime_type') or '',
                parent_ids=entry.get('parents') or [],
                read_locator=None if directory else f'googledrive:/{document_id}',
                folder_id=document_id if directory else None,
            ))
        next_token = None
        if page['next_page_token']:
            next_token = base64.urlsafe_b64encode(json.dumps({
                'scope': fingerprint, 'cursor': page['next_page_token'],
            }).encode()).decode()
        incomplete = page['incomplete_search']
        warnings = ['Google Drive reported incomplete search results.'] if incomplete else []
        if operation == 'search' and request.query_mode == 'full_text':
            warnings.append('Uses Google Drive fullText matching; coverage depends on Google indexing.')
        return DocumentDiscoveryResponse(source_id=request.source_id, provider=request.provider, items=items,
                                         next_page_token=next_token, incomplete=incomplete, warnings=warnings)
