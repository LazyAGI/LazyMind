from __future__ import annotations

from dataclasses import dataclass
from pathlib import Path


DEFAULT_WORK_DIR = Path('/tmp/lazyrag-skill-review')
DEFAULT_MIN_USER_TURNS = 3
DEFAULT_MIN_TOOL_TURNS = 2

STAGE_SESSION = 'session'
STAGE_TRAJECTORY = 'trajectory'
STAGE_CRAFT = 'craft'
STAGE_CLUSTER = 'cluster'
STAGE_OUTLINE = 'outline'
STAGE_CANDIDATE = 'candidate'
STAGE_DECISION = 'decision'
STAGE_RESULT = 'result'

STAGES = [
    STAGE_SESSION,
    STAGE_TRAJECTORY,
    STAGE_CRAFT,
    STAGE_CLUSTER,
    STAGE_OUTLINE,
    STAGE_CANDIDATE,
    STAGE_DECISION,
    STAGE_RESULT,
]

STAGE_FILES = {
    STAGE_SESSION: '01_session_raw.json',
    STAGE_TRAJECTORY: '02_trajectory.json',
    STAGE_CRAFT: '03_craft.json',
    STAGE_CLUSTER: '04_clusters.json',
    STAGE_OUTLINE: '05_outline.json',
    STAGE_CANDIDATE: '06_candidate.json',
    STAGE_DECISION: '07_decision.json',
    STAGE_RESULT: 'result.json',
}

MANIFEST_FILE = 'manifest.json'
LOCK_FILE = 'run.lock'


@dataclass(frozen=True)
class SkillReviewRuntimeConfig:
    session_id: str
    session_db_path: str
    work_dir: Path = DEFAULT_WORK_DIR
    min_user_turns: int = DEFAULT_MIN_USER_TURNS
    min_tool_turns: int = DEFAULT_MIN_TOOL_TURNS
    resume: bool = True
    force: bool = False
