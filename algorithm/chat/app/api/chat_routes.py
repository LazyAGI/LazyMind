from typing import Annotated, Any, Dict, List, Optional

from fastapi import APIRouter, Body, Request
from chat.app.core.chat_service import handle_chat
from chat.config import DEFAULT_CHAT_DATASET

router = APIRouter()


@router.post('/api/chat', summary='Chat with the knowledge base')
@router.post('/api/chat/stream', summary='Chat with the knowledge base (streaming)')
async def chat(
    query: Annotated[str, Body(description='User question')],
    history: Annotated[
        Optional[List[Dict[str, Any]]],
        Body(description='Conversation history (each item may contain role and content)'),
    ] = None,
    session_id: Annotated[str, Body(description='Session ID')] = 'session_id',
    filters: Annotated[Optional[Dict[str, Any]], Body(description='Retrieval filter conditions')] = None,
    files: Annotated[Optional[List[str]], Body(description='Uploaded temporary files')] = None,
    debug: Annotated[Optional[bool], Body(description='Enable debug mode')] = False,
    reasoning: Annotated[Optional[bool], Body(description='Enable reasoning mode')] = False,
    databases: Annotated[Optional[List[Dict]], Body(description='Associated databases')] = None,
    dataset: Annotated[Optional[str], Body(description='Dataset name')] = DEFAULT_CHAT_DATASET,
    priority: Annotated[
        Optional[int],
        Body(description='Request priority for vllm scheduling; higher value means higher priority'),
    ] = None,
    available_tools: Annotated[Optional[List[str]], Body(description='List of available tools')] = None,
    available_skills: Annotated[Optional[List[str]], Body(description='List of available skills')] = None,
    memory: Annotated[Optional[str], Body(description='Memory content')] = None,
    user_preference: Annotated[Optional[str], Body(description='User preference content')] = None,
    use_memory: Annotated[Optional[bool], Body(description='Whether to use memory')] = True,
    environment_context: Annotated[
        Optional[Dict[str, Any]],
        Body(description='Environment context, e.g. current user time and timezone'),
    ] = None,
    user_id: Annotated[Optional[str], Body(description='User ID for loading user-specific vocabulary')] = None,
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
        Optional[Dict[str, str]],
        Body(
            description=(
                'Per-request tool credentials. Keys are tool/provider names, values are tokens. '
                'For OAuth2 providers (e.g. feishu) pass a valid, unexpired access token. '
                'Example: {"feishu": "u-xxx", "bing": "sk-xxx"}'
            )
        ),
    ] = None,
    *,
    request: Request,
):
    is_stream = request.url.path.endswith('/stream')

    return await handle_chat(
        query=query,
        history=history,
        session_id=session_id,
        filters=filters,
        files=files,
        debug=debug,
        reasoning=reasoning,
        databases=databases,
        dataset=dataset,
        priority=priority,
        trace=bool(trace),
        available_tools=available_tools,
        available_skills=available_skills,
        memory=memory,
        user_preference=user_preference,
        use_memory=use_memory,
        is_stream=is_stream,
        environment_context=environment_context,
        user_id=(user_id or '').strip(),
        model_config=llm_config,
        tool_config=tool_config,
    )
