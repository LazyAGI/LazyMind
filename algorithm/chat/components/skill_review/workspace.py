from __future__ import annotations

import hashlib
import json
import shutil
from contextlib import contextmanager
from datetime import datetime, timezone
from pathlib import Path
from typing import Any, Iterator, Type, TypeVar

from pydantic import BaseModel

from chat.components.skill_review.config import LOCK_FILE, MANIFEST_FILE, STAGE_FILES
from chat.components.skill_review.schemas import StageManifest

T = TypeVar('T', bound=BaseModel)


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
        force: bool = False,
    ) -> None:
        self.base_dir = Path(base_dir)
        self.session_id = session_id
        self.path = self.base_dir / _safe_path_segment(session_id)
        self.input_hash = input_hash
        if force and self.path.exists():
            shutil.rmtree(self.path)
        self.path.mkdir(parents=True, exist_ok=True)

    @property
    def manifest_path(self) -> Path:
        return self.path / MANIFEST_FILE

    @property
    def lock_path(self) -> Path:
        return self.path / LOCK_FILE

    def stage_path(self, stage: str) -> Path:
        try:
            filename = STAGE_FILES[stage]
        except KeyError as exc:
            raise WorkspaceError(f'unknown skill review stage: {stage}') from exc
        return self.path / filename

    @contextmanager
    def lock(self) -> Iterator[None]:
        if self.lock_path.exists():
            raise WorkspaceError(f'skill review workspace is locked: {self.lock_path}')
        self.lock_path.write_text(utc_now(), encoding='utf-8')
        try:
            yield
        finally:
            self.lock_path.unlink(missing_ok=True)

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

    def mark_stage_started(self, stage: str) -> None:
        manifest = self.load_manifest() or self.init_manifest()
        manifest.current_stage = stage
        manifest.status = 'running'
        manifest.error = None
        self.save_manifest(manifest)

    def mark_stage_completed(self, stage: str) -> None:
        manifest = self.load_manifest() or self.init_manifest()
        manifest.current_stage = stage
        if stage not in manifest.completed_stages:
            manifest.completed_stages.append(stage)
        manifest.error = None
        self.save_manifest(manifest)

    def mark_failed(self, error: str) -> None:
        manifest = self.load_manifest() or self.init_manifest()
        manifest.status = 'failed'
        manifest.error = error
        self.save_manifest(manifest)

    def mark_completed(self) -> None:
        manifest = self.load_manifest() or self.init_manifest()
        manifest.status = 'completed'
        manifest.current_stage = 'result'
        manifest.error = None
        self.save_manifest(manifest)

    def has_valid_stage(self, stage: str, model: Type[T] | None = None) -> bool:
        path = self.stage_path(stage)
        if not path.exists():
            return False
        if model is None:
            return True
        try:
            self.read_model(stage, model)
            return True
        except Exception:
            return False

    def read_json(self, stage: str) -> Any:
        return json.loads(self.stage_path(stage).read_text(encoding='utf-8'))

    def read_model(self, stage: str, model: Type[T]) -> T:
        return model.model_validate(self.read_json(stage))

    def write_json(self, stage: str, value: Any) -> Path:
        path = self.stage_path(stage)
        self.write_json_path(path, _jsonable(value))
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
