"""Keep binary transport out of model-facing artifact results.

Apply at tool boundaries, never inside the SDK: file consumers still need the
original bytes. Return references/metadata rather than duplicating file payloads.
"""
import json
import re
from typing import Any

_DATA_URL = re.compile(r'data:[^\s,;]*;base64,[A-Za-z0-9+/=_-]+')


def artifact_output(value: Any) -> Any:
    if isinstance(value, dict):
        return {
            key: artifact_output(item) for key, item in value.items()
            if 'base64' not in str(key).lower() and str(key).lower() != 'b64'
        }
    if isinstance(value, (list, tuple)):
        return [artifact_output(item) for item in value]
    if isinstance(value, str):
        # Some artifact adapters serialize their value as JSON a second time.
        if 'base64' in value.lower() and value.lstrip().startswith(('{', '[')):
            try:
                return json.dumps(artifact_output(json.loads(value)), ensure_ascii=False)
            except ValueError:
                pass
        return _DATA_URL.sub('[binary omitted; use the artifact reference]', value)
    return value
