from __future__ import annotations

import logging
import uuid
from typing import Any, Dict, Optional

from fastapi import APIRouter, HTTPException
from pydantic import BaseModel, Field

from lazymind.router.core.process_manager import get_process_manager
from lazymind.router.core.registry import get_global_registry
from lazymind.router.db.client import AsyncSessionLocal
from lazymind.router.db.models import RouterAlgorithm, RouterChildProcess
from sqlalchemy import select

logger = logging.getLogger(__name__)

router = APIRouter(prefix='/inner/algorithm', tags=['algorithm-management'])


class RegisterAlgorithmRequest(BaseModel):
    id: Optional[str] = Field(None, description='Algorithm ID (auto-generated if omitted)')
    name: str
    code_path: str
    instance_count: int = 1
    config: Dict[str, Any] = Field(default_factory=dict)


@router.post('/register', summary='Register a new algorithm version and start its instances')
async def register_algorithm(req: RegisterAlgorithmRequest):
    algo_id = req.id or str(uuid.uuid4())
    pm = get_process_manager()

    async with AsyncSessionLocal() as session:
        existing = await session.get(RouterAlgorithm, algo_id)
        if existing is None:
            algo = RouterAlgorithm(
                id=algo_id,
                name=req.name,
                code_path=req.code_path,
                config=req.config,
                status='starting',
            )
            session.add(algo)
        else:
            existing.name = req.name
            existing.code_path = req.code_path
            existing.config = req.config
            existing.status = 'starting'
        await session.commit()

    ports = await pm.start_algorithm(
        algo_id=algo_id,
        code_path=req.code_path,
        count=req.instance_count,
        extra_env={k: str(v) for k, v in (req.config or {}).items()},
    )
    await pm.wait_all_healthy(ports)

    # Mark active only after all instances are healthy
    async with AsyncSessionLocal() as session:
        algo = await session.get(RouterAlgorithm, algo_id)
        if algo is not None:
            algo.status = 'active'
            await session.commit()

    return {'algorithm_id': algo_id, 'ports': ports}


@router.delete('/{algorithm_id}', summary='Disable an algorithm and stop its local instances')
async def delete_algorithm(algorithm_id: str):
    pm = get_process_manager()
    async with AsyncSessionLocal() as session:
        algo = await session.get(RouterAlgorithm, algorithm_id)
        if algo is None:
            raise HTTPException(status_code=404, detail=f'Algorithm {algorithm_id!r} not found')
        algo.status = 'disabled'
        await session.commit()

    await pm.stop_algorithm(algorithm_id)
    return {'algorithm_id': algorithm_id, 'status': 'disabled'}


@router.get('', summary='List all algorithms')
async def list_algorithms():
    async with AsyncSessionLocal() as session:
        rows = await session.execute(select(RouterAlgorithm))
        algos = rows.scalars().all()

    return {
        'algorithms': [
            {
                'id': algo.id,
                'name': algo.name,
                'code_path': algo.code_path,
                'status': algo.status,
                'config': algo.config,
            }
            for algo in algos
        ]
    }


@router.get('/{algorithm_id}', summary='Get a single algorithm version detail')
async def get_algorithm(algorithm_id: str):
    registry = get_global_registry()
    async with AsyncSessionLocal() as session:
        algo = await session.get(RouterAlgorithm, algorithm_id)
    if algo is None:
        raise HTTPException(status_code=404, detail=f'Algorithm {algorithm_id!r} not found')

    instances = registry.get_all_instances(algorithm_id)
    return {
        'id': algo.id,
        'name': algo.name,
        'code_path': algo.code_path,
        'status': algo.status,
        'config': algo.config,
        'created_at': algo.created_at.isoformat() if algo.created_at else None,
        'updated_at': algo.updated_at.isoformat() if algo.updated_at else None,
        'instances': [
            {
                'host': i.host,
                'port': i.port,
                'status': i.status,
                'failures': i.failures,
                'instance_id': i.instance_id,
            }
            for i in instances
        ],
    }


@router.post('/{algorithm_id}/restart', summary='Restart all local instances of an algorithm')
async def restart_algorithm(algorithm_id: str):
    pm = get_process_manager()

    async with AsyncSessionLocal() as session:
        rows = await session.execute(
            select(RouterChildProcess).where(
                RouterChildProcess.instance_id == pm.instance_id,
                RouterChildProcess.algorithm_id == algorithm_id,
            )
        )
        children = rows.scalars().all()

    if not children:
        raise HTTPException(
            status_code=404,
            detail=f'No local instances found for algorithm {algorithm_id!r}',
        )

    restarted = []
    for child in children:
        await pm.restart_instance(pm.host, child.port)
        restarted.append(child.port)

    return {'restarted_ports': restarted}
