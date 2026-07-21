from __future__ import annotations

import pytest

from lazymind.router.db import client


def test_database_engine_is_created_lazily(monkeypatch):
    monkeypatch.setattr(client, 'config', {'core_database_url': None})
    client.get_engine.cache_clear()

    with pytest.raises(RuntimeError, match='core_database_url is required'):
        client.get_engine()

