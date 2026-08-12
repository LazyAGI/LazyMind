from __future__ import annotations

import base64
import hashlib
import json
import os
from typing import Any, Dict

from lazyllm import LOG


def materialize_data_image(data_url: str, workspace_path: str) -> str:
    """Decode a legacy embedded Workflow image for a downstream local tool."""
    header, separator, payload = str(data_url or '').partition(',')
    if (not separator or not header.lower().startswith('data:image/')
            or ';base64' not in header.lower()):
        raise ValueError('Artifact image is not a supported base64 data URL.')
    mime = header[5:].split(';', 1)[0].lower()
    suffix = {
        'image/png': '.png',
        'image/jpeg': '.jpg',
        'image/gif': '.gif',
        'image/webp': '.webp',
    }.get(mime)
    if suffix is None:
        raise ValueError(f'Unsupported artifact image type: {mime or "unknown"}.')
    try:
        content = base64.b64decode(payload, validate=True)
    except Exception as exc:
        raise ValueError('Artifact image contains invalid base64 data.') from exc
    if not content or len(content) > 24 * 1024 * 1024:
        raise ValueError('Artifact image is empty or exceeds the 24 MB safety limit.')
    root = os.path.realpath(str(workspace_path or ''))
    if not root or not os.path.isdir(root):
        raise ValueError('The active step workspace is unavailable.')
    output_dir = os.path.join(root, 'materialized_artifacts')
    os.makedirs(output_dir, exist_ok=True)
    digest = hashlib.sha256(content).hexdigest()[:20]
    output = os.path.join(output_dir, f'image-{digest}{suffix}')
    if not os.path.exists(output):
        with open(output, 'wb') as file:
            file.write(content)
    return output


def compact_artifact_for_prompt(item: Dict[str, Any]) -> Dict[str, Any]:
    """Keep public artifact metadata while omitting binary payloads from LLM context."""
    result = {
        key: item[key]
        for key in (
            'artifact_id', 'slot_id', 'slot', 'content_type', 'revision',
            'list_index', 'caption',
        )
        if key in item
    }
    value = item.get('value')
    content_type = str(item.get('content_type') or '').lower()
    is_binary = content_type in {'image', 'file', 'file_list'} or content_type.startswith(
        ('image/', 'audio/', 'video/', 'application/'),
    )
    if not is_binary:
        result['value'] = value
        return result

    compact: Dict[str, Any] = {}
    if isinstance(value, dict):
        for key in ('filename', 'filenames', 'size', 'sizes', 'caption', '_source_tool'):
            if key in value:
                compact[key] = value[key]
        for key in ('path', 'url', 'paths', 'urls'):
            raw = value.get(key)
            values = raw if isinstance(raw, list) else [raw]
            safe_values = []
            for entry in values:
                text = str(entry or '')
                if text.startswith('data:'):
                    safe_values.append({
                        'embedded_data_omitted': True,
                        'encoded_chars': len(text),
                    })
                elif text:
                    safe_values.append(text[:2048])
            if safe_values:
                compact[key] = safe_values if isinstance(raw, list) else safe_values[0]
    result['value'] = compact
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
        public = [
            compact_artifact_for_prompt(item)
            for item in artifacts
            if isinstance(item, dict)
        ]
        return [
            '## Workflow inputs and artifacts [AUTHORITATIVE public runtime]',
            json.dumps(public, ensure_ascii=False, default=str),
        ]
    except Exception as exc:
        LOG.warning('[Workflow] public Artifact read failed: %s', exc)
        return []
