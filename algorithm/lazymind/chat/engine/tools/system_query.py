from __future__ import annotations

from typing import Any, Dict, List, Optional

from lazymind.chat.engine.tools.infra import get_core_api, handle_tool_errors, post_core_api, tool_success


def _string_list(value: Any) -> List[str]:
    if value is None:
        return []
    if isinstance(value, str):
        return [item.strip() for item in value.split(',') if item.strip()]
    if isinstance(value, (list, tuple, set)):
        return [str(item).strip() for item in value if str(item).strip()]
    return [str(value).strip()] if str(value).strip() else []


def _bounded_page_size(value: int, default: int = 20) -> int:
    try:
        page_size = int(value)
    except (TypeError, ValueError):
        page_size = default
    if page_size <= 0:
        return default
    return min(page_size, 100)


class SystemQueryToolGroup:
    """Read-only LazyMind system data tools for datasets, documents, and data sources."""

    __public_apis__ = ['list_datasets', 'list_documents', 'list_data_sources', 'aggregate_documents']

    @handle_tool_errors
    def list_datasets(
        self,
        keyword: str = '',
        tags: Optional[List[str]] = None,
        page_size: int = 20,
    ) -> Dict[str, Any]:
        """List datasets/knowledge bases the current user can read."""
        params: Dict[str, Any] = {'page_size': _bounded_page_size(page_size)}
        if keyword:
            params['keyword'] = keyword
        tag_values = _string_list(tags)
        if tag_values:
            params['tags'] = ','.join(tag_values)
        return tool_success('list_datasets', get_core_api('/datasets', params=params))

    @handle_tool_errors
    def list_documents(
        self,
        dataset_ids: List[str],
        keyword: str = '',
        page_size: int = 20,
    ) -> Dict[str, Any]:
        """List readable documents in the selected datasets."""
        payload: Dict[str, Any] = {
            'dataset_ids': _string_list(dataset_ids),
            'page_size': _bounded_page_size(page_size),
        }
        if keyword:
            payload['keyword'] = keyword
        return tool_success('list_documents', post_core_api('/documents:listByDatasets', payload)['response'])

    @handle_tool_errors
    def list_data_sources(
        self,
        keyword: str = '',
        include_external_databases: bool = True,
    ) -> Dict[str, Any]:
        """List connected data-source services and external database connections for the current user."""
        params = {'category': 'datasource'}
        if keyword:
            params['keyword'] = keyword
        result: Dict[str, Any] = {
            'provider_groups': get_core_api('/model_providers/provider_groups', params=params).get('groups', []),
        }
        if include_external_databases:
            result['database_connections'] = get_core_api('/data-sources/database-connections').get('connections', [])
        return tool_success('list_data_sources', result)

    @handle_tool_errors
    def aggregate_documents(
        self,
        dataset_ids: Optional[List[str]] = None,
        file_types: Optional[List[str]] = None,
        document_stages: Optional[List[str]] = None,
        data_source_types: Optional[List[str]] = None,
        creators: Optional[List[str]] = None,
        tags: Optional[List[str]] = None,
        group_by: Optional[List[str]] = None,
    ) -> Dict[str, Any]:
        """Count and sum readable documents, optionally grouped by dataset, type, status, source, creator, or tag."""
        payload = {
            'dataset_ids': _string_list(dataset_ids),
            'file_types': _string_list(file_types),
            'document_stages': _string_list(document_stages),
            'data_source_types': _string_list(data_source_types),
            'creators': _string_list(creators),
            'tags': _string_list(tags),
            'group_by': _string_list(group_by),
        }
        return tool_success(
            'aggregate_documents',
            post_core_api('/system-query/documents:aggregate', payload),
        )
