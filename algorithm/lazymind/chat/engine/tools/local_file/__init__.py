"""Chat workspace files, uploaded file resources, and attachment drafts."""

from .ingest import ingest_pdf_file, ingest_upload_pdfs
from .resolver import ResolvedTextResource, resolve_text_target
from .store import FileResourceStore, new_file_id, render_file_resource_catalog
from .workspace import chat_agent_workspace, grep, read_file, save_chat_artifact

__all__ = [
    'FileResourceStore',
    'ResolvedTextResource',
    'chat_agent_workspace',
    'grep',
    'ingest_pdf_file',
    'ingest_upload_pdfs',
    'new_file_id',
    'read_file',
    'render_file_resource_catalog',
    'resolve_text_target',
    'save_chat_artifact',
]
