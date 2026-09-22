"""Chat workspace files, uploaded file resources, and attachment drafts."""

from .ingest import ingest_pdf_file, ingest_upload_pdfs
from .resolver import ResolvedTextResource, resolve_text_target
from .store import FileResourceStore, new_file_id, render_file_resource_catalog
from lazymind.chat.engine.tools.conversation_workspace import chat_agent_workspace
from lazymind.chat.engine.tools.file_resources.tools import search_file_resource, read_file_resource
from lazymind.chat.engine.tools.chat_artifact import save_chat_artifact

__all__ = [
    'FileResourceStore',
    'ResolvedTextResource',
    'chat_agent_workspace',
    'search_file_resource',
    'ingest_pdf_file',
    'ingest_upload_pdfs',
    'new_file_id',
    'read_file_resource',
    'render_file_resource_catalog',
    'resolve_text_target',
    'save_chat_artifact',
]
