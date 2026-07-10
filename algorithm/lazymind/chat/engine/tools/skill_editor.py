from typing import Any, Callable, Dict, Optional

import lazyllm

from lazymind.chat.engine.tools.infra import (
    GitHubSkillInstaller,
    normalize_skill_category,
    parse_skill_frontmatter,
    resolve_skill_editor_identity,
    rewrite_skill_identity,
    SkillRemoteStore,
    skill_identity_from_content,
    tool_error,
    tool_success,
    validate_skill_content,
    validate_skill_name,
)
from lazymind.chat.engine.tools.infra.skill_operations import (
    create_skill_file,
    delete_skill_file,
    edit_skill_file,
    patch_skill_file,
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


class SkillEditorToolGroup:
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

    def install_skill(self, github_url: str, category: Optional[str] = None) -> Dict[str, Any]:
        """Install one public GitHub skill package as a disabled reusable skill.

        Call this only when the user explicitly asks to install the linked
        skill. Do not call it when the user only asks to inspect, explain, or
        analyze a GitHub link. Installation runs immediately without a second
        confirmation, but the installed skill remains disabled until the user
        reviews and enables it in Skill Management.

        Args:
            github_url: Public GitHub repository root or /tree/<ref>/<skill-path> URL.
            category: Destination skill category. Defaults to "external".
        """
        lazyllm.LOG.info(f'[install_skill] called github_url={github_url!r} category={category!r}')
        try:
            package = self.installer.prepare(github_url, category)
        except Exception as exc:
            return tool_error('install_skill', str(exc))

        skill_key = f'{package.category}/{package.name}'
        try:
            if self.store.package_exists(package.category, package.name):
                return tool_error('install_skill', f'Skill {skill_key!r} already exists.')
            duplicate_key = self._find_installed_github_source(package.source.identity)
            if duplicate_key:
                return tool_error(
                    'install_skill',
                    f'GitHub source is already installed as {duplicate_key!r}.',
                )
            self.store.install_package(package.category, package.name, package.files)
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
            name = package['name']
            try:
                frontmatter, _ = parse_skill_frontmatter(self.store.read_skill_md(category, name))
                github_url = str(frontmatter.get('github_url') or '').strip()
                if not github_url:
                    continue
                existing_source = self.installer.resolve_source(github_url)
            except ValueError as exc:
                lazyllm.LOG.warning(
                    '[install_skill] skip invalid existing github source '
                    f'skill={category}/{name} error={exc}'
                )
                continue
            if existing_source.identity == source_identity:
                return f'{category}/{name}'
        return None

    def create_skill(self, name: str, category: Optional[str] = None, *, content: str) -> Dict[str, Any]:
        """Create a new reusable skill from full SKILL.md content.

        The SKILL.md YAML frontmatter must include name, category, and
        description. Both name and category are path segments; category must be
        a single segment such as "engineering" or "coding". The name argument
        may be either a plain skill name or the full "category/name" key shown
        in the skill list; when category is also provided, it must match that
        key.

        Args:
            name: Skill name, or full "category/name" skill key.
            category: Skill category directory used for category/name/SKILL.md. Optional when name is a full key.
            content: Full SKILL.md content, including YAML frontmatter.
        """
        lazyllm.LOG.info(
            '[create_skill] called '
            f'name={name!r} category={category!r} content_len={len(content) if content else 0}'
        )
        resolved = resolve_skill_editor_identity(name, category, 'create_skill')
        if resolved.get('error'):
            return tool_error('create_skill', resolved['error'])
        normalized_category = resolved['category']
        name = resolved['name']
        lazyllm.LOG.info(f'[create_skill] lookup category={normalized_category!r} name={name!r}')

        content_error = validate_skill_content(content or '')
        if content_error:
            return tool_error(
                'create_skill',
                content_error,
                log_message=f'[create_skill] fail reason={content_error!r}',
            )
        content_category, content_name = skill_identity_from_content(content or '')
        if content_category != normalized_category or content_name != name:
            return tool_error(
                'create_skill',
                'SKILL.md frontmatter name/category must match the tool name/category for create.'
            )
        try:
            self.store.create(content_category, content_name, content or '')
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
        category: Optional[str],
        operation: Callable[..., Dict[str, Any]],
        reason: Optional[str] = None,
        **kwargs: Any,
    ) -> Dict[str, Any]:
        resolved = self.store.resolve_existing_identity(name, category)
        if resolved.get('error'):
            return tool_error(tool_name, resolved['error'])
        normalized_category = resolved['category']
        name = resolved['name']
        try:
            current_files = self.store.list_files(normalized_category, name)
            result = operation(current_files, normalized_category, name, **kwargs)
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
        category: Optional[str] = None,
        *,
        path: str,
        content: str,
        reason: Optional[str] = None,
    ) -> Dict[str, Any]:
        """Replace an existing file inside a reusable skill package.

        Args:
            name: Skill name, or full "category/name" skill key.
            category: Skill category directory used for category/name/SKILL.md.
                Optional when name is a full key or unique.
            path: Existing package file to replace. May be SKILL.md.
            content: Full replacement file content.
            reason: Short summary of why this file is being edited.
        """
        lazyllm.LOG.info(f'[edit_file] called name={name!r} category={category!r} path={path!r}')

        return self._run_file_operation(
            'edit_file',
            name,
            category,
            edit_skill_file,
            reason,
            path=path,
            content=content,
        )

    def patch_file(
        self,
        name: str,
        category: Optional[str] = None,
        *,
        path: str,
        old_text: str,
        new_text: str,
        replace_all: bool = False,
        reason: Optional[str] = None,
    ) -> Dict[str, Any]:
        """Patch an existing file inside a reusable skill package.

        Args:
            name: Skill name, or full "category/name" skill key.
            category: Skill category directory used for category/name/SKILL.md.
                Optional when name is a full key or unique.
            path: Existing package file to patch. Must be explicit; no default target is assumed.
            old_text: Text to find. It must identify a unique match unless replace_all is true.
            new_text: Replacement text. Use an empty string to delete matched text.
            replace_all: Replace every match instead of requiring uniqueness.
            reason: Short summary of why this file is being patched.
        """
        lazyllm.LOG.info(f'[patch_file] called name={name!r} category={category!r} path={path!r}')

        return self._run_file_operation(
            'patch_file',
            name,
            category,
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
        category: Optional[str] = None,
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
            name: Skill name, or full "category/name" skill key.
            category: Skill category directory used for category/name/SKILL.md.
                Optional when name is a full key or unique.
            path: New supporting file path to create.
            content: File content.
            reason: Short summary of why this file is being created.
        """
        lazyllm.LOG.info(f'[create_file] called name={name!r} category={category!r} path={path!r}')

        return self._run_file_operation(
            'create_file',
            name,
            category,
            create_skill_file,
            reason,
            path=path,
            content=content,
        )

    def delete_file(
        self,
        name: str,
        category: Optional[str] = None,
        *,
        path: str,
        reason: Optional[str] = None,
    ) -> Dict[str, Any]:
        """Delete a supporting file from a reusable skill package.

        SKILL.md cannot be deleted with this tool; use remove_skill to remove
        the whole skill package.

        Args:
            name: Skill name, or full "category/name" skill key.
            category: Skill category directory used for category/name/SKILL.md.
                Optional when name is a full key or unique.
            path: Existing supporting file path to delete.
            reason: Short summary of why this file is being deleted.
        """
        lazyllm.LOG.info(f'[delete_file] called name={name!r} category={category!r} path={path!r}')

        return self._run_file_operation(
            'delete_file',
            name,
            category,
            delete_skill_file,
            reason,
            path=path,
        )

    def rename_skill(
        self,
        name: str,
        category: Optional[str] = None,
        *,
        new_name: str,
        new_category: Optional[str] = None,
    ) -> Dict[str, Any]:
        """Rename or move an existing reusable skill package.

        This moves the package and rewrites SKILL.md frontmatter name/category.
        Use this instead of edit_file or patch_file whenever the skill identity changes.

        Args:
            name: Current skill name, or full "category/name" skill key.
            category: Current skill category. Optional when name is a full key or unique.
            new_name: New skill name.
            new_category: New skill category. If omitted, current category is kept.
        """
        lazyllm.LOG.info(
            '[rename_skill] called '
            f'name={name!r} category={category!r} new_name={new_name!r} new_category={new_category!r}'
        )
        resolved = self.store.resolve_existing_identity(name, category)
        if resolved.get('error'):
            return tool_error('rename_skill', resolved['error'])
        normalized_category = resolved['category']
        name = resolved['name']
        lazyllm.LOG.info(f'[rename_skill] lookup category={normalized_category!r} name={name!r}')

        target_name = str(new_name or '').strip()
        name_error = validate_skill_name(target_name)
        if name_error:
            return tool_error('rename_skill', f'new_name is invalid: {name_error}')
        target_category = normalize_skill_category(new_category if new_category is not None else normalized_category)
        if not target_category:
            return tool_error(
                'rename_skill',
                f'new_category {new_category!r} is invalid; it must be a single ASCII-safe path segment.'
            )
        if target_category == normalized_category and target_name == name:
            return tool_error('rename_skill', 'rename_skill requires a different new_name or new_category.')

        try:
            current_files = self.store.list_files(normalized_category, name)
            skill_content = current_files.get('SKILL.md') or ''
            renamed_content = rewrite_skill_identity(skill_content, target_category, target_name)
        except Exception as exc:
            return _skill_editor_error('rename_skill', 'Failed to prepare skill rename', exc)
        content_error = validate_skill_content(renamed_content)
        if content_error:
            return tool_error('rename_skill', content_error)

        try:
            self.store.rename(
                normalized_category,
                name,
                target_category,
                target_name,
                skill_content=renamed_content,
            )
        except Exception as exc:
            return _skill_editor_error('rename_skill', 'Failed to rename skill package', exc)

        payload = {
            'old': {'category': normalized_category, 'name': name},
            'new': {'category': target_category, 'name': target_name},
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
        category: Optional[str] = None,
        reason: Optional[str] = None,
    ) -> Dict[str, Any]:
        """Remove an existing reusable skill package.

        Use this when a skill is superseded or no longer correct.

        Args:
            name: Skill name, or full "category/name" skill key.
            category: Skill category directory. Optional when name is a full key or unique.
            reason: Why the skill should be removed.
        """
        lazyllm.LOG.info(f'[remove_skill] called name={name!r} category={category!r} reason={reason!r}')
        resolved = self.store.resolve_existing_identity(name, category)
        if resolved.get('error'):
            return tool_error('remove_skill', resolved['error'])
        normalized_category = resolved['category']
        name = resolved['name']
        lazyllm.LOG.info(f'[remove_skill] lookup category={normalized_category!r} name={name!r}')

        try:
            self.store.remove(normalized_category, name)
        except Exception as exc:
            return _skill_editor_error('remove_skill', 'Failed to remove skill package', exc)
        return tool_success('remove_skill', {
            'status': 'removed',
            'message': 'Skill package change was written.',
        })
