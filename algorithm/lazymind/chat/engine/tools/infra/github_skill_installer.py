from __future__ import annotations

import re
import stat
import tempfile
import zipfile
from dataclasses import dataclass
from pathlib import PurePosixPath
from typing import Dict, Optional
from urllib.parse import quote, unquote, urlparse

import requests

from lazymind.common.skill_document import require_valid_skill_document


_GITHUB_API = 'https://api.github.com'
_GITHUB_ARCHIVE = 'https://codeload.github.com'
_USER_AGENT = 'lazymind-github-skill-installer/1.0'
_REQUEST_TIMEOUT = (5, 30)
_MAX_DOWNLOAD_BYTES = 20 * 1024 * 1024
_MAX_EXPANDED_BYTES = 50 * 1024 * 1024
_MAX_FILE_BYTES = 10 * 1024 * 1024
_MAX_SKILL_MD_BYTES = 5 * 1024 * 1024
_MAX_FILES = 500
_OWNER_RE = re.compile(r'^[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?$')
_REPO_RE = re.compile(r'^[A-Za-z0-9._-]+$')
_IGNORED_PARTS = {
    '.git',
    '.github',
    '__pycache__',
    '.pytest_cache',
    '.mypy_cache',
    '.ruff_cache',
    '.cache',
}


@dataclass(frozen=True)
class GitHubSkillSource:
    owner: str
    repo: str
    ref: str
    skill_path: str
    canonical_url: str

    @property
    def identity(self) -> tuple[str, str]:
        return f'{self.owner.lower()}/{self.repo.lower()}', self.skill_path


@dataclass(frozen=True)
class PreparedSkillPackage:
    source: GitHubSkillSource
    name: str
    description: str
    files: Dict[str, bytes]


@dataclass(frozen=True)
class _ParsedGitHubURL:
    owner: str
    repo: str
    tree_parts: tuple[str, ...]


class GitHubSkillInstaller:
    """Prepare one public GitHub skill package for RemoteFS persistence."""

    def __init__(self, session: Optional[requests.Session] = None):
        self._session = session or requests.Session()
        self._source_cache: Dict[str, GitHubSkillSource] = {}

    def prepare(self, github_url: str) -> PreparedSkillPackage:
        source = self.resolve_source(github_url)
        with tempfile.TemporaryDirectory(prefix='lazymind-skill-') as temp_dir:
            archive_path = self._download_archive(source, temp_dir)
            files = self._read_package(archive_path, source.skill_path)
        return self._normalize_package(source, files)

    def resolve_source(self, github_url: str) -> GitHubSkillSource:
        cache_key = str(github_url or '').strip()
        cached = self._source_cache.get(cache_key)
        if cached:
            return cached
        parsed = self._parse_url(cache_key)
        if parsed.tree_parts:
            ref, skill_path = self._resolve_tree_parts(parsed)
        else:
            ref = self._resolve_default_branch(parsed.owner, parsed.repo)
            skill_path = ''
        canonical_url = f'https://github.com/{parsed.owner}/{parsed.repo}'
        if skill_path:
            canonical_url += f'/tree/{quote(ref, safe="/")}/{quote(skill_path, safe="/")}'
        source = GitHubSkillSource(
            owner=parsed.owner,
            repo=parsed.repo,
            ref=ref,
            skill_path=skill_path,
            canonical_url=canonical_url,
        )
        self._source_cache[cache_key] = source
        self._source_cache[canonical_url] = source
        return source

    @staticmethod
    def _parse_url(github_url: str) -> _ParsedGitHubURL:
        if not github_url:
            raise ValueError('github_url must be a non-empty public GitHub URL.')
        parsed = urlparse(github_url)
        try:
            port = parsed.port
        except ValueError as exc:
            raise ValueError('github_url contains an invalid port.') from exc
        host = (parsed.hostname or '').lower()
        if parsed.scheme != 'https' or host not in {'github.com', 'www.github.com'}:
            raise ValueError('github_url must use https://github.com.')
        if parsed.username or parsed.password or port is not None:
            raise ValueError('github_url must not contain credentials or a port.')
        raw_parts = [part for part in parsed.path.strip('/').split('/') if part]
        parts = [unquote(part) for part in raw_parts]
        if len(parts) < 2:
            raise ValueError('github_url must include owner and repository.')
        if any(part in {'.', '..'} or '/' in part or '\\' in part for part in parts):
            raise ValueError('github_url contains an invalid path segment.')
        owner = parts[0]
        repo = parts[1][:-4] if parts[1].endswith('.git') else parts[1]
        if not owner or not repo:
            raise ValueError('github_url must include owner and repository.')
        if not _OWNER_RE.fullmatch(owner) or not _REPO_RE.fullmatch(repo):
            raise ValueError('github_url contains an invalid owner or repository name.')
        if len(parts) == 2:
            return _ParsedGitHubURL(owner=owner, repo=repo, tree_parts=())
        if parts[2] != 'tree':
            raise ValueError('github_url must point to a repository root or /tree/<ref>/<skill-path>.')
        tree_parts = tuple(parts[3:])
        if len(tree_parts) < 2:
            raise ValueError('A tree github_url must include both ref and skill path.')
        return _ParsedGitHubURL(owner=owner, repo=repo, tree_parts=tree_parts)

    def _resolve_default_branch(self, owner: str, repo: str) -> str:
        url = f'{_GITHUB_API}/repos/{owner}/{repo}'
        response = self._get(url)
        try:
            branch = str(response.json().get('default_branch') or '').strip()
        except (TypeError, ValueError) as exc:
            raise RuntimeError('GitHub returned invalid repository metadata.') from exc
        if not branch:
            raise RuntimeError('GitHub repository metadata did not include a default branch.')
        return branch

    def _resolve_tree_parts(self, parsed: _ParsedGitHubURL) -> tuple[str, str]:
        parts = list(parsed.tree_parts)
        for split_at in range(len(parts) - 1, 0, -1):
            ref = '/'.join(parts[:split_at])
            url = f'{_GITHUB_API}/repos/{parsed.owner}/{parsed.repo}/commits/{quote(ref, safe="")}'
            response = self._session.get(
                url,
                headers={'User-Agent': _USER_AGENT, 'Accept': 'application/vnd.github+json'},
                timeout=_REQUEST_TIMEOUT,
            )
            if response.status_code in (404, 422):
                continue
            self._raise_http_error(response, url)
            return ref, '/'.join(parts[split_at:])
        raise ValueError('Could not resolve a GitHub ref while preserving a non-empty skill path.')

    def _get(self, url: str, *, stream: bool = False) -> requests.Response:
        try:
            response = self._session.get(
                url,
                headers={'User-Agent': _USER_AGENT, 'Accept': 'application/vnd.github+json'},
                timeout=_REQUEST_TIMEOUT,
                stream=stream,
            )
        except requests.RequestException as exc:
            raise RuntimeError(f'Failed to fetch GitHub resource: {exc}') from exc
        self._raise_http_error(response, url)
        return response

    @staticmethod
    def _raise_http_error(response: requests.Response, url: str) -> None:
        if response.status_code < 400:
            return
        if response.status_code == 403 and response.headers.get('X-RateLimit-Remaining') == '0':
            raise RuntimeError('GitHub API rate limit exceeded for unauthenticated requests.')
        raise RuntimeError(f'GitHub request failed with HTTP {response.status_code}: {url}')

    def _download_archive(self, source: GitHubSkillSource, temp_dir: str) -> str:
        url = f'{_GITHUB_ARCHIVE}/{source.owner}/{source.repo}/zip/{quote(source.ref, safe="")}'
        response = self._get(url, stream=True)
        archive_path = f'{temp_dir}/archive.zip'
        total = 0
        try:
            with open(archive_path, 'wb') as handle:
                for chunk in response.iter_content(chunk_size=64 * 1024):
                    if not chunk:
                        continue
                    total += len(chunk)
                    if total > _MAX_DOWNLOAD_BYTES:
                        raise ValueError('GitHub ZIP exceeds the 20 MiB download limit.')
                    handle.write(chunk)
            return archive_path
        finally:
            response.close()

    def _read_package(self, archive_path: str, skill_path: str) -> Dict[str, bytes]:
        files: Dict[str, bytes] = {}
        total_size = 0
        try:
            archive = zipfile.ZipFile(archive_path)
        except (OSError, zipfile.BadZipFile) as exc:
            raise ValueError('GitHub returned an invalid ZIP archive.') from exc
        with archive:
            members = archive.infolist()
            roots = {PurePosixPath(info.filename).parts[0] for info in members if info.filename}
            if len(roots) != 1:
                raise ValueError('GitHub ZIP must contain exactly one repository root directory.')
            root = next(iter(roots))
            package_prefix = PurePosixPath(root)
            if skill_path:
                package_prefix /= PurePosixPath(skill_path)
            prefix_parts = package_prefix.parts
            for info in members:
                rel_path = self._package_relative_path(info.filename, prefix_parts)
                if rel_path is None or info.is_dir():
                    continue
                if self._is_ignored(rel_path):
                    continue
                if self._is_symlink(info):
                    raise ValueError(f'Symbolic links are not allowed in skill packages: {rel_path}')
                if info.file_size > _MAX_FILE_BYTES:
                    raise ValueError(f'Skill file exceeds the 10 MiB limit: {rel_path}')
                if rel_path == 'SKILL.md' and info.file_size > _MAX_SKILL_MD_BYTES:
                    raise ValueError('SKILL.md exceeds the 5 MiB loading limit.')
                if len(files) >= _MAX_FILES:
                    raise ValueError('Skill package exceeds the 500 file limit.')
                total_size += info.file_size
                if total_size > _MAX_EXPANDED_BYTES:
                    raise ValueError('Skill package exceeds the 50 MiB expanded size limit.')
                files[rel_path] = archive.read(info)
        if 'SKILL.md' not in files:
            raise ValueError('The selected skill directory must contain SKILL.md at its root.')
        return files

    @staticmethod
    def _package_relative_path(filename: str, prefix_parts: tuple[str, ...]) -> Optional[str]:
        if (
            not filename
            or '\\' in filename
            or filename.startswith('/')
            or re.match(r'^[A-Za-z]:/', filename)
        ):
            raise ValueError(f'Unsafe ZIP path: {filename!r}')
        clean_name = filename[:-1] if filename.endswith('/') else filename
        raw_parts = clean_name.split('/')
        if any(part in {'', '.', '..'} for part in raw_parts):
            raise ValueError(f'Unsafe ZIP path: {filename!r}')
        path = PurePosixPath(clean_name)
        if path.parts[:len(prefix_parts)] != prefix_parts:
            return None
        relative_parts = path.parts[len(prefix_parts):]
        if not relative_parts:
            return None
        return '/'.join(relative_parts)

    @staticmethod
    def _is_symlink(info: zipfile.ZipInfo) -> bool:
        mode = info.external_attr >> 16
        return stat.S_IFMT(mode) == stat.S_IFLNK

    @staticmethod
    def _is_ignored(rel_path: str) -> bool:
        parts = PurePosixPath(rel_path).parts
        return any(part in _IGNORED_PARTS for part in parts) or parts[-1] == '.DS_Store'

    @staticmethod
    def _normalize_package(
        source: GitHubSkillSource,
        files: Dict[str, bytes],
    ) -> PreparedSkillPackage:
        raw_skill_md = files['SKILL.md']
        try:
            content = raw_skill_md.decode('utf-8')
        except UnicodeDecodeError as exc:
            raise ValueError('SKILL.md must be valid UTF-8.') from exc
        document = require_valid_skill_document(content)
        name = str(document.metadata['name'])
        description = str(document.metadata['description']).strip()
        normalized_content = document.with_metadata(
            github_url=source.canonical_url,
        ).render()
        if len(normalized_content.encode('utf-8')) > _MAX_SKILL_MD_BYTES:
            raise ValueError('Normalized SKILL.md exceeds the 5 MiB loading limit.')
        require_valid_skill_document(normalized_content, expected_name=name)
        normalized_files = dict(files)
        normalized_files['SKILL.md'] = normalized_content.encode('utf-8')
        return PreparedSkillPackage(
            source=source,
            name=name,
            description=description,
            files=normalized_files,
        )
