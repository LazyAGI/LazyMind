"""Rewrite service for skill drafts and prompt polishing."""

from __future__ import annotations

from .base import (
    BadRequestError,
    RewriteTaskType,
    UnprocessableContentError,
    rewrite_content,
)

# Import business modules to register their prompt builders and edit dispatch.
from . import skill  # noqa: F401
from . import polish  # noqa: F401
from .polish import rewrite_editable_selection

__all__ = [
    'BadRequestError',
    'RewriteTaskType',
    'UnprocessableContentError',
    'rewrite_content',
    'rewrite_editable_selection',
]
