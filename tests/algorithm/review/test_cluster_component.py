from importlib import import_module
import sys
from unittest.mock import Mock

import pytest

from lazymind.review.cluster_component import cluster_requirement_summary


@pytest.mark.parametrize('pipeline', ['skill_review', 'traj_to_skill'])
@pytest.mark.parametrize('disabled', [True, False], ids=['disabled', 'missing-package'])
def test_only_larger_batches_require_the_cluster_component(monkeypatch, pipeline, disabled):
    cluster = import_module(f'lazymind.review.{pipeline}.cluster')
    draft = cluster.SkillDraft(
        session_id='sample',
        cluster_signature={'intent': 'summarize', 'boundaries': ''},
        refined_trajectory={},
        guidelines={},
    )
    drafts = [draft] * (cluster.DEFAULT_LLM_CLUSTER_THRESHOLD + 1)
    llm_result = [cluster.TaskCluster(task_scope='summarize', drafts=drafts[:-1])]
    llm_cluster = Mock(return_value=llm_result)
    embedding = Mock()
    monkeypatch.setenv('LAZYMIND_CLUSTER_ENABLED', '0' if disabled else '1')
    if not disabled:
        monkeypatch.setitem(sys.modules, 'umap', None)
    monkeypatch.setattr(cluster, '_llm_clusters', llm_cluster)

    assert cluster.cluster_drafts(drafts[:-1], embedding, llm=object())[0] == llm_result
    clusters, report = cluster.cluster_drafts(drafts, embedding, llm=object())

    assert clusters == []
    assert report['status'] == 'failed'
    assert cluster_requirement_summary(report['errors']) == {'error_code': 'skill_review_cluster_required'}
    assert '#python-cluster-dependency' in report['errors'][0]['message']
    llm_cluster.assert_called_once()
    embedding.assert_not_called()
