from __future__ import annotations

from lazymind.chat.integrations.remote_fs import RemoteFS


def _remote_fs() -> RemoteFS:
    return RemoteFS()


def _skill_path(category: str, name: str) -> str:
    return f'remote://skills/{category}/{name}/SKILL.md'


def _skill_dir(category: str, name: str) -> str:
    return f'remote://skills/{category}/{name}'


def create_remote_skill(category: str, name: str, content: str) -> dict:
    path = _skill_path(category, name)
    _remote_fs().write(path, content)
    return {
        'persisted': 'remote_fs',
        'path': path,
        'name': name,
        'category': category,
        'action': 'create',
    }


def remove_remote_skill(category: str, name: str) -> dict:
    path = _skill_dir(category, name)
    _remote_fs().rm(path, recursive=True)
    return {
        'persisted': 'remote_fs',
        'deleted': True,
        'path': path,
        'name': name,
        'category': category,
        'action': 'remove',
    }
