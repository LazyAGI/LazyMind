from __future__ import annotations

from dataclasses import dataclass
from pathlib import Path


DEFAULT_MIN_USER_TURNS = 2
DEFAULT_MIN_TOOL_TURNS = 5

STAGE_SESSION = 'session'
STAGE_TRAJECTORY = 'trajectory'
STAGE_DRAFT = 'draft'
STAGE_CLUSTER = 'cluster'
STAGE_OUTLINE = 'outline'
STAGE_CANDIDATE = 'candidate'
STAGE_RESOLUTION = 'resolution'
STAGE_RESULT = 'result'

STAGES = [
    STAGE_SESSION,
    STAGE_TRAJECTORY,
    STAGE_DRAFT,
    STAGE_CLUSTER,
    STAGE_OUTLINE,
    STAGE_CANDIDATE,
    STAGE_RESOLUTION,
    STAGE_RESULT,
]

STAGE_FILES = {
    STAGE_SESSION: '01_session_raw.json',
    STAGE_TRAJECTORY: '02_trajectory.json',
    STAGE_DRAFT: '03_draft.json',
    STAGE_CLUSTER: '04_clusters.json',
    STAGE_OUTLINE: '05_outline.json',
    STAGE_CANDIDATE: '06_candidate.json',
    STAGE_RESOLUTION: '07_resolution.json',
    STAGE_RESULT: 'result.json',
}

MANIFEST_FILE = 'manifest.json'
LOCK_FILE = 'run.lock'
