from __future__ import annotations
import os
import socket
import subprocess
import time
import uuid
from pathlib import Path
import httpx
from .base import ChatInstance


def _free_port() -> int:
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
        s.bind(('127.0.0.1', 0))
        return s.getsockname()[1]


class SubprocessChatRunner:
    def __init__(
        self,
        *,
        log_dir: Path,
        command: list[str] | None = None,
        health_path: str = '/healthz',
        startup_timeout_s: float = 30.0,
        stop_timeout_s: float = 10.0,
    ) -> None:
        self.log_dir = Path(log_dir)
        self.log_dir.mkdir(parents=True, exist_ok=True)
        self.command = command or ['python', '-m', 'chat.app.chat']
        self.health_path = health_path
        self.startup_timeout_s = startup_timeout_s
        self.stop_timeout_s = stop_timeout_s
        self._procs: dict[str, subprocess.Popen] = {}

    def launch(
        self, *, source_dir: Path, label: str, env: dict | None = None, owner_thread_id: str | None = None
    ) -> ChatInstance:
        child_env = {**os.environ, **(env or {})}
        cwd = Path(child_env.pop('LAZYMIND_EVO_CANDIDATE_CWD', source_dir))
        if cwd.resolve() == Path(source_dir).resolve():
            _ensure_chat_import_alias(Path(source_dir))
        port = _free_port()
        chat_id = f'chat-{label}-{uuid.uuid4().hex[:6]}'
        log_path = self.log_dir / f'{chat_id}.log'
        cmd = [*self.command, '--port', str(port)]
        log_fp = open(log_path, 'ab')
        proc = subprocess.Popen(cmd, cwd=cwd, env=child_env, stdout=log_fp, stderr=log_fp)
        self._procs[chat_id] = proc
        base_url = f'http://127.0.0.1:{port}'
        instance = ChatInstance(
            chat_id=chat_id,
            pid=proc.pid,
            port=port,
            base_url=base_url,
            source_dir=Path(source_dir),
            health_url=f'{base_url}{self.health_path}',
            status='starting',
            owner_thread_id=owner_thread_id,
        )
        if not self._wait_healthy(instance):
            self.stop(chat_id)
            instance.status = 'unhealthy'
            raise RuntimeError(f'chat {chat_id} failed startup; see {log_path}')
        instance.status = 'healthy'
        return instance

    def stop(self, chat_id: str) -> None:
        proc = self._procs.pop(chat_id, None)
        if proc is None or proc.poll() is not None:
            return
        proc.terminate()
        try:
            proc.wait(timeout=self.stop_timeout_s)
        except subprocess.TimeoutExpired:
            proc.kill()
            proc.wait(timeout=2)

    def _wait_healthy(self, instance: ChatInstance) -> bool:
        deadline = time.time() + self.startup_timeout_s
        proc = self._procs.get(instance.chat_id)
        while time.time() < deadline:
            if proc is not None and proc.poll() is not None:
                return False
            try:
                if httpx.get(instance.health_url, timeout=1.0).is_success:
                    return True
            except httpx.HTTPError:
                pass
            time.sleep(0.2)
        return False


def _ensure_chat_import_alias(source_dir: Path) -> None:
    alias = source_dir / 'chat'
    if alias.exists() or not (source_dir / 'app' / 'chat.py').is_file():
        return
    try:
        alias.symlink_to('.', target_is_directory=True)
    except FileExistsError:
        pass
