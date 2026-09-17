"""Host policy and durable state for opt-in tool retrieval."""
from __future__ import annotations

from dataclasses import replace
import hashlib
import json
import os
from pathlib import Path
import tempfile

from filelock import FileLock
import lazyllm

from lazymind.config import config
from .budget import build_context_budget
from .context_estimator import estimate_non_history_tokens, estimate_tokens


RETRIEVAL_POLICY = '''# Tool discovery
Only the tools in this request are callable. For other capabilities, use search_tools with
English capability keywords, then load_tools with the selected tool/group names. Search does
not load schemas. Newly loaded tools are callable only in the NEXT model round. Tool discovery
and loading are allowed prerequisites to instructions requiring a particular business tool.
Use unload_tool_names to release optional tools when load_tools reports an exceeded budget.
Never guess arguments from old calls or use get_*_methods in this mode.
'''

BASE_TOOLS = {
    'search_tools', 'load_tools', 'ask_user', 'list_skills', 'get_skill', 'read_reference',
    'run_script', 'set_session_env', 'calculator', 'intentwrite', 'shell',
    'read_file', 'grep', 'list_dir', 'write_file', 'make_dir', 'move_file', 'delete_file',
    'read', 'write', 'edit', 'ls', 'glob', 'mkdir', 'move', 'remove', 'stat',
    'search_in_files', 'download_file', 'save_chat_artifact', 'list_chat_artifacts',
    'get_artifact', 'list_artifacts', 'find_artifact', 'save_artifacts', 'patch_artifact', 'discard_draft',
}


class ToolStateStore:
    def __init__(self, scope, *, readonly=False):
        key = hashlib.sha256(json.dumps(scope, ensure_ascii=False).encode()).hexdigest()
        self.path = Path(config['agentic_workspace']) / 'tool-retrieval-state' / f'{key}.json'
        self.readonly = readonly

    def read(self):
        try:
            payload = json.loads(self.path.read_text(encoding='utf-8'))
        except FileNotFoundError:
            return {}
        if (not isinstance(payload, dict) or payload.get('version') != 1
                or not isinstance(payload.get('loaded'), list)
                or not all(isinstance(name, str) for name in payload['loaded'])
                or not isinstance(payload.get('skills'), dict)):
            raise ValueError('Invalid tool retrieval state')
        return payload

    def update(self, update):
        if self.readonly:
            raise RuntimeError('Context preview cannot change tool loading state')
        self.path.parent.mkdir(parents=True, exist_ok=True)
        with FileLock(str(self.path) + '.lock'):
            state = {'version': 1, **update(self.read())}
            temporary = None
            try:
                with tempfile.NamedTemporaryFile(mode='w', encoding='utf-8', dir=self.path.parent,
                                                 prefix='.tools-', delete=False) as handle:
                    temporary = handle.name
                    json.dump(state, handle, ensure_ascii=False)
                    handle.flush()
                    os.fsync(handle.fileno())
                os.replace(temporary, self.path)
            finally:
                if temporary and os.path.exists(temporary):
                    os.unlink(temporary)
            return state


def configure_tool_retrieval(agent, plan):
    options = plan.execution_options
    cfg = lazyllm.globals.get('agentic_config') or {}
    if not cfg.get('enable_tool_retrieval'):
        return
    manager = agent._tools_manager
    catalog = manager.atomic_tool_catalog()
    required_groups = {'FileSystemToolkit', *options.required_tool_groups}
    required = [name for name, entry in catalog.items()
                if options.preload_all_tools or name in BASE_TOOLS or name in options.required_tool_names
                or required_groups.intersection(entry['groups'])]
    required.extend(name for name in plan.stop_tools if name in catalog)
    skill_manager = agent._skill_manager

    def skill_dependencies(name):
        info, error = skill_manager._get_visible_skill_info(name) if skill_manager else (None, None)
        return info.get('allowed-tools') or [] if info and not error else None

    budget = build_context_budget(options.max_input_tokens, llm_config=options.llm_config)
    controller = manager.enable_tool_retrieval(
        required=required,
        groups={'FeishuFS', 'NotionFS', 'GoogleDriveFS'},
        estimate_tokens=lambda definitions: estimate_non_history_tokens({'tool_definitions': definitions}),
        threshold_tokens=int(budget.effective_input_budget * 0.1),
        state_store=ToolStateStore(
            [str(cfg.get('user_id') or '0'), str(cfg.get('conversation_id') or ''),
             options.tool_state_scope or plan.role.value], readonly=options.context_preview),
        skill_dependencies=skill_dependencies,
    )
    if not options.context_preview:
        controller.initialize()
    if skill_manager:
        skill_manager.on_skill_loaded = controller.load_skill
        skill_tool = manager.tools_info.get('get_skill')
        if skill_tool is not None:
            skill_tool._runtime_metadata = replace(
                skill_tool._runtime_metadata,
                write_keys=(*(skill_tool._runtime_metadata.write_keys or ()), f'tool-retrieval:{manager._module_id}'))
    agent._prompt = agent._prompt.replace(
        'A tool named get_*Toolkit_methods is a Toolkit gateway: call it before using that Toolkit. ', '')
    agent._prompt = agent._prompt.replace(
        'call `video_generator` directly before calling any other tool.',
        'discover and load `video_generator` if needed, then call it before other business tools.')
    agent._prompt += '\n\n' + RETRIEVAL_POLICY

    def validate_context(prefix, history, current_input):
        total = estimate_non_history_tokens(prefix, current_input)
        total += sum(estimate_tokens(json.dumps(message, ensure_ascii=False, separators=(',', ':'),
                                                default=str)) + 4 for message in history)
        if total > budget.effective_input_budget:
            raise RuntimeError('Model context exceeds the effective input budget after compression; '
                               'unload optional tools or shorten the request.')
    manager.context_validator = validate_context
