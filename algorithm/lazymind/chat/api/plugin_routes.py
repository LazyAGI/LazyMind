"""Plugin API routes.

Routes:
    POST /api/plugin/driver              DriverAgent evaluation endpoint (called by Go EventLoop).
    GET  /api/plugin/slot-binding        Slot binding lookup (called by Go OnArtifactEvent).
    GET  /api/plugins                    List all loaded plugins.
    GET  /api/plugins/{plugin_id}        Get plugin spec.
"""
from __future__ import annotations

from typing import Any, Dict, Optional

from fastapi import APIRouter, HTTPException, Query
from pydantic import BaseModel

from lazymind.chat.plugin import plugin_loader
from lazymind.chat.plugin.driver_agent import evaluate_step

router = APIRouter()


class DriverRequest(BaseModel):
    plugin_id: str
    step_id: str
    step_result: str
    session_id: Optional[str] = None


class DriverResponse(BaseModel):
    verdict: str  # PASS | RETRY | DONE | FAIL
    reason: str


@router.post('/api/plugin/driver', response_model=DriverResponse, summary='Evaluate plugin step result')
async def plugin_driver(req: DriverRequest) -> DriverResponse:
    """DriverAgent evaluation endpoint.

    Called by the Go EventLoop after a plugin_step SubAgent reaches terminal status.
    Returns a structured verdict (PASS/RETRY/DONE/FAIL) and optional reason.
    """
    result = evaluate_step(
        plugin_id=req.plugin_id,
        step_id=req.step_id,
        step_result=req.step_result,
        session_id=req.session_id,
    )
    return DriverResponse(
        verdict=result.get('verdict', 'PASS'),
        reason=result.get('reason', ''),
    )


@router.get('/api/plugin/slot-binding', summary='Lookup slot binding for artifact key')
async def slot_binding(
    plugin_id: str = Query(..., description='Plugin identifier'),  # noqa: B008
    artifact_key: str = Query(..., description='Artifact key to look up'),  # noqa: B008
) -> Dict[str, Any]:
    """Return the slot_id and cardinality bound to an artifact key, if any."""
    spec = plugin_loader.get_plugin(plugin_id)
    if spec is None:
        raise HTTPException(status_code=404, detail=f'Plugin {plugin_id!r} not found')

    # Search step outputs in state.yml.
    slot_id: Optional[str] = None
    cardinality = 'single'
    for step_cfg in spec._steps.values():
        for out in step_cfg.get('outputs', []):
            if out.get('artifact_id') == artifact_key:
                slot_id = out.get('slot_id')
                # Cardinality from plugin.yaml UI slot definition.
                if slot_id:
                    slot_def = spec.get_slot_def(slot_id)
                    if slot_def:
                        cardinality = slot_def.get('cardinality', 'single')
                break
        if slot_id:
            break

    return {
        'slot_id': slot_id or '',
        'cardinality': cardinality,
    }


@router.get('/api/plugins', summary='List all loaded plugins')
async def list_plugins() -> Dict[str, Any]:
    """Return summary information for all loaded plugins."""
    return {'plugins': plugin_loader.list_plugins()}


@router.get('/api/plugins/{plugin_id}', summary='Get plugin spec')
async def get_plugin(plugin_id: str) -> Dict[str, Any]:
    """Return the full plugin specification including YAML, state machine, and scenario text."""
    spec = plugin_loader.get_plugin(plugin_id)
    if spec is None:
        raise HTTPException(status_code=404, detail=f'Plugin {plugin_id!r} not found')

    steps_detail = []
    for step in spec.yaml.get('steps', []):
        sid = step.get('id', '')
        steps_detail.append({
            'id': sid,
            'label': step.get('label', ''),
            'config': spec.get_step_config(sid),
        })
    return {
        'id': spec.plugin_id,
        'name': spec.yaml.get('name', spec.plugin_id),
        'description': spec.yaml.get('description', ''),
        'steps': steps_detail,
        'ui': spec.yaml.get('ui', {}),
        'state': spec.state,
    }
