from fastapi import APIRouter

from lazymind.chat.runtime_loader import chat_runtime_status, rag_runtime_status

router = APIRouter()


def _document_server_check_url(doc_url: str) -> str:
    base_url = doc_url.split(',', 1)[0].strip()
    return base_url.rstrip('/') + '/docs'


@router.get('/health', summary='Health check')
@router.get('/api/health', summary='Health check (API path)')
async def health():
    return {'status': 'ok'}


@router.get('/internal/runtime-status', summary='Get deferred Chat runtime status')
async def runtime_status():
    return {'chat': chat_runtime_status(), 'rag': rag_runtime_status()}
