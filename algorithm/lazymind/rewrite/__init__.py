"""Rewrite service — unified content generation for skill, memory, user_preference, and polish."""

from __future__ import annotations

from .base import (
    BadRequestError,
    RewriteTaskType,
    UnprocessableContentError,
    rewrite_content,
)

# Import business modules to register their prompt builders and edit dispatch
from . import skill  # noqa: F401
from . import memory  # noqa: F401
from . import preference  # noqa: F401
from . import polish  # noqa: F401

__all__ = [
    'BadRequestError',
    'RewriteTaskType',
    'UnprocessableContentError',
    'rewrite_content',
]
