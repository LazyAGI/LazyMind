"""Install local-runtime compatibility hooks in every Python subprocess.

Python imports ``sitecustomize`` automatically during interpreter startup when
the module is available on ``PYTHONPATH``.  The local runtime puts this
directory on ``PYTHONPATH``, including for subprocesses launched by LazyLLM.
"""

import os


def _uses_sqlite_proxy() -> bool:
    database_values = (
        os.getenv('LAZYMIND_DATABASE_URL', ''),
        os.getenv('LAZYMIND_CORE_DATABASE_URL', ''),
        os.getenv('LAZYMIND_SEGMENT_STORE_URI_OR_PATH', ''),
    )
    return any(value.strip().startswith('sqliteproxy://') for value in database_values)


if _uses_sqlite_proxy():
    from lazymind.common.database.sqlite_proxy import install_lazyllm_sqlite_proxy

    install_lazyllm_sqlite_proxy()
