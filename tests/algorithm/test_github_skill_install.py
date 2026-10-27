"""Chat installation uses Core's shared importer and authenticated RemoteFS transport."""
import json
from unittest.mock import patch

import lazyllm
import pytest
import requests
from lazyllm.tools.agent import ToolExecutionError

from lazymind.chat.engine.tools.skill_editor import SkillManagementToolkit
from lazymind.common.integrations.remote_fs import RemoteFS
from lazymind.common.skill.remote_store import SkillRemoteStore


def _response(payload, status=200):
    response = requests.Response()
    response.status_code = status
    response._content = json.dumps(payload).encode()
    return response


def test_chat_install_sends_one_authenticated_atomic_request(monkeypatch):
    monkeypatch.setattr(lazyllm, 'globals', {'agentic_config': {'user_id': 'user-1', 'session_id': 'chat-1'}})
    store = SkillRemoteStore(fs=RemoteFS(base_url='https://core.example/api/core'))
    result = {'status': 'installed', 'skill_id': 'skill-1', 'skill_key': 'external/example',
              'source_url': 'https://skills.sh/owner/repo/example', 'enabled': False}
    with patch('lazymind.common.integrations.remote_fs.requests.request', return_value=_response(result)) as request:
        installed = SkillManagementToolkit(store=store).install_skill(result['source_url'])
    assert installed['skill_key'] == 'external/example'
    assert installed['enabled'] is False
    request.assert_called_once()
    assert request.call_args.args == ('POST', 'https://core.example/api/core/remote-fs/skills:install')
    assert request.call_args.kwargs['params'] == {'user_id': 'user-1', 'task_id': 'chat-1'}
    assert request.call_args.kwargs['json'] == {'url': result['source_url']}
    assert request.call_args.kwargs['timeout'] == 360


def test_chat_install_reuses_existing_enabled_skill_without_disabling_it():
    store = SkillRemoteStore(fs=RemoteFS(base_url='https://core.example/api/core'))
    result = {'status': 'already_installed', 'skill_id': 'skill-1', 'skill_key': 'external/example',
              'source_url': 'https://github.com/owner/repo', 'enabled': True}
    with patch('lazymind.common.integrations.remote_fs.requests.request', return_value=_response(result)) as request:
        installed = SkillManagementToolkit(store=store).install_skill(result['source_url'])
    assert installed['status'] == 'already_installed'
    assert installed['enabled'] is True
    assert 'already installed' in installed['message']
    request.assert_called_once()


def test_chat_install_surfaces_import_limit_without_cleanup_requests():
    store = SkillRemoteStore(fs=RemoteFS(base_url='https://core.example/api/core'))
    response = _response({'message': 'skill package download exceeds 104857600 bytes'}, 413)
    with patch('lazymind.common.integrations.remote_fs.requests.request', return_value=response) as request:
        with pytest.raises(ToolExecutionError, match='104857600'):
            SkillManagementToolkit(store=store).install_skill('https://github.com/owner/repo')
    request.assert_called_once()
