from __future__ import annotations

import threading
from collections.abc import Callable
from typing import Any

import lark_oapi

from channel_gateway.feishu.domain import (
    FeishuAppRegistration,
    FeishuRuntimeError,
)


_ADDONS = {
    'scopes': {
        'tenant': [
            'im:message:send_as_bot',
            'im:message.p2p_msg:readonly',
            'im:resource',
            'cardkit:card:write',
        ],
    },
    'events': {
        'items': {
            'tenant': ['im.message.receive_v1'],
        },
    },
    'callbacks': {
        'items': ['card.action.trigger'],
    },
}


class LarkAppRegistrar:
    """Thin adapter around Feishu's official one-click app registration."""

    def register(
        self,
        *,
        on_qr_code: Callable[[str, int], None],
        on_status_change: Callable[[str], None],
        cancel_event: threading.Event,
    ) -> FeishuAppRegistration:
        def qr_callback(info: Any) -> None:
            payload = info if isinstance(info, dict) else {}
            url = str(payload.get('url') or '').strip()
            expire_in = int(payload.get('expire_in') or 0)
            if not url or expire_in <= 0:
                raise FeishuRuntimeError(
                    'Feishu registration returned an invalid QR code'
                )
            on_qr_code(url, expire_in)

        def status_callback(info: Any) -> None:
            if isinstance(info, dict):
                status = str(
                    info.get('status')
                    or info.get('state')
                    or ''
                )
            else:
                status = str(info or '')
            if status:
                on_status_change(status)

        try:
            result = lark_oapi.register_app(
                on_qr_code=qr_callback,
                on_status_change=status_callback,
                cancel_event=cancel_event,
                source='lazymind-channel-gateway',
                app_preset={
                    'name': '{user} 的 LazyMind 助手',
                    'desc': '在飞书对话中继续 LazyMind 会话',
                },
                addons=_ADDONS,
                create_only=True,
            )
        except Exception as exc:
            if cancel_event.is_set():
                raise FeishuRuntimeError(
                    'Feishu registration was canceled'
                ) from exc
            raise FeishuRuntimeError(
                f'Feishu registration failed: {exc}'
            ) from exc
        if not isinstance(result, dict):
            raise FeishuRuntimeError(
                'Feishu registration returned an invalid result'
            )
        user_info = result.get('user_info')
        if not isinstance(user_info, dict):
            user_info = {}
        app_id = str(
            result.get('client_id')
            or result.get('app_id')
            or ''
        ).strip()
        app_secret = str(
            result.get('client_secret')
            or result.get('app_secret')
            or ''
        ).strip()
        owner_open_id = str(
            user_info.get('open_id')
            or result.get('open_id')
            or ''
        ).strip()
        tenant_brand = str(
            result.get('tenant_brand')
            or user_info.get('tenant_brand')
            or 'feishu'
        ).strip().lower()
        if tenant_brand not in {'', 'feishu'}:
            raise FeishuRuntimeError(
                'This release supports Feishu accounts only'
            )
        if not app_id or not app_secret or not owner_open_id:
            raise FeishuRuntimeError(
                'Feishu registration result is missing app credentials'
            )
        return FeishuAppRegistration(
            app_id=app_id,
            app_secret=app_secret,
            owner_open_id=owner_open_id,
            owner_name=str(
                user_info.get('name')
                or user_info.get('display_name')
                or ''
            ).strip(),
            tenant_key=str(
                user_info.get('tenant_key')
                or result.get('tenant_key')
                or ''
            ).strip(),
        )
