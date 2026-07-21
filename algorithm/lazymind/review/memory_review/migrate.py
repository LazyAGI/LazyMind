from __future__ import annotations

from dataclasses import dataclass, field
from typing import Optional

from lazymind.common.integrations.remote_fs import RemoteFS

from .defaults import default_preference_md, default_profile_md
from .errors import MemoryStoreError
from .paths import (
    LEGACY_MEMORY_PATH,
    LEGACY_USER_PREFERENCE_PATH,
    PREFERENCE_PATH,
    PROFILE_PATH,
    SOUL_PATH,
    build_reference_path,
    normalize_memory_path,
)
from .schema import PreferenceItem, append_preference_item
from .schema.common import parse_yaml_frontmatter
from .store import MemoryStore


@dataclass
class MigrationResult:
    migrated: bool
    actions: list[str] = field(default_factory=list)
    warnings: list[str] = field(default_factory=list)


def is_memory_tree_initialized(store: MemoryStore) -> bool:
    return store.exists(SOUL_PATH) and store.exists(PROFILE_PATH) and store.exists(PREFERENCE_PATH)


def detect_legacy_format(fs: Optional[RemoteFS] = None) -> dict[str, bool]:
    client = fs or RemoteFS()
    return {
        'legacy_memory': _safe_exists(client, LEGACY_MEMORY_PATH),
        'legacy_user_preference': _safe_exists(client, LEGACY_USER_PREFERENCE_PATH),
        'soul': _safe_exists(client, SOUL_PATH),
        'profile': _safe_exists(client, PROFILE_PATH),
        'preference': _safe_exists(client, PREFERENCE_PATH),
    }


def migrate_legacy_memory(store: Optional[MemoryStore] = None) -> MigrationResult:
    """Best-effort migrate legacy memory.md / user.md into the structured tree.

    Existing soul/profile/preference files are never overwritten. Legacy working
    memory is archived under references/legacy-memory.md. Legacy user preference
    frontmatter maps into profile / soul defaults; Markdown body becomes a
    reference file.
    """
    memory_store = store or MemoryStore()
    fs = memory_store.fs
    result = MigrationResult(migrated=False)

    if is_memory_tree_initialized(memory_store):
        result.warnings.append('memory tree already initialized; skipped overwrite.')
        return result

    memory_store.ensure_defaults()
    result.actions.append('ensured default soul/profile/preference files')

    legacy_memory = _safe_read(fs, LEGACY_MEMORY_PATH)
    if legacy_memory and legacy_memory.strip():
        ref_path = build_reference_path('legacy-memory')
        if not memory_store.exists(ref_path):
            memory_store.write_reference(
                'legacy-memory',
                _wrap_reference(
                    name='legacy-memory',
                    description='Archived legacy agent working memory',
                    body=legacy_memory,
                ),
            )
            result.actions.append(f'archived {LEGACY_MEMORY_PATH} -> {ref_path}')
        else:
            result.warnings.append(f'{ref_path} already exists; skipped legacy memory archive')

    legacy_user = _safe_read(fs, LEGACY_USER_PREFERENCE_PATH)
    if legacy_user and legacy_user.strip():
        _migrate_legacy_user_preference(memory_store, legacy_user, result)
    else:
        result.warnings.append(f'{LEGACY_USER_PREFERENCE_PATH} missing or empty')

    result.migrated = True
    return result


def _migrate_legacy_user_preference(
    store: MemoryStore,
    legacy_user: str,
    result: MigrationResult,
) -> None:
    frontmatter, body = parse_yaml_frontmatter(legacy_user)
    preferred_name = str(frontmatter.get('preferred_name') or '').strip()
    agent_persona = str(frontmatter.get('agent_persona') or '').strip()
    response_style = str(frontmatter.get('response_style') or '').strip()

    if preferred_name and not store.exists(PROFILE_PATH):
        profile = default_profile_md().replace(
            'preferred_name: null',
            f'preferred_name: "{_escape_yaml(preferred_name)}"',
            1,
        )
        store.write_profile(profile)
        result.actions.append('mapped preferred_name into profile.md')
    elif preferred_name:
        # Profile already created by ensure_defaults; patch preferred_name only when still null.
        current = store.read_profile()
        if 'preferred_name: null' in current:
            store.write_profile(
                current.replace(
                    'preferred_name: null',
                    f'preferred_name: "{_escape_yaml(preferred_name)}"',
                    1,
                )
            )
            result.actions.append('updated profile preferred_name from legacy user.md')

    if agent_persona:
        soul = store.read_soul()
        if 'description: "面向研究、分析和复杂任务的个人智能助手"' in soul:
            store.write_soul(
                soul.replace(
                    'description: "面向研究、分析和复杂任务的个人智能助手"',
                    f'description: "{_escape_yaml(agent_persona)}"',
                    1,
                )
            )
            result.actions.append('mapped agent_persona into soul identity.description')

    body_text = (body or '').strip()
    style_note = response_style.strip()
    if body_text or style_note:
        sections = []
        if style_note:
            sections.append(f'## Response Style\n{style_note}\n')
        if body_text:
            sections.append(body_text if body_text.endswith('\n') else f'{body_text}\n')
        ref_name = 'legacy-preferences'
        ref_path = build_reference_path(ref_name)
        if not store.exists(ref_path):
            store.write_reference(
                ref_name,
                _wrap_reference(
                    name=ref_name,
                    description='Archived legacy user preference body',
                    body=''.join(sections),
                ),
            )
            preference = store.read_preference()
            preference = append_preference_item(
                preference if preference.strip() else default_preference_md(),
                PreferenceItem(
                    name='pref.legacy.user_preference',
                    summary='Legacy user preference notes migrated from memory/user.md.',
                    ref=f'references/{ref_name}.md',
                ),
            )
            store.write_preference(preference)
            result.actions.append(f'archived legacy preference body -> {ref_path}')
        else:
            result.warnings.append(f'{ref_path} already exists; skipped preference body archive')


def _wrap_reference(*, name: str, description: str, body: str) -> str:
    text = body if body.endswith('\n') else f'{body}\n'
    return (
        '---\n'
        f'name: {name}\n'
        f'description: "{_escape_yaml(description)}"\n'
        'metadata:\n'
        '  node_type: memory\n'
        '  type: legacy_migration\n'
        '---\n'
        f'{text}'
    )


def _escape_yaml(value: str) -> str:
    return value.replace('\\', '\\\\').replace('"', '\\"')


def _safe_exists(fs: RemoteFS, path: str) -> bool:
    try:
        return bool(fs.exists(normalize_memory_path(path)))
    except Exception:
        return False


def _safe_read(fs: RemoteFS, path: str) -> str:
    try:
        if not fs.exists(path):
            return ''
        with fs.open(path, 'r', encoding='utf-8', errors='replace') as fh:
            return fh.read()
    except Exception as exc:
        raise MemoryStoreError(f'failed to read legacy path {path}: {exc}') from exc
