from typing import Annotated, Optional

from fastapi import APIRouter, Body
import lazyllm

router = APIRouter()


@router.post('/api/model/check', summary='Check model provider connectivity')
async def check_model_connection(
    model: Annotated[Optional[str], Body(description='Model name')] = None,
    source: Annotated[Optional[str], Body(description='Provider name')] = None,
    url: Annotated[Optional[str], Body(description='Provider URL')] = None,
    api_key: Annotated[Optional[str], Body(description='Provider API key')] = None,
):
    try:
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
