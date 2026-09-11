import os
import subprocess

import pytest

from lazymind import windows_runtime


@pytest.mark.skipif(os.name != 'nt', reason='Windows-only subprocess policy')
def test_hidden_popen_kwargs_remove_new_console() -> None:
    kwargs = windows_runtime._hidden_popen_kwargs(
        {'creationflags': subprocess.CREATE_NEW_CONSOLE}
    )

    assert kwargs['creationflags'] & subprocess.CREATE_NO_WINDOW
    assert not kwargs['creationflags'] & subprocess.CREATE_NEW_CONSOLE
    assert kwargs['startupinfo'].dwFlags & subprocess.STARTF_USESHOWWINDOW
    assert kwargs['startupinfo'].wShowWindow == subprocess.SW_HIDE


@pytest.mark.skipif(os.name != 'nt', reason='Windows-only subprocess policy')
def test_hidden_popen_policy_keeps_popen_as_a_class(monkeypatch) -> None:
    original = subprocess.Popen
    monkeypatch.setattr(subprocess, 'Popen', original)

    windows_runtime.install_hidden_subprocess_policy()

    assert isinstance(subprocess.Popen, type)
    assert issubclass(subprocess.Popen, original)
    assert subprocess.Popen._lazymind_hides_windows is True


def test_dispatch_runs_module_with_original_arguments(monkeypatch) -> None:
    called = {}

    def fake_run_module(module, *, run_name, alter_sys):
        called.update(
            module=module,
            run_name=run_name,
            alter_sys=alter_sys,
            argv=list(windows_runtime.sys.argv),
        )

    monkeypatch.setattr(windows_runtime.runpy, 'run_module', fake_run_module)

    windows_runtime.dispatch(['--', '-m', 'example.module', '--port', '8092'])

    assert called == {
        'module': 'example.module',
        'run_name': '__main__',
        'alter_sys': True,
        'argv': ['example.module', '--port', '8092'],
    }
