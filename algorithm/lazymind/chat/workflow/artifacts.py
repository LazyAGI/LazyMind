from __future__ import annotations

import json
from typing import Any, Dict

from lazyllm import LOG


def artifact_inventory(response: Dict[str, Any]) -> Dict[str, Any]:
    """Project artifact metadata without repeatedly injecting bodies into prompts."""
    result = dict(response)
    values = response.get('artifacts') if isinstance(response, dict) else []
    result['artifacts'] = [{
        key: item[key] for key in (
            'artifact_id', 'slot_id', 'slot', 'content_type', 'revision', 'list_index',
            'selected', 'caption', 'created_at',
        ) if key in item
    } for item in (values or []) if isinstance(item, dict)]
    result['content_omitted'] = True
    result['read_hint'] = 'Use get_artifact/read_artifact for an exact slot when content is needed.'
    return result


def build_artifact_context_section(params: Dict[str, Any]) -> list[str]:
    """Read the public Workflow projection and make it safe for an LLM prompt."""
    session_id = str(params.get('session_id') or '').strip()
    if not session_id:
        return []
    try:
        import httpx
        from lazymind.config import config
        from lazymind.workflow_sdk import WorkflowClient

        response = WorkflowClient(
            str(config['core_api_url']).rstrip('/'),
            str(params.get('user_id') or ''),
            host='lazymind',
            transport=httpx,
        ).list_artifacts(session_id).result
        artifacts = response.get('artifacts') if isinstance(response, dict) else []
        if not artifacts:
            return []
        public = artifact_inventory({'artifacts': artifacts})['artifacts']
        return [
            '## Workflow inputs and artifacts [AUTHORITATIVE public runtime]',
            json.dumps(public, ensure_ascii=False, default=str),
        ]
    except Exception as exc:
        LOG.warning('[Workflow] public Artifact read failed: %s', exc)
        return []
