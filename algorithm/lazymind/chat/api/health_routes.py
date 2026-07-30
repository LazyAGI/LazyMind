from fastapi import APIRouter

from lazymind.chat.runtime_loader import chat_runtime_status, rag_runtime_status

router = APIRouter()


@router.get('/health', summary='Health check')
@router.get('/api/health', summary='Health check (API path)')
async def health():
    return {'status': 'ok'}


@router.get('/internal/runtime-status', summary='Get deferred Chat runtime status')
async def runtime_status():
    return {'chat': chat_runtime_status(), 'rag': rag_runtime_status()}
