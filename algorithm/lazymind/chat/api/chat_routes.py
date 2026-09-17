import asyncio
import importlib

from typing import Annotated, Any, Dict, List, Optional, Union

from fastapi import APIRouter, Body, Header, HTTPException, Response
from lazymind.chat.service.chat_request import ChatRequest
from lazymind.chat.runtime_loader import ensure_chat_runtime

router = APIRouter()


async def _chat_service():
    await asyncio.to_thread(ensure_chat_runtime)
    return importlib.import_module('lazymind.chat.service.chat_service')


@router.post('/api/chat/sensitive-check', summary='Check text against the configured sensitive-word list')
async def sensitive_check(payload: Annotated[Dict[str, Any], Body()]):
    service = await _chat_service()
    matched_word = service.check_sensitive_content(str(payload.get('text') or ''))
    return {'passed': matched_word is None, 'matched_word': matched_word}


@router.post('/api/chat/tools', summary='List the tool catalog and Toolkit methods')
async def list_chat_tools(
    response: Response,
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
    accept_language: Annotated[
        Optional[str],
        Header(
            alias='Accept-Language',
            description=(
                'Optional UI locale. zh and zh-* use zh-CN; en and en-* use en-US. '
                'Missing or unsupported values default to zh-CN.'
            ),
        ),
    ] = None,
):
    from lazymind.chat.service.component import get_all_tool_groups, normalize_tool_locale
    from lazymind.model_config import inject_model_config
    from lazymind.chat.engine.tool_auth import inject_tool_config

    inject_model_config(llm_config)
    inject_tool_config(tool_config)
    locale = normalize_tool_locale(accept_language)
    response.headers['Content-Language'] = locale
    response.headers['Vary'] = 'Accept-Language'
    return {'tool_groups': get_all_tool_groups(locale)}


async def _external_tool_request(payload, token, *, execute=False):
    from lazymind.chat.api.knowledge_search_routes import require_internal_token
    require_internal_token(token)
    await _chat_service()
    from lazymind.chat.service.external_tools import run_external_tools
    try:
        return await asyncio.to_thread(run_external_tools, payload, execute=execute)
    except ValueError as exc:
        raise HTTPException(status_code=400, detail='tool unavailable or invalid arguments') from exc
    except Exception as exc:
        raise HTTPException(
            status_code=502, detail='tool execution failed; check its connection and configuration',
        ) from exc


@router.post('/api/chat/tools/external-catalog', summary='List registry tools for the Core gateway')
async def external_tool_catalog(
    payload: Annotated[Dict[str, Any], Body()],
    token: Annotated[Optional[str], Header(alias='X-LazyMind-Internal-Token')] = None,
):
    return await _external_tool_request(payload, token)


@router.post('/api/chat/tools/execute', summary='Execute a registry tool for the Core gateway')
async def execute_chat_tool(
    payload: Annotated[Dict[str, Any], Body()],
    token: Annotated[Optional[str], Header(alias='X-LazyMind-Internal-Token')] = None,
):
    return await _external_tool_request(payload, token, execute=True)


@router.post('/api/chat/stream', summary='Chat with the knowledge base (streaming)')
async def chat(
    request: Annotated[
        ChatRequest,
        Body(
            description=(
                'Structured chat request grouped by message, conversation, retrieval, '
                'runtime, personalization, agent, and workflow options.'
            )
        ),
    ],
):
    service = await _chat_service()
    return await service.handle_chat(request)


@router.post('/api/chat/context-usage', summary='Estimate next-request ChatAgent context usage')
async def context_usage(
    request: Annotated[ChatRequest, Body(description='Same structured request as chat preview.')],
):
    request.runtime.context_usage_preview = True
    service = await _chat_service()
    return await service.handle_chat(request)


@router.post('/api/chat/context-prompt', summary='Export next-request ChatAgent context')
async def context_prompt(
    request: Annotated[ChatRequest, Body(description='Same structured request as chat preview.')],
):
    request.runtime.context_prompt_export = True
    service = await _chat_service()
    return await service.handle_chat(request)
