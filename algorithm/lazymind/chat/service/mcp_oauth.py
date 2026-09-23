"""Request-bound OAuth callbacks for remote MCP sessions.

Only Core supplies the immutable grant identity. Tokens stay inside the HTTP
callback, and every new MCP session checks the grant with auth-service.
"""
from contextvars import ContextVar
from dataclasses import asdict, dataclass
import os

import httpx

from lazymind.config import config


class MCPAuthorizationRequired(RuntimeError):
    status = 'needs_authorization'

    def __init__(self):
        super().__init__('MCP authorization required; reconnect this server in MCP settings.')


class MCPAuthUnavailable(RuntimeError):
    def __init__(self):
        super().__init__('MCP authorization service unavailable; try again later.')


@dataclass(frozen=True)
class _GrantReference:
    user_id: str
    server_id: str
    server_url: str
    grant_id: str
    grant_version: int


class MCPOAuthAdapter:
    def __init__(self, reference: dict, server_url: str):
        try:
            self._reference = _GrantReference(**reference)
        except (TypeError, ValueError):
            raise MCPAuthorizationRequired() from None
        ref = self._reference
        if (not all(isinstance(value, str) and value.strip() for value in
                    (ref.user_id, ref.server_id, ref.server_url, ref.grant_id))
                or type(ref.grant_version) is not int or ref.grant_version < 1
                or ref.server_url != server_url):
            raise MCPAuthorizationRequired()
        # Parallel calls on one tool must reject their own token version, not
        # the version observed by another task or another user's tool.
        self._token_version = ContextVar('mcp_token_version', default=None)

    async def _token(self, rejected_token_version=None):
        internal_token = str(config['core_internal_token']
                             or os.environ.get('LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN')
                             or os.environ.get('AUTH_SERVICE_INTERNAL_TOKEN') or '').strip()
        base_url = os.environ.get('LAZYMIND_AUTH_SERVICE_URL', '').rstrip('/')
        if not base_url or not internal_token:
            raise MCPAuthUnavailable()
        if not base_url.endswith('/api/authservice'):
            base_url += '/api/authservice'
        payload = asdict(self._reference)
        if rejected_token_version is not None:
            payload['rejected_token_version'] = rejected_token_version
        try:
            async with httpx.AsyncClient(timeout=15, follow_redirects=False, trust_env=False) as client:
                response = await client.post(
                    base_url + '/v1/mcp-oauth/token', json=payload,
                    headers={'X-LazyMind-Internal-Token': internal_token},
                )
            body = response.json()
            if response.status_code == 401 and body.get('status') == 'needs_authorization':
                raise MCPAuthorizationRequired()
            if response.status_code != 200:
                raise MCPAuthUnavailable()
            data = body['data']
            if data.get('status') == 'needs_authorization':
                raise MCPAuthorizationRequired()
            if (data.get('status') != 'authorized'
                    or not isinstance(data.get('access_token'), str) or not data['access_token']
                    or type(data.get('token_version')) is not int or data['token_version'] < 1):
                raise MCPAuthUnavailable()
            self._token_version.set(data['token_version'])
            return data['access_token']
        except (MCPAuthorizationRequired, MCPAuthUnavailable):
            raise
        except Exception:
            # Never propagate upstream bodies, tokens, URLs or transport details
            # into chat errors or tool logs.
            raise MCPAuthUnavailable() from None

    async def headers(self):
        return {'Authorization': 'Bearer ' + await self._token()}

    async def recover(self):
        version = self._token_version.get()
        if version is None:
            return False
        await self._token(rejected_token_version=version)
        return True
