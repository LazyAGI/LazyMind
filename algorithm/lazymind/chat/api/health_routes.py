import httpx
from fastapi import APIRouter

from lazymind.config import config as _cfg

router = APIRouter()


@router.get('/health', summary='Health check')
@router.get('/api/health', summary='Health check (API path)')
async def health():
    doc_url = _cfg['document_server_url']
    check_url = doc_url.rstrip('/') + '/'
    status = {'document_server_url': doc_url, 'document_server_reachable': None}
    try:
        async with httpx.AsyncClient(timeout=3.0) as client:
            await client.get(check_url)
        status['document_server_reachable'] = True
    except Exception as e:
        status['document_server_reachable'] = False
        status['document_server_error'] = str(e)
    return status
