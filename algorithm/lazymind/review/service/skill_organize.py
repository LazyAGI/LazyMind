from __future__ import annotations

from datetime import datetime
from pathlib import Path
from time import perf_counter
from typing import Any

import lazyllm
from lazyllm import AutoModel, LOG

from lazymind.common.skill_remote_store import SkillRemoteStore
from lazymind.common.skill_storage_key import parse_skill_storage_key
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
from lazymind.review.skill_organize.schemas import (
    SkillFsDraft,
    SkillFsDraftItem,
    SkillOrganizeRequest,
    SkillOrganizeResult,
    SourceSkill,
)
from lazymind.review.skill_organize.validator import validate_fs_draft, validate_source_skills

_MISSING = object()
ORG_STAGE_PLAN = 'organize_plan'
ORG_STAGE_DRAFT = 'organize_draft'
ORG_STAGE_APPLY = 'organize_apply'


def record_skill_organize_pending(request: SkillOrganizeRequest, taskid: str) -> int:
    work_dir = _resolve_artifact_dir(request.artifact_dir)
    artifact_dir = str(work_dir / taskid) if work_dir is not None else ''
    now = datetime.now()
    pending_result = {
        'kind': 'skill_organize',
        'requestid': request.requestid,
        'taskid': taskid,
        'userid': request.user_id,
        'status': 'pending',
        'skills': request.skills,
        'artifact_dir': artifact_dir,
        'started_at': now.isoformat(),
        'duration_ms': 0,
        'created_at': now.isoformat(),
    }
    return insert_skill_organize_result(
        record_id=taskid,
        requestid=request.requestid,
        user_id=request.user_id,
        organize_result=pending_result,
    )


def record_skill_organize_failed(request: SkillOrganizeRequest, taskid: str, error: str) -> int:
    work_dir = _resolve_artifact_dir(request.artifact_dir)
    artifact_dir = str(work_dir / taskid) if work_dir is not None else ''
    now = datetime.now()
    failed_result = {
        'kind': 'skill_organize',
        'requestid': request.requestid,
        'taskid': taskid,
        'userid': request.user_id,
        'status': 'failed',
        'skills': request.skills,
        'error': error,
        'artifact_dir': artifact_dir,
        'started_at': now.isoformat(),
        'duration_ms': 0,
        'created_at': now.isoformat(),
    }
    return insert_skill_organize_result(
        record_id=taskid,
        requestid=request.requestid,
        user_id=request.user_id,
        organize_result=failed_result,
    )


def record_skill_organize_stage(
    request: SkillOrganizeRequest,
    taskid: str,
    stage: str,
    *,
    started_at: datetime | None = None,
    duration_ms: int = 0,
    extra: dict[str, Any] | None = None,
) -> int:
    work_dir = _resolve_artifact_dir(request.artifact_dir)
    artifact_dir = str(work_dir / taskid) if work_dir is not None else ''
    now = datetime.now()
    stage_result = {
        'kind': 'skill_organize',
        'requestid': request.requestid,
        'taskid': taskid,
        'userid': request.user_id,
        'status': stage,
        'stage': stage,
        'skills': request.skills,
        'artifact_dir': artifact_dir,
        'started_at': (started_at or now).isoformat(),
        'duration_ms': duration_ms,
        'updated_at': now.isoformat(),
    }
    if extra:
        stage_result.update(extra)
    return insert_skill_organize_result(
        record_id=taskid,
        requestid=request.requestid,
        user_id=request.user_id,
        organize_result=stage_result,
    )


def run_skill_organize(
    request: SkillOrganizeRequest,
    taskid: str | None = None,
    *,
    remote_store: SkillRemoteStore | None = None,
) -> SkillOrganizeResult:
    resolved_taskid = taskid or build_skill_organize_taskid(request.requestid)
    with lazyllm.new_session(resolved_taskid):
        inject_model_config(request.model_configs)
        llm = AutoModel(model='llm')
        previous_agentic_config = _set_skill_remote_context(request)
        try:
            return _run_skill_organize(
                request,
                llm,
                taskid=resolved_taskid,
                remote_store=remote_store or SkillRemoteStore(),
            )
        finally:
            _restore_agentic_config(previous_agentic_config)


def _run_skill_organize(
    request: SkillOrganizeRequest,
    llm: AutoModel,
    *,
    taskid: str,
    remote_store: SkillRemoteStore,
) -> SkillOrganizeResult:
    work_dir = _resolve_artifact_dir(request.artifact_dir)
    artifact_dir = str(work_dir / taskid) if work_dir is not None else ''
    started_at = datetime.now()
    started_perf = perf_counter()
    current_stage = 'pending'
    try:
        source_skills = _load_source_skills(request, remote_store)
        validate_source_skills(source_skills)
        write_stage_file(work_dir, taskid, STAGE_SOURCE, source_skills)

        summaries = parse_skill_summaries(source_skills)
        write_stage_file(work_dir, taskid, STAGE_SUMMARY, summaries)

        current_stage = ORG_STAGE_PLAN
        _record_skill_organize_stage_safely(
            request,
            taskid,
            current_stage,
            started_at,
            started_perf,
            {'source_count': len(source_skills), 'summary_count': len(summaries)},
        )
        plan = build_organize_plan(summaries, source_skills, llm)
        write_stage_file(work_dir, taskid, STAGE_PLAN, plan)

        current_stage = ORG_STAGE_DRAFT
        _record_skill_organize_stage_safely(
            request,
            taskid,
            current_stage,
            started_at,
            started_perf,
            {'plan_count': len(plan.plans)},
        )
        draft = materialize_fs_draft(plan, source_skills, llm)
        write_stage_file(work_dir, taskid, STAGE_DRAFT, draft)

        current_stage = ORG_STAGE_APPLY
        _record_skill_organize_stage_safely(
            request,
            taskid,
            current_stage,
            started_at,
            started_perf,
            {
                'delete_count': len(draft.delete_keys),
                'upsert_count': len(draft.upsert_skills),
            },
        )
        fs_apply = _apply_fs_draft(draft, remote_store, source_skills)
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
            'failed_stage': current_stage,
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


def _record_skill_organize_stage_safely(
    request: SkillOrganizeRequest,
    taskid: str,
    stage: str,
    started_at: datetime,
    started_perf: float,
    extra: dict[str, Any] | None = None,
) -> None:
    try:
        record_skill_organize_stage(
            request,
            taskid,
            stage,
            started_at=started_at,
            duration_ms=_duration_ms(started_perf),
            extra=extra,
        )
    except Exception as exc:
        LOG.exception(f'[SkillOrganize] failed to update stage={stage} task={taskid}: {exc}')


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
            'delete_keys': draft.get('delete_keys', []),
            'upsert_keys': [
                item.get('target_key')
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


def _load_source_skills(request: SkillOrganizeRequest, store: SkillRemoteStore) -> list[SourceSkill]:
    result: list[SourceSkill] = []
    for item in request.skills:
        category, name = parse_skill_storage_key(item)
        files = store.list_files(category, name)
        content = files.get('SKILL.md')
        if not isinstance(content, str) or not content.strip():
            raise ValueError(f'skill {name!r} does not contain SKILL.md')
        result.append(SourceSkill(
            key=f'{category}/{name}',
            category=category,
            name=name,
            content=content,
        ))
    return result


def _apply_fs_draft(draft: SkillFsDraft, store: SkillRemoteStore, source_skills: list[SourceSkill]) -> dict:
    validate_fs_draft(draft, source_skills)
    source_by_key = {item.key: item for item in source_skills}
    upsert_operations: list[tuple[SkillFsDraftItem, str, str, str, str]] = []
    same_key_snapshots: dict[str, tuple[dict[str, str], dict[str, str]]] = {}
    delete_operations: list[tuple[str, str, str]] = []

    for item in draft.upsert_skills:
        source_category, source_name = parse_skill_storage_key(item.source_key)
        target_storage_category, target_name = parse_skill_storage_key(item.target_key)
        source_dir = store.package_dir(source_category, source_name)
        if not store.fs.exists(source_dir):
            raise FileNotFoundError(f'Skill package {item.source_key} does not exist.')
        if item.source_key == item.target_key:
            before = store.list_files(source_category, source_name)
            after = dict(before)
            after['SKILL.md'] = item.content
            same_key_snapshots[item.source_key] = (before, after)
        else:
            target_dir = store.package_dir(target_storage_category, target_name)
            if store.fs.exists(target_dir):
                raise FileExistsError(f'Skill package {item.target_key} already exists.')
        upsert_operations.append(
            (item, source_category, source_name, target_storage_category, target_name)
        )

    for key in draft.delete_keys:
        category, name = parse_skill_storage_key(key)
        if key not in source_by_key:
            raise ValueError(f'cannot delete unknown source skill {key!r}')
        if not store.fs.exists(store.package_dir(category, name)):
            raise FileNotFoundError(f'Skill package {key} does not exist.')
        delete_operations.append((key, category, name))

    upserted_keys: list[str] = []
    deleted_keys: list[str] = []
    for item, source_category, source_name, target_storage_category, target_name in upsert_operations:
        if item.source_key == item.target_key:
            before, after = same_key_snapshots[item.source_key]
            store.replace_files(source_category, source_name, before, after)
        else:
            store.rename(
                source_category,
                source_name,
                target_storage_category,
                target_name,
                skill_content=item.content,
            )
        upserted_keys.append(item.target_key)

    for key, category, name in delete_operations:
        store.remove(category, name)
        deleted_keys.append(key)

    return {
        'deleted_keys': deleted_keys,
        'upserted_keys': upserted_keys,
    }


def _set_skill_remote_context(request: SkillOrganizeRequest) -> object:
    previous = lazyllm.globals['agentic_config'] if 'agentic_config' in lazyllm.globals else _MISSING
    current = previous if isinstance(previous, dict) else {}
    lazyllm.globals['agentic_config'] = {
        **current,
        'user_id': request.user_id,
        'task_id': request.requestid,
        'session_id': request.requestid,
    }
    return previous


def _restore_agentic_config(previous: object) -> None:
    if previous is _MISSING:
        lazyllm.globals.pop('agentic_config', None)
    else:
        lazyllm.globals['agentic_config'] = previous


def build_skill_organize_taskid(requestid: str) -> str:
    return f'{requestid}_{datetime.now().strftime("%Y%m%d%H%M%S%f")}'


def _duration_ms(started_perf: float) -> int:
    return max(0, int((perf_counter() - started_perf) * 1000))


def _resolve_artifact_dir(artifact_dir: str | Path | None) -> Path | None:
    if artifact_dir is None or (isinstance(artifact_dir, str) and not artifact_dir.strip()):
        return None
    return Path(artifact_dir)
