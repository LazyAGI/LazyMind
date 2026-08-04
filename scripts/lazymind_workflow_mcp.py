#!/usr/bin/env python3
"""Repository entry point for the LazyMind Workflow stdio MCP server."""
from __future__ import annotations

import sys
from pathlib import Path

REPOSITORY_ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(REPOSITORY_ROOT / 'algorithm'))
sys.path.insert(0, str(REPOSITORY_ROOT / 'algorithm' / 'lazyllm'))

from lazymind.workflow_mcp.server import main  # noqa: E402


if __name__ == '__main__':
    main()
