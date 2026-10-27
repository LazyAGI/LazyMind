from typing import Annotated, Optional

from fastapi import APIRouter, Body
from lazymind.chat.provider_connection import check_provider_connection

router = APIRouter()


@router.post('/api/model/check', summary='Check model provider connectivity')
async def check_model_connection(
    model: Annotated[Optional[str], Body(description='Model name')] = None,
    source: Annotated[Optional[str], Body(description='Provider name')] = None,
    url: Annotated[Optional[str], Body(description='Provider URL')] = None,
    api_key: Annotated[Optional[str], Body(description='Provider API key')] = None,
    skip_auth: Annotated[bool, Body(description='Skip authentication for a self-hosted endpoint')] = False,
    models_url: Annotated[Optional[str], Body(description='Core-resolved model catalog URL')] = None,
    request_advanced: Annotated[Optional[dict], Body(description='Private supplier request options')] = None,
):
    return await check_provider_connection(model=model, source=source, url=url, api_key=api_key,
                                           skip_auth=skip_auth, models_url=models_url, request_advanced=request_advanced)
