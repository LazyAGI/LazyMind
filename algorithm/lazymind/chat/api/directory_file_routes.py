"""Model-free local preview, called only after Core resolves an owned reference."""
from __future__ import annotations

import asyncio
import base64
import mimetypes
import os
import socket
import stat

from fastapi import APIRouter, Depends
from pydantic import BaseModel, Field

from lazymind.config import config
from lazymind.chat.api.agent_control_routes import require_env_input_service
from lazymind.chat.engine.tools.host_file_resolution import open_input_file

router = APIRouter()
MAX_PREVIEW_BYTES = 20 * 1024 * 1024


class LocalFileRequest(BaseModel):
    path: str = Field(min_length=1, max_length=32768)
    namespace: str = Field(min_length=1, max_length=512)
    metadata_only: bool = False


def read_local_file(req: LocalFileRequest) -> dict:
    if config['runtime_mode'] != 'local':
        return {'error_code': 'unavailable'}
    if req.namespace != f'{socket.gethostname()}:{os.name}':
        return {'error_code': 'different_host'}
    if not os.path.isabs(req.path) or '\0' in req.path:
        return {'error_code': 'unavailable'}
    try:
        info = os.stat(req.path, follow_symlinks=False)
        if not stat.S_ISREG(info.st_mode):
            return {'error_code': 'unsupported'}
        # Pins the opened object and rejects symlink traversal on macOS/Windows.
        with open_input_file(req.path) as stream:
            info = os.fstat(stream.fileno())
            if not stat.S_ISREG(info.st_mode):
                return {'error_code': 'unsupported'}
            result = {'path': req.path, 'namespace': req.namespace,
                      'filename': os.path.basename(req.path), 'size': info.st_size,
                      'mime_type': mimetypes.guess_type(req.path)[0] or 'application/octet-stream'}
            if req.metadata_only:
                return result
            if info.st_size > MAX_PREVIEW_BYTES:
                return {'error_code': 'too_large'}
            body = stream.read(MAX_PREVIEW_BYTES + 1)
            if len(body) > MAX_PREVIEW_BYTES:
                return {'error_code': 'too_large'}
            result['content_base64'] = base64.b64encode(body).decode('ascii')
            return result
    except FileNotFoundError:
        return {'error_code': 'not_found'}
    except PermissionError:
        return {'error_code': 'denied'}
    except Exception:
        return {'error_code': 'unavailable'}


@router.post('/api/document-directories/local-file', dependencies=[Depends(require_env_input_service)])
async def local_file(req: LocalFileRequest) -> dict:
    try:
        return await asyncio.wait_for(asyncio.to_thread(read_local_file, req), timeout=25)
    except TimeoutError:
        return {'error_code': 'unavailable'}
