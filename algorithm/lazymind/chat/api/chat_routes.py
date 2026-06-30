from typing import Annotated, Any, Dict, List, Optional, Union

from fastapi import APIRouter, Body
from lazymind.chat.service.chat_request import ChatRequest
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
    request: Annotated[
        ChatRequest,
        Body(
            description=(
                'Structured chat request grouped by message, conversation, retrieval, '
                'runtime, personalization, agent, and plugin options.'
            )
        ),
    ],
):
    return await handle_chat(request)
