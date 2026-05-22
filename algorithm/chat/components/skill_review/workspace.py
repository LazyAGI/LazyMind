from __future__ import annotations

import hashlib
import json
from datetime import datetime, timezone
from pathlib import Path
from typing import Any

from pydantic import BaseModel

from chat.components.skill_review.config import MANIFEST_FILE, STAGE_FILES
from chat.components.skill_review.schemas import StageManifest


class WorkspaceError(RuntimeError):
    pass


def utc_now() -> str:
    return datetime.now(timezone.utc).isoformat()


def stable_hash(value: Any) -> str:
    data = json.dumps(value, ensure_ascii=False, sort_keys=True, default=str)
    return hashlib.sha256(data.encode('utf-8')).hexdigest()


class SkillReviewWorkspace:
    def __init__(
        self,
        *,
        base_dir: Path,
        session_id: str,
        input_hash: str,
    ) -> None:
        self.base_dir = Path(base_dir)
        self.session_id = session_id
        self.path = self.base_dir / _safe_path_segment(session_id)
        self.input_hash = input_hash
        self.path.mkdir(parents=True, exist_ok=True)

    @property
    def manifest_path(self) -> Path:
        return self.path / MANIFEST_FILE

    def stage_path(self, stage: str) -> Path:
        try:
            filename = STAGE_FILES[stage]
        except KeyError as exc:
            raise WorkspaceError(f'unknown skill review stage: {stage}') from exc
        return self.path / filename

    def load_manifest(self) -> StageManifest | None:
        if not self.manifest_path.exists():
            return None
        data = json.loads(self.manifest_path.read_text(encoding='utf-8'))
        return StageManifest.model_validate(data)

    def init_manifest(self) -> StageManifest:
        existing = self.load_manifest()
        now = utc_now()
        if existing is not None and existing.input_hash == self.input_hash:
            existing.updated_at = now
            existing.status = 'running'
            existing.error = None
            self.save_manifest(existing)
            return existing
        manifest = StageManifest(
            session_id=self.session_id,
            status='running',
            input_hash=self.input_hash,
            created_at=now,
            updated_at=now,
        )
        self.save_manifest(manifest)
        return manifest

    def save_manifest(self, manifest: StageManifest) -> None:
        manifest.updated_at = utc_now()
        self.write_json_path(self.manifest_path, manifest.model_dump())

    def start_stage(self, stage: str) -> None:
        self.stage_path(stage)
        manifest = self.load_manifest() or self.init_manifest()
        manifest.current_stage = stage
        manifest.status = 'running'
        manifest.error = None
        self.save_manifest(manifest)

    def complete_stage(self, stage: str) -> None:
        self.stage_path(stage)
        manifest = self.load_manifest() or self.init_manifest()
        manifest.current_stage = stage
        if stage not in manifest.completed_stages:
            manifest.completed_stages.append(stage)
        manifest.error = None
        self.save_manifest(manifest)

    def fail(self, error: str) -> None:
        manifest = self.load_manifest() or self.init_manifest()
        manifest.status = 'failed'
        manifest.error = error
        self.save_manifest(manifest)

    def complete(self) -> None:
        manifest = self.load_manifest() or self.init_manifest()
        manifest.status = 'completed'
        manifest.current_stage = 'result'
        manifest.error = None
        self.save_manifest(manifest)

    def can_resume_stage(self, stage: str) -> bool:
        manifest = self.load_manifest()
        if manifest is None or manifest.input_hash != self.input_hash:
            return False
        if stage not in manifest.completed_stages:
            return False
        try:
            self.read_json(stage)
            return True
        except Exception:
            return False

    def read_json(self, stage: str) -> Any:
        return json.loads(self.stage_path(stage).read_text(encoding='utf-8'))

    def write_json(self, stage: str, value: Any) -> Path:
        path = self.stage_path(stage)
        self.write_json_path(path, _jsonable(value))
        return path

    def checkpoint(self, stage: str, value: Any) -> Path:
        self.start_stage(stage)
        path = self.write_json(stage, value)
        self.complete_stage(stage)
        return path

    @staticmethod
    def write_json_path(path: Path, value: Any) -> None:
        tmp = path.with_suffix(path.suffix + '.tmp')
        tmp.write_text(
            json.dumps(_jsonable(value), ensure_ascii=False, indent=2, sort_keys=True),
            encoding='utf-8',
        )
        tmp.replace(path)


def _jsonable(value: Any) -> Any:
    if isinstance(value, BaseModel):
        return value.model_dump()
    if isinstance(value, list):
        return [_jsonable(item) for item in value]
    if isinstance(value, dict):
        return {key: _jsonable(item) for key, item in value.items()}
    return value


def _safe_path_segment(value: str) -> str:
    safe = ''.join(ch if ch.isalnum() or ch in ('-', '_', '.') else '_' for ch in value.strip())
    return safe or 'session'
