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
TRACE_BY_TOOL = {
    'glob': 'opencode.tool_use.search',
    'grep': 'opencode.tool_use.search',
    'list': 'opencode.tool_use.search',
    'read': 'opencode.tool_use.read_file',
    'edit': 'opencode.tool_use.edit_file',
    'write': 'opencode.tool_use.edit_file',
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


class OpenCodeRunResult(NamedTuple):
    returncode: int
    session_id: str
    last_error: dict[str, Any] | None


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

    def result(returncode: int, session: str, error: dict[str, Any] | None) -> OpenCodeRunResult:
        return OpenCodeRunResult(returncode, session, error)

    try:
        stdout_log = stdout_path.open('w', encoding='utf-8')
        events_log = events_path.open('w', encoding='utf-8')
    except Exception as exc:
        return result(1, session_id, {'type': 'prompt_write_failed', 'message': str(exc)})

    with stdout_log, events_log:
        stdout_tail = ''

        def record(event: dict[str, Any]) -> dict[str, Any]:
            clean = _clean(event, secrets)
            events_log.write(json.dumps(clean, ensure_ascii=False) + '\n')
            events_log.flush()
            if trace is not None:
                _emit_trace(trace, attempt, clean)
            return clean

        def write_stdout(line: str) -> None:
            nonlocal stdout_tail
            clean = _clean(line, secrets)
            stdout_log.write(clean)
            stdout_tail = (stdout_tail + clean)[-1000:]

        def fail(kind: str, message: object) -> OpenCodeRunResult:
            return result(1, session_id, record({'type': kind, 'message': str(message)}))

        if missing := _missing_config(settings):
            return fail('configuration_error', f'missing opencode config fields: {", ".join(missing)}')
        try:
            root = Path(workdir).resolve()
            config_path = root / 'opencode.json'
            prompt_path.write_text(prompt, encoding='utf-8')
            config_path.write_text(json.dumps(_opencode_json(settings), ensure_ascii=False), encoding='utf-8')
        except Exception as exc:
            if config_path is not None:
                with suppress(OSError):
                    config_path.unlink()
            return fail('prompt_write_failed', exc)

        prompt_arg = f'Read {prompt_path.as_posix()} first, then follow the JSON task card exactly.'
        record({'type': 'setup', 'status': 'completed', 'message': f'workdir={root}'})
        record({'type': 'process_start', 'status': 'running', 'message': 'starting opencode'})
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
            return fail('process_start_failed', exc)

        session, error = session_id, None
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
                session, error = _read_line(ready[0].readline(), write_stdout, record, session, error, secrets)
            if proc.stdout:
                for line in proc.stdout:
                    session, error = _read_line(line, write_stdout, record, session, error, secrets)
            returncode = proc.wait()
            record({'type': 'process_exit', 'status': 'completed' if returncode == 0 else 'failed',
                    'message': f'opencode exited with code {returncode}', 'returncode': returncode})
            if returncode and not error:
                error = record({'type': 'process_failed', 'message': stdout_tail})
        finally:
            if config_path is not None:
                with suppress(OSError):
                    config_path.unlink()
        return result(returncode, session, error)


def _read_line(line: str, write_stdout: Callable[[str], None], record: Callable[[dict[str, Any]], dict[str, Any]],
               session: str, error: dict[str, Any] | None, secrets: list[str]) -> tuple[str, dict[str, Any] | None]:
    if not line:
        return session, error
    write_stdout(line)
    try:
        event = _clean(json.loads(line), secrets)
    except json.JSONDecodeError:
        text = _clean(line.strip(), secrets)
        if text:
            record({'type': 'stdout', 'status': 'running', 'message': str(text)[:300]})
        return session, error
    if isinstance(event, dict):
        recorded = record(event)
        return session or str(event.get('sessionID') or ''), recorded if event.get('type') == 'error' else error
    return session, error


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
    required = ['model', 'provider', 'provider_model', 'base_url']
    missing = [key for key in required if not settings.get(key)]
    if not settings.get('api_key') and settings.get('skip_auth') != 'true':
        missing.append('api_key')
    return missing


def _process_env() -> dict[str, str]:
    return {key: value for key in ('HOME', 'PATH', 'SHELL', 'USER', 'LANG', 'LC_ALL', 'TMPDIR')
            if (value := os.environ.get(key))}


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
