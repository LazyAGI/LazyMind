"""Keep workflow launch intent distinct from continuation/control messages."""
from __future__ import annotations

import re
from typing import Any

_CONTROL = re.compile(
    r'^(?:继续(?:执行|生成|下一步)?|下一步|确认|同意|批准|重试|再试一次|'
    r'continue|next|next step|ok|yes|approve|approved|retry|try again)[\s。.!！]*$', re.I,
)


def image_request_update(params: dict[str, Any]) -> str:
    current = str(params.get('current_user_input') or params.get('user_input') or '').strip()
    launch = str(params.get('launch_user_input') or '').strip()
    return '' if not current or current == launch or _CONTROL.fullmatch(current) else current


def effective_image_request(params: dict[str, Any]) -> str:
    launch = str(params.get('launch_user_input') or '').strip()
    update = image_request_update(params)
    if launch and update:
        return f'{launch}\n\nUser update (overrides conflicting launch requirements):\n{update}'
    # Old revisions have only user_input. A bare control message is not a brief.
    return launch or update
