from lazymind.common.database.postgres import sqlalchemy_engine_options


def test_file_sqlite_uses_one_connection_and_waits_for_writer():
    assert sqlalchemy_engine_options('sqlite:////tmp/lazymind/core.db') == {
        'connect_args': {'timeout': 30.0},
        'pool_size': 1,
        'max_overflow': 0,
    }


def test_postgres_keeps_default_pooling():
    assert sqlalchemy_engine_options('postgresql://localhost/lazymind') == {}


def test_memory_sqlite_does_not_override_its_pool_class():
    assert sqlalchemy_engine_options('sqlite:///:memory:') == {
        'connect_args': {'timeout': 30.0},
    }
