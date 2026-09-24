"""Bounded MCP loading shared by normal chat and configuration discovery."""
import asyncio

from lazymind.chat.service.mcp_oauth import MCPAuthorizationRequired


_MAX_CONCURRENT_SERVICES = 4


async def load_mcp_catalog(catalog, loader, *, issues=None):
    semaphore = asyncio.Semaphore(_MAX_CONCURRENT_SERVICES)

    async def load(item):
        tools, error = [], None
        status = item['status']
        if item.get('runtime'):
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
