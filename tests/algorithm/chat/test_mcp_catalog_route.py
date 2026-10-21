"""Core's explicit MCP policy must reach the catalog without shared session state."""
import asyncio

import httpx
from fastapi import FastAPI

import lazyllm
from lazymind.chat.api.chat_routes import router


def test_catalog_uses_request_system_mcp_policy_and_does_not_inherit_previous_scope(monkeypatch):
    from lazymind import model_config
    from lazymind.chat.engine import tool_auth
    monkeypatch.setattr(model_config, 'inject_model_config', lambda _: None)
    monkeypatch.setattr(tool_auth, 'inject_tool_config', lambda _: None)
    app = FastAPI()
    app.include_router(router)
    policy = [{'id': 'lazymind-knowledge', 'allowed_tools': ['knowledge.create']}]
    previous = lazyllm.globals.get('agentic_config')
    lazyllm.globals['agentic_config'] = {'system_mcp_config': policy}

    async def exercise():
        async with httpx.AsyncClient(transport=httpx.ASGITransport(app=app), base_url='http://test') as client:
            for body, expected in (({'system_mcp_config': policy}, True),
                                   ({'system_mcp_config': []}, False), ({}, False)):
                response = await client.post('/api/chat/tools', json=body)
                assert response.status_code == 200
                group = next(item for item in response.json()['tool_groups']
                             if item['name'] == 'knowledge_management')
                assert group['active'] is expected
                assert {method['name'] for method in group['methods']} == {
                    'knowledge.list', 'knowledge.document.list', 'knowledge.document.aggregate',
                    'knowledge.create', 'knowledge.folder.create', 'knowledge.resource.update_metadata',
                    'knowledge.resource.move', 'knowledge.resource.delete', 'knowledge.document.upload',
                    'knowledge.document.reparse',
                }
    try:
        asyncio.run(exercise())
    finally:
        lazyllm.globals['agentic_config'] = previous or {}
