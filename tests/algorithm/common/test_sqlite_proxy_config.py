from lazymind.processor.service.db import parse_db_url


def test_parse_sqlite_proxy_url_as_sqlite_manager_config():
    assert parse_db_url('sqliteproxy://lazyllm') == {
        'db_type': 'sqlite',
        'user': '',
        'password': '',
        'host': '',
        'port': 0,
        'db_name': 'sqliteproxy://lazyllm',
    }
