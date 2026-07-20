from typing import Annotated, Optional

from fastapi import APIRouter, Body
import httpx
import lazyllm

router = APIRouter()

_OFFICIAL_DOUBAO_URL = 'https://ark.cn-beijing.volces.com/api/v3/'


@router.post('/api/model/check', summary='Check model provider connectivity')
async def check_model_connection(
    model: Annotated[Optional[str], Body(description='Model name')] = None,
    source: Annotated[Optional[str], Body(description='Provider name')] = None,
    url: Annotated[Optional[str], Body(description='Provider URL')] = None,
    api_key: Annotated[Optional[str], Body(description='Provider API key')] = None,
):
    try:
        if url == _OFFICIAL_DOUBAO_URL:
            async with httpx.AsyncClient(timeout=30.0) as client:
                response = await client.get(
                    f'{url}models',
                    headers={'Authorization': f'Bearer {api_key}'},
                )
            if not (200 <= response.status_code < 300):
                raise RuntimeError(f'HTTP {response.status_code}: {response.text}')
            result = response.text
        else:
            module = lazyllm.OnlineModule(
                model=model,
                source=source,
                url=url,
                api_key=api_key,
            )
            result = module('hi')
        return {
            'success': True,
            'message': 'model connection is available',
            'model': model,
            'source': source,
            'url': url,
            'result': result,
        }
    except Exception as exc:
        return {
            'success': False,
            'message': str(exc),
            'model': model,
            'source': source,
            'url': url,
        }
