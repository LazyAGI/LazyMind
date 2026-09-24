"""Product configuration actions; the tool manager only sees a generic resolver."""
from __future__ import annotations

import re
import copy
import lazyllm
import json
import threading
import uuid

from lazyllm.tools.agent.toolsManager import InstanceToolGroup, ToolGroup
from lazyllm.tools.agent.base import _write_agent_data
from lazymind.chat.engine.tools.infra.core_api_client import post_core_api
from lazyllm.tools.tool_config_inject import inject_tool_config, TOOL_AUTH_REGISTRY
from .workspace_authorization import response_data


class ToolConfigurationRuntime:
    def __init__(self, user_id, conversation_id, history_id, run_id, *, loader, resume=True, query=''):
        self.user_id, self.conversation_id = user_id, conversation_id
        self.history_id, self.run_id = history_id, run_id
        self.loader = loader
        self.resume = resume
        self.query = query
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
        self.services[service] = {'group': group, 'status': status}
        self.names[group._name] = service
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
                definition = {**cfg.tool, 'discoverable': True, 'prefix': cfg.tool.get('prefix')}
                service = cfg.name
                for provider in cfg.tool['tools']:
                    source = provider.source_name
                    if re.search(r'(?:使用|通过|用|using\s+|with\s+|via\s+)\s*' + re.escape(source)
                                 + r'(?![a-zA-Z0-9_])', self.query, re.IGNORECASE):
                        definition['tools'] = [provider]
                        service = f'{service}/{source}'
                        break
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

    def mcp_tools(self, catalog, *, issues=None):
        from lazymind.chat.service.mcp_oauth import MCPAuthorizationRequired

        result = []
        for item in catalog:
            service = item['service']
            runtime = item.get('runtime')
            try:
                tools = self.loader(runtime) if runtime else []
                status = item['status'] if not runtime or tools else 'unavailable'
            except Exception as exc:
                tools = []
                status = 'needs_authorization' if isinstance(exc, MCPAuthorizationRequired) else 'unavailable'
                if issues is not None:
                    issues.append({'server': str(item.get('label') or 'MCP'), 'status': status})
                lazyllm.LOG.warning(f'[MCP] skipped service {service}: {status}')
            group = ToolGroup(tools=tools, name='mcp_' + re.sub(r'[^a-zA-Z0-9_]', '_', service[4:]),
                              desc=f"MCP service {item['label']}. " + (
                                  'Member schemas are unknown until configuration is complete.' if not tools else ''),
                              lazy=not bool(tools), prefix=False, discoverable=True)
            result.append(self._register(service, group, status))
            self.services[service]['runtime'] = runtime
        return result

    def bind(self, manager):
        self.manager = manager
        manager.capability_resolver = self.prepare

    def _ready(self, service):
        info = self.services[service]
        if service.startswith('mail/'):
            from lazymind.chat.engine.tools.mail import _lookup_accounts
            return bool(_lookup_accounts(service[5:]))
        if info['status'] is not None:
            return info['status'] == 'ready'
        group = info['group']
        if group._pick_first_valid:
            return group._selected_provider() is not None
        return not group.should_skip()

    def prepare(self, names, *, arguments=None):
        with self._lock:
            for name in names:
                service = self.names.get(name)
                if service is None:
                    continue
                if service == 'mail' and isinstance(arguments, dict) and arguments.get('mailbox'):
                    target = str(arguments['mailbox']).strip().lower()
                    service = f'mail/{target}'
                    self.services.setdefault(service, self.services['mail'])
                try:
                    check = self._post('check', service=service)
                except Exception:
                    return {'status': 'unavailable', 'service': service,
                            'message': 'Configuration service is temporarily unavailable.'}
                selected = self.services[service]['group']._selected_provider() if (
                    self.services[service]['group']._pick_first_valid) else None
                instance = getattr(selected[0], '_instance', None) if selected else None
                platform_ready = instance is not None and (
                    getattr(instance, '_skip_auth', False) or not getattr(instance, '_dynamic_auth', True))
                if platform_ready and check['status'] not in {'forbidden', 'unavailable'}:
                    continue
                if check['status'] == 'ready' and self._ready(service):
                    if service.startswith('mcp:'):
                        if check.get('mcp_config') == self.services[service].get('runtime'):
                            continue
                    elif all((lazyllm.globals.config[TOOL_AUTH_REGISTRY.get(key, 'dynamic_tool_auth')] or {}).get(key)
                             == value
                             for key, value in (check.get('tool_config') or {}).items()):
                        continue
                if service.startswith('mcp:'):
                    self.services[service]['status'] = (
                        check['status'] if check['status'] != 'ready' else 'refresh_pending')
                if check['status'] in {'forbidden', 'unavailable'}:
                    return {'status': check['status'], 'service': service,
                            'message': 'This capability is forbidden or temporarily unavailable; '
                                       'do not retry or bypass it.'}
                # Technical failures must not be advertised as missing authorization.
                if self.services[service]['status'] == 'unavailable':
                    return {'status': 'unavailable', 'service': service,
                            'message': 'Connection unavailable. Configuration status is unknown.'}
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
                        self.services.setdefault(service, self.services['mail'])
                    if service in self.services and service not in self.actions:
                        self.actions[service] = action
            self.pending = {}
            if not self.actions:
                return
            try:
                data = self._post('poll_batch', action_ids=[action['id'] for action in self.actions.values()])
            except Exception:
                return
            for response in data.get('actions', []):
                service = response['action']['service']
                if service not in self.services:
                    continue
                action = response['action']
                self.actions[service] = action
                if action['status'] != 'ready':
                    info = self.services[service]
                    if info['status'] is not None:
                        info['status'] = action['status']
                        old = info['group']
                        if info.get('runtime') is not None:
                            self.manager.replace_tool_group(old._name, {
                                'name': old._name, 'desc': old._desc, 'tools': [],
                                'lazy': action['status'] != 'forbidden',
                                'prefix': False, 'discoverable': action['status'] != 'forbidden',
                            })
                            info['runtime'] = None
                    continue
                key = (action['id'], action['version'])
                if key in self.applied:
                    if response.get('pending_delivery'):
                        self.pending[service] = (action, 'Connection is ready. Use only tools present in this request.')
                    continue
                if service.startswith('mcp:') and (
                        not self._ready(service)
                        or response.get('mcp_config') != self.services[service].get('runtime')):
                    runtime = response.get('mcp_config')
                    try:
                        tools = self.loader(runtime) if runtime else []
                    except Exception:
                        tools = []
                    if not tools:
                        self.pending[service] = (action, 'Connected, but the tool catalog could not be loaded.')
                        continue
                    old = self.services[service]['group']
                    definition = {'name': old._name, 'desc': old._desc, 'tools': tools,
                                  'prefix': False, 'lazy': False, 'discoverable': True}
                    try:
                        self.manager.replace_tool_group(old._name, definition, load=bool(self.intents.get(service)))
                    except Exception:
                        self.pending[service] = (action, 'Connected, but the new tool catalog could not be loaded.')
                        continue
                    self.services[service]['status'] = 'ready'
                    self.services[service]['runtime'] = runtime
                    self.names.update({tool.__name__: service for tool in tools})
                old_auth = {key: copy.deepcopy(lazyllm.globals.config[key])
                            for key in ('dynamic_tool_auth', 'dynamic_fs_auth')}
                workspace = lazyllm.locals['_lazyllm_agent'].get('workspace', {})
                old_active = list(workspace.get('_active_groups', []))
                old_bindings = dict(workspace.get('_provider_bindings', {}))
                if not service.startswith('mcp:'):
                    inject_tool_config(response.get('tool_config') or {})
                if not self._ready(service):
                    for bucket, values in old_auth.items():
                        lazyllm.globals.config[bucket] = values
                    workspace['_provider_bindings'] = old_bindings
                    self.pending[service] = (action, 'Connected, but the tool prerequisites are still unmet.')
                    continue
                try:
                    names = sorted(self.intents.get(service, ()))
                    if self.manager.retrieval is not None:
                        # Complete the previous load intent using the actual new members.
                        if names and not service.startswith('mcp:'):
                            group_name = self.services[service]['group']._name
                            catalog = self.manager.retrieval.catalog()
                            members = [name for name, entry in catalog.items() if group_name in entry['groups']]
                            self.manager.retrieval.load(members, [], _prepared=True)
                    else:
                        if names:
                            group_name = self.services[service]['group']._name
                            self.manager.activate_group(group_name)
                    if not names and self.manager.retrieval is None and self.manager.tool_load_validator is not None:
                        self.manager.tool_load_validator(self.manager.tools_description)
                    if not service.startswith('mcp:'):
                        def invalidate_credentials(group):
                            instance = getattr(group, '_instance', None)
                            credential_id = getattr(instance, '_credential_id', None)
                            if credential_id:
                                lazyllm.globals['key_pool_state'].pop(credential_id, None)
                                lazyllm.locals['curr_key'].pop(credential_id, None)
                            for child in getattr(group, '_children', ()):
                                invalidate_credentials(child)
                        invalidate_credentials(self.services[service]['group'])
                    self.applied.add(key)
                    self.intents.pop(service, None)
                    self.pending[service] = (action, 'Connection is ready. Use only tools present in this request.')
                except Exception:
                    for bucket, values in old_auth.items():
                        lazyllm.globals.config[bucket] = values
                    workspace['_active_groups'] = old_active
                    workspace['_provider_bindings'] = old_bindings
                    self.pending[service] = (action, 'Connected, but tool loading was blocked by the current budget.')

    def model_context(self):
        self.notice_actions = {}
        lines = []
        for service, (action, notice) in self.pending.items():
            line = f"{json.dumps(action['label'][:80], ensure_ascii=False)}: {notice}"
            if sum(map(len, lines)) + len(line) > 1000:
                break
            lines.append(line)
            if (action['id'], action['version']) in self.applied:
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
