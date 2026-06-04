from __future__ import annotations

from fastapi import APIRouter

from lazymind.router.core.registry import get_global_registry
from lazymind.router.core.process_manager import get_process_manager

router = APIRouter()


@router.get('/health', summary='Health check (router mode)')
@router.get('/api/health', summary='Health check API path (router mode)')
async def health():
    pm = get_process_manager()
    registry = get_global_registry()
    snapshot = registry.snapshot()

    summary: dict[str, dict] = {}
    for algo_id, instances in snapshot.items():
        summary[algo_id] = {
            'total': len(instances),
            'healthy': sum(1 for i in instances if i.status == 'healthy'),
        }

    return {
        'status': 'ok',
        'instance_id': pm.instance_id,
        'host': pm.host,
        'port_range': list(pm.port_range),
        'algorithms': summary,
    }
