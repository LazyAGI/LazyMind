"""Pin temporary retrieval mode to a conversation without importing RAG.

Only the mode is persisted, not model credentials or chunk indexes. This keeps
the choice stable across requests, workers, and in-memory cache eviction.
"""
from contextlib import closing
from pathlib import Path
import sqlite3

import lazyllm

from lazymind.chat.engine.tools.file_resources.store import workspace_for_request
from lazymind.model_config import is_model_role_available
from lazymind.common.optional_components import component_enabled


def temporary_retrieval_scope(config: dict | None = None) -> str:
    config = config if config is not None else lazyllm.globals.get('agentic_config') or {}
    conversation_id = str(config.get('conversation_id') or '').strip()
    if not conversation_id:
        raise RuntimeError('conversation_id is required for temporary retrieval')
    return str(Path(workspace_for_request(
        user_id=str(config.get('user_id') or '0'), conversation_id=conversation_id,
    )).resolve())


def initialize_temporary_retrieval(config: dict | None = None) -> bool:
    """Return the immutable choice made on the first actual chat request.

    Direct tool callers initialize on first use. Context previews must not call
    this initializer: they are not the start of an actual conversation.
    """
    root = Path(temporary_retrieval_scope(config)) / 'file-resources'
    root.mkdir(parents=True, exist_ok=True)
    with closing(sqlite3.connect(root / 'temporary-retrieval.sqlite3', timeout=30)) as connection:
        with connection:
            connection.execute('BEGIN IMMEDIATE')
            connection.execute('CREATE TABLE IF NOT EXISTS retrieval_mode '
                               '(id INTEGER PRIMARY KEY CHECK (id = 1), '
                               'use_embed INTEGER NOT NULL CHECK (use_embed IN (0, 1)))')
            row = connection.execute('SELECT use_embed FROM retrieval_mode WHERE id = 1').fetchone()
            if row is not None:
                return bool(row[0])
            # Decide once: installing RAG or changing models later must not
            # change this conversation's retrieval mode.
            use_embed = component_enabled('rag') and is_model_role_available('embed_main')
            connection.execute('INSERT INTO retrieval_mode (id, use_embed) VALUES (1, ?)', (int(use_embed),))
            return use_embed
