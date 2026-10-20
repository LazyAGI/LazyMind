"""Bounded MCP loading shared by normal chat and configuration discovery."""
import asyncio

from lazymind.chat.service.mcp_oauth import MCPAuthorizationRequired


_MAX_CONCURRENT_SERVICES = 4


def mcp_allowed_tools(server):
    """None is unrestricted; an explicit empty whitelist must never discover tools.

    OAuth catalogs are grants, so an absent grant whitelist remains fail-closed.
    """
    allowed = server.get('allowed_tools')
    if server.get('id') == 'lazymind-knowledge':
        from lazymind.chat.service.knowledge_management import MANAGEMENT_TOOLS
        return [name for name in MANAGEMENT_TOOLS if allowed is None or name in allowed]
    if allowed is None and (server.get('auth_type') == 'oauth' or 'oauth' in server):
        return []
    return allowed


async def load_mcp_catalog(catalog, loader, *, issues=None):
    semaphore = asyncio.Semaphore(_MAX_CONCURRENT_SERVICES)

    async def load(item):
        tools, error = [], None
        status = item['status']
        runtime = item.get('runtime')
        if runtime and mcp_allowed_tools(runtime) != []:
            async with semaphore:
                try:
                    tools = await asyncio.to_thread(loader, item['runtime'])
                    if not tools:
                        status = 'unavailable'
                except Exception as exc:
                    status = 'needs_authorization' if isinstance(exc, MCPAuthorizationRequired) else 'unavailable'
                    error = type(exc).__name__  # Never propagate credentials from an upstream exception.
        return {'service': item['service'], 'tools': tools, 'status': status, 'error': error}

    tasks = [asyncio.create_task(load(item)) for item in catalog]
    try:
        results = await asyncio.gather(*tasks)
    except BaseException:
        for task in tasks:
            task.cancel()
        await asyncio.gather(*tasks, return_exceptions=True)
        raise
    if issues is not None:
        for item, result in zip(catalog, results):
            if item.get('runtime') and result['status'] != 'ready':
                issues.append({'server': str(item.get('label') or 'MCP'), 'status': result['status']})
    return results
