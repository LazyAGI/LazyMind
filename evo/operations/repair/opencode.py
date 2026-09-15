from __future__ import annotations

import json
import os
import select
import signal
import subprocess
import time
from contextlib import suppress
from pathlib import Path
from typing import Any, Callable, NamedTuple

PERMISSIONS = {
    **dict.fromkeys(('read', 'grep', 'glob', 'list', 'edit', 'write'), 'allow'),
    **dict.fromkeys(('bash', 'question', 'plan_enter', 'plan_exit', 'todowrite', 'task'), 'deny'),
}
OPENCODE_FIELDS = {
    'model',
    'provider',
    'provider_model',
    'npm',
    'base_url',
    'api_key',
    'skip_auth',
}
TRACE_BY_TOOL = {
    'glob': 'opencode.tool_use.search',
    'grep': 'opencode.tool_use.search',
    'list': 'opencode.tool_use.search',
    'read': 'opencode.tool_use.read_file',
    'edit': 'opencode.tool_use.edit_file',
    'write': 'opencode.tool_use.write_file',
    'bash': 'opencode.tool_use.run_command',
}
TRACE_BY_TYPE = {
    'setup': 'opencode.setup',
    'process_start': 'opencode.process_start',
    'process_exit': 'opencode.process_exit',
    'error': 'opencode.error',
    'timeout': 'opencode.error',
    'process_failed': 'opencode.error',
    'configuration_error': 'opencode.error',
    'prompt_write_failed': 'opencode.error',
    'process_start_failed': 'opencode.error',
}
PATH_KEYS = {'file', 'path', 'filepath', 'filePath'}
MAX_LOG_BYTES = 2 * 1024 * 1024
ENV_PASSTHROUGH = (
    'PATH', 'SHELL', 'USER', 'LANG', 'LC_ALL',
    'SSL_CERT_FILE', 'SSL_CERT_DIR', 'REQUESTS_CA_BUNDLE',
    'HTTP_PROXY', 'HTTPS_PROXY', 'NO_PROXY',
    'http_proxy', 'https_proxy', 'no_proxy',
)


class OpenCodeRunResult(NamedTuple):
    returncode: int
    session_id: str
    last_error: dict[str, Any] | None
    finish_reason: str


def run_opencode_streaming(
    *,
    workdir: str,
    prompt: str,
    artifact_dir: Path,
    session_id: str = '',
    config: dict[str, str] | None = None,
    timeout_s: int = 900,
    trace: Any | None = None,
    attempt: int | None = None,
) -> OpenCodeRunResult:
    started = time.time()
    settings, secrets = _opencode_settings(config or {}), _secrets(config or {})
    artifact_dir.mkdir(parents=True, exist_ok=True)
    prompt_path = artifact_dir / 'opencode_prompt.json'
    stdout_path = artifact_dir / 'stdout.log'
    events_path = artifact_dir / 'events.jsonl'
    config_path: Path | None = None

    def result(returncode: int, session: str, error: dict[str, Any] | None,
               finish_reason: str = '') -> OpenCodeRunResult:
        return OpenCodeRunResult(returncode, session, error, finish_reason)

    try:
        stdout_log = stdout_path.open('w', encoding='utf-8')
        events_log = events_path.open('w', encoding='utf-8')
    except Exception as exc:
        return result(1, session_id, {'type': 'prompt_write_failed', 'message': str(exc)})

    with stdout_log, events_log:
        stdout_tail = ''
        stdout_bytes = events_bytes = 0
        artifact_write_error: dict[str, Any] | None = None

        def remember_artifact_error(exc: OSError) -> None:
            nonlocal artifact_write_error
            if artifact_write_error is None:
                artifact_write_error = {
                    'type': 'artifact_write_failed',
                    'message': f'{type(exc).__name__}: {exc}',
                }

        def record(event: dict[str, Any]) -> dict[str, Any]:
            nonlocal events_bytes
            clean = _clean(event, secrets)
            try:
                events_bytes = _write_bounded(
                    events_log, json.dumps(clean, ensure_ascii=False) + '\n',
                    events_bytes, whole_line=True,
                )
            except OSError as exc:
                remember_artifact_error(exc)
            if trace is not None:
                _emit_trace(trace, attempt, clean)
            return clean

        def write_stdout(line: str) -> None:
            nonlocal stdout_bytes, stdout_tail
            clean = _clean(line, secrets)
            try:
                stdout_bytes = _write_bounded(stdout_log, clean, stdout_bytes)
            except OSError as exc:
                remember_artifact_error(exc)
            stdout_tail = (stdout_tail + clean)[-1000:]

        def fail(kind: str, message: object) -> OpenCodeRunResult:
            return result(1, session_id, record({'type': kind, 'message': str(message)}))

        if missing := _missing_config(settings):
            return fail('configuration_error', f'missing opencode config fields: {", ".join(missing)}')
        try:
            root = Path(workdir).resolve()
            config_path = root / 'opencode.json'
            _write_private_text(prompt_path, prompt)
            _write_private_text(config_path, json.dumps(_opencode_json(settings), ensure_ascii=False))
        except Exception as exc:
            if config_path is not None:
                with suppress(OSError):
                    config_path.unlink()
            return fail('prompt_write_failed', exc)

        prompt_arg = f'Read {prompt_path.as_posix()} first, then follow the JSON task card exactly.'
        record({'type': 'setup', 'status': 'completed', 'message': f'workdir={root}'})
        record({'type': 'process_start', 'status': 'running', 'message': 'starting opencode'})
        if artifact_write_error is not None:
            return result(1, session_id, artifact_write_error)
        try:
            proc = subprocess.Popen(
                _cmd(prompt_arg, session_id, settings),
                stdout=subprocess.PIPE,
                stderr=subprocess.STDOUT,
                text=True,
                bufsize=1,
                cwd=str(root),
                env=_process_env(artifact_dir.parent / '_runtime'),
                start_new_session=True,
            )
        except Exception as exc:
            if config_path is not None:
                with suppress(OSError):
                    config_path.unlink()
            return fail('process_start_failed', exc)

        session, error, finish_reason = session_id, None, ''
        try:
            while proc.poll() is None:
                now = time.time()
                if now - started > timeout_s:
                    error = record({'type': 'timeout', 'message': f'opencode timed out after {timeout_s}s'})
                    _terminate(proc)
                    break
                ready, _, _ = select.select([proc.stdout], [], [], 0.05) if proc.stdout else ([], [], [])
                if not ready:
                    continue
                session, error, finish_reason = _read_line(
                    ready[0].readline(), write_stdout, record,
                    session, error, finish_reason, secrets,
                )
                if artifact_write_error is not None:
                    error = artifact_write_error
                    _terminate(proc)
                    break
            if proc.stdout:
                for line in proc.stdout:
                    session, error, finish_reason = _read_line(
                        line, write_stdout, record,
                        session, error, finish_reason, secrets,
                    )
            returncode = proc.wait()
            record({'type': 'process_exit', 'status': 'completed' if returncode == 0 else 'failed',
                    'message': f'opencode exited with code {returncode}', 'returncode': returncode})
            if artifact_write_error is not None:
                error = artifact_write_error
            if returncode and not error:
                error = record({'type': 'process_failed', 'message': stdout_tail})
        finally:
            if config_path is not None:
                with suppress(OSError):
                    config_path.unlink()
        return result(returncode, session, error, finish_reason)


def _read_line(line: str, write_stdout: Callable[[str], None], record: Callable[[dict[str, Any]], dict[str, Any]],
               session: str, error: dict[str, Any] | None, finish_reason: str,
               secrets: list[str]) -> tuple[str, dict[str, Any] | None, str]:
    if not line:
        return session, error, finish_reason
    write_stdout(line)
    try:
        event = _clean(json.loads(line), secrets)
    except json.JSONDecodeError:
        text = _clean(line.strip(), secrets)
        if text:
            record({'type': 'stdout', 'status': 'running', 'message': str(text)[:300]})
        return session, error, finish_reason
    if isinstance(event, dict):
        recorded = record(event)
        part = event.get('part') if isinstance(event.get('part'), dict) else {}
        if event.get('type') == 'step_finish':
            finish_reason = str(part.get('reason') or event.get('reason') or '').strip()
        return (
            session or str(event.get('sessionID') or ''),
            recorded if event.get('type') == 'error' else error,
            finish_reason,
        )
    return session, error, finish_reason


def _cmd(prompt: str, session: str, settings: dict[str, str]) -> list[str]:
    binary = os.getenv('LAZYMIND_EVO_CODE_BINARY') or 'opencode'
    args = [binary, 'run', '--format', 'json']
    if settings.get('model'):
        args += ['--model', settings['model']]
    if session:
        args += ['--session', session]
    return [*args, prompt]


def _opencode_json(settings: dict[str, str]) -> dict[str, Any]:
    provider, model = settings.get('provider', ''), settings.get('provider_model', '')
    npm = settings.get('npm', '')
    base_url, api_key = settings.get('base_url', ''), settings.get('api_key', '')
    config: dict[str, Any] = {
        '$schema': 'https://opencode.ai/config.json',
        'permission': PERMISSIONS,
        # Repair owns rollback through its managed Git workspace. OpenCode snapshots
        # duplicate multi-gigabyte repositories and can stall every agent turn.
        'snapshot': False,
    }
    if provider and model and npm and base_url:
        options = {'baseURL': base_url}
        if api_key:
            options['apiKey'] = api_key
        config['provider'] = {provider: {
            'npm': npm,
            'options': options,
            'models': {model: {'name': model}},
        }}
    return config


def _compact(event: dict[str, Any]) -> dict[str, Any]:
    part = event.get('part') if isinstance(event.get('part'), dict) else {}
    call = event.get('call') if isinstance(event.get('call'), dict) else {}
    state = part.get('state') if isinstance(part.get('state'), dict) else {}
    tool_input = state.get('input') if isinstance(state.get('input'), dict) else {}
    fields = list(_walk(event))
    paths = [value for key, value in fields if key in PATH_KEYS and isinstance(value, str)]
    for key in ('changed_files', 'files'):
        extra = event.get(key)
        paths += [extra] if isinstance(extra, str) else [path for path in (extra or []) if isinstance(path, str)]
    raw_type = str(event.get('type') or 'unknown')
    tool = str(event.get('tool') or part.get('tool') or call.get('tool') or '')
    message = str(
        part.get('text') or event.get('text') or event.get('message')
        or event.get('error') or state.get('error') or part.get('title') or ''
    ).strip()
    command = str(tool_input.get('command') or event.get('command') or event.get('cmd') or '')
    status = str(event.get('status') or state.get('status') or event.get('state') or '')
    return {
        'event_type': raw_type,
        'tool': tool,
        'execution_type': 'tool_use' if tool else (
            'code' if raw_type in {'text', 'stdout'} and 'diff --git' in message else
            'message' if raw_type in {'text', 'stdout'} else raw_type
        ),
        'summary': message[:500],
        'file_paths': sorted(set(paths)),
        'command': command,
        'status': 'failed' if status == 'error' else status,
        'returncode': event.get('returncode'),
    }


def _emit_trace(trace: Any, attempt: int | None, event: dict[str, Any]) -> None:
    compact = _compact(event)
    raw_type, tool = compact['event_type'], compact['tool']
    if raw_type in {'step_start', 'step_finish'}:
        return
    event_type = TRACE_BY_TOOL.get(tool) or TRACE_BY_TYPE.get(raw_type)
    if not event_type and raw_type in {'text', 'stdout'}:
        event_type = 'opencode.code' if 'diff --git' in compact['summary'] else 'opencode.message'
    event_type = event_type or 'opencode.message'
    trace.emit(
        event_type,
        status='failed' if event_type == 'opencode.error' else compact['status'] or 'running',
        source='opencode',
        attempt=attempt,
        message=compact['summary'] or compact['command'] or raw_type,
        payload={
            'execution_type': compact['execution_type'],
            'tool': tool,
            'paths': compact['file_paths'],
            'command': _command_label(compact['command']),
            'returncode': compact.get('returncode'),
        },
    )


def _command_label(command: object) -> str:
    return ' '.join(str(command or '').split()[:8])[:200]


def _walk(value: Any):
    if isinstance(value, dict):
        for key, child in value.items():
            yield str(key), child
            yield from _walk(child)
    elif isinstance(value, list):
        for child in value:
            yield from _walk(child)


def _opencode_settings(raw: dict[str, str]) -> dict[str, str]:
    return {
        key: str(value).strip()
        for key, value in raw.items()
        if key in OPENCODE_FIELDS and str(value).strip()
    }


def _missing_config(settings: dict[str, str]) -> list[str]:
    required = ['model', 'provider', 'provider_model', 'npm', 'base_url']
    missing = [key for key in required if not settings.get(key)]
    if not settings.get('api_key') and settings.get('skip_auth') != 'true':
        missing.append('api_key')
    return missing


def _process_env(runtime_root: Path) -> dict[str, str]:
    runtime_root.mkdir(parents=True, exist_ok=True)
    directories = {
        'HOME': runtime_root / 'home',
        'TMPDIR': runtime_root / 'tmp',
        'XDG_CONFIG_HOME': runtime_root / 'config',
        'XDG_CACHE_HOME': runtime_root / 'cache',
        'XDG_DATA_HOME': runtime_root / 'data',
        'XDG_STATE_HOME': runtime_root / 'state',
        'OPENCODE_CONFIG_DIR': runtime_root / 'config' / 'opencode',
    }
    for path in directories.values():
        path.mkdir(parents=True, exist_ok=True)
    env = {key: value for key in ENV_PASSTHROUGH if (value := os.environ.get(key))}
    env.update({key: str(path) for key, path in directories.items()})
    return env


def _write_bounded(handle: Any, text: str, written: int, *, whole_line: bool = False) -> int:
    remaining = MAX_LOG_BYTES - written
    if remaining <= 0:
        return written
    encoded = text.encode('utf-8')
    if whole_line and len(encoded) > remaining:
        return written
    chunk = encoded[:remaining].decode('utf-8', 'ignore')
    handle.write(chunk)
    handle.flush()
    return written + len(chunk.encode('utf-8'))


def _write_private_text(path: Path, text: str) -> None:
    flags = os.O_WRONLY | os.O_CREAT | os.O_TRUNC | getattr(os, 'O_NOFOLLOW', 0)
    descriptor = os.open(path, flags, 0o600)
    os.fchmod(descriptor, 0o600)
    with os.fdopen(descriptor, 'w', encoding='utf-8') as handle:
        handle.write(text)


def _terminate(proc: subprocess.Popen, grace_s: float = 5.0) -> None:
    if proc.poll() is not None:
        return
    for sig, stop in ((signal.SIGTERM, proc.terminate), (signal.SIGKILL, proc.kill)):
        try:
            os.killpg(os.getpgid(proc.pid), sig)
        except Exception:
            stop()
        try:
            proc.wait(timeout=grace_s)
            return
        except subprocess.TimeoutExpired:
            pass


def _clean(value: Any, secrets: list[str]) -> Any:
    if isinstance(value, str):
        for secret in secrets:
            value = value.replace(secret, '<redacted>')
        return value
    if isinstance(value, list):
        return [_clean(item, secrets) for item in value]
    if isinstance(value, dict):
        return {key: _clean(item, secrets) for key, item in value.items()}
    return value


def _secrets(env: dict[str, str]) -> list[str]:
    return [
        str(value)
        for key, value in env.items()
        if value and any(token in key.lower() for token in ('key', 'token', 'secret'))
    ]
