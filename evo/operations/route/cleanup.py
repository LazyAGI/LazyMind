from __future__ import annotations

import re
import shutil
from collections.abc import Mapping
from pathlib import Path
from typing import Any

from .router_ledger import RouterAlgorithmLedger

THREAD_ID = re.compile(r'[A-Za-z0-9][A-Za-z0-9_.-]{0,127}')


def delete_managed_workspace(
    row: Mapping[str, Any],
    ledger: RouterAlgorithmLedger,
    managed_repair_root: Path,
) -> str:
    root = managed_repair_root.resolve()
    workspace = _managed_workspace(row, root)
    if workspace is None:
        return 'retained_external'
    code_path = Path(str(row.get('code_path') or '')).resolve()
    for other in ledger.list_algorithms():
        if (
            other['algorithm_id'] != row['algorithm_id']
            and Path(str(other.get('code_path') or '')).resolve() == code_path
        ):
            return 'retained_shared'
    if not workspace.exists():
        return 'missing'
    shutil.rmtree(workspace)
    parent = workspace.parent
    while parent != root:
        try:
            parent.rmdir()
        except OSError:
            break
        parent = parent.parent
    return 'deleted'


def _managed_workspace(row: Mapping[str, Any], root: Path) -> Path | None:
    thread_id = str(row.get('thread_id') or '')
    if THREAD_ID.fullmatch(thread_id) is None:
        return None
    code_path = Path(str(row.get('code_path') or '')).resolve()
    try:
        workspace = code_path.parents[2]
    except IndexError:
        return None
    expected = workspace / 'algorithm' / 'lazymind' / 'chat'
    thread_root = root / thread_id
    if (
        workspace.name != 'candidate'
        or code_path != expected
        or not workspace.is_relative_to(root)
        or not workspace.is_relative_to(thread_root)
    ):
        return None
    return workspace
