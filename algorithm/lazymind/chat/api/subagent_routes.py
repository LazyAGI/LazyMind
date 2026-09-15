from __future__ import annotations

from typing import Annotated, Any, Dict, List, Optional

from fastapi import APIRouter, Body
from fastapi.responses import StreamingResponse

router = APIRouter()


@router.post('/api/subagent/run', summary='Execute a SubAgent task (streaming)')
async def run_subagent(
    task_id: Annotated[str, Body(description='Task ID; also used as the request sid (queue bucket)')],
    task_spec: Annotated[Dict[str, Any], Body(description='Core-owned immutable task snapshot')],
    agent_type: Annotated[Optional[str], Body(description='Agent type')] = None,
    objective: Annotated[Optional[str], Body(description='Task objective')] = None,
    params: Annotated[Optional[Dict[str, Any]], Body(description='Task parameters')] = None,
    input_slots: Annotated[Optional[List[str]], Body(description='Input slot ids')] = None,
    output_slots: Annotated[
        Optional[List[str]],
        Body(description='Output slot ids (fixed declaration)'),
    ] = None,
    workspace_path: Annotated[Optional[str], Body(description='Workspace directory for this task')] = None,
    tools: Annotated[
        Optional[List[str]],
        Body(description='Optional tool names; default loads agent_type tools'),
    ] = None,
    resume: Annotated[Optional[bool], Body(description='Resume from persisted steps when true')] = False,
    llm_config: Annotated[Optional[Dict[str, Any]], Body(description='Per-request model config')] = None,
    tool_config: Annotated[Optional[Dict[str, Any]], Body(description='Per-request tool credentials (API keys)')] = None,
    initial_steps: Annotated[
        Optional[List[Dict[str, Any]]],
        Body(description='Core-owned durable step snapshot used for resume'),
    ] = None,
):
    from lazymind.chat.engine.subagent.runner import run_subagent_stream

    return StreamingResponse(
        run_subagent_stream(
            task_id=task_id,
            resume=bool(resume),
            model_config=llm_config,
            tool_config=tool_config,
            agent_type=agent_type,
            tools=tools,
            task_spec=task_spec,
            initial_steps=initial_steps,
        ),
        media_type='text/event-stream',
    )
