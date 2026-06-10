from __future__ import annotations


DEFAULT_MIN_USER_TURNS = 2
DEFAULT_MIN_TOOL_TURNS = 5

STAGE_TRAJECTORY = 'trajectory'
STAGE_DRAFT = 'draft'
STAGE_CLUSTER = 'cluster'
STAGE_OUTLINE = 'outline'
STAGE_CANDIDATE = 'candidate'
STAGE_RESOLUTION = 'resolution'
STAGE_RESULT = 'result'
STAGE_REPORT = 'report'

DEFAULT_STAGE_WORKERS = 4
DEFAULT_BACKGROUND_WORKERS = 2
DEFAULT_LLM_CALL_TIMEOUT_SECONDS = 180
DEFAULT_EMBEDDING_MAX_CHARS = 4000
DEFAULT_EMBEDDING_RETRIES = 3
DEFAULT_REPORT_DIR_NAME = 'lazyrag_skill_review_reports'

STAGE_FILES = {
    STAGE_TRAJECTORY: '01_trajectory.json',
    STAGE_DRAFT: '02_draft.json',
    STAGE_CLUSTER: '03_clusters.json',
    STAGE_OUTLINE: '04_outline.json',
    STAGE_CANDIDATE: '05_candidate.json',
    STAGE_RESOLUTION: '06_resolution.json',
    STAGE_RESULT: 'result.json',
    STAGE_REPORT: 'failure_report.json',
}
