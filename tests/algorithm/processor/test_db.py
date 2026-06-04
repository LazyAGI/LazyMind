import pytest

from lazymind.processor.service.db import (
    SHARED_DB_ENV_KEY,
    get_doc_task_db_config,
    get_shared_database_url,
    get_shared_db_config,
    parse_db_url,
    require_shared_db_config,
)


def test_parse_db_url_empty():
    assert parse_db_url(None) is None
    assert parse_db_url('') is None
    assert parse_db_url('   ') is None


def test_parse_db_url_postgres_with_driver():
    config = parse_db_url('postgresql+psycopg://user:pass@host:5433/mydb')

    assert config == {
        'db_type': 'postgresql',
        'user': 'user',
        'password': 'pass',
        'host': 'host',
        'port': 5433,
        'db_name': 'mydb',
    }


def test_parse_db_url_decodes_credentials_and_uses_defaults():
    config = parse_db_url('postgresql://user%40mail:pass%2Fword@db-host')

    assert config['user'] == 'user@mail'
    assert config['password'] == 'pass/word'
    assert config['port'] == 5432
    assert config['db_name'] == 'app'


def test_parse_db_url_rejects_unsupported_scheme():
    with pytest.raises(ValueError, match='unsupported database scheme'):
        parse_db_url('mysql://user:pass@host/db')


def test_parse_db_url_requires_host():
    with pytest.raises(ValueError, match='database host is required'):
        parse_db_url('postgresql:///db')


def test_get_shared_database_url_returns_non_blank_value(monkeypatch):
    monkeypatch.delenv(SHARED_DB_ENV_KEY, raising=False)
    assert get_shared_database_url() is None

    monkeypatch.setenv(SHARED_DB_ENV_KEY, '   ')
    assert get_shared_database_url() is None

    monkeypatch.setenv(SHARED_DB_ENV_KEY, 'postgresql://u:p@localhost/tasks')
    assert get_shared_database_url() == 'postgresql://u:p@localhost/tasks'


def test_get_shared_db_config_and_doc_task_alias(monkeypatch):
    monkeypatch.setenv(SHARED_DB_ENV_KEY, 'postgresql://u:p@localhost:5432/tasks')

    assert get_shared_db_config()['db_name'] == 'tasks'
    assert get_doc_task_db_config()['db_name'] == 'tasks'


def test_get_shared_db_config_returns_none_when_env_missing(monkeypatch):
    monkeypatch.delenv(SHARED_DB_ENV_KEY, raising=False)

    assert get_shared_db_config() is None
    assert get_doc_task_db_config() is None


def test_require_shared_db_config_reports_missing_env(monkeypatch):
    monkeypatch.delenv(SHARED_DB_ENV_KEY, raising=False)

    with pytest.raises(RuntimeError, match='DocumentProcessor requires a shared database configuration'):
        require_shared_db_config('DocumentProcessor')


def test_require_shared_db_config_wraps_invalid_url(monkeypatch):
    monkeypatch.setenv(SHARED_DB_ENV_KEY, 'sqlite:///tmp/app.db')

    with pytest.raises(RuntimeError, match='valid PostgreSQL URL'):
        require_shared_db_config('DocumentProcessor')


def test_require_shared_db_config_rejects_none_after_parse(monkeypatch):
    monkeypatch.setenv(SHARED_DB_ENV_KEY, 'postgresql://u:p@localhost/tasks')
    monkeypatch.setattr('lazymind.processor.service.db.parse_db_url', lambda url: None)

    with pytest.raises(RuntimeError, match='shared database configuration'):
        require_shared_db_config('DocumentProcessor')
