import importlib.util
import sys
import types
from pathlib import Path


ALEMBIC_ENV = Path(__file__).resolve().parents[4] / 'backend' / 'auth-service' / 'alembic' / 'env.py'


class _FakeConfig:
    config_ini_section = 'alembic'

    def __init__(self, config_file_name=None):
        self.config_file_name = config_file_name
        self.sections = {'alembic': {'script_location': 'alembic'}}

    def get_section(self, name):
        return dict(self.sections.get(name, {}))


class _Transaction:
    def __init__(self, context):
        self.context = context

    def __enter__(self):
        self.context.events.append('begin_transaction')
        return self

    def __exit__(self, exc_type, exc, tb):
        self.context.events.append('end_transaction')
        return False


class _FakeAlembicContext:
    def __init__(self, offline):
        self.offline = offline
        self.config = _FakeConfig()
        self.configure_calls = []
        self.migration_runs = 0
        self.events = []

    def is_offline_mode(self):
        return self.offline

    def configure(self, **kwargs):
        self.configure_calls.append(kwargs)

    def begin_transaction(self):
        return _Transaction(self)

    def run_migrations(self):
        self.migration_runs += 1
        self.events.append('run_migrations')


class _FakeConnection:
    def __init__(self, engine):
        self.engine = engine

    def __enter__(self):
        self.engine.events.append('connect_enter')
        return self

    def __exit__(self, exc_type, exc, tb):
        self.engine.events.append('connect_exit')
        return False


class _FakeEngine:
    def __init__(self):
        self.events = []

    def connect(self):
        self.events.append('connect')
        return _FakeConnection(self)


def _load_env_module(monkeypatch, offline=True, database_url=None, config_file_name=None):
    context = _FakeAlembicContext(offline=offline)
    context.config = _FakeConfig(config_file_name=config_file_name)
    alembic_module = types.ModuleType('alembic')
    alembic_module.context = context
    monkeypatch.setitem(sys.modules, 'alembic', alembic_module)

    if database_url is None:
        monkeypatch.delenv('LAZYMIND_DATABASE_URL', raising=False)
    else:
        monkeypatch.setenv('LAZYMIND_DATABASE_URL', database_url)

    module_name = f'_auth_alembic_env_test_{id(context)}'
    spec = importlib.util.spec_from_file_location(module_name, ALEMBIC_ENV)
    module = importlib.util.module_from_spec(spec)
    sys.modules[module_name] = module
    spec.loader.exec_module(module)
    return module, context


def test_get_url_prefers_env_database_url(monkeypatch):
    module, context = _load_env_module(monkeypatch, offline=True, database_url='postgresql://env/db')

    assert module._get_url() == 'postgresql://env/db'
    assert isinstance(module._get_url(), str)
    assert context.configure_calls[0]['url'] == 'postgresql://env/db'
    assert context.configure_calls[0]['literal_binds'] is True
    assert context.configure_calls[0]['compare_type'] is True
    assert context.configure_calls[0]['dialect_opts'] == {'paramstyle': 'named'}
    assert isinstance(context.configure_calls[0]['dialect_opts'], dict)
    assert context.migration_runs == 1
    assert context.events == ['begin_transaction', 'run_migrations', 'end_transaction']


def test_get_url_falls_back_to_core_database_url(monkeypatch):
    module, context = _load_env_module(monkeypatch, offline=True, database_url=None)

    assert module._get_url() == module.DATABASE_URL
    assert isinstance(module._get_url(), str)
    assert context.configure_calls[0]['url'] == module.DATABASE_URL


def test_module_import_calls_file_config_when_config_file_is_present(monkeypatch):
    seen = {}

    def fake_file_config(filename):
        seen['filename'] = filename

    import logging.config

    monkeypatch.setattr(logging.config, 'fileConfig', fake_file_config)

    module, _ = _load_env_module(
        monkeypatch,
        offline=True,
        database_url='postgresql://cfg/db',
        config_file_name='alembic.ini',
    )

    assert module.config.config_file_name == 'alembic.ini'
    assert seen['filename'] == 'alembic.ini'


def test_run_migrations_online_builds_engine_and_configures_connection(monkeypatch):
    engine = _FakeEngine()
    seen = {}

    def fake_engine_from_config(configuration, prefix, poolclass):
        seen['configuration'] = configuration
        seen['prefix'] = prefix
        seen['poolclass'] = poolclass
        return engine

    module, context = _load_env_module(monkeypatch, offline=True, database_url='postgresql://online/db')
    monkeypatch.setattr(module, 'engine_from_config', fake_engine_from_config)
    context.configure_calls.clear()
    context.migration_runs = 0
    context.events.clear()

    module.run_migrations_online()

    assert seen['configuration']['sqlalchemy.url'] == 'postgresql://online/db'
    assert isinstance(seen['configuration'], dict)
    assert seen['prefix'] == 'sqlalchemy.'
    assert seen['poolclass'] is module.pool.NullPool
    assert engine.events == ['connect', 'connect_enter', 'connect_exit']
    assert context.configure_calls == [
        {
            'connection': context.configure_calls[0]['connection'],
            'target_metadata': module.target_metadata,
            'compare_type': True,
        }
    ]
    assert isinstance(context.configure_calls[0]['connection'], _FakeConnection)
    assert context.migration_runs == 1
    assert context.events == ['begin_transaction', 'run_migrations', 'end_transaction']


def test_module_import_runs_online_branch_when_context_is_online(monkeypatch):
    engine = _FakeEngine()
    seen = {}

    def fake_engine_from_config(configuration, prefix, poolclass):
        seen['configuration'] = configuration
        seen['prefix'] = prefix
        seen['poolclass'] = poolclass
        return engine

    import sqlalchemy

    monkeypatch.setattr(sqlalchemy, 'engine_from_config', fake_engine_from_config)

    module, context = _load_env_module(monkeypatch, offline=False, database_url='postgresql://import/db')

    assert seen['configuration']['sqlalchemy.url'] == 'postgresql://import/db'
    assert isinstance(context.configure_calls[0], dict)
    assert seen['prefix'] == 'sqlalchemy.'
    assert seen['poolclass'] is module.pool.NullPool
    assert context.configure_calls[0]['connection'].engine is engine
    assert context.configure_calls[0]['target_metadata'] is module.target_metadata
    assert context.migration_runs == 1
