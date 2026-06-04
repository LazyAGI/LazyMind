"""
Unit tests for lazymind.processor.service.db (parse_db_url, get_doc_task_db_config).
No external deps - pure functions.
"""
import pytest

from lazymind.processor.service.db import parse_db_url, get_doc_task_db_config


def test_parse_db_url_empty():
    assert parse_db_url(None) is None
    assert parse_db_url('') is None
    assert parse_db_url('   ') is None


def test_parse_db_url_postgres():
    url = 'postgresql+psycopg://user:pass@host:5432/mydb'
    r = parse_db_url(url)
    assert r is not None
    assert r['db_type'] == 'postgresql'
    assert r['user'] == 'user'
    assert r['password'] == 'pass'
    assert r['host'] == 'host'
    assert r['port'] == 5432
    assert r['db_name'] == 'mydb'


def test_parse_db_url_postgres_default_port():
    url = 'postgresql://u:p@localhost/db'
    r = parse_db_url(url)
    assert r['port'] == 5432


def test_parse_db_url_mysql():
    url = 'mysql://u:p@host:3306/app'
    with pytest.raises(ValueError, match='unsupported database scheme'):
        parse_db_url(url)


def test_parse_db_url_urlencoded_password():
    url = 'postgresql://u:pass%40word@h/db'
    r = parse_db_url(url)
    assert r['password'] == 'pass@word'


def test_parse_db_url_no_host():
    with pytest.raises(ValueError, match='database host is required'):
        parse_db_url('postgresql:///db')


def test_get_doc_task_db_config_unset(monkeypatch):
    monkeypatch.delenv('LAZYMIND_DATABASE_URL', raising=False)
    assert get_doc_task_db_config() is None


def test_get_doc_task_db_config_set(monkeypatch):
    monkeypatch.setenv('LAZYMIND_DATABASE_URL', 'postgresql://u:p@localhost:5432/tasks')
    r = get_doc_task_db_config()
    assert r is not None
    assert r['db_name'] == 'tasks'
