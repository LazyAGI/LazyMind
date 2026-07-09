from __future__ import annotations

from datetime import datetime
from pathlib import Path
from time import perf_counter
from typing import Any

import lazyllm
from lazyllm import AutoModel, LOG

from lazymind.model_config import inject_model_config
from lazymind.review.skill_organize.config import (
    STAGE_DRAFT,
    STAGE_PLAN,
    STAGE_RESULT,
    STAGE_SOURCE,
    STAGE_SUMMARY,
    STAGE_VALIDATION,
)
from lazymind.review.skill_organize.db import insert_skill_organize_result
from lazymind.review.skill_organize.materializer import materialize_fs_draft
from lazymind.review.skill_organize.parser import parse_skill_summaries
from lazymind.review.skill_organize.planner import build_organize_plan
from lazymind.review.skill_organize.reports import write_stage_file
from lazymind.review.skill_organize.schemas import SkillOrganizeRequest, SkillOrganizeResult
from lazymind.review.skill_organize.skillremotefs import SkillRemoteFSClient, build_skill_remote_fs
from lazymind.review.skill_organize.validator import validate_source_skills


def run_skill_organize(
    request: SkillOrganizeRequest,
    taskid: str | None = None,
    *,
    remote_fs: SkillRemoteFSClient | None = None,
) -> SkillOrganizeResult:
    resolved_taskid = taskid or build_skill_organize_taskid(request.requestid)
    with lazyllm.new_session(resolved_taskid):
        inject_model_config(request.model_configs)
        llm = AutoModel(model='llm')
        owns_remote_fs = remote_fs is None
        client = remote_fs or build_skill_remote_fs()
        try:
            return _run_skill_organize(
                request,
                llm,
                taskid=resolved_taskid,
                remote_fs=client,
            )
        finally:
            if owns_remote_fs:
                close = getattr(client, 'close', None)
                if callable(close):
                    close()


def _run_skill_organize(
    request: SkillOrganizeRequest,
    llm: AutoModel,
    *,
    taskid: str,
    remote_fs: SkillRemoteFSClient,
) -> SkillOrganizeResult:
    work_dir = _resolve_artifact_dir(request.artifact_dir)
    artifact_dir = str(work_dir / taskid) if work_dir is not None else ''
    started_at = datetime.now()
    started_perf = perf_counter()
    try:
        source_skills = remote_fs.load_skills(request.user_id, request.skills, task_id=request.requestid)
        validate_source_skills(source_skills)
        write_stage_file(work_dir, taskid, STAGE_SOURCE, source_skills)

        summaries = parse_skill_summaries(source_skills)
        write_stage_file(work_dir, taskid, STAGE_SUMMARY, summaries)

        plan = build_organize_plan(summaries, source_skills, llm)
        write_stage_file(work_dir, taskid, STAGE_PLAN, plan)

        draft = materialize_fs_draft(plan, source_skills, llm)
        write_stage_file(work_dir, taskid, STAGE_DRAFT, draft)
        fs_apply = remote_fs.apply_draft(request.user_id, draft, task_id=request.requestid)
        write_stage_file(work_dir, taskid, STAGE_VALIDATION, {'status': 'completed', 'fs_apply': fs_apply})

        organize_result = _build_organize_result(
            request=request,
            plan=plan.model_dump(),
            draft=draft.model_dump(),
            fs_apply=fs_apply,
            artifact_dir=artifact_dir,
            taskid=taskid,
            started_at=started_at,
            duration_ms=_duration_ms(started_perf),
        )
        write_stage_file(work_dir, taskid, STAGE_RESULT, organize_result)
        inserted_count = insert_skill_organize_result(
            record_id=taskid,
            requestid=request.requestid,
            user_id=request.user_id,
            organize_result=organize_result,
        )
        LOG.info(f'[SkillOrganize] completed request={request.requestid} task={taskid} inserted_count={inserted_count}')
        return SkillOrganizeResult(
            success=True,
            requestid=request.requestid,
            taskid=taskid,
            inserted_count=inserted_count,
            artifact_dir=artifact_dir,
        )
    except Exception as exc:
        LOG.exception(f'[SkillOrganize] failed request={request.requestid} task={taskid}: {exc}')
        error_result = {
            'kind': 'skill_organize',
            'requestid': request.requestid,
            'taskid': taskid,
            'userid': request.user_id,
            'status': 'failed',
            'error': str(exc),
            'artifact_dir': artifact_dir,
            'started_at': started_at.isoformat(),
            'duration_ms': _duration_ms(started_perf),
            'created_at': datetime.now().isoformat(),
        }
        write_stage_file(work_dir, taskid, STAGE_RESULT, error_result)
        inserted_count = 0
        try:
            inserted_count = insert_skill_organize_result(
                record_id=taskid,
                requestid=request.requestid,
                user_id=request.user_id,
                organize_result=error_result,
            )
        except Exception as insert_exc:
            LOG.exception(f'[SkillOrganize] failed to insert failed run stats: {insert_exc}')
        return SkillOrganizeResult(
            success=False,
            requestid=request.requestid,
            taskid=taskid,
            inserted_count=inserted_count,
            artifact_dir=artifact_dir,
            error=str(exc),
        )


def _build_organize_result(
    *,
    request: SkillOrganizeRequest,
    plan: dict[str, Any],
    draft: dict[str, Any],
    fs_apply: dict[str, Any],
    artifact_dir: str,
    taskid: str,
    started_at: datetime,
    duration_ms: int,
) -> dict[str, Any]:
    return {
        'kind': 'skill_organize',
        'requestid': request.requestid,
        'taskid': taskid,
        'userid': request.user_id,
        'status': 'completed',
        'plans': plan.get('plans', []),
        'fs_draft': {
            'delete_paths': draft.get('delete_paths', []),
            'upsert_paths': [
                item.get('path')
                for item in draft.get('upsert_skills', [])
                if isinstance(item, dict)
            ],
        },
        'fs_apply': fs_apply,
        'artifact_dir': artifact_dir,
        'started_at': started_at.isoformat(),
        'duration_ms': duration_ms,
        'created_at': datetime.now().isoformat(),
    }


def build_skill_organize_taskid(requestid: str) -> str:
    return f'{requestid}_{datetime.now().strftime("%Y%m%d%H%M%S%f")}'


def _duration_ms(started_perf: float) -> int:
    return max(0, int((perf_counter() - started_perf) * 1000))


def _resolve_artifact_dir(artifact_dir: str | Path | None) -> Path | None:
    if artifact_dir is None or (isinstance(artifact_dir, str) and not artifact_dir.strip()):
        return None
    return Path(artifact_dir)
