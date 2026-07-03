from fastapi import APIRouter

router = APIRouter()


def _document_server_check_url(doc_url: str) -> str:
    base_url = doc_url.split(',', 1)[0].strip()
    return base_url.rstrip('/') + '/docs'


@router.get('/health', summary='Health check')
@router.get('/api/health', summary='Health check (API path)')
async def health():
    return {'status': 'ok'}
