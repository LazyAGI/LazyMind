from __future__ import annotations

from typing import Any

from channel_gateway.common.domain.outbound import (
    CloudDocumentPresentation,
)
from channel_gateway.common.ports.core import CloudDocumentClient


class CloudDocumentActions:
    """Read-only access through cloud accounts already connected to LazyMind."""

    def __init__(self, client: CloudDocumentClient):
        self._client = client

    def list_accounts(
        self,
        *,
        owner_user_id: str,
        request_id: str,
        keyword: str,
        status: str,
        page_token: str,
    ) -> tuple[str, CloudDocumentPresentation]:
        result = self._client.list_cloud_documents(
            owner_user_id=owner_user_id,
            request_id=request_id,
            keyword=keyword,
            status=status,
            page_token=page_token,
        )
        items = _objects(result.get('items'))
        page = _object(result.get('page'))
        lines = ['LazyMind 已授权云文档账号：']
        lines.extend(
            f'{index}. {item.get("name") or item.get("id") or "未命名"}'
            for index, item in enumerate(items, start=1)
        )
        if not items:
            lines.append('暂无已授权并启用对话的云文档账号。')
        return '\n'.join(lines), CloudDocumentPresentation(
            kind='cloud_document',
            mode='sources',
            items=tuple(items),
            next_page_token=str(page.get('next_page_token') or ''),
            total=_optional_int(page.get('total')),
        )

    def browse(
        self,
        *,
        owner_user_id: str,
        request_id: str,
        source_id: str,
        node_ref: str = '',
        target_type: str = '',
        target_ref: str = '',
        page_token: str = '',
    ) -> tuple[str, CloudDocumentPresentation]:
        result = self._client.get_cloud_document(
            owner_user_id=owner_user_id,
            request_id=request_id,
            source_id=source_id,
            node_ref=node_ref,
            target_type=target_type,
            target_ref=target_ref,
            page_token=page_token,
        )
        source = _object(result.get('source'))
        source.update({
            'node_ref': node_ref,
            'target_type': target_type,
            'target_ref': target_ref,
        })
        items = _objects(result.get('documents'))
        page = _object(result.get('documents_page'))
        name = str(source.get('name') or source_id)
        lines = [f'云文档账号：{name}']
        for index, item in enumerate(items, start=1):
            title = str(
                item.get('display_name')
                or item.get('name')
                or item.get('id')
                or '未命名'
            )
            kind = '目录' if item.get('has_children') else '文档'
            lines.append(f'{index}. {title}（{kind}）')
        if not items:
            lines.append('当前目录没有可访问的文档。')
        return '\n'.join(lines), CloudDocumentPresentation(
            kind='cloud_document',
            mode='documents',
            source=source,
            items=tuple(items),
            next_page_token=str(page.get('next_page_token') or ''),
            total=_optional_int(page.get('total')),
        )

    def search(
        self,
        *,
        owner_user_id: str,
        request_id: str,
        source_id: str,
        query: str,
        page_token: str,
    ) -> tuple[str, CloudDocumentPresentation]:
        result = self._client.search_cloud_documents(
            owner_user_id=owner_user_id,
            request_id=request_id,
            source_id=source_id,
            query=query,
            page_token=page_token,
        )
        items = _objects(result.get('hits'))
        page = _object(result.get('page'))
        lines = [f'在线搜索云文档：{query}']
        lines.extend(
            f'{index}. {item.get("display_name") or item.get("key") or "未命名"}'
            for index, item in enumerate(items, start=1)
        )
        if not items:
            lines.append('没有匹配的在线文档或目录。')
        return '\n'.join(lines), CloudDocumentPresentation(
            kind='cloud_document',
            mode='search',
            source={'id': source_id},
            items=tuple(items),
            query=query,
            next_page_token=str(page.get('next_page_token') or ''),
            total=_optional_int(page.get('total')),
        )


def _object(value: Any) -> dict[str, Any]:
    return dict(value) if isinstance(value, dict) else {}


def _objects(value: Any) -> list[dict[str, Any]]:
    return [dict(item) for item in value if isinstance(item, dict)] if isinstance(value, list) else []


def _optional_int(value: Any) -> int | None:
    if isinstance(value, bool) or value is None:
        return None
    try:
        return int(value)
    except (TypeError, ValueError):
        return None
