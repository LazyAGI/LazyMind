'''Authenticate TCP clients and proxy them to the local Codex daemon.'''

from __future__ import annotations

import argparse
import hmac
import os
import secrets
import socket
import subprocess
import threading
from pathlib import Path
from typing import BinaryIO


def _authenticate(client: socket.socket, token: str) -> bool:
    payload = bytearray()
    while len(payload) <= 512:
        chunk = client.recv(1)
        if not chunk:
            return False
        if chunk == b'\n':
            break
        payload.extend(chunk)
    else:
        return False
    expected = f'AUTH {token}'.encode()
    return hmac.compare_digest(bytes(payload), expected)


def _copy_to_proxy(source: socket.socket, target: BinaryIO) -> None:
    try:
        while True:
            data = source.recv(64 * 1024)
            if not data:
                return
            target.write(data)
            target.flush()
    except OSError:
        return
    finally:
        try:
            target.close()
        except OSError:
            pass


def _copy_from_proxy(source: BinaryIO, target: socket.socket) -> None:
    try:
        while True:
            read = getattr(source, 'read1', source.read)
            data = read(64 * 1024)
            if not data:
                return
            target.sendall(data)
    except OSError:
        return
    finally:
        try:
            target.shutdown(socket.SHUT_WR)
        except OSError:
            pass


def _serve(
    client: socket.socket,
    codex_bin: str,
    socket_path: str,
    token: str,
) -> None:
    with client:
        try:
            client.settimeout(5)
            if not _authenticate(client, token):
                print('Codex daemon bridge rejected an unauthenticated client', flush=True)
                return
            client.settimeout(None)
            proxy = subprocess.Popen(
                [codex_bin, 'app-server', 'proxy', '--sock', socket_path],
                stdin=subprocess.PIPE,
                stdout=subprocess.PIPE,
            )
        except (OSError, subprocess.SubprocessError):
            print('Codex daemon bridge could not start the official proxy', flush=True)
            return
        assert proxy.stdin is not None
        assert proxy.stdout is not None
        upstream = threading.Thread(
            target=_copy_to_proxy,
            args=(client, proxy.stdin),
            daemon=True,
        )
        upstream.start()
        try:
            _copy_from_proxy(proxy.stdout, client)
        finally:
            if proxy.poll() is None:
                proxy.terminate()
            try:
                proxy.wait(timeout=2)
            except subprocess.TimeoutExpired:
                proxy.kill()
                proxy.wait()
            if proxy.returncode:
                print(
                    f'Codex daemon proxy exited with status {proxy.returncode}',
                    flush=True,
                )


def _load_token(token_file: Path) -> str:
    token_file.parent.mkdir(parents=True, exist_ok=True)
    if not token_file.exists():
        descriptor = os.open(
            token_file,
            os.O_WRONLY | os.O_CREAT | os.O_EXCL,
            0o600,
        )
        with os.fdopen(descriptor, 'w', encoding='utf-8') as handle:
            handle.write(secrets.token_hex(32))
    token = token_file.read_text(encoding='utf-8').strip()
    has_whitespace = any(character.isspace() for character in token)
    if not 32 <= len(token) <= 256 or has_whitespace:
        raise SystemExit(f'Invalid Codex bridge token: {token_file}')
    return token


def main() -> None:
    parser = argparse.ArgumentParser(
        description='Authenticate TCP and use the official Codex daemon proxy.',
    )
    parser.add_argument('--host', default='127.0.0.1')
    parser.add_argument('--port', type=int, default=14501)
    parser.add_argument('--codex-bin', required=True)
    parser.add_argument(
        '--socket',
        default='~/.codex/app-server-control/app-server-control.sock',
    )
    parser.add_argument('--token-file', required=True)
    parser.add_argument('--pid-file', default='')
    args = parser.parse_args()

    codex_bin = os.path.abspath(os.path.expanduser(args.codex_bin))
    socket_path = os.path.abspath(os.path.expanduser(args.socket))
    if not os.path.isfile(codex_bin) or not os.access(codex_bin, os.X_OK):
        raise SystemExit(f'Codex executable does not exist: {codex_bin}')
    if not os.path.exists(socket_path):
        raise SystemExit(f'Codex daemon socket does not exist: {socket_path}')
    token = _load_token(Path(args.token_file).expanduser().resolve())
    pid_file = Path(args.pid_file).resolve() if args.pid_file else None
    if pid_file is not None:
        pid_file.parent.mkdir(parents=True, exist_ok=True)
        pid_file.write_text(str(os.getpid()), encoding='utf-8')

    listener = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    listener.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    try:
        listener.bind((args.host, args.port))
        listener.listen()
        print(
            f'Codex daemon bridge listening on {args.host}:{args.port}',
            flush=True,
        )
        while True:
            client, _ = listener.accept()
            threading.Thread(
                target=_serve,
                args=(client, codex_bin, socket_path, token),
                daemon=True,
            ).start()
    finally:
        listener.close()
        if (
            pid_file is not None
            and pid_file.exists()
            and pid_file.read_text(encoding='utf-8').strip() == str(os.getpid())
        ):
            pid_file.unlink()


if __name__ == '__main__':
    main()
