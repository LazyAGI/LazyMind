"""Trusted internal connection for the built-in vocabulary query tools."""
from lazymind.config import config


BUILTIN_ID = 'lazymind-vocabulary'
TOOLS = ('vocabulary.wordbook.list', 'vocabulary.word.list')


def bind_vocabulary_server(server, agentic_config):
    if server.get('id') != BUILTIN_ID:
        return server
    user_id = str(agentic_config.get('user_id') or '')
    token = str(config['core_internal_token'] or '')
    requested = server.get('allowed_tools')

    async def headers():
        if not user_id or not token:
            raise RuntimeError('Vocabulary tools require an authenticated user and internal service credentials')
        return {'Authorization': 'Bearer ' + token, 'X-User-Id': user_id}

    return {
        **server, 'url': str(config['core_api_url']).rstrip('/') + '/internal/mcp/vocabulary',
        'transport': 'streamable-http', 'result_mode': 'structured', 'headers': None,
        'allowed_tools': [name for name in TOOLS if requested is None or name in requested],
        'auth_provider': headers,
    }
