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
from lazyllm.tools.agent.toolError import ToolExecutionError

from lazymind.config import config
from .budget import build_context_budget
from .context_estimator import estimate_non_history_tokens, estimate_tokens


RETRIEVAL_POLICY = '''# Tool discovery
Only the tools in this request are callable. For other capabilities, use search_tools with
English capability keywords, then load_tools with the selected tool/group names. Search does
not load schemas. Group results include up to three matching member summaries, not the complete
member list. Loading a group exposes all available members directly; no gateway activation is needed.
Grouped tools are usually complementary and intended to work together. Prefer loading the group;
load an individual member only when the required capability is clearly limited to that tool.
Newly loaded tools are callable only in the NEXT model round. Tool discovery
and loading are allowed prerequisites to instructions requiring a particular business tool.
Use unload_tool_names to release optional tools when load_tools reports an exceeded budget.
Never guess arguments from old calls or use get_*_methods in this mode.
'''

BASE_TOOLS = {
    'search_tools', 'load_tools', 'ask_user', 'list_skills', 'get_skill', 'read_reference',
    'run_script', 'set_session_env', 'calculator', 'intentwrite', 'shell',
    'read_file_resource', 'search_file_resource', 'grep',
    'read', 'write', 'edit', 'ls', 'glob', 'mkdir', 'move', 'remove', 'stat',
    'save_chat_artifact',
    'get_artifact', 'list_artifacts', 'find_artifact', 'save_artifacts', 'patch_artifact', 'discard_draft',
}


# Retrieval-only summaries; legacy Toolkit descriptions and gateways stay unchanged.
GROUP_DESCRIPTIONS = {
    'FeishuFS': (
        'Search, browse, resolve links, read and edit Feishu Lark cloud documents and wiki content.'
    ),
    'NotionFS': (
        'Search, browse, resolve links, read and edit Notion pages, databases, blocks and referenced '
        'content.'
    ),
    'GoogleDriveFS': (
        'Search, browse, read, download and manage files and documents in Google Drive.'
    ),
    'MailToolkit': (
        'Search emails, read messages, threads and attachments, compose and update drafts, and send '
        'confirmed drafts through connected mailboxes.'
    ),
    'KBToolkit': (
        'Discover knowledge bases, list and inspect documents and statistics, search semantically or by '
        'keyword, read documents and expand parent or neighboring context.'
    ),
    'ExternalDatabaseToolkit': (
        'List configured external database connections, inspect table schemas and execute read-only SQL '
        'queries.'
    ),
    'ScheduleToolkit': (
        'Create, list, update, cancel and run scheduled tasks; organize task groups and dependencies.'
    ),
    'WriterCreateToolkit': (
        'Create long-form documents: build a writing task, profile source resources, prepare outlines, '
        'draft sections, assemble content, check consistency and render final output.'
    ),
    'WriterRevisionToolkit': (
        'Revise existing documents: locate target content, plan modifications, generate and validate '
        'patches or string replacements, and apply revisions.'
    ),
    'MemoryTools': (
        'Read persistent soul, profile and preference memory and references, edit those memory documents '
        'and record episodes.'
    ),
    'SkillManagementToolkit': (
        'Create and install reusable skill packages, edit, patch, create and delete package files, rename '
        'and remove skills.'
    ),
    'WebSearchToolkit': (
        'Search the public web for current information, news and research, then read individual or '
        'multiple result pages using the selected search provider.'
    ),
    'AcademicSearchToolkit': (
        'Search academic papers, authors, abstracts and scholarly metadata; inspect metadata fields and '
        'retrieve paper contents through the selected provider.'
    ),
    'WikipediaToolkit': (
        'Search Wikipedia articles about established concepts, people, places and history, and read '
        'article content.'
    ),
    'FeishuWikiFS': (
        'Search, browse, resolve links, read and edit Feishu Lark cloud documents and wiki content.'
    ),
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
        if (not isinstance(payload, dict) or payload.get('version') not in (1, 2)
                or not isinstance(payload.get('loaded'), list)
                or not all(isinstance(name, str) for name in payload['loaded'])
                or not isinstance(payload.get('skills'), dict)):
            raise ValueError('Invalid tool retrieval state')
        if payload['version'] == 1:
            # Old public names cannot identify their original provider. Rebuild
            # required tools and Skill dependencies, never reuse optional names.
            return {'version': 2, 'loaded': [], 'skills': payload['skills']}
        return payload

    def update(self, update):
        if self.readonly:
            raise RuntimeError('Context preview cannot change tool loading state')
        self.path.parent.mkdir(parents=True, exist_ok=True)
        with FileLock(str(self.path) + '.lock'):
            state = {**update(self.read()), 'version': 2}
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

    agent._prompt = agent._prompt.replace(
        'A tool named get_*Toolkit_methods is a Toolkit gateway: call it before using that Toolkit. ', '')
    agent._prompt = agent._prompt.replace(
        'call `video_generator` directly before calling any other tool.',
        'discover and load `video_generator` if needed, then call it before other business tools.')
    agent._prompt += '\n\n' + RETRIEVAL_POLICY

    group_members, group_descriptions = {}, dict(GROUP_DESCRIPTIONS)
    server_names = {}
    from lazyllm.tools import get_tool_runtime_metadata
    for tool in agent._tools:
        metadata = get_tool_runtime_metadata(tool[0] if isinstance(tool, tuple) and len(tool) == 2 else tool)
        if metadata and metadata.tool_source == 'mcp' and metadata.tool_origin:
            server_names[metadata.tool_origin] = getattr(tool, '_lazymind_mcp_server_name', metadata.tool_origin)
    for name, entry in catalog.items():
        if entry['source'] == 'mcp' and entry['origin']:
            origin = entry['origin']
            group = f'mcp:{origin}'
            group_members.setdefault(group, []).append(name)
            group_descriptions[group] = f'MCP server: {server_names.get(origin, origin)}.'

    budget = build_context_budget(options.max_input_tokens, llm_config=options.llm_config)

    def validate_load(definitions):
        # Use candidate schemas directly: reading manager.tools_description here would
        # re-read durable state while the load transaction holds its file lock.
        prefix = {
            'system_prompt': agent._prompt,
            'tool_definitions': definitions,
            'skills_prompt': skill_manager.build_prompt() if skill_manager else '',
            'skill_prompt_parts': skill_manager.describe_prompt() if skill_manager else [],
        }
        if estimate_non_history_tokens(prefix, plan.prompt.current_input) > budget.effective_input_budget:
            raise ToolExecutionError('加载失败，原因是新增工具定义导致固定上下文超过有效输入上限；'
                                     '请释放可选工具，或只加载所需成员。')

    controller = manager.enable_tool_retrieval(
        required=required,
        max_search_results=5,
        matched_member_limit=3,
        groups=set(GROUP_DESCRIPTIONS),
        group_descriptions=group_descriptions,
        group_members=group_members,
        validate_load=validate_load,
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

    def validate_context(prefix, history, current_input):
        total = estimate_non_history_tokens(prefix, current_input)
        total += sum(estimate_tokens(json.dumps(message, ensure_ascii=False, separators=(',', ':'),
                                                default=str)) + 4 for message in history)
        if total > budget.effective_input_budget:
            raise RuntimeError('Model context exceeds the effective input budget after compression; '
                               'unload optional tools or shorten the request.')
    manager.context_validator = validate_context
