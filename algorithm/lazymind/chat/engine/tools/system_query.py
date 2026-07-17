from __future__ import annotations

from typing import Any, Dict

from lazymind.chat.engine.tools.infra import get_core_api, handle_tool_errors, tool_success


@handle_tool_errors
def list_data_sources(keyword: str = '') -> Dict[str, Any]:
    """List configured data-source providers available to the current user.

    Use this only when the user asks which data-source providers or integrations
    are configured. Do not call it to answer which tools, toolkits, or general
    capabilities are available; it does not provide a tool catalog.

    Use ExternalDatabaseToolkit to inspect database connections; this tool only
    reports provider services that can supply data to LazyMind.
    """
    params = {'category': 'datasource'}
    if keyword:
        params['keyword'] = keyword
    groups = get_core_api('/model_providers/provider_groups', params=params).get('groups', [])
    return tool_success('list_data_sources', {'provider_groups': groups})
