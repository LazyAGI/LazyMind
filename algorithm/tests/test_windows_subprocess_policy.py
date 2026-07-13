import os
import subprocess

import pytest

import sitecustomize


@pytest.mark.skipif(os.name != 'nt', reason='Windows-only subprocess policy')
def test_hidden_popen_kwargs_suppress_console_windows():
    kwargs = sitecustomize._hidden_popen_kwargs(
        {'creationflags': subprocess.CREATE_NEW_CONSOLE}
    )

    assert kwargs['creationflags'] & subprocess.CREATE_NO_WINDOW
    assert not kwargs['creationflags'] & subprocess.CREATE_NEW_CONSOLE
    assert kwargs['startupinfo'].dwFlags & subprocess.STARTF_USESHOWWINDOW
    assert kwargs['startupinfo'].wShowWindow == subprocess.SW_HIDE


@pytest.mark.skipif(os.name != 'nt', reason='Windows-only subprocess policy')
def test_install_preserves_popen_class_semantics():
    original = subprocess.Popen
    try:
        sitecustomize._install()

        assert isinstance(subprocess.Popen, type)
        assert issubclass(subprocess.Popen, original)
    finally:
        subprocess.Popen = original
