from __future__ import annotations

import json
import os
import select
import shlex
import signal
import shutil
import subprocess
import time
from pathlib import Path
from typing import Any, NamedTuple

PERMISSIONS = {
    **dict.fromkeys(('read', 'grep', 'glob', 'list', 'bash', 'edit', 'external_directory'), 'allow'),
    **dict.fromkeys(('question', 'plan_enter', 'plan_exit', 'todowrite', 'task'), 'deny'),
}
OPENCODE_CONFIG_KEYS = {
    'OPENCODE_MODEL',
    'OPENCODE_PROVIDER',
    'OPENCODE_PROVIDER_MODEL',
    'OPENCODE_PROVIDER_LABEL',
    'OPENCODE_PROVIDER_BASE_URL',
    'OPENCODE_PROVIDER_KEY_ENV',
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


def run_opencode_streaming(*, container: str, workdir: str, prompt: str, artifact_dir: Path, session_id: str = '',
                           env: dict[str, str] | None = None, timeout_s: int = 900,
                           first_response_timeout_s: int = 120, on_event: Any = None,
                           register_cancel: Any = None) -> Any:
    started = time.time()
    stdout: list[str] = []
    events: list[dict[str, Any]] = []
    safe_env, secrets = _opencode_env(env or {}), _secrets(env or {})

    def fail(err_type: str, exc: Any, prompt_arg: str = '', setup: float | None = None) -> OpenCodeRunResult:
        error = _clean_obj({'type': err_type, 'message': str(exc)}, secrets)
        _push(error, events, on_event)
        paths = _safe_write_logs(artifact_dir, stdout, events, secrets)
        return _result(1, session_id, events, paths, prompt_arg, error, started,
                       setup if setup is not None else time.time(), None, safe_env)

    if missing := _missing_config(safe_env):
        return fail('configuration_error', f'missing opencode config fields: {", ".join(missing)}')

    try:
        artifact_dir.mkdir(parents=True, exist_ok=True)
    except Exception as exc:
        return fail('prompt_write_failed', exc)
    host_workdir = Path(workdir) if container else None
    run_workdir = f'/tmp/evo_repair_worktrees/{host_workdir.name}' if host_workdir else workdir
    prompt_path = artifact_dir / 'opencode_prompt.json'
    try:
        prompt_path.write_text(prompt, encoding='utf-8')
    except Exception as exc:
        return fail('prompt_write_failed', exc)
    prompt_arg = _prompt_arg(prompt_path, host_workdir, run_workdir)
    _push({'type': 'setup', 'status': 'running', 'message': 'preparing opencode workspace'}, events, on_event)
    if container and host_workdir:
        try:
            _push({'type': 'setup', 'status': 'running',
                   'message': f'syncing candidate workspace into {container}'}, events, on_event)
            _run(['docker', 'exec', container, 'mkdir', '-p', str(Path(run_workdir).parent)])
            _run(['docker', 'exec', container, 'rm', '-rf', run_workdir])
            _run(['docker', 'cp', str(host_workdir), f'{container}:{run_workdir}'])
        except Exception as exc:
            return fail('workspace_sync_failed', exc, prompt_arg)

    setup_done = time.time()
    _push({'type': 'process_start', 'status': 'running', 'message': 'starting opencode process'}, events, on_event)
    try:
        proc = subprocess.Popen(_cmd(container, run_workdir, prompt_arg, session_id, safe_env),
                                stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True, bufsize=1,
                                env=_process_env(safe_env), start_new_session=True)
    except Exception as exc:
        return fail('process_start_failed', exc, prompt_arg, setup_done)
    if register_cancel:
        register_cancel(lambda: _terminate(proc))

    session, error, first, heartbeat = session_id, None, None, setup_done
    while proc.poll() is None:
        now = time.time()
        if now - started > timeout_s:
            error = {'type': 'timeout', 'message': f'opencode timed out after {timeout_s}s'}
            _terminate(proc)
            break
        ready, _, _ = select.select([proc.stdout], [], [], 0.05) if proc.stdout else ([], [], [])
        if not ready:
            if first is None and now - started >= first_response_timeout_s:
                error = {'type': 'first_response_timeout',
                         'message': f'opencode produced no model/tool events within {first_response_timeout_s}s'}
                _terminate(proc)
                break
            if now - heartbeat >= 10:
                heartbeat = now
                _push(_heartbeat(proc, container, run_workdir, started), events, on_event)
            continue
        session, error, first = _read_line(
            ready[0].readline(), stdout, events, session, error, first, started, secrets, on_event
        )

    if proc.stdout:
        for line in proc.stdout:
            session, error, first = _read_line(line, stdout, events, session, error, first, started, secrets,
                                               on_event)
    returncode = proc.wait()
    _push({'type': 'process_exit', 'status': 'completed' if returncode == 0 else 'failed',
           'message': f'opencode exited with code {returncode}'}, events, on_event)
    if returncode and not error:
        error = {'type': 'process_failed', 'message': _clean_obj(''.join(stdout).strip()[-1000:], secrets)}
    if error and (not events or events[-1] is not error):
        _push({'type': str(error.get('type') or 'error'), 'message': str(error.get('message') or error)},
              events, on_event)
    if container and host_workdir:
        _push({'type': 'sync_back', 'status': 'running',
               'message': 'syncing opencode changes back to candidate workspace'}, events, on_event)
        try:
            _sync_back(container, run_workdir, host_workdir)
            _push({'type': 'sync_back', 'status': 'completed',
                   'message': 'candidate workspace synchronized'}, events, on_event)
        except Exception as exc:
            error = _clean_obj({'type': 'workspace_sync_back_failed', 'message': str(exc)}, secrets)
            _push(error, events, on_event)
            returncode = returncode or 1
    paths = _safe_write_logs(artifact_dir, stdout, events, secrets)
    return _result(returncode, session, events, paths, prompt_arg, error, started, setup_done, first, safe_env)


def _result(returncode: int, session: str, events: list[dict[str, Any]], paths: dict[str, str], prompt_arg: str,
            error: dict[str, Any] | None, started: float, setup_done: float, first: float | None,
            env: dict[str, str]) -> OpenCodeRunResult:
    return OpenCodeRunResult(
        returncode=returncode, session_id=session, events=events, raw_paths=paths, prompt_arg=prompt_arg,
        last_error=_clean_obj(error, _secrets(env)) if error else None,
        duration_seconds=round(time.time() - started, 3), setup_seconds=round(setup_done - started, 3),
        first_response_seconds=first, model=env.get('OPENCODE_MODEL', ''), provider=env.get('OPENCODE_PROVIDER', ''),
    )


def trace_payload(result: Any, repair_plan_ref: str, attempt: int, artifact_id: str | None = None) -> dict[str, Any]:
    timeline = [_compact(i, event) for i, event in enumerate(result.events)]
    ui = [item for item in (_ui(i, event) for i, event in enumerate(timeline)) if item]
    modified = sorted({
        path for event in timeline
        if event.get('tool') in {'edit', 'write'} or event.get('event_type') == 'patch'
        for path in event.get('file_paths', [])
    })
    if modified and 'patch' not in {item.get('kind') for item in ui}:
        ui.append({'index': len(ui), 'kind': 'patch', 'title': '生成代码补丁',
                   'summary': f'修改 {len(modified)} 个文件',
                   'paths': [p.split('/candidate/', 1)[-1].lstrip('/') for p in modified],
                   'status': 'completed', 'raw_event_index': None})
    event_types = sorted({str(event.get('type') or 'unknown') for event in result.events})
    return {
        'id': artifact_id or f'opencode_run_trace_attempt_{attempt}',
        'repair_plan_ref': repair_plan_ref,
        'attempt': attempt,
        'returncode': result.returncode,
        'raw_paths': result.raw_paths,
        'prompt_delivery': {'mode': 'file', 'instruction': result.prompt_arg,
                            'prompt_path': result.raw_paths.get('prompt', '')},
        'provider': result.provider,
        'model': result.model,
        'mapping_status': _mapping_status(result, modified),
        'session_mapping': {'status': 'mapped' if result.session_id else 'unmapped',
                            'source': 'opencode_stdout_json_events', 'session_id': result.session_id},
        'event_counts': {kind: sum(str(e.get('type') or 'unknown') == kind for e in result.events)
                         for kind in event_types},
        'ui_events': ui,
        'files_modified': modified,
        'last_error': result.last_error,
        'duration_seconds': result.duration_seconds,
        'setup_seconds': result.setup_seconds,
        'first_response_seconds': result.first_response_seconds,
        'first_response_diagnosis': _diagnosis(result),
    }


def _read_line(line: str, stdout: list[str], events: list[dict[str, Any]], session: str,
               error: dict[str, Any] | None, first: float | None, start: float, secrets: list[str],
               on_event: Any) -> tuple[str, dict[str, Any] | None, float | None]:
    if not line:
        return session, error, first
    stdout.append(_clean_obj(line, secrets))
    try:
        event = _clean_obj(json.loads(line), secrets)
    except json.JSONDecodeError:
        text = _clean_obj(line.strip(), secrets)
        if text:
            _push({'type': 'stdout', 'status': 'running', 'message': str(text)[:300]}, events, on_event)
        return session, error, first
    if not isinstance(event, dict):
        return session, error, first
    _push(event, events, on_event)
    if first is None and (_tool(event) or _text(event) or str(event.get('type') or '').lower() == 'error'):
        first = round(time.time() - start, 3)
    return session or str(event.get('sessionID') or ''), event if event.get('type') == 'error' else error, first


def _push(event: dict[str, Any], events: list[dict[str, Any]], on_event: Any) -> None:
    events.append(event)
    if on_event:
        compact = _compact(len(events) - 1, event)
        ui = _ui(int(compact['index']), compact)
        on_event(event, compact | ({'ui_event': ui} if ui else {}))


def _cmd(container: str, workdir: str, prompt: str, session: str, env: dict[str, str]) -> list[str]:
    # Permissions come from the explicit allow/deny config written to opencode.json,
    # never from --dangerously-skip-permissions.
    args = ['opencode', 'run', '--format', 'json']
    if env.get('OPENCODE_MODEL'):
        args += ['--model', env['OPENCODE_MODEL']]
    if session:
        args += ['--session', session]
    command = f"cd {shlex.quote(workdir)} && {_config_cmd(env)} {' '.join(shlex.quote(x) for x in [*args, prompt])}"
    flags = [item for key, value in env.items() if value for item in ('-e', key)]
    if container:
        return ['docker', 'exec', '-i', *flags, container, 'sh', '-lc', command]
    return ['sh', '-lc', command]


def _opencode_env(raw: dict[str, str]) -> dict[str, str]:
    key_env = str(raw.get('OPENCODE_PROVIDER_KEY_ENV') or '').strip()
    allowed = OPENCODE_CONFIG_KEYS | ({key_env} if key_env else set())
    return {key: str(value).strip() for key, value in raw.items() if key in allowed and str(value).strip()}


def _missing_config(env: dict[str, str]) -> list[str]:
    required = [
        'OPENCODE_MODEL',
        'OPENCODE_PROVIDER',
        'OPENCODE_PROVIDER_MODEL',
        'OPENCODE_PROVIDER_BASE_URL',
        'OPENCODE_PROVIDER_KEY_ENV',
    ]
    missing = [key for key in required if not env.get(key)]
    key_env = env.get('OPENCODE_PROVIDER_KEY_ENV', '')
    if key_env and not env.get(key_env):
        missing.append(key_env)
    return missing


def _process_env(safe_env: dict[str, str]) -> dict[str, str]:
    base_keys = ('HOME', 'PATH', 'SHELL', 'USER', 'LANG', 'LC_ALL', 'TMPDIR', 'SSL_CERT_FILE', 'REQUESTS_CA_BUNDLE')
    base = {key: value for key in base_keys if (value := os.environ.get(key))}
    return {**base, **safe_env}


def _config_cmd(env: dict[str, str]) -> str:
    provider, model = env.get('OPENCODE_PROVIDER', ''), env.get('OPENCODE_PROVIDER_MODEL', '')
    base_url, key_env = env.get('OPENCODE_PROVIDER_BASE_URL', ''), env.get('OPENCODE_PROVIDER_KEY_ENV', '')
    config: dict[str, Any] = {'$schema': 'https://opencode.ai/config.json', 'permission': PERMISSIONS}
    if provider and model and base_url and key_env and env.get(key_env):
        # Custom base URLs (inner lazyllm, proxies) must use chat-completions via
        # openai-compatible; @ai-sdk/openai routes through Responses API which inner
        # endpoints reject for tool use.
        official = base_url.rstrip('/').endswith('api.openai.com/v1')
        npm = '@ai-sdk/openai' if provider == 'openai' and official else '@ai-sdk/openai-compatible'
        model_cfg: dict[str, Any] = {'name': model, 'tool_call': True}
        if not official:
            model_cfg['limit'] = {'context': 32768, 'output': 1024}
        config['provider'] = {provider: {
            'npm': npm,
            'name': env.get('OPENCODE_PROVIDER_LABEL') or provider,
            'options': {'baseURL': base_url, 'apiKey': f'{{env:{key_env}}}'},
            'models': {model: model_cfg},
        }}
    return f'printf %s {shlex.quote(json.dumps(config, ensure_ascii=False))} > opencode.json;'


def _compact(index: int, event: dict[str, Any]) -> dict[str, Any]:
    paths = _paths(event)
    for key in ('changed_files', 'files'):
        paths += [str(path) for path in event.get(key, []) if isinstance(path, str)]
    return {
        'index': index,
        'event_type': str(event.get('type') or 'unknown'),
        'tool': _tool(event),
        'summary': _text(event)[:300] or str(event.get('message') or event.get('error') or '')[:300],
        'file_paths': sorted(set(paths)),
        'status': str(event.get('status') or event.get('state') or ''),
    }


def _ui(index: int, event: dict[str, Any]) -> dict[str, Any] | None:
    paths = [p.split('/candidate/', 1)[-1].lstrip('/') for p in event.get('file_paths') or [] if isinstance(p, str)]
    tool, etype = str(event.get('tool') or ''), str(event.get('event_type') or '')
    kind = {'glob': 'search', 'grep': 'search', 'list': 'search', 'read': 'read_file', 'edit': 'edit_file',
            'write': 'edit_file', 'bash': 'run_command'}.get(tool) or {
        'patch': 'patch', 'process_heartbeat': 'heartbeat', 'error': 'error', 'setup_failed': 'error',
        'process_failed': 'error', 'timeout': 'error', 'first_response_timeout': 'error', 'setup': 'setup',
        'process_start': 'process', 'process_exit': 'process', 'sync_back': 'sync', 'stdout': 'agent_note',
    }.get(etype)
    kind = kind or ('agent_note' if event.get('summary') and etype == 'text' else '')
    if not kind:
        return None
    title = {
        'search': '查找文件',
        'read_file': f"读取 {paths[0] if paths else '文件'}",
        'edit_file': f"修改 {paths[0] if paths else '文件'}",
        'patch': '生成代码补丁',
        'run_command': '执行命令',
        'heartbeat': 'opencode 运行中',
        'error': 'opencode 出错',
        'agent_note': 'opencode 分析',
        'setup': '准备 opencode 工作区',
        'process': 'opencode 进程',
        'sync': '同步候选工作区',
    }[kind]
    status = str(event.get('status') or '')
    return {'index': index, 'kind': kind, 'title': title, 'summary': event.get('summary', ''), 'paths': paths,
            'status': 'completed' if 'completed' in status else 'pending' if 'pending' in status else status[:80],
            'raw_event_index': event.get('index')}


def _tool(event: dict[str, Any]) -> str:
    part = event.get('part') if isinstance(event.get('part'), dict) else {}
    call = event.get('call') if isinstance(event.get('call'), dict) else {}
    return str(event.get('tool') or part.get('tool') or call.get('tool') or '')


def _text(event: dict[str, Any]) -> str:
    part = event.get('part') if isinstance(event.get('part'), dict) else {}
    text = part.get('text') or event.get('text')
    return text.strip() if event.get('type') == 'text' and isinstance(text, str) else ''


def _paths(value: Any) -> list[str]:
    if isinstance(value, dict):
        return [item for key, child in value.items()
                for item in ([child] if key in {'file', 'path', 'filepath', 'filePath'} and isinstance(child, str)
                             else _paths(child))]
    if isinstance(value, list):
        return [item for child in value for item in _paths(child)]
    return []


def _write_logs(root: Path, stdout: list[str], events: list[dict[str, Any]], secrets: list[str]) -> dict[str, str]:
    paths = {'prompt': root / 'opencode_prompt.json', 'stdout': root / 'stdout.log', 'stderr': root / 'stderr.log',
             'events_jsonl': root / 'events.jsonl', 'text_summary': root / 'text_summary.md'}
    paths['stdout'].write_text(''.join(stdout), encoding='utf-8')
    paths['stderr'].write_text('', encoding='utf-8')
    paths['events_jsonl'].write_text(
        ''.join(json.dumps(_clean_obj(e, secrets), ensure_ascii=False) + '\n' for e in events), encoding='utf-8'
    )
    paths['text_summary'].write_text(
        '\n'.join(_text(e) for e in events if _text(e)).strip() or '_(no text events)_\n', encoding='utf-8'
    )
    return {key: str(path) for key, path in paths.items()}


def _safe_write_logs(root: Path, stdout: list[str], events: list[dict[str, Any]], secrets: list[str]) -> dict[str, str]:
    try:
        root.mkdir(parents=True, exist_ok=True)
        return _write_logs(root, stdout, events, secrets)
    except Exception:
        return {'prompt': '', 'stdout': '', 'stderr': '', 'events_jsonl': '', 'text_summary': ''}


def _heartbeat(proc: subprocess.Popen, container: str, workdir: str, start: float) -> dict[str, Any]:
    elapsed = round(time.time() - start, 1)
    return {'type': 'process_heartbeat', 'status': 'running', 'pid': proc.pid, 'elapsed_seconds': elapsed,
            'message': f'opencode running for {elapsed}s; waiting for json events',
            'changed_files': _changed(container, workdir)}


def _changed(container: str, workdir: str) -> list[str]:
    try:
        cmd = ['git', '-c', f'safe.directory={workdir}', '-C', workdir, 'diff', '--name-only']
        command = ['docker', 'exec', container, *cmd] if container else cmd
        return subprocess.run(command, capture_output=True, text=True, timeout=5, check=False).stdout.splitlines()
    except Exception:
        return []


def _sync_back(container: str, run_workdir: str, host_workdir: Path) -> None:
    backup = host_workdir.with_name(f'{host_workdir.name}.host_before_opencode')
    _rm(backup)
    host_workdir.rename(backup)
    try:
        _run(['docker', 'cp', f'{container}:{run_workdir}', str(host_workdir)])
    except Exception:
        _rm(host_workdir)
        backup.rename(host_workdir)
        raise
    _rm(backup)
    _run(['docker', 'exec', container, 'rm', '-rf', run_workdir])


def _run(cmd: list[str], timeout: int = 30) -> str:
    result = subprocess.run(cmd, capture_output=True, text=True, timeout=timeout, check=False)
    if result.returncode:
        raise RuntimeError((result.stderr or result.stdout).strip())
    return result.stdout


def _rm(path: Path) -> None:
    if path.exists():
        shutil.rmtree(path)


def _prompt_arg(prompt_path: Path, host_workdir: Path | None, workdir: str) -> str:
    if host_workdir and prompt_path.is_relative_to(host_workdir):
        path = Path(workdir, prompt_path.relative_to(host_workdir)).as_posix()
    else:
        path = prompt_path.as_posix()
    return f'Read {path} as your first tool call, then follow the JSON task card mode and stop condition exactly.'


def _diagnosis(result: Any) -> dict[str, Any]:
    err = str((result.last_error or {}).get('type') or '')
    if result.first_response_seconds is not None and result.first_response_seconds < 120:
        kind = 'ok'
    elif result.first_response_seconds is not None:
        kind = 'slow_model_or_tool_start'
    elif 'key' in json.dumps(result.last_error or {}, ensure_ascii=False).lower():
        kind = 'api_or_auth_error'
    else:
        kind = err or 'no_model_or_tool_event'
    return {'kind': kind, 'setup_seconds': result.setup_seconds,
            'first_response_seconds': result.first_response_seconds,
            'evidence': {'last_error_type': err, 'event_count': len(result.events),
                         'text_event_count': sum(e.get('type') == 'text' for e in result.events),
                         'tool_event_count': sum(bool(_tool(e)) for e in result.events)}}


def _mapping_status(result: Any, modified: list[str]) -> str:
    if result.session_id and result.events:
        return 'complete'
    if modified:
        return 'events_and_diff'
    return 'failed' if result.last_error else 'events_only'


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


def _clean_obj(value: Any, secrets: list[str]) -> Any:
    if isinstance(value, str):
        for secret in secrets:
            value = value.replace(secret, '<redacted>')
        return value
    if isinstance(value, list):
        return [_clean_obj(item, secrets) for item in value]
    if isinstance(value, dict):
        return {key: _clean_obj(item, secrets) for key, item in value.items()}
    return value


def _secrets(env: dict[str, str]) -> list[str]:
    return [str(value) for key, value in env.items() if value and any(x in key for x in ('KEY', 'TOKEN', 'SECRET'))]
