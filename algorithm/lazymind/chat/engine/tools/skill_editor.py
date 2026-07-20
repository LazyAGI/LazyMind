from typing import Any, Callable, Dict, Optional

import lazyllm

from lazymind.chat.engine.tools.infra import (
    GitHubSkillInstaller,
    tool_error,
    tool_success,
)
from lazymind.chat.engine.tools.infra.skill_operations import (
    create_skill_file,
    delete_skill_file,
    edit_skill_file,
    patch_skill_file,
)
from lazymind.common.skill_document import (
    SkillDocumentError,
    parse_skill_document,
    require_skill_name,
    require_valid_skill_document,
)
from lazymind.common.skill_remote_store import SkillRemoteStore
from lazymind.common.skill_storage_key import (
    EXTERNAL_SKILL_CATEGORY,
    INTERNAL_SKILL_CATEGORY,
    parse_skill_key,
)


_DRAFT_BELONGS_TO_ANOTHER_TASK_ERROR = 'draft belongs to another task'
_PENDING_SKILL_CHANGE_MESSAGE = (
    'There are pending changes. Please ask the user to handle them before modifying.'
)


def _skill_editor_error(tool_name: str, prefix: str, exc: Exception) -> Dict[str, Any]:
    message = str(exc)
    if _DRAFT_BELONGS_TO_ANOTHER_TASK_ERROR in message:
        return tool_error(tool_name, _PENDING_SKILL_CHANGE_MESSAGE)
    return tool_error(tool_name, f'{prefix}: {message}')


class SkillManagementToolkit:
    """Create, edit, rename, and remove reusable skill packages."""

    __public_apis__ = [
        'create_skill',
        'install_skill',
        'edit_file',
        'patch_file',
        'create_file',
        'delete_file',
        'rename_skill',
        'remove_skill',
    ]

    def __init__(
        self,
        store: Optional[SkillRemoteStore] = None,
        installer: Optional[GitHubSkillInstaller] = None,
    ):
        self.store = store or SkillRemoteStore()
        self.installer = installer or GitHubSkillInstaller()

    def install_skill(self, github_url: str) -> Dict[str, Any]:
        """Install one public GitHub skill package as a disabled reusable skill.

        Call this only when the user explicitly asks to install the linked
        skill. Do not call it when the user only asks to inspect, explain, or
        analyze a GitHub link. Installation runs immediately without a second
        confirmation, but the installed skill remains disabled until the user
        reviews and enables it in Skill Management.

        Args:
            github_url: Public GitHub repository root or /tree/<ref>/<skill-path> URL.
        """
        lazyllm.LOG.info(f'[install_skill] called github_url={github_url!r}')
        try:
            package = self.installer.prepare(github_url)
        except Exception as exc:
            return tool_error('install_skill', str(exc))

        category = EXTERNAL_SKILL_CATEGORY
        skill_key = f'{category}/{package.name}'
        try:
            if self.store.package_exists(category, package.name):
                return tool_error('install_skill', f'Skill {skill_key!r} already exists.')
            duplicate_key = self._find_installed_github_source(package.source.identity)
            if duplicate_key:
                return tool_error(
                    'install_skill',
                    f'GitHub source is already installed as {duplicate_key!r}.',
                )
            self.store.install_package(category, package.name, package.files)
        except Exception as exc:
            return _skill_editor_error('install_skill', 'Failed to install skill package', exc)

        return tool_success('install_skill', {
            'status': 'installed',
            'skill_key': skill_key,
            'github_url': package.source.canonical_url,
            'enabled': False,
            'message': (
                'Skill installed. Go to Skill Management > My Skills to review and enable it.'
            ),
        })

    def _find_installed_github_source(self, source_identity: tuple[str, str]) -> Optional[str]:
        for package in self.store.list_packages():
            category = package['category']
            if category != EXTERNAL_SKILL_CATEGORY:
                continue
            name = package['name']
            try:
                document = parse_skill_document(self.store.read_skill_md(category, name))
                github_url = str(document.metadata.get('github_url') or '').strip()
                if not github_url:
                    continue
                existing_source = self.installer.resolve_source(github_url)
            except (OSError, RuntimeError, ValueError) as exc:
                lazyllm.LOG.warning(
                    '[install_skill] skip unreadable or invalid existing github source '
                    f'skill={category}/{name} error_type={type(exc).__name__} error={exc}'
                )
                continue
            if existing_source.identity == source_identity:
                return f'{category}/{name}'
        return None

    def create_skill(self, name: str, *, content: str) -> Dict[str, Any]:
        """Create a new reusable skill from full SKILL.md content.

        The SKILL.md YAML frontmatter must include name and description. The
        package is always created under the internal category. Frontmatter
        category, when present, is preserved as document content and does not
        control storage.

        Args:
            name: Single-segment skill name.
            content: Full SKILL.md content, including YAML frontmatter.
        """
        lazyllm.LOG.info(
            '[create_skill] called '
            f'name={name!r} content_len={len(content) if content else 0}'
        )
        try:
            name = require_skill_name(name)
            document = require_valid_skill_document(content, expected_name=name)
        except SkillDocumentError as exc:
            return tool_error(
                'create_skill',
                str(exc),
                log_message=f'[create_skill] fail reason={str(exc)!r}',
            )
        lazyllm.LOG.info(
            f'[create_skill] lookup category={INTERNAL_SKILL_CATEGORY} name={name!r}'
        )
        try:
            self.store.create(
                INTERNAL_SKILL_CATEGORY,
                str(document.metadata['name']),
                content,
            )
        except Exception as exc:
            return _skill_editor_error('create_skill', 'Failed to create skill package', exc)
        return tool_success('create_skill', {
            'status': 'created',
            'message': 'Skill package change was written.',
        })

    def _run_file_operation(
        self,
        tool_name: str,
        name: str,
        operation: Callable[..., Dict[str, Any]],
        reason: Optional[str] = None,
        **kwargs: Any,
    ) -> Dict[str, Any]:
        resolved = self.store.resolve_existing_identity(name)
        if resolved.get('error'):
            return tool_error(tool_name, resolved['error'])
        normalized_category = resolved['category']
        name = resolved['name']
        try:
            current_files = self.store.list_files(normalized_category, name)
            result = operation(current_files, name, **kwargs)
            edited_files = result.pop('files')
            change_set = self.store.replace_files(normalized_category, name, current_files, edited_files)
        except ValueError as exc:
            if _DRAFT_BELONGS_TO_ANOTHER_TASK_ERROR in str(exc):
                return tool_error(tool_name, _PENDING_SKILL_CHANGE_MESSAGE)
            return tool_error(tool_name, str(exc))
        except Exception as exc:
            return _skill_editor_error(tool_name, 'Failed to load or edit skill package', exc)
        result['written_files'] = change_set['written']
        result['deleted_files'] = change_set['deleted']
        if 'summary' not in result:
            touched = ', '.join(result.get('touched_files') or [])
            result['summary'] = reason or f'skill_editor {tool_name}: {touched}'
        return tool_success(tool_name, result)

    def edit_file(
        self,
        name: str,
        *,
        path: str,
        content: str,
        reason: Optional[str] = None,
    ) -> Dict[str, Any]:
        """Replace an existing file inside a reusable skill package.

        Args:
            name: Unique skill name, or full "internal/name" or "external/name" skill key.
            path: Existing package file to replace. May be SKILL.md.
            content: Full replacement file content.
            reason: Short summary of why this file is being edited.
        """
        lazyllm.LOG.info(f'[edit_file] called name={name!r} path={path!r}')

        return self._run_file_operation(
            'edit_file',
            name,
            edit_skill_file,
            reason,
            path=path,
            content=content,
        )

    def patch_file(
        self,
        name: str,
        *,
        path: str,
        old_text: str,
        new_text: str,
        replace_all: bool = False,
        reason: Optional[str] = None,
    ) -> Dict[str, Any]:
        """Patch an existing file inside a reusable skill package.

        Args:
            name: Unique skill name, or full "internal/name" or "external/name" skill key.
            path: Existing package file to patch. Must be explicit; no default target is assumed.
            old_text: Text to find. It must identify a unique match unless replace_all is true.
            new_text: Replacement text. Use an empty string to delete matched text.
            replace_all: Replace every match instead of requiring uniqueness.
            reason: Short summary of why this file is being patched.
        """
        lazyllm.LOG.info(f'[patch_file] called name={name!r} path={path!r}')

        return self._run_file_operation(
            'patch_file',
            name,
            patch_skill_file,
            reason,
            path=path,
            old_text=old_text,
            new_text=new_text,
            replace_all=replace_all,
        )

    def create_file(
        self,
        name: str,
        *,
        path: str,
        content: str,
        reason: Optional[str] = None,
    ) -> Dict[str, Any]:
        """Create a new supporting file inside a reusable skill package.

        SKILL.md cannot be created or overwritten with this tool; use
        create_skill for new packages and edit_file or patch_file for SKILL.md.
        After creating a supporting file, update SKILL.md with a relative link
        or instruction that explains when and how the new file should be used.

        Put reference material, examples, or detailed guidance under
        references/. Put executable helper scripts under scripts/. Put static
        media or data assets under assets/. Put reusable output or prompt
        templates under templates/.

        Args:
            name: Unique skill name, or full "internal/name" or "external/name" skill key.
            path: New supporting file path to create.
            content: File content.
            reason: Short summary of why this file is being created.
        """
        lazyllm.LOG.info(f'[create_file] called name={name!r} path={path!r}')

        return self._run_file_operation(
            'create_file',
            name,
            create_skill_file,
            reason,
            path=path,
            content=content,
        )

    def delete_file(
        self,
        name: str,
        *,
        path: str,
        reason: Optional[str] = None,
    ) -> Dict[str, Any]:
        """Delete a supporting file from a reusable skill package.

        SKILL.md cannot be deleted with this tool; use remove_skill to remove
        the whole skill package.

        Args:
            name: Unique skill name, or full "internal/name" or "external/name" skill key.
            path: Existing supporting file path to delete.
            reason: Short summary of why this file is being deleted.
        """
        lazyllm.LOG.info(f'[delete_file] called name={name!r} path={path!r}')

        return self._run_file_operation(
            'delete_file',
            name,
            delete_skill_file,
            reason,
            path=path,
        )

    def rename_skill(
        self,
        name: str,
        *,
        new_name: str,
    ) -> Dict[str, Any]:
        """Rename an existing reusable skill package within its category.

        This moves the package within its existing storage category and rewrites
        only the SKILL.md frontmatter name. The storage category is preserved:
        an internal skill remains internal, and an external skill remains external.

        Args:
            name: Unique current skill name, or full "internal/name" or "external/name" skill key.
            new_name: New single-segment skill name without an "internal/" or
                "external/" prefix.
        """
        lazyllm.LOG.info(
            '[rename_skill] called '
            f'name={name!r} new_name={new_name!r}'
        )
        resolved = self.store.resolve_existing_identity(name)
        if resolved.get('error'):
            return tool_error('rename_skill', resolved['error'])
        normalized_category = resolved['category']
        name = resolved['name']
        lazyllm.LOG.info(f'[rename_skill] lookup category={normalized_category!r} name={name!r}')

        try:
            target_name = require_skill_name(new_name)
        except SkillDocumentError as exc:
            return tool_error('rename_skill', f'new_name is invalid: {exc}')
        if target_name == name:
            return tool_error('rename_skill', 'rename_skill requires a different new_name.')

        try:
            current_files = self.store.list_files(normalized_category, name)
            skill_content = current_files.get('SKILL.md') or ''
            document = require_valid_skill_document(skill_content)
            renamed_content = document.with_metadata(name=target_name).render()
            require_valid_skill_document(renamed_content, expected_name=target_name)
        except Exception as exc:
            return _skill_editor_error('rename_skill', 'Failed to prepare skill rename', exc)

        try:
            self.store.rename(
                normalized_category,
                name,
                normalized_category,
                target_name,
                skill_content=renamed_content,
            )
        except Exception as exc:
            return _skill_editor_error('rename_skill', 'Failed to rename skill package', exc)

        payload = {
            'old': {'category': normalized_category, 'name': name},
            'new': {'category': normalized_category, 'name': target_name},
        }
        result = {
            'status': 'renamed',
            'message': 'Skill package change was written.',
        }
        result.update(payload)
        return tool_success('rename_skill', result)

    def remove_skill(
        self,
        name: str,
        reason: Optional[str] = None,
    ) -> Dict[str, Any]:
        """Remove an existing reusable skill package.

        Use this when a skill is superseded or no longer correct.

        Args:
            name: Unique full skill name.
            reason: Why the skill should be removed.
        """
        lazyllm.LOG.info(f'[remove_skill] called name={name!r} reason={reason!r}')
        try:
            normalized_category, name = parse_skill_key(name)
        except ValueError as exc:
            return tool_error('remove_skill', str(exc))
        lazyllm.LOG.info(f'[remove_skill] lookup category={normalized_category!r} name={name!r}')

        try:
            self.store.remove(normalized_category, name)
        except Exception as exc:
            return _skill_editor_error('remove_skill', 'Failed to remove skill package', exc)
        return tool_success('remove_skill', {
            'status': 'removed',
            'message': 'Skill package change was written.',
        })
