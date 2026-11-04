import os
from pathlib import Path
from typing import List, Optional
from fastapi import HTTPException

from lazymind.chat.config import MOUNT_BASE_DIR


def validate_and_resolve_files(files: Optional[List[str]]) -> List[str]:
    if not files:
        return []

    root = Path(MOUNT_BASE_DIR).resolve()
    resolved: List[str] = []
    for f in files:
        if '\x00' in f:
            raise HTTPException(status_code=400, detail='Invalid path')
        p = Path(f)
        cand = (p if p.is_absolute() else root / p).resolve()
        if not cand.is_relative_to(root):
            raise HTTPException(status_code=400, detail='Path outside mount directory')
        if not cand.is_file() or not os.access(cand, os.R_OK):
            raise HTTPException(status_code=400, detail=f'File not accessible: {f}')
        resolved.append(str(cand))

    return resolved
