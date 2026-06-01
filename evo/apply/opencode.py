from __future__ import annotations
import json
import logging
import os
import shutil
import subprocess
import tempfile
from collections import namedtuple
from contextlib import suppress
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any, Callable
from urllib.parse import urlparse
from evo.apply.errors import ApplyError

from algorithm.config import config

log = logging.getLogger('evo.apply.opencode')
PERMISSIONS = {
    **dict.fromkeys(('read', 'grep', 'glob', 'list', 'bash', 'edit', 'external_directory'), 'allow'),
    **dict.fromkeys(('question', 'plan_enter', 'plan_exit', 'todowrite', 'task'), 'deny'),
}

OpencodeProviderConfig = namedtuple('OpencodeProviderConfig', 'provider model api_key base_url label', defaults=[''])

PROVIDER_BASE_URLS = {
    'qwen': 'https://dashscope.aliyuncs.com/compatible-mode/v1',
    'deepseek': 'https://api.deepseek.com',
    'openai': 'https://api.openai.com/v1',
}


def apply_model() -> OpencodeProviderConfig:
    provider = str(config['evo_code_provider'] or '').strip()
    base_url = str(config['evo_code_base_url'] or '').strip() or PROVIDER_BASE_URLS.get(provider, '')
    label = str(config['evo_code_label'] or provider).strip()
    return OpencodeProviderConfig(
        _provider_id(provider, base_url, label),
        str(config['evo_code_model'] or '').strip(),
        str(config['evo_code_api_key'] or '').strip() or _provider_api_key(provider),
        base_url,
        label,
    )


def provider_config_from_evo_llm(model_config: dict[str, Any] | None) -> OpencodeProviderConfig | None:
    evo_llm = (model_config or {}).get('evo_llm')
    if not isinstance(evo_llm, dict):
        return None
    provider = str(evo_llm.get('source') or '').strip().lower()
    model = str(evo_llm.get('model') or '').strip()
    api_key = str(evo_llm.get('api_key') or '').strip()
    base_url = _normalize_provider_base_url(provider, str(evo_llm.get('base_url') or '').strip())
    label = provider
    return OpencodeProviderConfig(_provider_id(provider, base_url, label), model, api_key, base_url, label)


@dataclass
class OpencodeOptions:
    binary: str | None = None
    model: str | None = None
    agent: str | None = None
    variant: str | None = None
    timeout_s: int = 600
    skip_permissions: bool = config['evo_code_skip_permissions']
    provider_config: OpencodeProviderConfig | None = field(default_factory=apply_model)


OpencodeOutcome = namedtuple(
    'OpencodeOutcome', 'returncode text_summary last_error events_path stdout_path stderr_path')


class OpencodeSession:
    def __init__(self, *, cwd: Path, binary: str, options: OpencodeOptions,
                 on_proc: Callable[[subprocess.Popen], None] | None = None) -> None:
        self.cwd, self.binary, self.options, self.on_proc = cwd, binary, options, on_proc
        self.provider_config = options.provider_config
        self.model = (
            f'{self.provider_config.provider}/{self.provider_config.model}'
            if self.provider_config else options.model
        )
        self.temp_config = None
        self._sync_project_provider_config()
        self.home = _prepare_opencode_home(default_auth_dir())
        self.env = {**os.environ, 'HOME': str(self.home)}
        if self.provider_config:
            self.env[_api_key_env(self.provider_config.provider)] = self.provider_config.api_key
            _append_no_proxy(self.env, self.provider_config.base_url)
            _write_provider_auth(self.home, self.provider_config)

    def close(self) -> None:
        _cleanup_temp(self.temp_config, unlink=True)
        _cleanup_temp(self.home)

    def run(self, prompt: str, artifact_dir: Path) -> OpencodeOutcome:
        self._sync_project_provider_config()
        try:
            artifact_dir.mkdir(parents=True, exist_ok=True)
            cmd = [self.binary, 'run', '--format', 'json']
            if self.options.skip_permissions:
                cmd.append('--dangerously-skip-permissions')
            for flag, value in (
                ('--model', self.model), ('--agent', self.options.agent), ('--variant', self.options.variant)
            ):
                if value:
                    cmd.extend([flag, value])
            cmd.append(prompt)
            log.info('opencode run: cwd=%s timeout_s=%d model=%s', self.cwd, self.options.timeout_s, self.model)
            proc = subprocess.Popen(
                cmd, cwd=str(self.cwd), stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, env=self.env
            )
            if self.on_proc:
                self.on_proc(proc)
            try:
                stdout, stderr = proc.communicate(timeout=self.options.timeout_s)
            except subprocess.TimeoutExpired:
                _terminate(proc)
                raise ApplyError('OPENCODE_TIMEOUT', 'opencode run timed out',
                                 {'timeout_s': self.options.timeout_s, 'cwd': str(self.cwd)})
            return _write_outcome(artifact_dir, proc.returncode, stdout or '', stderr or '')
        finally:
            _cleanup_temp(self.temp_config, unlink=True)
            self.temp_config = None

    def _sync_project_provider_config(self) -> None:
        """Rewrite project opencode.json when missing — opencode may delete it between rounds."""
        path = _ensure_project_provider_config(self.cwd, self.provider_config)
        if path is not None:
            self.temp_config = path


def resolve_binary(binary: str | None) -> str:
    candidate = (binary or config['evo_code_binary'] or shutil.which('opencode') or '').strip()
    if not candidate:
        raise ApplyError('OPENCODE_BIN_MISSING', 'opencode binary not found on PATH')
    return candidate


def default_auth_dir() -> Path:
    if data_dir := config['evo_code_data_dir']:
        return Path(data_dir)
    return Path.home() / '.local' / 'share' / 'opencode'


def preflight(binary: str | None, *, auth_dir: Path | None = None, options: OpencodeOptions | None = None) -> str:
    resolved = resolve_binary(binary)
    _validate_provider_config((options or OpencodeOptions()).provider_config)
    if not shutil.which('rg'):
        raise ApplyError('OPENCODE_SEARCH_TOOL_MISSING', 'ripgrep (rg) is required for opencode global search tools')
    try:
        r = subprocess.run([resolved, '--version'], capture_output=True, text=True, timeout=15, check=False)
    except (FileNotFoundError, subprocess.TimeoutExpired) as exc:
        msg = (
            'opencode --version timed out'
            if isinstance(exc, subprocess.TimeoutExpired)
            else 'opencode binary not executable'
        )
        raise ApplyError('OPENCODE_BIN_MISSING', msg, {'binary': resolved}) from exc
    if r.returncode != 0:
        raise ApplyError('OPENCODE_BIN_MISSING', 'opencode --version failed', {'stderr': r.stderr[-500:]})
    return resolved


def run_opencode(
    prompt: str, *, cwd: Path, artifact_dir: Path, binary: str, options: OpencodeOptions,
    on_proc: Callable[[subprocess.Popen], None] | None = None,
) -> OpencodeOutcome:
    session = None
    try:
        session = OpencodeSession(cwd=cwd, binary=binary, options=options, on_proc=on_proc)
        return session.run(prompt, artifact_dir)
    finally:
        if session:
            session.close()


def _write_outcome(artifact_dir: Path, returncode: int, stdout: str, stderr: str) -> OpencodeOutcome:
    events, text_chunks, last_error = _parse_event_stream(stdout or '')
    stdout_path, stderr_path, events_path = (
        artifact_dir / name for name in ('stdout.log', 'stderr.log', 'events.jsonl'))
    stdout_path.write_text(stdout or '', encoding='utf-8')
    stderr_path.write_text(stderr or '', encoding='utf-8')
    events_path.write_text(''.join(json.dumps(e, ensure_ascii=False) + '\n' for e in events), encoding='utf-8')
    text_summary = '\n'.join(text_chunks).strip()
    (artifact_dir / 'text_summary.md').write_text(text_summary or '_(no text events)_\n', encoding='utf-8')
    return OpencodeOutcome(returncode, text_summary, last_error, events_path, stdout_path, stderr_path)


def _prepare_opencode_home(auth_dir: Path) -> Path:
    home = Path(tempfile.mkdtemp(prefix='evo-opencode-home-'))
    state_dir = home / '.local' / 'share' / 'opencode'
    state_dir.parent.mkdir(parents=True, exist_ok=True)
    if auth_dir.exists():
        shutil.copytree(auth_dir, state_dir, dirs_exist_ok=True)
    return home


def _write_provider_auth(home: Path, provider_config: OpencodeProviderConfig) -> None:
    if not provider_config.api_key:
        return
    auth_path = home / '.local' / 'share' / 'opencode' / 'auth.json'
    auth_path.parent.mkdir(parents=True, exist_ok=True)
    data: dict[str, Any] = {}
    if auth_path.exists():
        with suppress(Exception):
            loaded = json.loads(auth_path.read_text(encoding='utf-8'))
            if isinstance(loaded, dict):
                data = loaded
    data[provider_config.provider] = {'type': 'api', 'key': provider_config.api_key}
    auth_path.write_text(json.dumps(data, ensure_ascii=False), encoding='utf-8')


def _cleanup_temp(path: Path | None, *, unlink: bool = False) -> None:
    if path and unlink:
        with suppress(FileNotFoundError):
            path.unlink()
    elif path:
        shutil.rmtree(path, ignore_errors=True)


def _api_key_env(provider: str) -> str:
    safe = ''.join((ch if ch.isalnum() else '_' for ch in provider.upper()))
    return f'OPENCODE_{safe}_API_KEY'


def _append_no_proxy(env: dict[str, str], base_url: str) -> None:
    parsed = urlparse(base_url)
    if not parsed.hostname:
        return
    for key in ('NO_PROXY', 'no_proxy'):
        values = [part.strip() for part in (env.get(key) or '').split(',') if part.strip()]
        if parsed.hostname not in values:
            values.append(parsed.hostname)
        env[key] = ','.join(values)


def _provider_api_key(provider: str) -> str:
    safe = ''.join((ch if ch.isalnum() else '_' for ch in provider.upper()))
    return os.environ.get(f'LAZYLLM_{safe}_API_KEY', '') or os.environ.get(f'{safe}_API_KEY', '')


def _provider_id(provider: str, base_url: str, label: str) -> str:
    if provider == 'openai' and base_url.rstrip('/') != PROVIDER_BASE_URLS['openai'].rstrip('/'):
        return _safe_provider_id(label or 'openai_compatible')
    return provider


def _normalize_provider_base_url(provider: str, base_url: str) -> str:
    if not base_url:
        return PROVIDER_BASE_URLS.get(provider, '')
    stripped = base_url.rstrip('/')
    if provider == 'qwen' and stripped == 'https://dashscope.aliyuncs.com':
        return PROVIDER_BASE_URLS['qwen']
    return stripped


def _safe_provider_id(value: str) -> str:
    out = ''.join(ch.lower() if ch.isalnum() else '_' for ch in value.strip())
    return out.strip('_') or 'openai_compatible'


def _validate_provider_config(provider_config: OpencodeProviderConfig | None) -> None:
    if provider_config is None:
        return
    missing = [
        name for name, value in (
            ('provider', provider_config.provider),
            ('model', provider_config.model),
            ('api_key', provider_config.api_key),
            ('base_url', provider_config.base_url),
        )
        if not str(value or '').strip()
    ]
    if missing:
        raise ApplyError(
            'OPENCODE_CONFIG_INVALID',
            f'opencode provider config missing: {", ".join(missing)}',
            {'missing': missing, 'provider': provider_config.provider, 'model': provider_config.model},
        )
    parsed = urlparse(provider_config.base_url)
    if parsed.scheme not in {'http', 'https'} or not parsed.netloc:
        raise ApplyError(
            'OPENCODE_CONFIG_INVALID',
            'opencode provider base_url must be an absolute http(s) URL',
            {'base_url': provider_config.base_url, 'provider': provider_config.provider},
        )


def _ensure_project_provider_config(cwd: Path, provider_config: OpencodeProviderConfig | None = None) -> Path | None:
    path = cwd / 'opencode.json'
    if provider_config is None or path.exists():
        return None
    provider = provider_config.provider
    data = {
        '$schema': 'https://opencode.ai/config.json',
        'permission': PERMISSIONS,
        'provider': {provider: {
            'npm': '@ai-sdk/openai-compatible',
            'name': provider_config.label or provider,
            'options': {'baseURL': provider_config.base_url.rstrip('/'), 'apiKey': f'{{env:{_api_key_env(provider)}}}'},
            'models': {provider_config.model: {'name': provider_config.model}},
        }},
    }
    path.write_text(json.dumps(data, ensure_ascii=False, indent=2), encoding='utf-8')
    return path


def _terminate(proc: subprocess.Popen, grace_s: float = 5.0) -> None:
    if proc.poll() is not None:
        return
    for stop in (proc.terminate, proc.kill):
        stop()
        try:
            proc.wait(timeout=grace_s)
            return
        except subprocess.TimeoutExpired:
            pass


def _parse_event_stream(raw: str) -> tuple[list[dict], list[str], dict | None]:
    events: list[dict] = []
    text_chunks: list[str] = []
    last_error: dict | None = None
    for line in raw.splitlines():
        if not (stripped := line.strip()):
            continue
        try:
            obj: Any = json.loads(stripped)
        except json.JSONDecodeError:
            continue
        if not isinstance(obj, dict):
            continue
        events.append(obj)
        etype = obj.get('type')
        if etype == 'text':
            part = obj.get('part')
            text = part.get('text') if isinstance(part, dict) else None
            if isinstance(text, str) and text.strip():
                text_chunks.append(text.strip())
        elif etype == 'error':
            last_error = obj
    return (events, text_chunks, last_error)
