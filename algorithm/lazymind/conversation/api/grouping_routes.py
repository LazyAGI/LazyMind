"""Grouping execution endpoints owned by one service process."""
import asyncio

from fastapi import APIRouter, HTTPException
from fastapi.responses import StreamingResponse

from ..conversation_grouping.execution import ExecutionConflict, cancel_execution, stream_execution
from ..conversation_grouping.grouping import run_grouping
from ..conversation_grouping.schemas import GroupingRequest
from ..schemas import ConversationResult

router = APIRouter(prefix='/api/conversation')


@router.post('/grouping:run', response_model=ConversationResult)
async def grouping_run(request: GroupingRequest):
    return await asyncio.to_thread(run_grouping, request)


@router.post('/grouping-executions/{execution_id}:stream')
async def grouping_stream(execution_id: str, request: GroupingRequest):
    try:
        events = await stream_execution(execution_id, request)
    except ExecutionConflict as exc:
        raise HTTPException(409, str(exc)) from exc
    return StreamingResponse(events, media_type='application/x-ndjson', headers={'X-Accel-Buffering': 'no'})


@router.post('/grouping-executions/{execution_id}:cancel')
async def grouping_cancel(execution_id: str):
    return await cancel_execution(execution_id)
