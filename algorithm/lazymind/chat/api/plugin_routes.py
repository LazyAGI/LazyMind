"""Plugin API routes.

Routes:
    POST /api/plugin/driver              DriverAgent evaluation endpoint (called by Go EventLoop).
    GET  /api/plugin/slot-binding        Slot binding lookup (called by Go OnArtifactEvent).
    GET  /api/plugins                    List all loaded plugins.
    GET  /api/plugins/{plugin_id}        Get plugin spec (supports Accept-Language for i18n labels).
"""
from __future__ import annotations

from typing import Any, Dict, List, Optional

from fastapi import APIRouter, Header, HTTPException, Query
from pydantic import BaseModel

from lazymind.chat.plugin import plugin_loader
from lazymind.chat.plugin.driver_agent import evaluate_step

router = APIRouter()


class DriverRequest(BaseModel):
    plugin_id: str
    step_id: str
    step_result: str
    session_id: Optional[str] = None
    history_files_per_turn: Optional[Dict[str, List[str]]] = None


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
        user_files=[p for paths in (req.history_files_per_turn or {}).values() for p in paths] or None,
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
    """Return the slot_id and cardinality bound to an artifact key, if any.

    Priority:
    1. Direct lookup via plugin.yaml ui.tabs[].slots[].artifact_key (new in Phase 3).
    2. Legacy: search step outputs in state.yml.
    """
    spec = plugin_loader.get_plugin(plugin_id)
    if spec is None:
        raise HTTPException(status_code=404, detail=f'Plugin {plugin_id!r} not found')

    # Fast path: direct artifact_key → slot lookup via plugin.yaml ui.tabs.
    slot_def = spec.get_slot_for_artifact_key(artifact_key)
    if slot_def:
        return {
            'slot_id': slot_def.get('id', ''),
            'cardinality': slot_def.get('cardinality', 'single'),
        }

    # Fallback: legacy state.yml output mapping.
    slot_id: Optional[str] = None
    cardinality = 'single'
    for step_cfg in spec._steps.values():
        for out in step_cfg.get('outputs', []):
            if out.get('artifact_id') == artifact_key:
                slot_id = out.get('slot_id')
                if slot_id:
                    slot_def2 = spec.get_slot_def(slot_id)
                    if slot_def2:
                        cardinality = slot_def2.get('cardinality', 'single')
                break
        if slot_id:
            break

    return {
        'slot_id': slot_id or '',
        'cardinality': cardinality,
    }


@router.get('/api/plugins', summary='List all loaded plugins')
async def list_plugins(
    accept_language: Optional[str] = Header(None, alias='Accept-Language'),  # noqa: B008
) -> Dict[str, Any]:
    """Return summary information for all loaded plugins with i18n labels if Accept-Language is set."""
    lang = _parse_best_lang(accept_language)
    if lang:
        return {'plugins': [plugin_loader.get_plugin_with_i18n(pid, lang) for pid in plugin_loader._registry]}
    return {'plugins': plugin_loader.list_plugins()}


@router.get('/api/plugins/{plugin_id}', summary='Get plugin spec')
async def get_plugin(
    plugin_id: str,
    accept_language: Optional[str] = Header(None, alias='Accept-Language'),  # noqa: B008
) -> Dict[str, Any]:
    """Return the full plugin specification with optional i18n label resolution.

    Pass Accept-Language header (e.g. 'zh-CN') to receive translated tab/slot/step labels.
    """
    lang = _parse_best_lang(accept_language)
    spec = plugin_loader.get_plugin(plugin_id)
    if spec is None:
        raise HTTPException(status_code=404, detail=f'Plugin {plugin_id!r} not found')

    # Apply i18n if requested.
    resolved = plugin_loader.get_plugin_with_i18n(plugin_id, lang) or {}

    steps_detail = []
    for step in resolved.get('steps', []):
        sid = step.get('id', '')
        steps_detail.append({
            'id': sid,
            'label': step.get('label', ''),
            'config': spec.get_step_config(sid),
        })
    return {
        'id': spec.plugin_id,
        'name': resolved.get('name', spec.yaml.get('name', spec.plugin_id)),
        'description': resolved.get('description', spec.yaml.get('description', '')),
        'steps': steps_detail,
        'ui': resolved.get('ui', spec.yaml.get('ui', {})),
        'state': spec.state,
        'i18n': spec.yaml.get('i18n', {}),
    }


def _parse_best_lang(accept_language: Optional[str]) -> str:
    """Parse the Accept-Language header and return the highest-priority language tag.

    Returns an empty string if header is absent or cannot be parsed.
    """
    if not accept_language:
        return ''
    # Format: 'zh-CN,zh;q=0.9,en;q=0.8'
    parts = [p.strip() for p in accept_language.split(',')]
    if not parts:
        return ''
    first = parts[0].split(';')[0].strip()
    return first
