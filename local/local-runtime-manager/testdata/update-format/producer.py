"""Create isolated update-format fixtures using the actual schema owners."""
import ast
from contextlib import closing
import json
import os
from pathlib import Path
import sqlite3
import subprocess
import sys
import tempfile
import threading
from types import SimpleNamespace
from unittest.mock import patch


def segment_store(repo, filename):
    # Execute the pinned class unchanged; only framework imports/base/decorators
    # are neutralized. All DDL, serialization and SQLite operations remain real.
    source = repo / 'algorithm/lazyllm/lazyllm/tools/rag/store/segment/sqlite_store.py'
    module = ast.parse(source.read_text())
    cls = next(node for node in module.body if isinstance(node, ast.ClassDef) and node.name == 'SQLiteStore')
    cls.bases = [ast.Name(id='object', ctx=ast.Load())]
    for node in ast.walk(cls):
        if isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef)):
            node.decorator_list = [decorator for decorator in node.decorator_list
                                   if not isinstance(decorator, ast.Name) or decorator.id != 'override']

    def fail(message):
        raise AssertionError(message)
    env = {
        'json': json, 'os': os, 'sqlite3': sqlite3, 'threading': threading,
        'LOG': SimpleNamespace(error=fail, info=lambda *_args: None),
        'StoreCapability': SimpleNamespace(SEGMENT='segment'),
        'INSERT_BATCH_SIZE': 500, 'DEFAULT_KB_ID': 'default',
        'RAG_DOC_ID': 'docid', 'RAG_KB_ID': 'kb_id', 'RAG_DOC_FILE_NAME': 'file_name',
    }
    exec(compile(ast.fix_missing_locations(ast.Module(body=[cls], type_ignores=[])), str(source), 'exec'), env)
    store = env['SQLiteStore'](str(filename))
    store.connect()
    return store


def create(repo, family, filename, action=None):
    filename.parent.mkdir(parents=True, exist_ok=True)
    if family == 'segments':
        store = segment_store(repo, filename)
        if action:
            operation, group = action.split(':', 1)
            if operation == 'write':
                assert store.upsert(group, [{'uid': 'fixture-' + group, 'doc_id': 'fixture-document',
                                             'group': group, 'content': 'synthetic fixture text'}])
            elif operation == 'drop':
                assert store.delete(group)
            else:
                raise ValueError(operation)
        store._open_conn().close()
    elif family == 'auth':
        os.environ['LAZYMIND_DATABASE_URL'] = 'sqlite:///' + str(filename)
        sys.path.insert(0, str(repo / 'backend/auth-service'))
        from alembic import command
        from alembic.config import Config
        cfg = Config(str(repo / 'backend/auth-service/alembic.ini'))
        cfg.set_main_option('script_location', str(repo / 'backend/auth-service/alembic'))
        command.upgrade(cfg, 'head')
    elif family == 'auth-state':
        os.environ['LAZYMIND_STATE_BACKEND'] = 'sqlite'
        sys.path.insert(0, str(repo / 'backend/auth-service'))
        from core.state_store import SQLiteStateStore
        SQLiteStateStore(str(filename))._conn.close()
    elif family == 'channel':
        sys.path.insert(0, str(repo / 'backend/channel-gateway'))
        from channel_gateway.common.infrastructure.sqlite import SQLiteGatewayStore
        SQLiteGatewayStore('sqlite://' + str(filename)).initialize()
    elif family == 'lazyllm':
        os.environ['LAZYLLM_HOME'] = str(filename.parent / 'isolated-lazyllm-home')
        sys.path.insert(0, str(repo / 'algorithm/lazyllm'))
        from lazyllm.tools.rag.doc_service.parser_client import ParserClient
        from lazyllm.tools.rag.doc_service.doc_manager import DocManager
        # Only the parser transport is stubbed; schema/migration/index owners run.
        with patch.object(ParserClient, 'health', lambda _self: {'ready': True}):
            manager = DocManager(
                db_config={'db_type': 'sqlite', 'user': '', 'password': '', 'host': '', 'port': 0,
                           'db_name': str(filename)},
                parser_url='http://127.0.0.1:1',
            )
            manager.close()
    else:
        raise ValueError(family)


def snapshot(filename, family):
    with closing(sqlite3.connect('file:' + str(filename) + '?mode=ro', uri=True)) as db, db:
        shape = {
            'present': True,
            'applicationId': db.execute('PRAGMA application_id').fetchone()[0],
            'userVersion': db.execute('PRAGMA user_version').fetchone()[0],
            'schema': [
                dict(zip(('type', 'name', 'table', 'sql'), row))
                for row in db.execute(
                    'SELECT type,name,tbl_name,sql FROM sqlite_schema '
                    "WHERE sql IS NOT NULL AND name NOT GLOB 'sqlite_*' "
                    'ORDER BY type COLLATE BINARY,name COLLATE BINARY'
                )
            ],
        }
        if family == 'core':
            shape['coreVersion'] = db.execute('SELECT version FROM schema_migrations').fetchone()[0]
            shape['coreHistory'] = [
                {'version': version, 'name': name}
                for version, name in db.execute('SELECT version,name FROM schema_migration_history ORDER BY version')
            ]
        return shape


def check_fixture(repo, family):
    with tempfile.TemporaryDirectory(prefix='lazymind-format-owner-') as directory:
        filename = Path(directory) / (family + '.db')
        subprocess.run(
            [sys.executable, '-I', '-B', str(Path(__file__).resolve()), str(repo), family, str(filename)],
            check=True, capture_output=True, text=True, timeout=60,
        )
        expected = json.loads((Path(__file__).parent / (family + '.json')).read_text())['shape']
        actual = snapshot(filename, family)
        assert actual == expected, (
            family + ' schema owner changed; review the supported data-format recipe and fixture'
        )


if __name__ == '__main__':
    create(Path(sys.argv[1]).resolve(), sys.argv[2], Path(sys.argv[3]).resolve(),
           sys.argv[4] if len(sys.argv) > 4 else None)
