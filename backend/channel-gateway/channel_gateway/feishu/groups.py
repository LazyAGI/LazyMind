"""Read-only group discovery through the already-installed official Feishu SDK."""
import re
from time import monotonic

import lark_oapi
from lark_oapi.api.im.v1 import ListChatRequest

from channel_gateway.common.errors import GatewayError


class FeishuGroups:
    def __init__(self, credentials):
        self._client = (lark_oapi.Client.builder().app_id(credentials.app_id)
                        .app_secret(credentials.app_secret).timeout(10).log_level(lark_oapi.LogLevel.ERROR).build())

    @staticmethod
    def _data(call, request):
        try:
            response = call(request)
            if response.success() and response.data is not None:
                return response.data
        except Exception:
            pass
        raise GatewayError(503, 'FEISHU_GROUPS_UNAVAILABLE', '无法读取飞书群，请检查群信息权限并重新授权')

    @staticmethod
    def _target(chat_id, name, chat_type='group'):
        if not isinstance(chat_id, str) or not re.fullmatch(r'oc_[A-Za-z0-9_-]{1,240}', chat_id):
            raise GatewayError(503, 'FEISHU_GROUPS_UNAVAILABLE', '无法读取飞书群信息')
        # Feishu uses the same ``oc_`` chat id namespace for group and
        # one-to-one conversations.  Keep both kinds selectable; the
        # notification picker can then distinguish them for the user.
        kind = 'conversation' if str(chat_type or '').lower() in {'p2p', 'single', 'user'} else 'group'
        return {'recipient_id': chat_id, 'label': str(name or chat_id)[:256], 'kind': kind, 'available': True}

    def list(self, cursor='', limit=100):
        request = ListChatRequest.builder().page_size(limit).page_token(cursor).build()
        data = self._data(self._client.im.v1.chat.list, request)
        items = [self._target(item.chat_id, item.name, getattr(item, 'chat_type', 'group'))
                 for item in (data.items or [])]
        token = str(data.page_token or '') if data.has_more else ''
        if len(items) > limit or len(token) > 2048 or (data.has_more and not token):
            raise GatewayError(503, 'FEISHU_GROUPS_UNAVAILABLE', '无法读取飞书群列表')
        return {'items': items, 'next_cursor': token}

    def get(self, recipient_id):
        # A fresh bot-scoped list proves membership using the same read-only
        # permission as discovery; the separate members API needs extra scopes.
        cursor = ''
        deadline = monotonic() + 15
        seen = set()
        for _ in range(20):
            if monotonic() >= deadline:
                break
            page = self.list(cursor, 100)
            target = next((item for item in page['items'] if item['recipient_id'] == recipient_id), None)
            if target:
                return target
            cursor = page['next_cursor']
            if not cursor:
                raise GatewayError(422, 'NOTIFICATION_TARGET_UNAVAILABLE', '机器人已不在所选群中，请重新选择接收对象')
            if cursor in seen:
                break
            seen.add(cursor)
        raise GatewayError(503, 'FEISHU_GROUPS_UNAVAILABLE', '暂时无法完成群成员校验，请稍后重试')
