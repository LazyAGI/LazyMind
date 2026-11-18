"""Capture the complete pinned RAG service schema without starting a server."""
from pathlib import Path
import runpy
import sys
from unittest.mock import patch


BASE = runpy.run_path(str(Path(__file__).with_name('producer.py')))


def create(repo, filename):
    BASE['create'](repo, 'lazyllm', filename)
    from lazyllm.tools.rag.parsing_service.server import DocumentProcessor

    processor = DocumentProcessor._Impl(db_config={
        'db_type': 'sqlite', 'user': '', 'password': '', 'host': '', 'port': 0,
        'db_name': str(filename),
    }, num_workers=0)
    # Only background polling is suppressed. The real processor, SqlManager,
    # queue constructors and their schema initialization execute unchanged.
    with patch.object(DocumentProcessor._Impl, 'process_finished_task', lambda _self: None):
        processor._lazy_init()
    processor._post_func_thread.join(timeout=5)
    assert not processor._post_func_thread.is_alive()
    for manager in (processor._db_manager, processor._waiting_task_queue._sql_manager,
                    processor._finished_task_queue._sql_manager):
        manager.engine.dispose()


if __name__ == '__main__':
    create(Path(sys.argv[1]).resolve(), Path(sys.argv[2]).resolve())
