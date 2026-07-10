from __future__ import annotations

import json
from collections.abc import Mapping
from pathlib import Path
from typing import Any


def read_worker_report(path: Path) -> dict[str, Any]:
    try:
        value = json.loads(path.read_text(encoding='utf-8'))
    except (json.JSONDecodeError, OSError) as exc:
        return {'status': 'missing', 'reason': type(exc).__name__}
    if not isinstance(value, Mapping):
        return {'status': 'invalid', 'reason': 'report_not_mapping'}
    files = [str(item).strip() for item in value.get('files_changed') or () if str(item or '').strip()]
    locations = [dict(item) for item in value.get('confirmed_locations') or () if isinstance(item, Mapping)]
    status = str(value.get('status') or '').strip() or 'invalid'
    return {
        'status': status,
        'mode': str(value.get('mode') or '').strip(),
        'files_changed': files,
        'confirmed_locations': locations[:20],
        'touched_symbols': [str(item).strip() for item in value.get('touched_symbols') or ()
                            if str(item or '').strip()][:40],
        'change_intent': str(value.get('change_intent') or '').strip(),
        'risk': str(value.get('risk') or '').strip(),
        'notes': str(value.get('notes') or '').strip()[:1000],
    }
