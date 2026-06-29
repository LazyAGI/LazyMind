from typing import Annotated, Any, Dict, List, Optional, Union

from fastapi import APIRouter, Body
from lazymind.chat.config import DEFAULT_CHAT_DATASET
from lazymind.chat.service.chat_service import handle_chat
from lazymind.chat.service.component import get_all_tool_groups
from lazymind.model_config import inject_model_config
from lazyllm.tools.tool_config_inject import inject_tool_config

router = APIRouter()


@router.post('/api/chat/tools', summary='List all tool groups with their methods')
async def list_chat_tools(
    llm_config: Annotated[
        Optional[Dict[str, Any]],
        Body(
            description=(
                'Per-request model configuration. Keys are role names from runtime_models.yaml '
                '(llm, reranker, embed_main), each with its own config dict '
                '{source, model, base_url, api_key, skip_auth}.'
            )
        ),
    ] = None,
    tool_config: Annotated[
        Optional[Dict[str, Union[str, List[str]]]],
        Body(
            description=(
                'Per-request tool credentials. Format: {tool_name: token} or {tool_name: [token, ...]}. '
                'For OAuth2 providers (e.g. feishu) pass a valid, unexpired access token.'
            )
        ),
    ] = None,
):
    inject_model_config(llm_config)
    inject_tool_config(tool_config)
    return {'tool_groups': get_all_tool_groups()}


@router.post('/api/chat/stream', summary='Chat with the knowledge base (streaming)')
async def chat(
    query: Annotated[str, Body(description='User question')],
    history: Annotated[
        Optional[List[Dict[str, Any]]],
        Body(description='Conversation history (each item may contain role and content)'),
    ] = None,
    session_id: Annotated[str, Body(description='Session ID')] = 'session_id',
    conversation_id: Annotated[Optional[str], Body(description='Conversation ID for SubAgent task lookup')] = None,
    filters: Annotated[Optional[Dict[str, Any]], Body(description='Retrieval filter conditions')] = None,
    files: Annotated[
        Optional[Dict[str, List[str]]],
        Body(description='Per-turn file paths. Keys: "current" or "<seq>". Values: local paths.'),
    ] = None,
    debug: Annotated[Optional[bool], Body(description='Enable debug mode')] = False,
    reasoning: Annotated[Optional[bool], Body(description='Enable reasoning mode')] = False,
    databases: Annotated[Optional[List[Dict]], Body(description='Associated databases')] = None,
    dataset: Annotated[Optional[str], Body(description='Dataset name')] = DEFAULT_CHAT_DATASET,
    priority: Annotated[
        Optional[int],
        Body(description='Request priority for vllm scheduling; higher value means higher priority'),
    ] = None,
    disabled_tools: Annotated[Optional[List[str]], Body(description='List of disabled tool groups')] = None,
    available_skills: Annotated[Optional[List[str]], Body(description='List of available skills')] = None,
    memory: Annotated[Optional[str], Body(description='Memory content')] = None,
    user_preference: Annotated[Optional[str], Body(description='User preference content')] = None,
    use_memory: Annotated[Optional[bool], Body(description='Whether to use memory')] = True,
    environment_context: Annotated[
        Optional[Dict[str, Any]],
        Body(description='Environment context, e.g. current user time and timezone'),
    ] = None,
    user_id: Annotated[Optional[str], Body(description='User ID for loading user-specific vocabulary')] = None,
    mode: Annotated[Optional[str], Body(description="SubAgent driving mode: 'auto' or 'manual'")] = 'auto',
    has_subagents: Annotated[
        Optional[bool],
        Body(description='Whether the conversation already has SubAgent tasks (enables query tools)'),
    ] = False,
    trace: Annotated[Optional[bool], Body(description='Enable trace recording (for admin debugging only)')] = False,
    llm_config: Annotated[
        Optional[Dict[str, Any]],
        Body(
            description=(
                'Per-request model configuration. Keys are role names from runtime_models.yaml '
                '(llm, reranker, embed_main), each with its own config dict '
                '{source, model, base_url, api_key, skip_auth}. '
                'Example: {"llm": {"source": "openai", "model": "gpt-4o", "api_key": "sk-..."}, '
                '"embed_main": {"source": "siliconflow", "model": "BAAI/bge-m3", "api_key": "..."}}'
            )
        ),
    ] = None,
    tool_config: Annotated[
        Optional[Dict[str, Union[str, List[str]]]],
        Body(
            description=(
                'Per-request tool credentials. Format: {tool_name: token} or {tool_name: [token, ...]}. '
                'For OAuth2 providers (e.g. feishu, notion) pass a valid, unexpired access token. '
                'Example: {"feishu": "u-xxx", "notion": "ntn_xxx", "bing": ["sk-1", "sk-2"]}'
            )
        ),
    ] = None,
    mcp_config: Annotated[
        Optional[List[Dict[str, Any]]],
        Body(
            description=(
                'Per-request MCP server configuration. Each item: '
                '{id, name, transport, url, headers, allowed_tools, timeout}. '
                'headers contains decrypted real credentials and is discarded after the request.'
            )
        ),
    ] = None,
    plugin_context: Annotated[
        Optional[Dict[str, Any]],
        Body(
            description=(
                'Active plugin session context injected by Go. '
                'Fields: session_id, plugin_id, current_step, advance. '
                'When present with session_id, only advance_step is injected; '
                'when absent or empty, cold-start trigger tools are injected.'
            )
        ),
    ] = None,
    local_fs_sources: Annotated[
        Optional[List[Dict[str, Any]]],
        Body(
            description=(
                'Per-request local filesystem source scopes. Each item: '
                '{source_id, paths, file_extensions}. file_extensions must be lowercase suffixes '
                'without dot and limited to pdf, doc, docx, csv, xls, xlsx.'
            )
        ),
    ] = None,
    ask_response: Annotated[
        Optional[Dict[str, Any]],
        Body(
            description=(
                'User response to a pending ask_user request. '
                'Fields: ask_id (str), selected (list of str). '
                'Injected by Go when the user replies to an ask_pending card.'
            )
        ),
    ] = None,
    current_turn_seq: Annotated[
        Optional[int],
        Body(description='The seq number of the current conversation turn, provided by Go core. '
                         'Used to correctly label the current-turn attachments.'),
    ] = None,
    enable_plugin: Annotated[
        Optional[bool],
        Body(description='Whether plugin tooling is enabled for this conversation. '
                         'Resolved by Go from conversations.enable_plugin.'),
    ] = None,
    enable_subagent: Annotated[
        Optional[bool],
        Body(description='Whether SubAgent task creation is enabled for this conversation. '
                         'Resolved by Go from conversations.enable_subagent.'),
    ] = None,
):
    return await handle_chat(
        query=query,
        history=history,
        session_id=session_id,
        conversation_id=(conversation_id or '').strip(),
        filters=filters,
        files=files,
        databases=databases,
        priority=priority,
        disabled_tools=disabled_tools,
        available_skills=available_skills,
        memory=memory,
        user_preference=user_preference,
        use_memory=use_memory,
        environment_context=environment_context,
        user_id=(user_id or '').strip(),
        mode=mode,
        has_subagents=bool(has_subagents),
        model_config=llm_config,
        tool_config=tool_config,
        mcp_config=mcp_config,
        trace=trace,
        plugin_context=plugin_context,
        local_fs_sources=local_fs_sources,
        ask_response=ask_response,
        current_turn_seq=current_turn_seq,
        enable_plugin=enable_plugin,
        enable_subagent=enable_subagent,
    )
