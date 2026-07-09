from __future__ import annotations

from pathlib import Path
from typing import Any, Protocol
from urllib.parse import urlparse

import requests

from lazymind.chat.engine.tools.infra.skill_validation import parse_skill_frontmatter
from lazymind.config import config as _cfg
from lazymind.review.skill_organize.schemas import SkillFsDraft, SourceSkill


class SkillRemoteFSClient(Protocol):
    def load_skills(self, user_id: str, skills: list[str], *, task_id: str = '') -> list[SourceSkill]:
        ...

    def apply_draft(self, user_id: str, draft: SkillFsDraft, *, task_id: str) -> dict:
        ...


class SkillRemoteFS:
    """Remote skill filesystem client for the Skill Organize pipeline."""

    def __init__(self, base_url: str = '', timeout: float | None = None):
        self.base_url = (base_url or str(_cfg['skill_fs_url'] or '').strip()).rstrip('/')
        self.timeout = timeout or float(_cfg['core_api_timeout'] or 10)
        self.session = requests.Session()
        if not self.base_url:
            raise RuntimeError("'skill_fs_url' is required for skill remote fs access.")

    def load_skills(self, user_id: str, skills: list[str], *, task_id: str = '') -> list[SourceSkill]:
        loaded = [_load_local_skill_if_possible(item) for item in skills]
        if all(item is not None for item in loaded):
            return [item for item in loaded if item is not None]

        organize_task_id = _organize_task_id(task_id)
        result: list[SourceSkill] = []
        for item in skills:
            path = _skill_package_root(item)
            content = self.read_skill(path, user_id=user_id, task_id=organize_task_id)
            frontmatter, _ = parse_skill_frontmatter(content)
            category, name = _category_name_from_path(path)
            result.append(SourceSkill(
                name=str(frontmatter.get('name') or name).strip(),
                path=path,
                category=str(frontmatter.get('category') or category).strip(),
                content=content,
            ))
        return result

    def apply_draft(self, user_id: str, draft: SkillFsDraft, *, task_id: str) -> dict:
        organize_task_id = _organize_task_id(task_id)
        deleted_paths: list[str] = []
        upserted_paths: list[str] = []
        for path in draft.delete_paths:
            package_root = _skill_package_root(path)
            self.trash_skill(package_root, user_id=user_id)
            deleted_paths.append(package_root)
        for item in draft.upsert_skills:
            path = _skill_package_root(item.path)
            package_root = path
            if not self.exists(package_root, user_id=user_id, task_id=organize_task_id):
                self.mkdir(package_root, user_id=user_id, task_id=organize_task_id)
            self.write_skill(path, item.content, user_id=user_id, task_id=organize_task_id)
            upserted_paths.append(path)
        return {
            'deleted_paths': deleted_paths,
            'upserted_paths': upserted_paths,
        }

    def read_skill(self, path: str, *, user_id: str, task_id: str = '') -> str:
        response = self._request(
            'GET',
            'content',
            params=_params(user_id=user_id, task_id=task_id, path=_skill_file_path(path)),
        )
        return response.text

    def exists(self, path: str, *, user_id: str, task_id: str = '') -> bool:
        payload = self._request_json(
            'GET',
            'exists',
            params=_params(user_id=user_id, task_id=task_id, path=_normalize_remote_path(path)),
        )
        return bool(payload.get('exists'))

    def mkdir(self, path: str, *, user_id: str, task_id: str) -> None:
        self._request_json(
            'POST',
            'dir',
            params=_params(user_id=user_id, task_id=task_id),
            json={'path': _normalize_remote_path(path), 'recursive': True},
        )

    def write_skill(self, path: str, content: str, *, user_id: str, task_id: str) -> None:
        self._request_json(
            'PUT',
            'content',
            params=_params(user_id=user_id, task_id=task_id, path=_skill_file_path(path)),
            data=content.encode('utf-8'),
            headers={'Content-Type': 'text/markdown; charset=utf-8'},
        )

    def trash_skill(self, path: str, *, user_id: str) -> None:
        self._request_json(
            'POST',
            'trash',
            params=_params(user_id=user_id, path=_normalize_remote_path(path)),
            json={'path': _normalize_remote_path(path)},
        )

    def _request(self, method: str, endpoint: str, **kwargs) -> requests.Response:
        response = self.session.request(
            method,
            f'{self.base_url}/remote-fs/{endpoint}',
            timeout=self.timeout,
            **kwargs,
        )
        if not response.ok:
            raise RuntimeError(
                f'SkillRemoteFS {method} /remote-fs/{endpoint} failed '
                f'with HTTP {response.status_code}: {response.text[:500]}'
            )
        return response

    def close(self) -> None:
        self.session.close()

    def _request_json(self, method: str, endpoint: str, **kwargs) -> dict:
        response = self._request(method, endpoint, **kwargs)
        try:
            payload: Any = response.json()
        except ValueError as exc:
            raise RuntimeError(f'SkillRemoteFS {method} /remote-fs/{endpoint} returned non-JSON response') from exc
        if isinstance(payload, dict) and payload.get('code') not in (None, 0):
            raise RuntimeError(payload.get('message') or payload.get('msg') or f'remote-fs {endpoint} failed')
        if isinstance(payload, dict) and isinstance(payload.get('data'), dict):
            return payload['data']
        return payload if isinstance(payload, dict) else {}


def build_skill_remote_fs(base_url: str = '') -> SkillRemoteFSClient:
    return SkillRemoteFS(base_url=base_url)


def _load_local_skill_if_possible(value: str) -> SourceSkill | None:
    path = Path(value)
    if not path.exists() or not path.is_file():
        return None
    content = path.read_text(encoding='utf-8')
    frontmatter, _ = parse_skill_frontmatter(content)
    name = str(frontmatter.get('name') or path.parent.name).strip()
    category = str(frontmatter.get('category') or path.parent.parent.name).strip()
    return SourceSkill(
        name=name,
        path=_normalize_local_skill_path(path),
        category=category,
        content=content,
    )


def _params(**kwargs: str) -> dict[str, str]:
    return {key: value for key, value in kwargs.items() if value}


def _organize_task_id(task_id: str) -> str:
    normalized = str(task_id or '').strip()
    if not normalized:
        raise ValueError('task_id is required for skill organize remote fs operations')
    return normalized if normalized.startswith('org_') else f'org_{normalized}'


def _normalize_remote_path(path: str) -> str:
    raw = str(path or '').strip()
    if '://' in raw:
        parsed = urlparse(raw)
        raw = f'{parsed.netloc}{parsed.path}'
    return raw.strip('/')


def _skill_file_path(path: str) -> str:
    normalized = _normalize_remote_path(path)
    if not normalized:
        raise ValueError('skill path must be non-empty')
    if normalized.endswith('/SKILL.md'):
        return normalized
    if normalized.endswith('.md'):
        return normalized
    return f'{normalized}/SKILL.md'


def _skill_package_root(path: str) -> str:
    normalized = _normalize_remote_path(path)
    if normalized.endswith('/SKILL.md'):
        return normalized[:-len('/SKILL.md')]
    if normalized.endswith('.md'):
        parts = normalized.split('/')
        if len(parts) < 4:
            raise ValueError(f'cannot infer skill package root from path {path!r}')
        return '/'.join(parts[:3])
    return normalized


def _category_name_from_path(path: str) -> tuple[str, str]:
    root = _skill_package_root(path)
    parts = root.split('/')
    if len(parts) < 3 or parts[0] != 'skills':
        raise ValueError(f'skill path must be skills/<category>/<name>, got {path!r}')
    return parts[1], parts[2]


def _normalize_local_skill_path(path: Path) -> str:
    if path.name == 'SKILL.md':
        return str(path.parent)
    return str(path)
