import httpx
from fastapi import APIRouter

from lazymind.config import config as _cfg

router = APIRouter()


def _document_server_check_url(doc_url: str) -> str:
    base_url = doc_url.split(',', 1)[0].strip()
    return base_url.rstrip('/') + '/docs'


@router.get('/health', summary='Health check')
@router.get('/api/health', summary='Health check (API path)')
async def health():
    doc_url = _cfg['document_server_url']
    check_url = _document_server_check_url(doc_url)
    status = {'document_server_url': doc_url, 'document_server_reachable': None}
    try:
        async with httpx.AsyncClient(timeout=3.0) as client:
            await client.get(check_url)
        status['document_server_reachable'] = True
    except Exception as e:
        status['document_server_reachable'] = False
        status['document_server_error'] = str(e)
    return status
