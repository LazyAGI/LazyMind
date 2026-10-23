from fastapi import APIRouter
from pydantic import BaseModel, Field, SecretStr

from lazymind.chat.model_probe import ProbeInput, probe_model

router = APIRouter()


class ModelProbeRequest(BaseModel):
    model: str = Field(min_length=1, max_length=512)
    model_type: str = Field(default='', max_length=64)
    source: str = Field(min_length=1, max_length=128)
    url: str = Field(min_length=1, max_length=2048)
    api_key: SecretStr = Field(default_factory=lambda: SecretStr(''))
    skip_auth: bool = False
    vision: bool = False


@router.post('/api/model/probe', summary='Run a minimal model-specific inference request')
async def probe_model_connection(body: ModelProbeRequest):
    return await probe_model(ProbeInput(
        model=body.model, model_type=body.model_type, source=body.source,
        url=body.url, api_key=body.api_key.get_secret_value(),
        skip_auth=body.skip_auth, vision=body.vision,
    ))
