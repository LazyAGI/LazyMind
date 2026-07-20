from __future__ import annotations

from dataclasses import dataclass
import hashlib
import json
import os
import tempfile
import uuid

import lazyllm

from lazymind.chat.engine.tools.chat_artifact import (
    chat_agent_workspace,
    save_chat_file_bytes,
    validate_chat_artifact_filename,
)
from lazymind.chat.engine.tools.text_edit import (
    build_text_diff,
    build_text_replacement,
)


@dataclass(frozen=True)
class AttachmentEditDraft:
    """Conversation attachment draft backed by one replaceable download artifact."""

    source_path: str
    draft_path: str
    artifact_id: str
    filename: str

    @classmethod
    def for_current_conversation(cls, source_path: str) -> 'AttachmentEditDraft':
        config = lazyllm.globals.get('agentic_config') or {}
        user_id = str(config.get('user_id') or '0').strip()
        conversation_id = str(config.get('conversation_id') or '').strip()
        if not conversation_id:
            raise RuntimeError('conversation_id is required to edit an attachment')

        source = os.path.realpath(source_path)
        filename = os.path.basename(source)
        # Keep one edit history per uploaded attachment for the full conversation.
        # This makes a later user turn able to continue or undo the previous edit.
        identity = '\n'.join((user_id, conversation_id, source))
        draft_key = hashlib.sha256(identity.encode('utf-8')).hexdigest()
        workspace = chat_agent_workspace(user_id, conversation_id)
        draft_path = os.path.join(workspace, 'attachment-edits', draft_key, filename)
        artifact_id = str(uuid.uuid5(uuid.NAMESPACE_URL, f'lazymind:attachment-edit:{identity}'))
        return cls(source, draft_path, artifact_id, filename)

    @property
    def effective_path(self) -> str:
        return self.draft_path if os.path.isfile(self.draft_path) else self.source_path

    @property
    def root(self) -> str:
        return os.path.dirname(self.draft_path)

    @property
    def state_path(self) -> str:
        return os.path.join(self.root, 'state.json')

    @staticmethod
    def _sha256(content: bytes) -> str:
        return hashlib.sha256(content).hexdigest()

    @staticmethod
    def _write_bytes(path: str, content: bytes) -> None:
        parent = os.path.dirname(path)
        os.makedirs(parent, exist_ok=True)
        temp_path = ''
        try:
            with tempfile.NamedTemporaryFile(
                dir=parent,
                prefix=f'.{os.path.basename(path)}.',
                suffix='.tmp',
                delete=False,
            ) as output:
                temp_path = output.name
                output.write(content)
                output.flush()
                os.fsync(output.fileno())
            os.replace(temp_path, path)
            temp_path = ''
        finally:
            if temp_path:
                try:
                    os.unlink(temp_path)
                except FileNotFoundError:
                    pass

    @classmethod
    def _write_json(cls, path: str, value: dict) -> None:
        cls._write_bytes(
            path,
            json.dumps(value, ensure_ascii=False, separators=(',', ':')).encode('utf-8'),
        )

    @staticmethod
    def _read_json(path: str) -> dict:
        with open(path, 'r', encoding='utf-8') as source:
            value = json.load(source)
        if not isinstance(value, dict):
            raise ValueError('Invalid attachment edit state')
        return value

    def _read_current(self) -> bytes:
        with open(self.effective_path, 'rb') as source:
            return source.read()

    def create_preview(
        self,
        pattern: str,
        replacement_text: str,
        expected_replacements: int,
        mode: str,
        regex_flags: str,
        output_filename: str,
    ) -> dict:
        output_filename = validate_chat_artifact_filename(output_filename)
        current = self._read_current()
        replacement = build_text_replacement(
            current,
            pattern,
            replacement_text,
            expected_replacements=expected_replacements,
            mode=mode,
            regex_flags=regex_flags,
        )
        preview_id = str(uuid.uuid4())
        preview_dir = os.path.join(self.root, 'previews')
        candidate_path = os.path.join(preview_dir, f'{preview_id}.bin')
        metadata_path = os.path.join(preview_dir, f'{preview_id}.json')
        metadata = {
            'preview_id': preview_id,
            'source_sha256': self._sha256(current),
            'candidate_sha256': self._sha256(replacement.content),
            'output_filename': output_filename,
            'mode': mode,
            'regex_flags': regex_flags,
            'replacements': replacement.replacements,
            'matches': list(replacement.matches),
            'diff': replacement.diff,
            'bytes_before': len(current),
            'bytes_after': len(replacement.content),
        }
        self._write_bytes(candidate_path, replacement.content)
        self._write_json(metadata_path, metadata)
        return metadata

    def apply_preview(self, preview_id: str) -> tuple[dict, bytes, int]:
        try:
            normalized_id = str(uuid.UUID(str(preview_id or '').strip()))
        except (ValueError, AttributeError) as exc:
            raise ValueError('preview_id must be a valid preview returned by this tool') from exc
        preview_dir = os.path.join(self.root, 'previews')
        metadata_path = os.path.join(preview_dir, f'{normalized_id}.json')
        candidate_path = os.path.join(preview_dir, f'{normalized_id}.bin')
        if not os.path.isfile(metadata_path) or not os.path.isfile(candidate_path):
            raise ValueError('Preview not found or already applied; request a new preview')
        metadata = self._read_json(metadata_path)
        current = self._read_current()
        if self._sha256(current) != metadata.get('source_sha256'):
            raise ValueError('Preview is stale because the draft changed; request a new preview')
        with open(candidate_path, 'rb') as source:
            candidate = source.read()
        if self._sha256(candidate) != metadata.get('candidate_sha256'):
            raise ValueError('Preview candidate failed integrity validation')

        state = self._read_json(self.state_path) if os.path.isfile(self.state_path) else {'revisions': []}
        revisions = state.get('revisions')
        if not isinstance(revisions, list):
            raise ValueError('Invalid attachment edit revision state')
        undo_id = str(uuid.uuid4())
        undo_relpath = os.path.join('undo', f'{undo_id}.bin')
        self._write_bytes(os.path.join(self.root, undo_relpath), current)
        revisions.append({'path': undo_relpath, 'sha256': self._sha256(current)})
        state['revisions'] = revisions
        state['output_filename'] = metadata.get('output_filename') or self.filename
        self._write_bytes(self.draft_path, candidate)
        self._write_json(self.state_path, state)
        try:
            os.unlink(candidate_path)
            os.unlink(metadata_path)
        except FileNotFoundError:
            pass
        return metadata, candidate, len(revisions)

    def undo(self, output_filename: str | None = None) -> tuple[bytes, str, int, str]:
        if not os.path.isfile(self.state_path):
            raise ValueError('No applied attachment edit is available to undo')
        state = self._read_json(self.state_path)
        revisions = state.get('revisions')
        if not isinstance(revisions, list) or not revisions:
            raise ValueError('No applied attachment edit is available to undo')
        current = self._read_current()
        revision = revisions.pop()
        root = os.path.realpath(self.root)
        undo_path = os.path.realpath(os.path.join(root, str(revision.get('path') or '')))
        if os.path.commonpath((root, undo_path)) != root or not os.path.isfile(undo_path):
            raise ValueError('Attachment undo state is invalid')
        with open(undo_path, 'rb') as source:
            previous = source.read()
        if self._sha256(previous) != revision.get('sha256'):
            raise ValueError('Attachment undo state failed integrity validation')
        before = current.decode('utf-8', errors='strict')
        after = previous.decode('utf-8', errors='strict')
        diff = build_text_diff(before, after)
        self._write_bytes(self.draft_path, previous)
        state['revisions'] = revisions
        if output_filename:
            state['output_filename'] = output_filename
        self._write_json(self.state_path, state)
        os.unlink(undo_path)
        return (
            previous,
            diff,
            len(revisions),
            str(state.get('output_filename') or self.filename),
        )

    def publish(self, filename: str, content: bytes) -> dict:
        return save_chat_file_bytes(
            filename,
            content,
            caption=f'Edited copy of {self.filename}',
            artifact_id=self.artifact_id,
            replace_existing=True,
        )


def effective_attachment_path(source_path: str) -> str:
    """Return the conversation's edited draft when one exists, otherwise the upload."""
    try:
        return AttachmentEditDraft.for_current_conversation(source_path).effective_path
    except RuntimeError:
        return source_path
