from __future__ import annotations

from typing import Annotated, Any, Dict, List, Optional

from fastapi import APIRouter, Body
from fastapi.responses import StreamingResponse

from lazymind.chat.engine.subagent.runner import run_subagent_stream

router = APIRouter()


@router.post('/api/subagent/run', summary='Execute a SubAgent task (streaming)')
async def run_subagent(
    task_id: Annotated[str, Body(description='Task ID; also used as the request sid (queue bucket)')],
    db_dsn: Annotated[str, Body(description='Core database DSN, used to persist steps and read/write artifacts')],
    agent_type: Annotated[Optional[str], Body(description='Agent type')] = None,
    objective: Annotated[Optional[str], Body(description='Task objective')] = None,
    params: Annotated[Optional[Dict[str, Any]], Body(description='Task parameters')] = None,
    input_artifact_keys: Annotated[Optional[List[str]], Body(description='Input artifact keys')] = None,
    output_artifact_keys: Annotated[
        Optional[List[str]],
        Body(description='Output artifact keys (fixed declaration)'),
    ] = None,
    workspace_path: Annotated[Optional[str], Body(description='Workspace directory for this task')] = None,
    tools: Annotated[
        Optional[List[str]],
        Body(description='Optional tool names; default loads agent_type tools'),
    ] = None,
    resume: Annotated[Optional[bool], Body(description='Resume from persisted steps when true')] = False,
    llm_config: Annotated[Optional[Dict[str, Any]], Body(description='Per-request model config')] = None,
):
    return StreamingResponse(
        run_subagent_stream(
            task_id=task_id,
            db_dsn=db_dsn,
            resume=bool(resume),
            model_config=llm_config,
            agent_type=agent_type,
            tools=tools,
        ),
        media_type='text/event-stream',
    )
