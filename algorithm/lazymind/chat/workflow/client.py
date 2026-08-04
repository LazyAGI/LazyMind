"""Typed client for the Workflow Tool facade.

All algorithm/chat Workflow HTTP traffic belongs here.  The legacy transport is
kept behind an explicit rollback flag and records every fallback invocation.
"""
from __future__ import annotations

import logging
import os
from typing import Any, Dict

import httpx
from lazymind.workflow_sdk import client as _sdk

CONTRACT_VERSION = _sdk.CONTRACT_VERSION
AdvanceRequest = _sdk.AdvanceRequest
StepCommand = _sdk.StepCommand
WorkflowClient = _sdk.WorkflowClient
WorkflowClientError = _sdk.WorkflowClientError
WorkflowResponse = _sdk.WorkflowResponse

LOG = logging.getLogger(__name__)
workflow_legacy_client_hits = 0
WorkflowTransportTimeout = httpx.TimeoutException


def workflow_http_get(url: str, **kwargs: Any) -> Any:
    """Compatibility transport boundary for old read endpoints."""
    record_legacy_client_call('GET ' + url.rsplit('/', 1)[-1])
    transport = kwargs.pop('transport', httpx)
    return transport.get(url, **kwargs)


def workflow_http_post(url: str, **kwargs: Any) -> Any:
    """Compatibility transport boundary for old command endpoints."""
    record_legacy_client_call('POST ' + url.rsplit('/', 1)[-1])
    transport = kwargs.pop('transport', httpx)
    return transport.post(url, **kwargs)


def use_legacy_client() -> bool:
    return os.getenv('LAZYMIND_WORKFLOW_CLIENT_V1', 'true').lower() not in {'1', 'true', 'yes', 'on'}


def record_legacy_client_call(caller: str) -> None:
    global workflow_legacy_client_hits
    workflow_legacy_client_hits += 1
    LOG.warning('workflow_legacy_client_call caller=%s count=%d deletion_gate=zero_calls',
                caller, workflow_legacy_client_hits)


_FORBIDDEN_ATTEMPT_KEYS = {
    'llm_config', 'model_config', 'tool_config', 'api_key', 'token',
    'history_files_per_turn', 'local_path', 'temporary_url', 'host_prompt',
}


def sanitize_attempt_context(context: Dict[str, Any]) -> Dict[str, Any]:
    """Fail closed when Host-private values are about to cross the Runtime boundary."""
    def clean(value: Any, key: str = '') -> Any:
        if key.lower() in _FORBIDDEN_ATTEMPT_KEYS:
            raise WorkflowClientError('ATTEMPT_CONTEXT_PRIVATE_DATA', f'forbidden field: {key}')
        if isinstance(value, dict):
            return {name: clean(item, name) for name, item in value.items()}
        if isinstance(value, list):
            return [clean(item, key) for item in value]
        if isinstance(value, str) and (value.startswith('/') or value.startswith('file://')):
            raise WorkflowClientError('ATTEMPT_CONTEXT_PRIVATE_DATA', f'private path in field: {key}')
        return value

    return clean(context)
