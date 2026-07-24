from __future__ import annotations


DEFAULT_SKILL_ORGANIZE_LIMIT = 20
MAX_SKILL_ORGANIZE_LIMIT = 20
DEFAULT_BACKGROUND_WORKERS = 2
DEFAULT_MATERIALIZE_WORKERS = 4
DEFAULT_REPORT_DIR_NAME = 'lazyrag_skill_organize_reports'

STAGE_SOURCE = 'source'
STAGE_SUMMARY = 'summary'
STAGE_PLAN = 'plan'
STAGE_DRAFT = 'draft'
STAGE_VALIDATION = 'validation'
STAGE_RESULT = 'result'
STAGE_REPORT = 'report'

STAGE_FILES = {
    STAGE_SOURCE: '01_source_skills.json',
    STAGE_SUMMARY: '02_skill_summaries.json',
    STAGE_PLAN: '03_organize_plan.json',
    STAGE_DRAFT: '04_fs_draft.json',
    STAGE_VALIDATION: '05_validation.json',
    STAGE_RESULT: 'result.json',
    STAGE_REPORT: 'failure_report.json',
}
