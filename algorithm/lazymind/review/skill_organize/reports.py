from __future__ import annotations

from pathlib import Path
from typing import Any

from lazymind.review.skill_organize.config import STAGE_FILES
from lazymind.review.skill_review.reports import (
    finish_stage_report,
    stable_hash,
    stage_error,
    start_stage,
    write_json_file,
)


def write_stage_file(base_dir: Path | None, taskid: str, stage: str, value: Any) -> Path | None:
    if base_dir is None:
        return None
    filename = STAGE_FILES[stage]
    return write_json_file(Path(base_dir) / taskid / filename, value)


__all__ = [
    'finish_stage_report',
    'stable_hash',
    'stage_error',
    'start_stage',
    'write_json_file',
    'write_stage_file',
]
