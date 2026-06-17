from __future__ import annotations

from typing import Literal

from evo.artifact_runtime import ArtifactKey

StepName = Literal['dataset', 'eval', 'analysis', 'repair', 'abtest']

DATASET_ROOT = ArtifactKey.of('eval.dataset')
EVAL_ROOT = ArtifactKey.of('eval.summary')
ANALYSIS_ROOT = ArtifactKey.of('analysis.summary')
REPAIR_ROOT = ArtifactKey.of('repair.verified_patch')
ABTEST_ROOT = ArtifactKey.of('abtest.comparison')

STEP_ROOTS: dict[StepName, ArtifactKey] = {
    'dataset': DATASET_ROOT,
    'eval': EVAL_ROOT,
    'analysis': ANALYSIS_ROOT,
    'repair': REPAIR_ROOT,
    'abtest': ABTEST_ROOT,
}


def case_ids(count: int) -> tuple[str, ...]:
    if count < 1:
        raise ValueError('case count must be >= 1')
    return tuple(f'case_{index:04d}' for index in range(1, count + 1))


def case_key(artifact_id: str, case_id: str) -> ArtifactKey:
    return ArtifactKey(artifact_id, case_id)
