"""Pin Windows directory components while a folder page is inspected."""
import ctypes
import os
from contextlib import contextmanager
from ctypes import wintypes
from pathlib import Path

from lazyllm.tools.agent import ToolExecutionError


class _FileInformation(ctypes.Structure):
    _fields_ = [('attributes', wintypes.DWORD),
                ('created', wintypes.FILETIME), ('accessed', wintypes.FILETIME),
                ('modified', wintypes.FILETIME), ('volume', wintypes.DWORD),
                ('size_high', wintypes.DWORD), ('size_low', wintypes.DWORD),
                ('links', wintypes.DWORD), ('index_high', wintypes.DWORD), ('index_low', wintypes.DWORD)]


@contextmanager
def pinned_directory(root, relative):
    """Do not follow reparse points or allow ancestors to be renamed while listing."""
    parts = relative.split('/') if relative else []
    if any(part in ('', '.', '..') or '\\' in part or ':' in part for part in parts):
        raise ToolExecutionError('path_invalid')
    target = Path(root).joinpath(*parts)
    kernel = ctypes.WinDLL('kernel32', use_last_error=True)
    create = kernel.CreateFileW
    create.argtypes = [wintypes.LPCWSTR, wintypes.DWORD, wintypes.DWORD, ctypes.c_void_p,
                       wintypes.DWORD, wintypes.DWORD, wintypes.HANDLE]
    create.restype = wintypes.HANDLE
    inspect = kernel.GetFileInformationByHandle
    inspect.argtypes = [wintypes.HANDLE, ctypes.POINTER(_FileInformation)]
    inspect.restype = wintypes.BOOL
    close = kernel.CloseHandle
    close.argtypes = [wintypes.HANDLE]
    close.restype = wintypes.BOOL
    handles = []
    try:
        # READ sharing only also prevents changing a pinned directory into a
        # reparse point. Child creation remains detectable by the page mtime check.
        for path in [*reversed(target.parents), target]:
            access = 0x81 if path == target else 0x80  # List only the requested directory.
            handle = create(str(path), access, 0x1, None, 3, 0x02000000 | 0x00200000, None)
            if handle == ctypes.c_void_p(-1).value:
                raise ctypes.WinError(ctypes.get_last_error())
            handles.append(handle)
            info = _FileInformation()
            if not inspect(handle, ctypes.byref(info)):
                raise ctypes.WinError(ctypes.get_last_error())
            if not info.attributes & 0x10 or info.attributes & 0x400:
                raise ToolExecutionError('path_invalid: directory is a reparse point or not a directory')
        yield os.fspath(target)
    finally:
        for handle in reversed(handles):
            close(handle)
