"""Windows Desktop process policy loaded automatically by Python.

The packaged algorithm services use several third-party launchers that create
relay processes with ``shell=True``. Windows otherwise allocates a console for
each of those descendants because Electron itself has no console to inherit.
"""

from __future__ import annotations

import copy
import os
import subprocess
from typing import Any


_ENABLE_ENV = 'LAZYMIND_WINDOWS_HIDE_SUBPROCESS_WINDOWS'
_PATCH_RELAY_ENV = 'LAZYMIND_WINDOWS_PATCH_LAZYLLM_RELAY'


def _enabled() -> bool:
    return os.name == 'nt' and os.environ.get(_ENABLE_ENV, '').strip().lower() in {
        '1',
        'true',
        'yes',
        'on',
    }


def _hidden_popen_kwargs(kwargs: dict[str, Any]) -> dict[str, Any]:
    updated = dict(kwargs)
    create_no_window = getattr(subprocess, 'CREATE_NO_WINDOW', 0x08000000)
    create_new_console = getattr(subprocess, 'CREATE_NEW_CONSOLE', 0x00000010)
    updated['creationflags'] = (
        int(updated.get('creationflags', 0)) & ~create_new_console
    ) | create_no_window

    startupinfo = updated.get('startupinfo')
    startupinfo = (
        copy.copy(startupinfo)
        if startupinfo is not None
        else subprocess.STARTUPINFO()
    )
    startupinfo.dwFlags |= subprocess.STARTF_USESHOWWINDOW
    startupinfo.wShowWindow = subprocess.SW_HIDE
    updated['startupinfo'] = startupinfo
    return updated


def _install() -> None:
    current = subprocess.Popen
    if getattr(current, '_lazymind_hides_windows', False):
        return

    class HiddenPopen(current):  # type: ignore[misc, valid-type]
        _lazymind_hides_windows = True

        def __init__(self, *args: Any, **kwargs: Any) -> None:
            super().__init__(*args, **_hidden_popen_kwargs(kwargs))

    subprocess.Popen = HiddenPopen  # type: ignore[assignment]


if _enabled():
    _install()
    if os.environ.get(_PATCH_RELAY_ENV, '').strip().lower() in {
        '1',
        'true',
        'yes',
        'on',
    }:
        from lazymind.windows_relay import enable_windows_relay_payload_files

        enable_windows_relay_payload_files()
