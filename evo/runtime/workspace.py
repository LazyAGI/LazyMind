from __future__ import annotations

import json
import shutil
from pathlib import Path
from typing import Any

from .. import validate_id


class DraftWorkspace:
    def __init__(self, root: str | Path):
        self.root = Path(root)
        self.root.mkdir(parents=True, exist_ok=True)

    def dir_for(self, operation_run_id: str) -> Path:
        validate_id(operation_run_id, 'operation_run_id')
        return self.root / operation_run_id

    def prepare(self, operation_run_id: str) -> Path:
        return self._fresh_dir(self.dir_for(operation_run_id))

    def write_json(self, operation_run_id: str, name: str, data: Any) -> Path:
        path = self.dir_for(operation_run_id) / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(json.dumps(data, ensure_ascii=False, indent=2, sort_keys=True), encoding='utf-8')
        return path

    def prepare_tx(self, operation_run_id: str) -> Path:
        return self._fresh_dir(self.dir_for(operation_run_id) / 'tx')

    def discard(self, operation_run_id: str) -> None:
        shutil.rmtree(self.dir_for(operation_run_id), ignore_errors=True)

    @staticmethod
    def _fresh_dir(path: Path) -> Path:
        if path.exists(): shutil.rmtree(path)
        path.mkdir(parents=True)
        return path
