from __future__ import annotations

from dataclasses import dataclass
import hashlib
import json
import os
import uuid

import lazyllm

from lazymind.chat.engine.tools.chat_artifact import (
    chat_agent_workspace,
    save_chat_file,
)
from lazymind.chat.engine.tools.text_edit import (
    build_text_diff,
    build_text_replacement,
    write_file_atomically,
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
    def _write_json(path: str, value: dict) -> None:
        write_file_atomically(
            path,
            json.dumps(value, ensure_ascii=False, separators=(',', ':')).encode('utf-8'),
        )

    @staticmethod
    def _read_json(path: str) -> dict:
        with open(path, 'r', encoding='utf-8') as source:
            return json.load(source)

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
    ) -> dict:
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
        metadata = {
            'preview_id': preview_id,
            'source_sha256': self._sha256(current),
            'mode': mode,
            'regex_flags': regex_flags,
            'replacements': replacement.replacements,
            'matches': list(replacement.matches),
            'diff': replacement.diff,
            'bytes_before': len(current),
            'bytes_after': len(replacement.content),
        }
        write_file_atomically(os.path.join(self.root, 'preview.bin'), replacement.content)
        self._write_json(os.path.join(self.root, 'preview.json'), metadata)
        return metadata

    def apply_preview(self, preview_id: str) -> tuple[dict, bytes, int]:
        metadata_path = os.path.join(self.root, 'preview.json')
        candidate_path = os.path.join(self.root, 'preview.bin')
        if not os.path.isfile(metadata_path) or not os.path.isfile(candidate_path):
            raise ValueError('Preview not found, stale, or already applied; request a new preview')
        metadata = self._read_json(metadata_path)
        if preview_id != metadata.get('preview_id'):
            raise ValueError('Preview is stale because a newer preview was created; request a new preview')
        current = self._read_current()
        if self._sha256(current) != metadata.get('source_sha256'):
            raise ValueError('Preview is stale because the draft changed; request a new preview')
        with open(candidate_path, 'rb') as source:
            candidate = source.read()

        state = self._read_json(self.state_path) if os.path.isfile(self.state_path) else {'revisions': []}
        revisions = state['revisions']
        undo_id = str(uuid.uuid4())
        undo_relpath = f'undo/{undo_id}.bin'
        write_file_atomically(os.path.join(self.root, undo_relpath), current)
        revisions.append(undo_id)
        state['revisions'] = revisions
        write_file_atomically(self.draft_path, candidate)
        self._write_json(self.state_path, state)
        os.unlink(candidate_path)
        os.unlink(metadata_path)
        return metadata, candidate, len(revisions)

    def undo(self) -> tuple[bytes, str, int]:
        if not os.path.isfile(self.state_path):
            raise ValueError('No applied attachment edit is available to undo')
        state = self._read_json(self.state_path)
        revisions = state['revisions']
        if not revisions:
            raise ValueError('No applied attachment edit is available to undo')
        current = self._read_current()
        undo_id = revisions.pop()
        undo_path = os.path.join(self.root, 'undo', f'{undo_id}.bin')
        with open(undo_path, 'rb') as source:
            previous = source.read()
        before = current.decode('utf-8', errors='strict')
        after = previous.decode('utf-8', errors='strict')
        diff = build_text_diff(before, after)
        write_file_atomically(self.draft_path, previous)
        state['revisions'] = revisions
        self._write_json(self.state_path, state)
        os.unlink(undo_path)
        return previous, diff, len(revisions)

    def publish(self) -> dict:
        return save_chat_file(
            self.filename,
            self.draft_path,
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
