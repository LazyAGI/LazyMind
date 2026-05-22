import asyncio
import importlib
import importlib.util
import sys
from pathlib import Path
from types import ModuleType, SimpleNamespace


def _load_chat_routes_module():
    module_name = 'test_chat_routes_isolated'
    module_path = Path(__file__).resolve().parents[3] / 'algorithm/chat/app/api/chat_routes.py'
    spec = importlib.util.spec_from_file_location(module_name, module_path)
    module = importlib.util.module_from_spec(spec)
    sys.modules.pop(module_name, None)
    assert spec.loader is not None
    spec.loader.exec_module(module)
    return module


def test_chat_route_forwards_parameters_and_stream_flag(monkeypatch):
    recorded = {}

    async def fake_handle_chat(**kwargs):
        recorded.update(kwargs)
        return {'ok': True}

    fake_service = ModuleType('chat.app.core.chat_service')
    fake_service.handle_chat = fake_handle_chat
    monkeypatch.setitem(sys.modules, 'chat.app.core.chat_service', fake_service)
    module = _load_chat_routes_module()
    request = SimpleNamespace(url=SimpleNamespace(path='/api/chat/stream'))

    result = asyncio.run(
        module.chat(
            query='hello',
            history=[{'role': 'user', 'content': 'hi'}],
            session_id='sid-1',
            filters={'scope': 'all'},
            files=['a.txt'],
            debug=True,
            reasoning=True,
            databases=[{'name': 'db'}],
            dataset='algo',
            priority=9,
            request=request,
        )
    )

    assert result == {'ok': True}
    assert recorded == {
        'query': 'hello',
        'history': [{'role': 'user', 'content': 'hi'}],
        'session_id': 'sid-1',
        'filters': {'scope': 'all'},
        'files': ['a.txt'],
        'debug': True,
        'reasoning': True,
        'databases': [{'name': 'db'}],
        'dataset': 'algo',
        'priority': 9,
        'is_stream': True,
        'available_tools': None,
        'available_skills': None,
        'memory': None,
        'user_preference': None,
        'use_memory': True,
        'user_id': '',
        'trace': False,
        'model_config': None,
        'environment_context': None,
    }

def test_chat_route_forwards_user_id(monkeypatch):
    recorded = {}

    async def fake_handle_chat(**kwargs):
        recorded.update(kwargs)
        return {'ok': True}

    fake_service = ModuleType('chat.app.core.chat_service')
    fake_service.handle_chat = fake_handle_chat
    monkeypatch.setitem(sys.modules, 'chat.app.core.chat_service', fake_service)
    module = _load_chat_routes_module()
    request = SimpleNamespace(url=SimpleNamespace(path='/api/chat'))

    result = asyncio.run(
        module.chat(
            query='hello',
            session_id='sid-1',
            user_id='user-42',
            request=request,
        )
    )

    assert result == {'ok': True}
    assert recorded['user_id'] == 'user-42'
