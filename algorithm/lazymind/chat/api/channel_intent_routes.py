import asyncio
import logging

from fastapi import APIRouter, HTTPException

from lazymind.chat.service.channel_intent import (
    ChannelCommandEnvelope,
    ChannelIntentModelError,
    ChannelIntentOutputError,
    ChannelIntentRequest,
    classify_channel_intent,
)


router = APIRouter()
_logger = logging.getLogger(__name__)


@router.post(
    '/api/chat/channel-intent-classify',
    response_model=ChannelCommandEnvelope,
    summary='Select one command using a caller-supplied command registry',
)
async def channel_intent_classify(request: ChannelIntentRequest) -> ChannelCommandEnvelope:
    try:
        return await asyncio.to_thread(classify_channel_intent, request)
    except ChannelIntentOutputError as exc:
        _logger.warning('channel_intent_output_invalid')
        raise HTTPException(
            status_code=502,
            detail='channel intent model returned invalid output',
        ) from exc
    except ChannelIntentModelError as exc:
        _logger.warning('channel_intent_model_unavailable')
        raise HTTPException(
            status_code=502,
            detail='channel intent model unavailable',
        ) from exc
    except Exception as exc:
        _logger.error(
            'channel_intent_internal_failure error_type=%s',
            type(exc).__name__,
        )
        raise HTTPException(
            status_code=502,
            detail='channel intent classification unavailable',
        ) from exc
