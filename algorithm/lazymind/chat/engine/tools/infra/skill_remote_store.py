from __future__ import annotations

from typing import Dict, Mapping, Optional

from lazymind.chat.integrations.remote_fs import RemoteFS
from lazymind.config import config

from .skill_paths import normalize_skill_package_path, relative_to_package
from .skill_validation import normalize_skill_category, validate_skill_name


class SkillRemoteStore:
    """RemoteFS-backed storage operations for reusable skill packages."""

    def __init__(self, fs: Optional[RemoteFS] = None):
        self.fs = fs or RemoteFS()
        self.root = str(config['skill_fs_url']).rstrip('/')

    def package_dir(self, category: str, name: str) -> str:
        return f'{self.root}/{category}/{name}'

    def resolve_existing_identity(self, name: str, category: Optional[str] = None) -> Dict[str, str]:
        raw_name = str(name or '').strip()
        normalized_category = normalize_skill_category(category) if category is not None else None
        if category is not None and not normalized_category:
            return {
                'error': (
                    f'category {category!r} is invalid; it must be a single ASCII-safe path segment.'
                )
            }

        if '/' in raw_name:
            parts = [part.strip() for part in raw_name.split('/')]
            if len(parts) != 2 or not all(parts):
                return {'error': f'Skill key {raw_name!r} must be in \'category/name\' form.'}
            key_category = normalize_skill_category(parts[0])
            key_name = parts[1]
            if not key_category:
                return {
                    'error': (
                        f'Skill key {raw_name!r} has invalid category; it must be a single ASCII-safe path segment.'
                    )
                }
            name_error = validate_skill_name(key_name)
            if name_error:
                return {'error': f'Skill key {raw_name!r} has invalid name: {name_error}'}
            if normalized_category and normalized_category != key_category:
                return {
                    'error': (
                        f'Skill key {raw_name!r} conflicts with category {category!r}; '
                        'they must refer to the same category.'
                    )
                }
            return {'category': key_category, 'name': key_name}

        name_error = validate_skill_name(raw_name)
        if name_error:
            return {'error': name_error}
        if normalized_category:
            return {'category': normalized_category, 'name': raw_name}

        matches = self._find_packages_by_name(raw_name)
        if not matches:
            return {'error': f'Skill {raw_name!r} was not found; provide category or full skill key.'}
        if len(matches) > 1:
            first_match = matches[0]
            first_category = first_match['category']
            first_name = first_match['name']
            first = f'{first_category}/{first_name}'
            return {'error': f'Ambiguous skill name {raw_name!r}; use the full skill key such as {first!r}.'}
        return matches[0]

    def list_files(self, category: str, name: str) -> Dict[str, str]:
        package_dir = self.package_dir(category, name)
        files: Dict[str, str] = {}

        def walk(path: str) -> None:
            for entry in self.fs.ls(path, detail=True):
                entry_name = str((entry or {}).get('name') or '').strip()
                if not entry_name:
                    continue
                entry_type = str((entry or {}).get('type') or 'file').strip()
                if entry_type in ('directory', 'dir'):
                    walk(entry_name)
                    continue
                rel_path = relative_to_package(package_dir, entry_name)
                with self.fs.open(f'{package_dir}/{rel_path}', 'r', encoding='utf-8', errors='replace') as fh:
                    files[rel_path] = fh.read()

        walk(package_dir)
        return files

    def replace_files(
        self,
        category: str,
        name: str,
        before: Mapping[str, str],
        after: Mapping[str, str],
    ) -> Dict[str, list[str]]:
        package_dir = self.package_dir(category, name)
        before_paths = set(before)
        after_paths = set(after)
        deleted = sorted(before_paths - after_paths)
        written = sorted(path for path in after_paths if before.get(path) != after.get(path))
        for rel_path in deleted:
            self.fs.rm(f'{package_dir}/{normalize_skill_package_path(rel_path)}', recursive=False)
        for rel_path in written:
            self.fs.write(f'{package_dir}/{normalize_skill_package_path(rel_path)}', after[rel_path])
        return {'written': written, 'deleted': deleted}

    def create(self, category: str, name: str, content: str) -> dict:
        package_dir = self.package_dir(category, name)
        path = f'{package_dir}/SKILL.md'
        self.fs.mkdir(package_dir, create_parents=True)
        self.fs.write(path, content)
        return {
            'persisted': 'remote_fs',
            'path': path,
            'name': name,
            'category': category,
            'action': 'create',
        }

    def rename(
        self,
        old_category: str,
        old_name: str,
        new_category: str,
        new_name: str,
        *,
        skill_content: str,
    ) -> dict:
        old_path = self.package_dir(old_category, old_name)
        new_path = self.package_dir(new_category, new_name)
        self.fs.move(old_path, new_path, recursive=True)
        self.fs.write(f'{new_path}/SKILL.md', skill_content)
        return {
            'persisted': 'remote_fs',
            'path': new_path,
            'old_name': old_name,
            'old_category': old_category,
            'name': new_name,
            'category': new_category,
            'action': 'rename',
        }

    def remove(self, category: str, name: str) -> dict:
        path = self.package_dir(category, name)
        self.fs.trash(path)
        return {
            'persisted': 'remote_fs',
            'deleted': True,
            'path': path,
            'name': name,
            'category': category,
            'action': 'remove',
        }

    def _find_packages_by_name(self, name: str) -> list[Dict[str, str]]:
        matches: list[Dict[str, str]] = []
        for category_entry in self.fs.ls(self.root, detail=True):
            if str((category_entry or {}).get('type') or 'file').strip() not in ('directory', 'dir'):
                continue
            category_path = str((category_entry or {}).get('name') or '').strip()
            category = _last_path_part(category_path)
            if not category:
                continue
            for package_entry in self.fs.ls(category_path, detail=True):
                if str((package_entry or {}).get('type') or 'file').strip() not in ('directory', 'dir'):
                    continue
                package_path = str((package_entry or {}).get('name') or '').strip()
                package_name = _last_path_part(package_path)
                if package_name == name:
                    matches.append({'category': category, 'name': package_name})
        return sorted(matches, key=lambda item: (item['category'], item['name']))


def _last_path_part(path: str) -> str:
    raw = RemoteFS._normalize_path(path)
    return raw.rstrip('/').rsplit('/', 1)[-1] if raw else ''
