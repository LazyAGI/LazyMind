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
    'provider_label',
    'base_url',
    'api_key',
    'skip_auth',
}


class OpenCodeRunResult(NamedTuple):
    returncode: int
    session_id: str
    events: list[dict[str, Any]]
    raw_paths: dict[str, str]
    prompt_arg: str
    last_error: dict[str, Any] | None
    duration_seconds: float
    setup_seconds: float
    first_response_seconds: float | None
    model: str
    provider: str


def run_opencode_streaming(
    *,
    workdir: str,
    prompt: str,
    artifact_dir: Path,
    session_id: str = '',
    config: dict[str, str] | None = None,
    timeout_s: int = 900,
    first_response_timeout_s: int = 300,
    trace: Any | None = None,
    attempt: int | None = None,
) -> OpenCodeRunResult:
    started = time.time()
    stdout: list[str] = []
    events: list[dict[str, Any]] = []
    settings, secrets = _opencode_settings(config or {}), _secrets(config or {})
    config_path: Path | None = None

    def emit(event: dict[str, Any]) -> None:
        if trace is not None:
            _emit_trace(trace, attempt, len(events) - 1, event)

    def fail(kind: str, message: object, prompt_arg: str = '', setup_done: float | None = None) -> OpenCodeRunResult:
        error = _clean({'type': kind, 'message': str(message)}, secrets)
        events.append(error)
        emit(error)
        paths = _write_logs(artifact_dir, stdout, events, secrets)
        return _result(1, session_id, events, paths, prompt_arg, error, started, setup_done or time.time(), None,
                       settings)

    if missing := _missing_config(settings):
        return fail('configuration_error', f'missing opencode config fields: {", ".join(missing)}')
    try:
        root = Path(workdir).resolve()
        artifact_dir.mkdir(parents=True, exist_ok=True)
        prompt_path = artifact_dir / 'opencode_prompt.json'
        config_path = root / 'opencode.json'
        prompt_path.write_text(prompt, encoding='utf-8')
        config_path.write_text(json.dumps(_opencode_json(settings), ensure_ascii=False), encoding='utf-8')
    except Exception as exc:
        if config_path is not None:
            with suppress(OSError):
                config_path.unlink()
        return fail('prompt_write_failed', exc)

    prompt_arg = f'Read {prompt_path.as_posix()} first, then follow the JSON task card exactly.'
    events.append({'type': 'setup', 'status': 'completed', 'message': f'workdir={root}'})
    emit(events[-1])
    setup_done = time.time()
    events.append({'type': 'process_start', 'status': 'running', 'message': 'starting opencode'})
    emit(events[-1])
    try:
        proc = subprocess.Popen(
            _cmd(prompt_arg, session_id, settings),
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            text=True,
            bufsize=1,
            cwd=str(root),
            env=_process_env(),
            start_new_session=True,
        )
    except Exception as exc:
        if config_path is not None:
            with suppress(OSError):
                config_path.unlink()
        return fail('process_start_failed', exc, prompt_arg, setup_done)

    session, error, first, heartbeat = session_id, None, None, setup_done
    try:
        while proc.poll() is None:
            now = time.time()
            if now - started > timeout_s:
                error = {'type': 'timeout', 'message': f'opencode timed out after {timeout_s}s'}
                events.append(error)
                emit(error)
                _terminate(proc)
                break
            ready, _, _ = select.select([proc.stdout], [], [], 0.05) if proc.stdout else ([], [], [])
            if not ready:
                if first is None and now - started >= first_response_timeout_s:
                    error = {
                        'type': 'first_response_timeout',
                        'message': f'opencode produced no model/tool event within {first_response_timeout_s}s',
                    }
                    events.append(error)
                    emit(error)
                    _terminate(proc)
                    break
                if now - heartbeat >= 10:
                    heartbeat = now
                    events.append({'type': 'process_heartbeat', 'status': 'running',
                                   'elapsed_seconds': round(now - started, 1), 'changed_files': _changed(root)})
                    emit(events[-1])
                continue
            session, error, first = _read_line(ready[0].readline(), stdout, events, emit, session, error, first,
                                               started, secrets)
        if proc.stdout:
            for line in proc.stdout:
                session, error, first = _read_line(line, stdout, events, emit, session, error, first, started, secrets)
        returncode = proc.wait()
        events.append({'type': 'process_exit', 'status': 'completed' if returncode == 0 else 'failed',
                       'message': f'opencode exited with code {returncode}', 'returncode': returncode})
        emit(events[-1])
        if returncode and not error:
            error = {'type': 'process_failed', 'message': _clean(''.join(stdout)[-1000:], secrets)}
            events.append(error)
            emit(error)
    finally:
        if config_path is not None:
            with suppress(OSError):
                config_path.unlink()
    paths = _write_logs(artifact_dir, stdout, events, secrets)
    return _result(returncode, session, events, paths, prompt_arg, error, started, setup_done, first, settings)


def _result(returncode: int, session: str, events: list[dict[str, Any]], paths: dict[str, str], prompt_arg: str,
            error: dict[str, Any] | None, started: float, setup_done: float, first: float | None,
            settings: dict[str, str]) -> OpenCodeRunResult:
    return OpenCodeRunResult(
        returncode=returncode,
        session_id=session,
        events=events,
        raw_paths=paths,
        prompt_arg=prompt_arg,
        last_error=error,
        duration_seconds=round(time.time() - started, 3),
        setup_seconds=round(setup_done - started, 3),
        first_response_seconds=first,
        model=settings.get('model', ''),
        provider=settings.get('provider', ''),
    )


def _read_line(line: str, stdout: list[str], events: list[dict[str, Any]],
               emit: Callable[[dict[str, Any]], None], session: str,
               error: dict[str, Any] | None, first: float | None, start: float,
               secrets: list[str]) -> tuple[str, dict[str, Any] | None, float | None]:
    if not line:
        return session, error, first
    stdout.append(_clean(line, secrets))
    try:
        event = _clean(json.loads(line), secrets)
    except json.JSONDecodeError:
        text = _clean(line.strip(), secrets)
        if text:
            events.append({'type': 'stdout', 'status': 'running', 'message': str(text)[:300]})
            emit(events[-1])
        return session, error, first
    if isinstance(event, dict):
        events.append(event)
        emit(event)
        if first is None and (_tool(event) or _message(event) or event.get('type') == 'error'):
            first = round(time.time() - start, 3)
        return session or str(event.get('sessionID') or ''), event if event.get('type') == 'error' else error, first
    return session, error, first


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
    base_url, api_key = settings.get('base_url', ''), settings.get('api_key', '')
    config: dict[str, Any] = {'$schema': 'https://opencode.ai/config.json', 'permission': PERMISSIONS}
    if provider and model and base_url:
        official = base_url.rstrip('/').endswith('api.openai.com/v1')
        npm = '@ai-sdk/openai' if provider == 'openai' and official else '@ai-sdk/openai-compatible'
        model_cfg: dict[str, Any] = {'name': model, 'tool_call': True}
        if not official:
            model_cfg['limit'] = {'context': 32768, 'output': 1024}
        options = {'baseURL': base_url}
        if api_key:
            options['apiKey'] = api_key
        config['provider'] = {provider: {
            'npm': npm,
            'name': settings.get('provider_label') or provider,
            'options': options,
            'models': {model: model_cfg},
        }}
    return config


def _compact(index: int, event: dict[str, Any]) -> dict[str, Any]:
    paths = _paths(event)
    for key in ('changed_files', 'files'):
        paths += [str(path) for path in event.get(key, []) if isinstance(path, str)]
    return {
        'index': index,
        'event_type': str(event.get('type') or 'unknown'),
        'tool': _tool(event),
        'execution_type': _execution_type(event),
        'summary': _message(event)[:500],
        'file_paths': sorted(set(paths)),
        'command': _command(event),
        'status': _status(event),
        'returncode': event.get('returncode'),
    }


def _emit_trace(trace: Any, attempt: int | None, index: int, event: dict[str, Any]) -> None:
    compact = _compact(index, event)
    raw_type, tool = compact['event_type'], compact['tool']
    if raw_type in {'step_start', 'step_finish'}:
        return
    event_type = {
        'glob': 'opencode.tool_use.search',
        'grep': 'opencode.tool_use.search',
        'list': 'opencode.tool_use.search',
        'read': 'opencode.tool_use.read_file',
        'edit': 'opencode.tool_use.edit_file',
        'write': 'opencode.tool_use.edit_file',
        'bash': 'opencode.tool_use.run_command',
    }.get(tool) or {
        'setup': 'opencode.setup',
        'process_start': 'opencode.process_start',
        'process_heartbeat': 'opencode.heartbeat',
        'process_exit': 'opencode.process_exit',
        'error': 'opencode.error',
        'timeout': 'opencode.error',
        'first_response_timeout': 'opencode.error',
        'process_failed': 'opencode.error',
        'configuration_error': 'opencode.error',
        'prompt_write_failed': 'opencode.error',
        'process_start_failed': 'opencode.error',
        'text': 'opencode.code' if 'diff --git' in compact['summary'] else 'opencode.message',
        'stdout': 'opencode.code' if 'diff --git' in compact['summary'] else 'opencode.message',
    }.get(raw_type, 'opencode.message')
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
            'raw_event_ref': index,
            'returncode': compact.get('returncode'),
        },
    )
    if tool in {'edit', 'write'} and _has_diff(event):
        trace.emit(
            'opencode.code',
            status=compact['status'] or 'completed',
            source='opencode',
            attempt=attempt,
            message='code patch produced',
            payload={
                'execution_type': 'code',
                'paths': compact['file_paths'],
                'raw_event_ref': index,
            },
        )


def _execution_type(event: dict[str, Any]) -> str:
    if _tool(event):
        return 'tool_use'
    if event.get('type') in {'text', 'stdout'}:
        return 'code' if 'diff --git' in _message(event) else 'message'
    return str(event.get('type') or 'unknown')


def _tool(event: dict[str, Any]) -> str:
    part = event.get('part') if isinstance(event.get('part'), dict) else {}
    call = event.get('call') if isinstance(event.get('call'), dict) else {}
    return str(event.get('tool') or part.get('tool') or call.get('tool') or '')


def _message(event: dict[str, Any]) -> str:
    part = event.get('part') if isinstance(event.get('part'), dict) else {}
    state = part.get('state') if isinstance(part.get('state'), dict) else {}
    text = (
        part.get('text') or event.get('text') or event.get('message')
        or event.get('error') or state.get('error') or part.get('title') or ''
    )
    return str(text).strip()


def _command(event: dict[str, Any]) -> str:
    part = event.get('part') if isinstance(event.get('part'), dict) else {}
    state = part.get('state') if isinstance(part.get('state'), dict) else {}
    tool_input = state.get('input') if isinstance(state.get('input'), dict) else {}
    if tool_input.get('command'):
        return str(tool_input['command'])
    for key in ('command', 'cmd'):
        if event.get(key):
            return str(event[key])
    return ''


def _command_label(command: object) -> str:
    return ' '.join(str(command or '').split()[:8])[:200]


def _status(event: dict[str, Any]) -> str:
    part = event.get('part') if isinstance(event.get('part'), dict) else {}
    state = part.get('state') if isinstance(part.get('state'), dict) else {}
    status = str(event.get('status') or state.get('status') or event.get('state') or '')
    return 'failed' if status == 'error' else status


def _has_diff(value: Any) -> bool:
    if isinstance(value, dict):
        return any(
            isinstance(child, str) and key in {'diff', 'patch'} and child.strip()
            or _has_diff(child)
            for key, child in value.items()
        )
    if isinstance(value, list):
        return any(_has_diff(child) for child in value)
    return False


def _paths(value: Any) -> list[str]:
    if isinstance(value, dict):
        return [item for key, child in value.items()
                for item in ([child] if key in {'file', 'path', 'filepath', 'filePath'} and isinstance(child, str)
                             else _paths(child))]
    if isinstance(value, list):
        return [item for child in value for item in _paths(child)]
    return []


def _changed(workdir: Path) -> list[str]:
    try:
        result = subprocess.run(['git', '-C', str(workdir), 'diff', '--name-only'],
                                capture_output=True, text=True, timeout=5, check=False)
        return result.stdout.splitlines()
    except Exception:
        return []


def _opencode_settings(raw: dict[str, str]) -> dict[str, str]:
    return {
        key: str(value).strip()
        for key, value in raw.items()
        if key in OPENCODE_FIELDS and str(value).strip()
    }


def _missing_config(settings: dict[str, str]) -> list[str]:
    required = ['model', 'provider', 'provider_model', 'base_url']
    missing = [key for key in required if not settings.get(key)]
    if not settings.get('api_key') and settings.get('skip_auth') != 'true':
        missing.append('api_key')
    return missing


def _process_env() -> dict[str, str]:
    return {key: value for key in ('HOME', 'PATH', 'SHELL', 'USER', 'LANG', 'LC_ALL', 'TMPDIR')
            if (value := os.environ.get(key))}


def _write_logs(root: Path, stdout: list[str], events: list[dict[str, Any]], secrets: list[str]) -> dict[str, str]:
    try:
        root.mkdir(parents=True, exist_ok=True)
        paths = {'prompt': root / 'opencode_prompt.json', 'stdout': root / 'stdout.log',
                 'events_jsonl': root / 'events.jsonl'}
        paths['stdout'].write_text(''.join(stdout), encoding='utf-8')
        paths['events_jsonl'].write_text(
            ''.join(json.dumps(_clean(event, secrets), ensure_ascii=False) + '\n' for event in events),
            encoding='utf-8',
        )
        return {key: str(path) for key, path in paths.items()}
    except Exception:
        return {'prompt': '', 'stdout': '', 'events_jsonl': ''}


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
