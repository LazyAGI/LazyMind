"""Core's explicit MCP policy must reach the catalog without shared session state."""
import asyncio

import httpx
import pytest
from fastapi import FastAPI

import lazyllm
from lazymind import model_config
from lazymind.chat.api.chat_routes import router
from lazymind.chat.request_scope import request_configuration_scope


def test_catalog_uses_request_system_mcp_policy_and_does_not_inherit_previous_scope():
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
                    'knowledge.document.reparse', 'knowledge.paper.sources', 'knowledge.paper.import',
                }
    try:
        asyncio.run(exercise())
    finally:
        lazyllm.globals['agentic_config'] = previous or {}


def _catalog_availability(response):
    groups = {group['name']: group for group in response.json()['tool_groups']}
    sciverse = next(method for method in groups['academic_search']['methods'] if method['name'] == 'SciverseSearch')
    return {'multimodal': groups['multimodal']['active'], 'sciverse': sciverse['active']}


@pytest.mark.asyncio
@pytest.mark.parametrize('invalid_model', [False, True], ids=['success', 'injection-error'])
async def test_catalog_isolates_model_and_tool_config_across_requests(monkeypatch, tmp_path, invalid_model):
    runtime_models = tmp_path / 'runtime_models.yaml'
    runtime_models.write_text('vlm: {source: dynamic, type: vlm}\n', encoding='utf-8')
    monkeypatch.setattr(model_config, 'get_config_path', lambda: str(runtime_models))
    monkeypatch.setitem(lazyllm.config._impl, 'dynamic_tool_auth', {})

    model = {'source': 'openai', 'model': 'catalog-test-vision', 'api_key': 'catalog-test-model-key'}
    if invalid_model:
        model['request_advanced'] = {'headers': ['invalid']}
    configured = {'llm_config': {'vlm': model}, 'tool_config': {'sciverse': 'catalog-test-sciverse-key'}}
    app = FastAPI()
    app.include_router(router)
    transport = httpx.ASGITransport(app=app, raise_app_exceptions=False)

    with request_configuration_scope() as caller_sid:
        lazyllm.globals['catalog_test_sentinel'] = 'caller-global'
        lazyllm.locals['catalog_test_sentinel'] = 'caller-local'
        async with httpx.AsyncClient(transport=transport, base_url='http://test') as client:
            # Separate request tasks inherit the same caller binding.
            first = await asyncio.create_task(client.post(
                '/api/chat/tools', json=configured, headers={'Accept-Language': 'en-GB'},
            ))
            second = await asyncio.create_task(client.post('/api/chat/tools', json={}))

        if invalid_model:
            assert first.status_code == 500
            assert first.text == 'Internal Server Error'
        else:
            assert first.status_code == 200
            assert _catalog_availability(first) == {'multimodal': True, 'sciverse': True}
            assert first.headers['Content-Language'] == 'en-US'
            assert first.headers['Vary'] == 'Accept-Language'
        assert second.status_code == 200
        assert _catalog_availability(second) == {'multimodal': False, 'sciverse': False}
        assert second.headers['Content-Language'] == 'zh-CN'
        assert second.headers['Vary'] == 'Accept-Language'
        for response in (first, second):
            assert 'catalog-test-model-key' not in response.text
            assert 'catalog-test-sciverse-key' not in response.text
        assert lazyllm.globals._sid == lazyllm.locals._sid == caller_sid
        assert lazyllm.globals['catalog_test_sentinel'] == 'caller-global'
        assert lazyllm.locals['catalog_test_sentinel'] == 'caller-local'
        config = lazyllm.globals['config']
        assert 'vlm' not in config.get('dynamic_model_configs', {})
        assert 'vlm' not in config.get('openai_api_key', {})
        assert 'sciverse' not in config.get('dynamic_tool_auth', {})
