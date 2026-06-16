from __future__ import annotations

from typing import Dict

from lazyllm.tools.agent.skill_manager import SkillManager as LazySkillManager
from lazyllm.tools.fs.client import FS

from lazymind.chat.integrations import RemoteFS  # noqa: F401
from lazymind.model_config import extract_skill_fs_source


def extract_skill_category_from_path(skill_dir: str, skill_name: str) -> str:
    path = str(skill_dir or '').rstrip('/')
    marker = '/skills/'

    if marker in path:
        tail = path.split(marker, 1)[1]
    else:
        tail = path.strip('/')

    parts = [part for part in tail.split('/') if part and part != '.']
    if not parts:
        return ''

    if parts[-1] == skill_name:
        parts = parts[:-1]

    return parts[-1] if parts else ''


def build_skill_identity(category: str, skill_name: str) -> str:
    return f'{category}/{skill_name}' if category else skill_name


def list_all_skill_entries(skill_fs_url: str) -> Dict[str, Dict[str, str]]:
    manager = LazySkillManager(dir=skill_fs_url, fs=FS)
    results: Dict[str, Dict[str, str]] = {}

    for skill_dir, skill_md in manager._iter_skill_files():
        if manager._fs_getsize(skill_md) > manager._max_skill_md_bytes:
            continue
        try:
            content = manager._fs_read(skill_md)
        except Exception:
            continue

        meta = manager._extract_yaml_meta(content)
        if not manager._is_meta_valid(meta):
            continue

        name = str(meta.get('name') or '').strip()
        if not name:
            continue

        category = extract_skill_category_from_path(skill_dir, name)
        skill_id = build_skill_identity(category, name)
        if skill_id in results:
            continue

        results[skill_id] = {
            'name': name,
            'category': category,
            'path': skill_dir,
            'source': extract_skill_fs_source(skill_dir),
            'content': content,
        }
    return results


def list_all_skills_with_category(skill_fs_url: str) -> Dict[str, str]:
    results: Dict[str, str] = {}
    for info in list_all_skill_entries(skill_fs_url).values():
        results.setdefault(info['name'], info['category'])
    return results


def is_writable_skill_source(source: str) -> bool:
    return source == 'remote'
