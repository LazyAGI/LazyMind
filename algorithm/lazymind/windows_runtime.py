"""Explicit Windows Desktop bootstrap for LazyMind algorithm services."""

from __future__ import annotations

import copy
import os
import runpy
import subprocess
import sys
from typing import Any, Sequence


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


def install_hidden_subprocess_policy() -> None:
    """Hide descendants without relying on Python's implicit site hooks."""
    if os.name != 'nt':
        return
    current = subprocess.Popen
    if getattr(current, '_lazymind_hides_windows', False):
        return

    class HiddenPopen(current):  # type: ignore[misc, valid-type]
        _lazymind_hides_windows = True

        def __init__(self, *args: Any, **kwargs: Any) -> None:
            super().__init__(*args, **_hidden_popen_kwargs(kwargs))

    subprocess.Popen = HiddenPopen  # type: ignore[assignment]


def dispatch(argv: Sequence[str]) -> None:
    """Run an original ``python -m module`` or script command in this process."""
    args = list(argv)
    if args[:1] == ['--']:
        args = args[1:]
    if not args:
        raise SystemExit('missing Python module or script')

    if args[0] == '-m':
        if len(args) < 2:
            raise SystemExit('missing module name after -m')
        module, module_args = args[1], args[2:]
        sys.argv = [module, *module_args]
        runpy.run_module(module, run_name='__main__', alter_sys=True)
        return

    script, script_args = args[0], args[1:]
    sys.argv = [script, *script_args]
    runpy.run_path(script, run_name='__main__')


def main() -> None:
    install_hidden_subprocess_policy()
    dispatch(sys.argv[1:])


if __name__ == '__main__':
    main()
