"""Explicit, conflict-checked Obsidian IO owned by LazyMind."""
from __future__ import annotations

from concurrent.futures import Future, ThreadPoolExecutor
import hashlib
import os
from pathlib import Path, PurePosixPath
import re
import tempfile
from threading import Lock
from urllib.parse import parse_qs, quote, unquote, urlsplit

from filelock import FileLock
from lazyllm import config
from lazyllm.tools.fs.supplier.obsidian import ObsidianFS, ObsidianNote, ObsidianVault
from lazyllm.tools.writer.data_models import TargetDocument
from lazyllm.tools.writer.provider.obsidian import ObsidianWriterProvider


class ObsidianTargetError(ValueError):
    def __init__(self, code):
        self.error_code = code
        hints = {
            'OBSIDIAN_NOT_CONFIGURED': 'Configure a vault accessible to the LazyMind runtime.',
            'OBSIDIAN_VAULT_REQUIRED': 'Ask the user to choose an available vault; do not choose the first one.',
            'OBSIDIAN_PATH_REQUIRED': 'Ask for a filename or an existing directory inside the chosen vault.',
            'OBSIDIAN_PATH_INVALID': 'Use a valid relative Markdown path inside the chosen vault.',
            'OBSIDIAN_PERMISSION_REQUIRED': 'Obtain write permission for this vault and note before retrying.',
            'OBSIDIAN_TARGET_EXISTS': 'This note already exists. Ask before updating it, or request another filename.',
            'OBSIDIAN_TARGET_CHANGED': (
                'The note changed externally. Read the latest note and review changes before writing.'
            ),
            'OBSIDIAN_BASELINE_REQUIRED': 'Read the existing note before updating it.',
            'OBSIDIAN_ATTACHMENT_UNAVAILABLE': 'A referenced image is unavailable. Restore access before retrying.',
        }
        super().__init__(f'{code}: {hints.get(code, "Check the selected target before retrying.")}')


class _PublicationObsidianFS(ObsidianFS):
    _vaults_by_root = {}
    _pending_discoveries = {}
    _discovery_lock = Lock()

    @classmethod
    def discover_vaults_for_root(cls, root, *, refresh=False):
        root = Path(root).resolve()
        cached = cls._vaults_by_root.get(root)
        if cached is not None and not refresh:
            return list(cached)
        with cls._discovery_lock:
            cached = cls._vaults_by_root.get(root)
            if cached is not None and not refresh:
                return list(cached)
            pending = cls._pending_discoveries.get(root)
            should_scan = pending is None
            if should_scan:
                pending = cls._pending_discoveries[root] = Future()
        if not should_scan:
            return list(pending.result())
        try:
            # Keep the previous snapshot available until traversal finishes.
            def scan(scan_root, split=False):
                vaults, subtrees = [], []
                if os.path.islink(scan_root):
                    return vaults, subtrees
                for current, directories, _ in os.walk(scan_root, followlinks=False):
                    path = Path(current)
                    if (path / '.obsidian').is_dir():
                        vaults.append(ObsidianVault(
                            vault_id='vlt_' + hashlib.sha256(str(path).encode()).hexdigest()[:16],
                            root=path, display_name=path.name))
                        directories[:] = []
                    elif split and path != root:
                        subtrees.extend(path / name for name in directories if not os.path.islink(path / name))
                        directories[:] = []
                return vaults, subtrees

            # Partition below the first level; each worker still traverses its entire subtree.
            vaults, subtrees = scan(root, split=True)
            if subtrees:
                with ThreadPoolExecutor(max_workers=4) as executor:
                    for found, _ in executor.map(scan, subtrees):
                        vaults.extend(found)
            vaults = tuple(sorted(vaults, key=lambda item: (item.display_name.lower(), str(item.root))))
            with cls._discovery_lock:
                cls._vaults_by_root[root] = vaults
            pending.set_result(vaults)
        except BaseException as exc:
            pending.set_exception(exc)
            raise
        finally:
            with cls._discovery_lock:
                del cls._pending_discoveries[root]
        return list(vaults)

    def _note_from_relative(self, vault, relative_path):
        if vault.root.resolve() != vault.root.absolute() or not (vault.root / '.obsidian').is_dir():
            raise ValueError('Obsidian Vault is no longer available.')
        return super()._note_from_relative(vault, relative_path)


def _safe_path(root: Path, relative: str) -> Path:
    path = PurePosixPath(relative)
    if (not relative or path.is_absolute() or '\\' in relative
            or any(part in {'.', '..', '.obsidian'} for part in relative.split('/'))
            or any(ord(char) < 32 for char in relative)):
        raise ObsidianTargetError('OBSIDIAN_PATH_INVALID')
    candidate = root / relative
    if candidate.resolve() != candidate.absolute() or not candidate.resolve().is_relative_to(root.resolve()):
        raise ObsidianTargetError('OBSIDIAN_PATH_INVALID')
    return candidate


def _writable(path: Path) -> bool:
    return path.exists() and bool(path.stat().st_mode & 0o222) and os.access(path, os.W_OK)


class _Attachments:
    def __init__(self, fs, assets, dry_run=False):
        self.fs = fs
        self.dry_run = dry_run
        self.sources = {str(Path(asset.local_path).resolve()) for asset in (assets.assets.values() if assets else [])
                        if asset.local_path}

    def copy_attachment(self, note, source):
        source = Path(source).resolve()
        if str(source) not in self.sources and not source.is_relative_to(note.vault.root):
            raise ObsidianTargetError('OBSIDIAN_ATTACHMENT_UNAVAILABLE')
        if not source.is_file():
            raise ObsidianTargetError('OBSIDIAN_ATTACHMENT_UNAVAILABLE')
        data = source.read_bytes()
        relative = f'assets/lazymind/{hashlib.sha256(data).hexdigest()}{source.suffix.lower() or ".png"}'
        destination = _safe_path(note.vault.root, relative)
        if self.dry_run:
            if not destination.is_file() or destination.read_bytes() != data:
                raise ObsidianTargetError('OBSIDIAN_ATTACHMENT_UNAVAILABLE')
        else:
            destination.parent.mkdir(parents=True, exist_ok=True)
            _safe_path(note.vault.root, relative)
            try:
                with destination.open('xb') as stream:
                    stream.write(data)
            except FileExistsError:
                if destination.read_bytes() != data:
                    raise ObsidianTargetError('OBSIDIAN_ATTACHMENT_UNAVAILABLE')
        return relative


class LazyMindObsidianProvider(ObsidianWriterProvider):
    """Reuse Markdown conversion; keep target selection and mutations here."""

    def _fs(self):
        configured = super()._fs()
        return _PublicationObsidianFS(token=str(configured._vault_root), skip_instance_cache=True)

    @staticmethod
    def _asset_path(uri, assets):
        if uri.startswith('lazymind-media:'):
            asset_id = unquote(uri.removeprefix('lazymind-media:'))
            asset = next((item for item in assets if item.media_asset_id == asset_id), None)
            if asset and asset.local_path and Path(asset.local_path).is_file():
                return Path(asset.local_path)
            raise ObsidianTargetError('OBSIDIAN_ATTACHMENT_UNAVAILABLE')
        path = ObsidianWriterProvider._asset_path(uri, assets)
        if path is None:
            from .host_files import staged_writer_input
            # Markdown can retain the source path while the asset has been
            # staged again. Resolve only to an asset approved for this call.
            staged = staged_writer_input(uri)
            if staged:
                path = ObsidianWriterProvider._asset_path(staged, assets)
        if path is None and (not uri.startswith(('https://', 'http://', '//'))
                             or any(uri in {asset.uri, asset.local_path} for asset in assets)):
            raise ObsidianTargetError('OBSIDIAN_ATTACHMENT_UNAVAILABLE')
        return path

    @staticmethod
    def _serialize_writer_document(document, media_assets):
        # Staging paths last for one tool call. Keep references stable across
        # conversion and publication; resolve bytes from the newly approved assets.
        stable_assets = media_assets.model_copy(deep=True) if media_assets else None
        if stable_assets:
            for asset in stable_assets.assets.values():
                if asset.local_path:
                    asset.local_path = asset.uri or f'lazymind-media:{quote(asset.media_asset_id, safe="")}'
        return ObsidianWriterProvider._serialize_writer_document(document, stable_assets)

    def _vaults(self, *, refresh=False):
        fs = self._fs()
        # ObsidianFS otherwise falls back to the process working directory.
        if not config['obsidian_vault_path'] and Path(fs._vault_root) == Path.cwd():
            raise ObsidianTargetError('OBSIDIAN_NOT_CONFIGURED')
        vaults = [vault for vault in fs.discover_vaults_for_root(fs._vault_root, refresh=refresh)
                  if vault.root.resolve() == vault.root.absolute()
                  and (vault.root / '.obsidian').is_dir()]
        if not vaults:
            raise ObsidianTargetError('OBSIDIAN_NOT_CONFIGURED')
        return fs, vaults

    def status(self):
        try:
            _, vaults = self._vaults(refresh=True)
            items = [{'id': vault.vault_id, 'name': vault.display_name, 'writable': _writable(vault.root)}
                     for vault in vaults]
            return {'state': 'ready' if any(item['writable'] for item in items) else 'permission_required',
                    'vaults': items}
        except (OSError, ObsidianTargetError):
            return {'state': 'not_configured', 'vaults': []}

    def prepare_target(self, title='', parent_uri='', target=None):
        fs, vaults = self._vaults()
        if target is not None:
            if target.meta.get('create_pending'):
                # Resume the saved destination, never choose a new default vault.
                _, note = self._note(target)
                if note.path.exists():
                    raise ObsidianTargetError('OBSIDIAN_TARGET_EXISTS')
                if not note.path.parent.is_dir():
                    raise ObsidianTargetError('OBSIDIAN_PATH_REQUIRED')
                if not _writable(note.path.parent):
                    raise ObsidianTargetError('OBSIDIAN_PERMISSION_REQUIRED')
                return target
            note = fs.resolve_locator(str(target.uri or ''))
            _safe_path(note.vault.root, note.relative_path)
            if not _writable(note.path) or not _writable(note.path.parent):
                raise ObsidianTargetError('OBSIDIAN_PERMISSION_REQUIRED')
            expected = (
                target.meta.get('publication_revision') or target.meta.get('obsidian_bridge', {}).get('source_hash')
            )
            actual = self._hash(note.path.read_text(encoding='utf-8'))
            if not expected:
                raise ObsidianTargetError('OBSIDIAN_BASELINE_REQUIRED')
            if expected != actual:
                raise ObsidianTargetError('OBSIDIAN_TARGET_CHANGED')
            target.meta['publication_revision'] = actual
            return target
        stem = re.sub(r'[<>:"/\\|?*\x00-\x1f]+', '-', title).strip(' .')
        if not stem and not parent_uri:
            raise ObsidianTargetError('OBSIDIAN_PATH_REQUIRED')
        relative = f'{stem}.md'
        if parent_uri:
            parsed = urlsplit(parent_uri)
            if parsed.scheme != 'obsidian' or parsed.fragment or parsed.username:
                raise ObsidianTargetError('OBSIDIAN_PATH_INVALID')
            if parsed.netloc == 'open':
                query = parse_qs(parsed.query)
                matches = [item for item in vaults if item.display_name == query.get('vault', [''])[0]]
                relative = query.get('file', [''])[0]
            else:
                matches = [item for item in vaults if item.vault_id == parsed.netloc]
                directory = unquote(parsed.path.lstrip('/'))
                relative = directory + relative if not directory or directory.endswith('/') else directory
            if len(matches) != 1:
                raise ObsidianTargetError('OBSIDIAN_VAULT_REQUIRED')
            vault = matches[0]
        elif len(vaults) == 1:
            vault = vaults[0]
        else:
            raise ObsidianTargetError('OBSIDIAN_VAULT_REQUIRED')
        if not relative.lower().endswith('.md'):
            relative += '.md'
        if len(Path(relative).name.encode('utf-8')) > 255 or Path(relative).name == '.md':
            raise ObsidianTargetError('OBSIDIAN_PATH_INVALID')
        path = _safe_path(vault.root, relative)
        if path.exists():
            raise ObsidianTargetError('OBSIDIAN_TARGET_EXISTS')
        if not path.parent.is_dir():
            raise ObsidianTargetError('OBSIDIAN_PATH_REQUIRED')
        if not _writable(path.parent):
            raise ObsidianTargetError('OBSIDIAN_PERMISSION_REQUIRED')
        note = ObsidianNote(vault=vault, relative_path=relative, path=path)
        return TargetDocument(adapter='obsidian', uri=self._canonical_uri(note), title=path.stem,
                              doc_id=self._document_id(note),
                              meta={'create_pending': True, 'local_path': fs.display_note_path(note),
                                    'display_location': f'{vault.display_name} / {relative}'})

    def create_document(self, title, parent_uri=''):
        # Creation is deferred until content is ready; no empty orphan note.
        return self.prepare_target(title, parent_uri)

    def load_document(self, target, *, stage='final'):
        fs, _ = self._vaults()
        note = fs.resolve_locator(str(target.uri or ''))
        _safe_path(note.vault.root, note.relative_path)
        loaded = super().load_document(target, stage=stage)
        resolved = loaded['target_document']
        resolved.meta['publication_revision'] = resolved.meta['obsidian_bridge']['source_hash']
        return loaded

    def _note(self, target):
        fs, vaults = self._vaults()
        if not target.meta.get('create_pending'):
            note = fs.resolve_locator(str(target.uri or ''))
            _safe_path(note.vault.root, note.relative_path)
            return fs, note
        parsed = urlsplit(str(target.uri or ''))
        vault = next((item for item in vaults if item.vault_id == parsed.netloc), None)
        if parsed.scheme != 'obsidian' or vault is None or parsed.query or parsed.fragment:
            raise ObsidianTargetError('OBSIDIAN_PATH_INVALID')
        relative = unquote(parsed.path.lstrip('/'))
        return fs, ObsidianNote(vault=vault, relative_path=relative, path=_safe_path(vault.root, relative))

    def reconcile(self, converted, target, media_assets=None):
        fs, note = self._note(target)
        if not note.path.exists():
            return {'state': 'absent'}
        expected = self._from_writer_markdown(
            converted.content, target.meta.get('obsidian_bridge', {}),
            note, _Attachments(fs, media_assets, dry_run=True), media_assets,
        )
        if note.path.read_text(encoding='utf-8') != expected:
            return {'state': 'unknown'}
        self._record_target(target, fs, note, expected)
        return {'state': 'confirmed', 'target_document': target.model_dump(),
                'persisted_document': converted.content}

    def _record_target(self, target, fs, note, content):
        target.meta.pop('create_pending', None)
        target.meta['local_path'] = fs.display_note_path(note)
        target.meta['display_location'] = f'{note.vault.display_name} / {note.relative_path}'
        target.meta['publication_revision'] = self._hash(content)
        if target.meta.get('obsidian_bridge'):
            target.meta['obsidian_bridge']['source_hash'] = self._hash(content)

    def replace_document(self, content, target, *, media_assets=None):
        if not isinstance(content, str):
            content = self._serialize_writer_document(content, media_assets)
        fs, note = self._note(target)
        lock_path = Path(tempfile.gettempdir()) / ('lazymind-obsidian-' + self._hash(str(note.path)) + '.lock')
        with FileLock(str(lock_path), timeout=10):
            creating = bool(target.meta.get('create_pending'))
            if creating:
                if note.path.exists():
                    raise ObsidianTargetError('OBSIDIAN_TARGET_EXISTS')
                if not _writable(note.path.parent):
                    raise ObsidianTargetError('OBSIDIAN_PERMISSION_REQUIRED')
            else:
                self.prepare_target(target=target)
            expected = target.meta.get('publication_revision')
            restored = self._from_writer_markdown(
                content, target.meta.get('obsidian_bridge', {}), note,
                _Attachments(fs, media_assets), media_assets,
            )
            _safe_path(note.vault.root, note.relative_path)
            descriptor, temporary = tempfile.mkstemp(prefix='.lazymind-', suffix='.tmp', dir=note.path.parent)
            try:
                with os.fdopen(descriptor, 'w', encoding='utf-8', newline='') as stream:
                    stream.write(restored)
                    stream.flush()
                    os.fsync(stream.fileno())
                _safe_path(note.vault.root, note.relative_path)
                if creating:
                    try:
                        os.link(temporary, note.path)
                    except FileExistsError as exc:
                        raise ObsidianTargetError('OBSIDIAN_TARGET_EXISTS') from exc
                else:
                    if self._hash(note.path.read_text(encoding='utf-8')) != expected:
                        raise ObsidianTargetError('OBSIDIAN_TARGET_CHANGED')
                    os.chmod(temporary, note.path.stat().st_mode & 0o777)
                    os.replace(temporary, note.path)
            finally:
                Path(temporary).unlink(missing_ok=True)
            self._record_target(target, fs, note, restored)
        return {'adapter': 'obsidian', 'doc_id': self._document_id(note), 'locator': self._canonical_uri(note),
                'local_path': fs.display_note_path(note), 'warnings': []}

    def write_document(self, converted, target, *, media_assets=None, mode='replace'):
        result = super().write_document(converted, target, media_assets=media_assets, mode=mode)
        result['published_link'] = 'obsidian://open?path=' + quote(target.meta['local_path'], safe='')
        return result
