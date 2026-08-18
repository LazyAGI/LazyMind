import json

from lazyllm.tools.agent.toolsManager import ToolManager
from lazymind.chat.engine.tools.infra.tool_runtime import tool_error


def test_tool_error_builds_canonical_envelope_and_keeps_compatibility():
    result = tool_error(
        'search',
        'upstream timed out',
        category='TRANSIENT_ERROR',
        code='UPSTREAM_TIMEOUT',
        retryable=True,
        recovery_attempts_remaining=1,
        details={'attempt': 1},
    )

    assert result['error'] == {
        'category': 'TRANSIENT_ERROR',
        'code': 'UPSTREAM_TIMEOUT',
        'tool': 'search',
        'message': 'upstream timed out',
        'retryable': True,
        'recovery_attempts_remaining': 1,
        'details': {'attempt': 1},
        'reason': 'upstream timed out',
    }
    assert result['ok'] is False
    assert result['success'] is False
    assert result['value'] is None


def test_tool_manager_exposes_only_canonical_error_fields_to_agent():
    def reported_failure(resource: str):
        '''Read one resource.

        Args:
            resource (str): Resource identifier.
        '''
        return tool_error(
            'reported_failure', 'document not found', code='RESOURCE_NOT_FOUND',
            details={'resource_type': 'document'}, error_type='NotFoundError',
        )

    result = ToolManager([reported_failure])({
        'function': {
            'name': 'reported_failure',
            'arguments': json.dumps({'resource': 'missing'}),
        },
    })[0]

    assert result['error'] == {
        'category': 'DOMAIN_FAILURE',
        'code': 'RESOURCE_NOT_FOUND',
        'tool': 'reported_failure',
        'message': 'document not found',
        'retryable': False,
        'recovery_attempts_remaining': 0,
        'details': {
            'resource_type': 'document',
            'error_type': 'NotFoundError',
        },
    }
