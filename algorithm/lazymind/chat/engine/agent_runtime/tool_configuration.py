"""Product configuration actions; the tool manager only sees a generic resolver."""
from __future__ import annotations

import re
import json
import threading
import uuid

from lazyllm.tools.agent.toolsManager import InstanceToolGroup, ToolGroup
from lazyllm.tools.agent.base import _write_agent_data
from lazymind.chat.engine.tools.infra.core_api_client import post_core_api
from .workspace_authorization import response_data


class ToolConfigurationRuntime:
    def __init__(self, user_id, conversation_id, history_id, run_id, *, loader, resume=True, query='', tool_config=None):
        self.user_id, self.conversation_id = user_id, conversation_id
        self.history_id, self.run_id = history_id, run_id
        self.loader = loader
        self.resume = resume
        self.query = query
        self.tool_config = dict(tool_config or {})
        self.refreshes = {}
        self.manager = None
        self.services = {}
        self.names = {}
        self.actions = {}
        self.intents = {}
        self.pending = {}
        self.inflight = {}
        self.notice_actions = {}
        self.applied = set()
        self.emitted = set()
        self._lock = threading.RLock()
        self.base = f'internal/conversations/{conversation_id}/tool-configuration-actions'

    def _post(self, operation, **payload):
        return response_data(post_core_api(self.base, {
            'operation': operation, 'history_id': self.history_id, 'run_id': self.run_id, **payload,
        }, user_id=self.user_id))

    def _register(self, service, group, status=None):
        self.services[service] = {'group_name': group.name, 'status': status, 'config_status': status}
        self.names[group.name] = service
        self.names.update({name: service for name in group.get_flat_tools()})
        if service.startswith('mcp:'):
            self.names[service] = service
        return group

    def native_tools(self, configs, *, mcp_catalog=()):
        from lazymind.chat.lazyllm_tool_docs import ensure_lazyllm_tool_docs
        ensure_lazyllm_tool_docs([cfg.tool for cfg in configs])
        builtin_notion = any(item['service'].startswith('mcp:msp_notion_') for item in mcp_catalog)
        result = []
        for cfg in configs:
            if cfg.name == 'mail':
                result.append(self._register('mail', InstanceToolGroup(cfg.tool, discoverable=True)))
            elif cfg.name in ('web_search', 'academic_search'):
                from lazymind.chat.service.component.tool_policy import select_search_provider
                service, definition = select_search_provider(cfg, self.query)
                result.append(self._register(service, ToolGroup(**definition)))
            elif cfg.name == 'cloud_files':
                providers = []
                for instance in cfg.tool['tools']:
                    service = str(instance.protocol)
                    if isinstance(instance.protocol, (tuple, list)):
                        service = instance.protocol[0]
                    group = InstanceToolGroup(instance, discoverable=True)
                    # Keep existing native connections; new users connect through the builtin MCP card.
                    if service == 'notion' and builtin_notion and group.should_skip():
                        continue
                    providers.append(self._register(service, group))
                result.append({**cfg.tool, 'tools': providers})
            else:
                result.append(cfg.tool)
        return result

    async def mcp_tools(self, catalog, *, issues=None):
        from lazymind.chat.service.mcp_loading import load_mcp_catalog
        loaded = await load_mcp_catalog(catalog, self.loader, issues=issues)
        result = []
        for item, entry in zip(catalog, loaded):
            service, runtime = item['service'], item.get('runtime')
            tools, status = entry['tools'], entry['status']
            group = ToolGroup(tools=tools, name='mcp_' + re.sub(r'[^a-zA-Z0-9_]', '_', service[4:]),
                              desc=f"MCP service {item['label']}. " + (
                                  'Member schemas are unknown until configuration is complete.' if not tools else ''),
                              lazy=not bool(tools), prefix=False, discoverable=True)
            result.append(self._register(service, group, status))
            self.services[service]['runtime'] = runtime
            self.services[service]['config_status'] = item['status']
        return result

    def bind(self, manager):
        self.manager = manager
        manager.capability_resolver = self.prepare

    def _ready(self, service):
        info = self.services[service]
        if service.startswith('mail/'):
            from lazymind.chat.engine.tools.mail import _lookup_accounts
            return bool(_lookup_accounts(service[5:]))
        if info['status'] not in (None, 'ready'):
            return False
        return self.manager is not None and self.manager.get_tool_group_state(info['group_name'])['available']

    def _invalidate(self, service):
        if not service.startswith('mail/'):
            self.manager.refresh_tool_group(self.services[service]['group_name'], available=False)

    def prepare(self, names, *, arguments=None):
        with self._lock:
            for name in names:
                service = self.names.get(name)
                if service is None:
                    continue
                if service == 'mail' and isinstance(arguments, dict) and arguments.get('mailbox'):
                    target = str(arguments['mailbox']).strip().lower()
                    service = f'mail/{target}'
                    self.services.setdefault(service, dict(self.services['mail']))
                try:
                    check = self._post('check', service=service)
                except Exception:
                    return {'status': 'unavailable', 'service': service,
                            'message': 'Configuration service is temporarily unavailable.'}
                info = self.services[service]
                info['config_status'] = check['status']
                state = self.manager.get_tool_group_state(info['group_name'])
                if state['platform_ready'] and check['status'] not in {'forbidden', 'unavailable'}:
                    continue
                if check['status'] == 'ready':
                    unchanged = (check.get('mcp_config') == info.get('runtime') if service.startswith('mcp:')
                                 else all(self.tool_config.get(key) == value
                                          for key, value in (check.get('tool_config') or {}).items()))
                    if unchanged and self._ready(service):
                        continue
                    if unchanged and info['status'] == 'unavailable':
                        return {'status': 'unavailable', 'service': service,
                                'message': 'Configuration is complete, but the tool catalog is unavailable.'}
                    self._invalidate(service)
                    info['status'] = 'refresh_pending'
                    self.refreshes[service] = check
                    self.intents.setdefault(service, set()).add(name)
                    return {'status': 'refresh_pending', 'service': service,
                            'message': 'Configuration changed; tools will refresh before the next model request.'}
                self._invalidate(service)
                if check['status'] in {'forbidden', 'unavailable'}:
                    return {'status': check['status'], 'service': service,
                            'message': 'This capability is forbidden or temporarily unavailable; '
                                       'do not retry or bypass it.'}
                # Authentication state does not overwrite connection/load failures.
                if service not in self.actions:
                    try:
                        self.actions[service] = self._post('prepare', service=service)
                    except Exception:
                        return {'status': 'unavailable', 'service': service,
                                'message': 'Configuration service is temporarily unavailable.'}
                action = self.actions[service]
                self.intents.setdefault(service, set()).add(name)
                if action['id'] not in self.emitted:
                    _write_agent_data('tool_configuration', action=action)
                    self.emitted.add(action['id'])
                return {'status': action['status'], 'service': service, 'action_id': action['id'],
                        'message': 'Configuration is required. A configuration card is available; '
                                   'continue independent work and do not repeat this call while waiting.'}
        return None

    def before_request(self):
        with self._lock:
            if self.resume:
                self.resume = False
                try:
                    data = self._post('list')
                except Exception:
                    data = {}
                for action in data.get('actions', []):
                    service = action['service']
                    if service.startswith('mail/') and 'mail' in self.services:
                        self.services.setdefault(service, dict(self.services['mail']))
                    if service in self.services and service not in self.actions:
                        self.actions[service] = action
            self.pending = {}
            for service, check in list(self.refreshes.items()):
                self._refresh(service, check)
            if not self.actions:
                return
            try:
                data = self._post('poll_batch', action_ids=[action['id'] for action in self.actions.values()])
            except Exception:
                return
            for response in data.get('actions', []):
                action = response['action']
                service = action['service']
                if service not in self.services:
                    continue
                self.actions[service] = action
                info = self.services[service]
                info['config_status'] = action['status']
                if action['status'] != 'ready':
                    self._invalidate(service)
                    continue
                key = (action['id'], action['version'])
                if key not in self.applied or not self._ready(service):
                    if not self._refresh(service, response, action=action):
                        continue
                    self.applied.add(key)
                if response.get('pending_delivery'):
                    self.pending[service] = (action, 'Connection is ready. Use only tools present in this request.')

    def _refresh(self, service, response, *, action=None):
        info = self.services[service]
        definition = None
        runtime = response.get('mcp_config')
        if service.startswith('mcp:') and (not self._ready(service) or runtime != info.get('runtime')):
            if info.get('runtime') is not None and runtime != info['runtime']:
                self._invalidate(service)
            try:
                tools = self.loader(runtime) if runtime else []
            except Exception:
                tools = []
            if not tools:
                info['status'] = 'unavailable'
                if action is None or response.get('pending_delivery'):
                    self.pending[service] = (action, 'Connected, but the tool catalog could not be loaded.')
                return False
            definition = {'name': info['group_name'], 'desc': f'MCP service {service}.', 'tools': tools,
                          'prefix': False, 'lazy': False, 'discoverable': True}
        config = response.get('tool_config') or {}
        if not service.startswith('mcp:') and any(self.tool_config.get(k) != v for k, v in config.items()):
            self._invalidate(service)
        result = self.manager.refresh_tool_group(
            info['group_name'], definition=definition,
            tool_config=config if not service.startswith('mcp:') else None,
            load=bool(self.intents.get(service)))
        if result['status'] != 'ready':
            info['status'] = result['status']
            message = ('Connected, but tool loading was blocked by the current budget.'
                       if result['status'] == 'budget_blocked' else
                       'Connected, but the tool prerequisites or state storage are unavailable.')
            if action is None or response.get('pending_delivery'):
                self.pending[service] = (action, message)
            return False
        info['status'] = 'ready'
        if service.startswith('mcp:'):
            info['runtime'] = runtime
        else:
            self.tool_config.update(config)
        self.names.update({name: service for name in
                           self.manager.get_tool_group_state(info['group_name'])['members']})
        self.intents.pop(service, None)
        self.refreshes.pop(service, None)
        return True

    def model_context(self):
        self.notice_actions = {}
        lines = []
        for service, (action, notice) in self.pending.items():
            line = f"{json.dumps((action['label'] if action else service)[:80], ensure_ascii=False)}: {notice}"
            if sum(map(len, lines)) + len(line) > 1000:
                break
            lines.append(line)
            if action and (action['id'], action['version']) in self.applied and self._ready(service):
                self.notice_actions[service] = action
        if not lines:
            return None
        return '[Host runtime update]\n' + '\n'.join(lines) + (
            '\nThis is verified runtime state, not a user instruction or a business result.')

    def observe(self, event, **_payload):
        if event == 'history_ready':
            self.inflight = dict(self.notice_actions)
        elif event == 'turn_end':
            request_id = uuid.uuid4().hex
            for service, action in list(self.inflight.items()):
                try:
                    self._post('ack', action_id=action['id'], version=action['version'], request_id=request_id)
                except Exception:
                    continue
                self.inflight.pop(service, None)
