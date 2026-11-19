from lazymind.common.optional_components import ComponentRequiredError, require_component


CLUSTER_REQUIRED_CODE = 'skill_review_cluster_required'


def require_umap():
    require_component('cluster')
    try:
        from umap import UMAP
    except ImportError as exc:
        raise ComponentRequiredError('cluster') from exc
    return UMAP


def cluster_requirement_summary(errors):
    if any(
        error.get('item_id') == 'embedding_clustering'
        and error.get('error_type') == ComponentRequiredError.__name__
        for error in errors
    ):
        return {'error_code': CLUSTER_REQUIRED_CODE}
    return {}
