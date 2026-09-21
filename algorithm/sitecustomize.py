"""Install local-runtime compatibility hooks in application Python processes.

Python imports ``sitecustomize`` automatically during interpreter startup when
the module is available on ``PYTHONPATH``.  The local runtime puts this
directory on ``PYTHONPATH``, including for subprocesses launched by LazyLLM.
CPython's resource tracker is infrastructure, not an application process, and
must remain free of business imports and their exit handlers.
"""

import os
import sys


def _is_resource_tracker() -> bool:
    # At site initialization sys.argv contains only '-c'; orig_argv retains
    # CPython's actual command. This helper must not import application code:
    # LazyLLM's atexit logger cleanup can otherwise spawn another tracker.
    args = getattr(sys, 'orig_argv', ())
    try:
        command = args[args.index('-c') + 1]
    except (ValueError, IndexError):
        return False
    return command.startswith('from multiprocessing.resource_tracker import main;main(')


def _uses_sqlite_proxy() -> bool:
    database_values = (
        os.getenv('LAZYMIND_DATABASE_URL', ''),
        os.getenv('LAZYMIND_CORE_DATABASE_URL', ''),
        os.getenv('LAZYMIND_SEGMENT_STORE_URI_OR_PATH', ''),
    )
    return any(value.strip().startswith('sqliteproxy://') for value in database_values)


if _uses_sqlite_proxy() and not _is_resource_tracker():
    from lazymind.common.database.sqlite_proxy import install_lazyllm_sqlite_proxy

    install_lazyllm_sqlite_proxy()
