from __future__ import annotations

import hashlib
import json
import traceback
from datetime import datetime
from pathlib import Path
from typing import Any

from lazyllm import LOG
from pydantic import BaseModel

from lazymind.review.skill_review.config import STAGE_FILES, STAGE_REPORT


def stable_hash(value: Any) -> str:
    data = json.dumps(value, ensure_ascii=False, sort_keys=True, default=str)
    return hashlib.sha256(data.encode('utf-8')).hexdigest()


def write_json_file(path: Path, value: Any) -> Path:
    path.parent.mkdir(parents=True, exist_ok=True)
    tmp = path.with_suffix(path.suffix + '.tmp')
    tmp.write_text(
        json.dumps(_jsonable(value), ensure_ascii=False, indent=2, sort_keys=True),
        encoding='utf-8',
    )
    tmp.replace(path)
    return path


def write_report_file(base_dir: Path, value: Any) -> Path:
    return write_json_file(Path(base_dir) / STAGE_FILES[STAGE_REPORT], value)


def start_stage() -> datetime:
    return datetime.now()


def finish_stage_report(
    stage: str,
    started_at: datetime,
    *,
    input_count: int,
    output_count: int,
    errors: list[dict] | None = None,
    status: str | None = None,
    metadata: dict[str, Any] | None = None,
) -> dict[str, Any]:
    ended_at = datetime.now()
    stage_errors = errors or []
    resolved_status = status or ('failed' if stage_errors and output_count == 0 else 'completed')
    report = {
        'stage': stage,
        'status': resolved_status,
        'input_count': input_count,
        'output_count': output_count,
        'error_count': len(stage_errors),
        'started_at': started_at.isoformat(),
        'ended_at': ended_at.isoformat(),
        'duration_ms': max(0, int((ended_at - started_at).total_seconds() * 1000)),
        'errors': stage_errors,
    }
    if metadata:
        report['metadata'] = metadata
    return report


def stage_error(stage: str, item_id: str, exc: Exception) -> dict:
    trace = ''.join(traceback.format_exception(exc.__class__, exc, exc.__traceback__))
    LOG.error(
        f'[SkillReview] stage={stage} item={item_id} '
        f'error_type={exc.__class__.__name__} message={exc}\n{trace}'
    )
    return {
        'stage': stage,
        'item_id': str(item_id),
        'error_type': exc.__class__.__name__,
        'message': str(exc),
        'traceback': trace,
        'created_at': datetime.now().isoformat(),
    }


def _jsonable(value: Any) -> Any:
    if isinstance(value, BaseModel):
        return value.model_dump()
    if isinstance(value, list):
        return [_jsonable(item) for item in value]
    if isinstance(value, dict):
        return {key: _jsonable(item) for key, item in value.items()}
    return value
