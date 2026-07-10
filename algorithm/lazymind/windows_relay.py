"""Windows-native LazyLLM relay launch compatibility."""

from __future__ import annotations

import os
import subprocess
import sys
import tempfile
import uuid
from pathlib import Path


def enable_windows_relay_payload_files() -> None:
    """Keep cloudpickle payloads out of Windows' process command line."""
    if os.name != 'nt':
        return

    from lazyllm import LazyLLMCMD, config, dump_obj
    from lazyllm.components.deploy.relay.base import FastapiApp, RelayServer
    from lazyllm.components.deploy.utils import get_log_path
    from lazyllm.components.deploy.base import verify_fastapi_func, verify_ray_func

    if getattr(RelayServer, '_lazymind_windows_payload_patch', False):
        return

    def payload_root() -> Path:
        runtime_root = os.environ.get('LAZYMIND_RUNTIME_ROOT')
        root = Path(runtime_root) / 'tmp' / 'relay-payloads' if runtime_root else Path(tempfile.gettempdir()) / 'lazymind-relay-payloads'
        root.mkdir(parents=True, exist_ok=True)
        return root

    def write_payload(value: str) -> str:
        path = payload_root() / f'{uuid.uuid4().hex}.payload'
        path.write_text(value, encoding='ascii')
        return str(path)

    def windows_cmd(self, func=None):
        FastapiApp.update()
        self._func = dump_obj(func or self._func)

        def impl():
            self._real_port = self._port if self._port else __import__('random').randint(30000, 40000)
            args = [
                sys.executable,
                '-m',
                'lazymind.relay_payload_server',
                f'--open_port={self._real_port}',
                f'--function-file={write_payload(self._func)}',
            ]
            for option, value in (
                ('before_function', self._pre),
                ('after_function', self._post),
                ('defined_pos', dump_obj(self._defined_pos.replace('"', r'\"')) if self._defined_pos else None),
            ):
                if value:
                    args.append(f'--{option}-file={write_payload(value)}')
            if self._pythonpath:
                args.append(f'--pythonpath={self._pythonpath}')
            if self._num_replicas > 1 and config['use_ray']:
                args.append(f'--num_replicas={self._num_replicas}')
            if self._security_key:
                args.append(f'--security_key={self._security_key}')
            command = subprocess.list2cmdline(args)
            if self.temp_folder:
                command += f' 2>&1 | tee "{get_log_path(self.temp_folder)}"'
            return command

        return LazyLLMCMD(
            cmd=impl,
            return_value=self.geturl,
            checkf=verify_ray_func if config['use_ray'] else verify_fastapi_func,
            no_displays=['function', 'before_function', 'after_function', 'security_key', 'defined_pos'],
        )

    RelayServer.cmd = windows_cmd
    RelayServer._lazymind_windows_payload_patch = True
