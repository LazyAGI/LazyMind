"""Install local-runtime compatibility hooks in every Python subprocess.

Python imports ``sitecustomize`` automatically during interpreter startup when
the module is available on ``PYTHONPATH``.  The local runtime puts this
directory on ``PYTHONPATH``, including for subprocesses launched by LazyLLM.
"""

import os
import sys


def _is_resource_tracker() -> bool:
    arguments = getattr(sys, 'orig_argv', ())
    for index, argument in enumerate(arguments[:-1]):
        if argument == '-c':
            return arguments[index + 1].lstrip().startswith(
                'from multiprocessing.resource_tracker import main;'
            )
    return False


def _uses_sqlite_proxy() -> bool:
    database_values = (
        os.getenv('LAZYMIND_DATABASE_URL', ''),
        os.getenv('LAZYMIND_CORE_DATABASE_URL', ''),
        os.getenv('LAZYMIND_SEGMENT_STORE_URI_OR_PATH', ''),
    )
    return any(value.strip().startswith('sqliteproxy://') for value in database_values)


# Loading LazyLLM in this stdlib helper can create another resource tracker
# before its own main() starts, recursively spawning processes during shutdown.
if _uses_sqlite_proxy() and not _is_resource_tracker():
    from lazymind.common.database.sqlite_proxy import install_lazyllm_sqlite_proxy

    install_lazyllm_sqlite_proxy()
